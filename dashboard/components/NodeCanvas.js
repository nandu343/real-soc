import { useState, useEffect, useRef } from 'react'

export default function NodeCanvas({ initialNodes, initialEdges, onChange, activeTaskId, failedTaskErrors }){
  const [nodes, setNodes] = useState(initialNodes || [])
  const [edges, setEdges] = useState(initialEdges || [])
  const [selectedNodeId, setSelectedNodeId] = useState(null)
  const [moving, setMoving] = useState(null) // { id, startX, startY, origX, origY }

  const containerRef = useRef(null)
  const nodeRefs = useRef({})
  const [nodeRects, setNodeRects] = useState({})
  const [dragging, setDragging] = useState(null) // { source, x, y }

  useEffect(()=>{ setNodes(initialNodes || []) }, [initialNodes])
  useEffect(()=>{ setEdges(initialEdges || []) }, [initialEdges])

  // Notify parent when nodes/edges change
  const notify = (ns, es) => { if(onChange) onChange({ nodes: ns, edges: es }) }

  // Utility: recompute node DOM rects relative to container
  const recomputeRects = ()=>{
    const out = {}
    if(!containerRef.current) return setNodeRects(out)
    const crect = containerRef.current.getBoundingClientRect()
    Object.keys(nodeRefs.current).forEach(id => {
      const el = nodeRefs.current[id]
      if(!el) return
      const r = el.getBoundingClientRect()
      out[id] = {
        left: r.left - crect.left,
        top: r.top - crect.top,
        width: r.width,
        height: r.height,
        cx: r.left - crect.left + r.width/2,
        cy: r.top - crect.top + r.height/2,
      }
    })
    setNodeRects(out)
  }

  useEffect(()=>{ recomputeRects(); window.addEventListener('resize', recomputeRects); return ()=> window.removeEventListener('resize', recomputeRects) }, [nodes])

  useEffect(()=>{ // when refs change after render, recompute
    const t = setTimeout(()=>recomputeRects(), 50)
    return ()=>clearTimeout(t)
  }, [nodes, edges])

  const onDrop = (e)=>{
    e.preventDefault()
    const data = e.dataTransfer.getData('application/json')
    if(!data) return
    try{
      const agent = JSON.parse(data)
      const rect = e.currentTarget.getBoundingClientRect()
      const x = e.clientX - rect.left
      const y = e.clientY - rect.top
      const node = {
        id: Date.now().toString(),
        data: {
          agentId: agent.agent_id || agent.AgentID || agent.agentId,
          image: agent.image,
          endpoint: agent.endpoint || agent.image || '',
          method: agent.method || 'POST',
          headers: agent.headers || {},
          taskName: agent.name || agent.agent_id,
          config: agent.config || {},
          type: agent.type || agent.Type || 'docker',
          capabilities: agent.capabilities || agent.Capabilities || []
        },
        x, y,
      }
      setNodes(ns => {
        const next = [...ns, node]
        notify(next, edges)
        return next
      })
    }catch(err){
      // ignore
    }
  }

  // select node for editing on double click; moving handled by mouse drag
  const onNodeClick = (node)=>{
    // single click selects for quick focus
    setSelectedNodeId(node.id)
  }

  const startMove = (nodeId, clientX, clientY)=>{
    if(!containerRef.current) return
    const crect = containerRef.current.getBoundingClientRect()
    const startX = clientX - crect.left
    const startY = clientY - crect.top
    const n = nodes.find(n=>n.id===nodeId)
    if(!n) return
    setMoving({ id: nodeId, startX, startY, origX: n.x, origY: n.y })
  }

  useEffect(()=>{
    if(!moving) return
    const onMove = (ev)=>{
      if(!containerRef.current) return
      const crect = containerRef.current.getBoundingClientRect()
      const px = ev.clientX - crect.left
      const py = ev.clientY - crect.top
      const dx = px - moving.startX
      const dy = py - moving.startY
      setNodes(ns => {
        const next = ns.map(n => n.id === moving.id ? ({ ...n, x: Math.max(0, moving.origX + dx), y: Math.max(0, moving.origY + dy) }) : n)
        notify(next, edges)
        return next
      })
    }
    const onUp = ()=>{
      setMoving(null)
      recomputeRects()
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return ()=>{ window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
  }, [moving, edges, nodes])

  // Drag-to-connect handlers
  const startDragFrom = (sourceId, clientX, clientY)=>{
    if(!containerRef.current) return
    const crect = containerRef.current.getBoundingClientRect()
    setDragging({ source: sourceId, x: clientX - crect.left, y: clientY - crect.top })
  }

  useEffect(()=>{
    if(!dragging) return
    const onMove = (ev)=>{
      if(!containerRef.current) return
      const crect = containerRef.current.getBoundingClientRect()
      setDragging(d => d ? ({ ...d, x: ev.clientX - crect.left, y: ev.clientY - crect.top }) : null)
    }
    const onUp = (ev)=>{
      if(!containerRef.current) { setDragging(null); return }
      const crect = containerRef.current.getBoundingClientRect()
      const px = ev.clientX - crect.left
      const py = ev.clientY - crect.top
      // find target node under pointer
      let targetId = null
      Object.entries(nodeRects).forEach(([id, r])=>{
        if(id === dragging.source) return
        if(px >= r.left && px <= r.left + r.width && py >= r.top && py <= r.top + r.height){ targetId = id }
      })
      if(targetId){
        const edge = { id: Date.now().toString(), source: dragging.source, target: targetId }
        setEdges(es => { const next = [...es, edge]; notify(nodes, next); return next })
      }
      setDragging(null)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return ()=>{ window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
  }, [dragging, nodeRects, nodes, edges])

  return (
    <div className="flex-1">
      <div ref={containerRef} onDragOver={(e)=>e.preventDefault()} onDrop={onDrop} className="h-[60vh] bg-slate-100 p-4 relative">

        {/* SVG overlay for edges */}
        <svg className="absolute left-0 top-0 w-full h-full pointer-events-none">
          <defs>
            <marker id="arrowhead" markerWidth="10" markerHeight="10" refX="10" refY="5" orient="auto">
              <path d="M0,0 L10,5 L0,10 z" fill="#0f172a" />
            </marker>
          </defs>
          {edges.map(e => {
            const s = nodeRects[e.source]
            const t = nodeRects[e.target]
            if(!s || !t) return null
            return <line key={e.id} x1={s.cx} y1={s.cy} x2={t.cx} y2={t.cy} stroke="#0f172a" strokeWidth={2} markerEnd="url(#arrowhead)" />
          })}
          {dragging && (()=>{
            const s = nodeRects[dragging.source]
            if(!s) return null
            return <line x1={s.cx} y1={s.cy} x2={dragging.x} y2={dragging.y} stroke="#0f172a" strokeWidth={2} strokeDasharray="6" markerEnd="url(#arrowhead)" />
          })()}
        </svg>

        {nodes.map(n=> {
          const isActive = activeTaskId && (n.id === activeTaskId || n.data?.taskId === activeTaskId)
          const failed = failedTaskErrors && (failedTaskErrors[n.id] || failedTaskErrors[n.data?.taskId])
          return (
              <div key={n.id}
                ref={el=>{ nodeRefs.current[n.id]=el }}
                onClick={()=>onNodeClick(n)}
                onMouseDown={(e)=>{ if(e.button===0){ e.stopPropagation(); startMove(n.id, e.clientX, e.clientY) } }}
                title={failed ? (failed || 'error') : ''}
                style={{position:'absolute', left:n.x, top:n.y}}
                className={`w-44 p-3 bg-amber-50 rounded-lg shadow-md cursor-pointer border ${isActive? 'ring-2 ring-amber-400': ''} ${failed? 'border-red-600': 'border-amber-200'}`}>
              <div className="font-semibold text-sm text-amber-900">{n.data?.taskName || n.data?.agentId}</div>
              <div className="text-xs text-amber-700">{n.data?.image || n.data?.endpoint}</div>

              {/* ports: input (left) and output (right) */}
              <div style={{position:'absolute', left:-8, top:'50%', transform:'translateY(-50%)'}}>
                <div className="w-3 h-3 bg-amber-300 rounded-full border" />
              </div>
              <div style={{position:'absolute', right:-8, top:'50%', transform:'translateY(-50%)'}}>
                <div onMouseDown={(e)=>{ e.stopPropagation(); startDragFrom(n.id, e.clientX, e.clientY) }} className="w-4 h-4 bg-amber-600 rounded-full border cursor-grab" />
              </div>

              <div className="flex justify-between mt-2 text-xs text-slate-500">
                <div className="flex items-center gap-1"><span className="w-2 h-2 bg-blue-500 rounded-full"></span> In</div>
                <div className="flex items-center gap-1"><span className="w-2 h-2 bg-green-500 rounded-full"></span> Out</div>
              </div>
            </div>
          )
        })}
        {nodes.length===0 && (
          <div className="text-sm text-slate-500">Drag agents from the palette to create nodes.</div>
        )}
      
          {/* Node editor panel */}
          {selectedNodeId && (()=>{
            const node = nodes.find(x=>x.id===selectedNodeId)
            if(!node) return null
            return (
              <div style={{position:'absolute', right:8, top:8, width:320}} className="bg-amber-50 border-amber-200 rounded p-3 shadow">
                <div className="flex items-center justify-between mb-2">
                  <div className="font-medium">Edit Node</div>
                  <div className="text-xs text-slate-500">id: {node.id}</div>
                </div>
                <label className="text-xs">Agent ID</label>
                <input className="w-full mb-2 p-1 border rounded" value={node.data.agentId || ''} onChange={(e)=>{
                  const v = e.target.value
                  setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, agentId: v}}) : n))
                }} />
                <label className="text-xs">Task Name</label>
                <input className="w-full mb-2 p-1 border rounded" value={node.data.taskName || ''} onChange={(e)=>{
                  const v = e.target.value
                  setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, taskName: v}}) : n))
                }} />
                <label className="text-xs">Image</label>
                <input className="w-full mb-2 p-1 border rounded" value={node.data.image || ''} onChange={(e)=>{
                  const v = e.target.value
                  setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, image: v}}) : n))
                }} />
                {(node.data && (node.data.type==='remote' || node.data.type==='api')) && (
                  <>
                    <label className="text-xs">Endpoint (URL)</label>
                    <input className="w-full mb-2 p-1 border rounded" value={node.data.endpoint || ''} onChange={(e)=>{
                      const v = e.target.value
                      setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, endpoint: v}}) : n))
                    }} />
                    <label className="text-xs">Method</label>
                    <input className="w-full mb-2 p-1 border rounded" value={node.data.method || 'POST'} onChange={(e)=>{
                      const v = e.target.value
                      setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, method: v}}) : n))
                    }} />
                    <label className="text-xs">Headers (JSON)</label>
                    <textarea rows={3} className="w-full mb-2 p-1 border rounded font-mono text-xs" value={JSON.stringify(node.data.headers||{}, null, 2)} onChange={(e)=>{
                      try{ const v=JSON.parse(e.target.value); setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, headers: v}}) : n)) }catch(err){}
                    }} />
                  </>
                )}
                <label className="text-xs">Config (JSON)</label>
                <textarea rows={6} className="w-full mb-2 p-1 border rounded font-mono text-xs" value={JSON.stringify(node.data.config||{}, null, 2)} onChange={(e)=>{
                  try{
                    const v = JSON.parse(e.target.value)
                    setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, config: v}}) : n))
                  }catch(err){ /* ignore parse errors until save */ }
                }} />
                <div className="flex gap-2">
                  <button onClick={()=>{ notify(nodes, edges); setSelectedNodeId(null); recomputeRects() }} className="px-3 py-1 bg-amber-600 text-white rounded">Save</button>
                  <button onClick={()=>{ setNodes(ns=>ns.filter(x=>x.id!==node.id)); setEdges(es=>es.filter(e=>e.source!==node.id && e.target!==node.id)); notify(nodes.filter(x=>x.id!==node.id), edges.filter(e=>e.source!==node.id && e.target!==node.id)); setSelectedNodeId(null); recomputeRects() }} className="px-3 py-1 bg-red-600 text-white rounded">Delete</button>
                  <button onClick={()=>setSelectedNodeId(null)} className="px-3 py-1 bg-amber-200 rounded">Close</button>
                </div>
              </div>
            )
          })()}
      </div>

      <div className="mt-2 bg-white p-2 rounded border">
        <div className="text-sm text-slate-600">Connect nodes: drag from the green output dot to another node, or click to select then click a target.</div>
        <div className="mt-2 text-sm">
          {edges.length===0 ? (<div className="text-slate-500">No edges</div>) : (
            edges.map(e => (<div key={e.id} className="text-xs">{e.source} → {e.target}</div>) )
          )}
        </div>
      </div>
    </div>
  )
}
