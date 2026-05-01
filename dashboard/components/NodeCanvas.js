import { useState, useEffect, useRef } from 'react'

export default function NodeCanvas({ initialNodes, initialEdges, onChange, activeTaskId, failedTaskErrors, workflow }){
  const [nodes, setNodes] = useState(initialNodes || [])
  const [edges, setEdges] = useState(initialEdges || [])
  const [selectedNodeId, setSelectedNodeId] = useState(null)
  const [editingNodeId, setEditingNodeId] = useState(null)
  const [moving, setMoving] = useState(null) // { id, startX, startY, origX, origY }

  // Autocomplete state for expression inputs (shows previous nodes to reference)
  const [autocompleteOpen, setAutocompleteOpen] = useState(false)
  const [autocompleteItems, setAutocompleteItems] = useState([])
  const [autocompleteQuery, setAutocompleteQuery] = useState('')
  const autocompleteInputRef = useRef(null)
  const [editingField, setEditingField] = useState(null)

  // pan / zoom state
  const [scale, setScale] = useState(1)
  const [offset, setOffset] = useState({ x: 0, y: 0 })
  const panningRef = useRef(null)
  const [isSpaceDown, setIsSpaceDown] = useState(false)

  const containerRef = useRef(null)
  const nodeRefs = useRef({})
  const [nodeRects, setNodeRects] = useState({})
  const [dragging, setDragging] = useState(null) // { source, x, y }
  const PORT_OUTER_OFFSET = 20 // px distance from node edge to port center
  const PORT_GAP = 18 // vertical gap for multiple ports (conditional)

  useEffect(()=>{ setNodes(initialNodes || []) }, [initialNodes])
  useEffect(()=>{ setEdges(initialEdges || []) }, [initialEdges])

  // Notify parent when nodes/edges change
  const notify = (ns, es) => { if(onChange) onChange({ nodes: ns, edges: es }) }

  // Populate autocomplete candidates whenever nodes or editor changes
  useEffect(()=>{
    if(!editingNodeId){ setAutocompleteItems([]); return }
    const editorNode = nodes.find(n=>n.id===editingNodeId)
    if(!editorNode){ setAutocompleteItems([]); return }

    const candidates = []
    const wfTasks = (workflow && workflow.Tasks) || (workflow && workflow.tasks) || null

    const collectPaths = (v, prefix, depth)=>{
      const out = []
      if(depth <= 0) return out
      if(v && typeof v === 'object' && !Array.isArray(v)){
        Object.keys(v).forEach(k=>{
          const p = prefix ? `${prefix}.${k}` : k
          out.push(p)
          const child = collectPaths(v[k], p, depth-1)
          out.push(...child)
        })
      }
      return out
    }

    nodes.filter(n=>n.id!==editorNode.id).forEach(n=>{
      const name = n.data?.taskName || n.data?.agentId || n.data?.name || n.id
      const baseInsert = `"${name}".output`
      const item = { id: n.id, label: name, insert: baseInsert, fields: [] }

      if(wfTasks){
        // try to find matching task by name or id
        const t = wfTasks.find(t=> (t.Name && (t.Name === name)) || (t.TaskID && t.TaskID === name) || (t.task_id && t.task_id === name))
        if(t && t.Output){
          // collect up to 2 levels of nested keys
          const paths = collectPaths(t.Output, '', 2)
          item.fields = paths.map(p=> ({ path: p, insert: `${baseInsert}.${p}` }))
        }
      }
      candidates.push(item)
    })

    setAutocompleteItems(candidates)
  }, [nodes, editingNodeId, workflow])

  const insertSuggestionAtCursor = (text)=>{
    const input = autocompleteInputRef.current
    if(!input) return
    const start = input.selectionStart || 0
    const end = input.selectionEnd || 0
    const val = input.value || ''
    const next = val.slice(0, start) + text + val.slice(end)

    // update the appropriate field on the editing node depending on
    // which input the user is focused in (condition vs loop.condition).
    setNodes(ns=> ns.map(n=> {
      if(n.id!==editingNodeId) return n
      const nd = { ...n }
      if(!nd.data) nd.data = {}
      if(editingField === 'loop.condition'){
        const cfg = { ...(nd.data.config||{}) }
        const loop = { ...(cfg.loop||{}) }
        loop.condition = next
        cfg.loop = loop
        nd.data.config = cfg
      } else {
        // default to top-level condition
        nd.data = { ...(nd.data||{}), condition: next }
      }
      return nd
    }))

    setTimeout(()=>{ try{ input.focus(); const pos = start + text.length; input.setSelectionRange(pos, pos) }catch(err){} }, 0)
  }

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

    // keyboard: hold space to pan (optional)
    useEffect(()=>{
      const down = (e)=>{ if(e.code === 'Space') setIsSpaceDown(true) }
      const up = (e)=>{ if(e.code === 'Space') setIsSpaceDown(false) }
      window.addEventListener('keydown', down)
      window.addEventListener('keyup', up)
      return ()=>{ window.removeEventListener('keydown', down); window.removeEventListener('keyup', up) }
    }, [])

    const clamp = (v, a, b) => Math.max(a, Math.min(b, v))
    const zoomAtPoint = (newScale, screenX, screenY)=>{
      if(!containerRef.current) return
      const crect = containerRef.current.getBoundingClientRect()
      const mouseX = screenX - crect.left
      const mouseY = screenY - crect.top
      const prevScale = scale
      const s = clamp(newScale, 0.25, 3)
      const nx = mouseX - ((mouseX - offset.x) / prevScale) * s
      const ny = mouseY - ((mouseY - offset.y) / prevScale) * s
      setScale(s)
      setOffset({ x: nx, y: ny })
      setTimeout(()=>recomputeRects(), 50)
    }

    const handleWheel = (e)=>{
      // require ctrl/meta for wheel zoom to avoid blocking page scroll
      if(!(e.ctrlKey || e.metaKey)) return
      e.preventDefault()
      const factor = Math.exp(-e.deltaY * 0.0015)
      zoomAtPoint(scale * factor, e.clientX, e.clientY)
    }

  const onDrop = (e)=>{
    e.preventDefault()
    const data = e.dataTransfer.getData('application/json')
    if(!data) return
    try{
      const agent = JSON.parse(data)
      const rect = e.currentTarget.getBoundingClientRect()
      // convert screen coords to logical canvas coordinates accounting for pan/zoom
      const x = (e.clientX - rect.left - offset.x) / scale
      const y = (e.clientY - rect.top - offset.y) / scale
      const node = {
        id: Date.now().toString(),
        // preserve any custom flags from the palette agent (e.g. onlyOut)
        data: {
          ...agent,
          agentId: agent.agent_id || agent.AgentID || agent.agentId,
          image: agent.image,
          endpoint: agent.endpoint || agent.image || '',
          method: agent.method || 'POST',
          headers: agent.headers || {},
          taskName: agent.name || agent.agent_id,
          // config is used as the task input when generating the server workflow
          config: agent.config || {},
          type: agent.type || agent.Type || 'docker',
          capabilities: agent.capabilities || agent.Capabilities || []
        },
        x, y,
      }
      // If this is a Start-type agent, pre-generate a webhook token and
      // provide a visible provisional URL so the dashboard shows it before
      // the workflow is started. The server will reuse the token and rewrite
      // the final path to include the real workflow ID when the run is started.
      if((agent.type === 'start') || (agent.agent_id === 'start') || (agent.AgentID === 'start')){
        const token = (typeof crypto !== 'undefined' && crypto.randomUUID) ? crypto.randomUUID() : (Date.now().toString() + '-' + Math.random().toString(36).slice(2))
        const path = `/api/v1/webhooks/draft/${node.id}/${token}`
        const base = (typeof window !== 'undefined' && window.location && window.location.origin) ? window.location.origin : ''
        const full = base + path
        // ensure config exists and store the provisional webhook metadata
        node.data.config = {...(node.data.config||{}), _webhook_token: token, _webhook_path: path, _webhook_url: full, _webhook_method: 'POST'}
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
    const startX = (clientX - crect.left - offset.x) / scale
    const startY = (clientY - crect.top - offset.y) / scale
    const n = nodes.find(n=>n.id===nodeId)
    if(!n) return
    setMoving({ id: nodeId, startX, startY, origX: n.x, origY: n.y })
  }

  useEffect(()=>{
    if(!moving) return
    const onMove = (ev)=>{
      if(!containerRef.current) return
      const crect = containerRef.current.getBoundingClientRect()
      const clientX = ev.clientX || (ev.touches && ev.touches[0] && ev.touches[0].clientX)
      const clientY = ev.clientY || (ev.touches && ev.touches[0] && ev.touches[0].clientY)
      const px = clientX - crect.left
      const py = clientY - crect.top
      const pxLogical = (px - offset.x) / scale
      const pyLogical = (py - offset.y) / scale
      const dx = pxLogical - moving.startX
      const dy = pyLogical - moving.startY
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
  const startDragFrom = (sourceId, portKey, clientX, clientY)=>{
    if(!containerRef.current) return
    const crect = containerRef.current.getBoundingClientRect()
    setDragging({ source: sourceId, port: portKey, x: clientX - crect.left, y: clientY - crect.top })
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
        // compute preferred ports for visual connection
        const srect = nodeRects[dragging.source]
        const trect = nodeRects[targetId]
        // source port: prefer dragging.port if set, otherwise decide left/right
        const sourcePort = dragging.port || (srect && trect && srect.cx < trect.cx ? 'right' : 'left')
        // target will connect to left side by default
        const targetPort = (srect && trect && srect.cx < trect.cx) ? 'left' : 'left'
        const edge = { id: Date.now().toString(), source: dragging.source, target: targetId, sourcePort, targetPort }
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
      <div ref={containerRef} onDragOver={(e)=>e.preventDefault()} onDrop={onDrop} onWheel={handleWheel} className="h-[60vh] bg-slate-100 p-4 relative">

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
            // determine source port coordinates
            const sp = e.sourcePort || 'right'
            let sx, sy
            if(sp.startsWith('left')){ sx = s.left - PORT_OUTER_OFFSET; sy = s.cy }
            else if(sp === 'right-if' || sp === 'right-true' || sp === 'right-then'){ sx = s.left + s.width + PORT_OUTER_OFFSET; sy = s.cy - PORT_GAP }
            else if(sp === 'right-else' || sp === 'right-false'){ sx = s.left + s.width + PORT_OUTER_OFFSET; sy = s.cy + PORT_GAP }
            else { sx = s.left + s.width + PORT_OUTER_OFFSET; sy = s.cy }

            // target port coords (default to left-center)
            const tp = e.targetPort || 'left'
            const tx = (tp && tp.startsWith('left')) ? (t.left - PORT_OUTER_OFFSET) : (t.left + t.width + PORT_OUTER_OFFSET)
            const ty = t.cy

            // style: dashed for 'else' outputs
            const isElse = (sp && sp.includes('else')) || (sp && sp.includes('false'))
            return <line key={e.id} x1={sx} y1={sy} x2={tx} y2={ty} stroke="#0f172a" strokeWidth={2} strokeDasharray={isElse ? '6' : ''} markerEnd="url(#arrowhead)" />
          })}
          {dragging && (()=>{
            const s = nodeRects[dragging.source]
            if(!s) return null
            const pk = dragging.port || 'right'
            let sx, sy
            if(pk.startsWith('left')){ sx = s.left - PORT_OUTER_OFFSET; sy = s.cy }
            else if(pk === 'right-if' || pk === 'right-true' || pk === 'right-then'){ sx = s.left + s.width + PORT_OUTER_OFFSET; sy = s.cy - PORT_GAP }
            else if(pk === 'right-else' || pk === 'right-false'){ sx = s.left + s.width + PORT_OUTER_OFFSET; sy = s.cy + PORT_GAP }
            else { sx = s.left + s.width + PORT_OUTER_OFFSET; sy = s.cy }
            const isElse = (pk && (pk.includes('else') || pk.includes('false')))
            return <line x1={sx} y1={sy} x2={dragging.x} y2={dragging.y} stroke="#0f172a" strokeWidth={2} strokeDasharray={isElse ? '6' : ''} markerEnd="url(#arrowhead)" />
          })()}
        </svg>

        <div className="absolute left-0 top-0 w-full h-full" style={{ transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})`, transformOrigin: '0 0' }}>
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
                className={`w-44 node-card cursor-pointer ${selectedNodeId===n.id ? 'node-selected' : ''} ${failed? 'node-failed' : ''}`}>
                <div className="node-accent" />

                <div style={{position:'absolute', right:8, top:8}}>
                  <button onClick={(e)=>{ e.stopPropagation(); setEditingNodeId(n.id) }} className="w-6 h-6 rounded-full bg-white border text-xs flex items-center justify-center">i</button>
                </div>

                <div className="node-inner">
                  <div>
                    <div className="node-title">{n.data?.taskName || n.data?.agentId}</div>
                    <div className="text-xs text-muted mt-1">{n.data?.image || n.data?.endpoint}</div>
                  </div>
                </div>
              </div>
          )
          })}
          {nodes.length===0 && (
            <div className="text-sm text-slate-500">Drag agents from the palette to create nodes.</div>
          )}
        </div>

        {/* Ports overlay: rendered on top of nodes, outside node DOM so dragging doesn't move the node */}
        {Object.entries(nodeRects).map(([id, r]) => {
          const node = nodes.find(x => x.id === id)
          if(!node || !r) return null
          const size = 20
          const inX = r.left - PORT_OUTER_OFFSET - (size/2)
          const inY = r.cy - (size/2)
          const outCenterX = r.left + r.width + PORT_OUTER_OFFSET - (size/2)
          const outTopY = r.cy - PORT_GAP - (size/2)
          const outBottomY = r.cy + PORT_GAP - (size/2)

          const isConditional = (node.data && (node.data.type === 'conditional' || node.data.type === 'if'))

          return (
            <div key={"ports-"+id}>
              { !node.data?.onlyOut && (
                <div onPointerDown={(e)=>{ e.stopPropagation(); startDragFrom(id, 'left', e.clientX, e.clientY) }}
                     style={{ position: 'absolute', left: inX, top: inY, width: size, height: size, zIndex: 60, cursor: 'grab' }}
                     className="node-port in" />
              ) }

              { isConditional ? (
                <>
                  {/* conditional: two outs (if / else) */}
                  <div onPointerDown={(e)=>{ e.stopPropagation(); startDragFrom(id, 'right-if', e.clientX, e.clientY) }}
                       style={{ position: 'absolute', left: outCenterX, top: outTopY, width: size, height: size, zIndex: 60, cursor: 'grab' }}
                       className="node-port out" title="If (true)" />
                  <div style={{ position: 'absolute', left: outCenterX + size + 6, top: outTopY + 2, fontSize: 11, color: 'var(--muted)' }}>If</div>

                  <div onPointerDown={(e)=>{ e.stopPropagation(); startDragFrom(id, 'right-else', e.clientX, e.clientY) }}
                       style={{ position: 'absolute', left: outCenterX, top: outBottomY, width: size, height: size, zIndex: 60, cursor: 'grab' }}
                       className="node-port out" title="Else (false)" />
                  <div style={{ position: 'absolute', left: outCenterX + size + 6, top: outBottomY + 2, fontSize: 11, color: 'var(--muted)' }}>Else</div>
                </>
              ) : (
                !node.data?.onlyIn && (
                  <div onPointerDown={(e)=>{ e.stopPropagation(); startDragFrom(id, 'right', e.clientX, e.clientY) }}
                       style={{ position: 'absolute', left: outCenterX, top: inY, width: size, height: size, zIndex: 60, cursor: 'grab' }}
                       className="node-port out" />
                )
              )}
            </div>
          )
        })}

        {/* Zoom controls */}
        <div className="absolute right-6 z-50 flex flex-col gap-2" style={{ top: 12 }}>
          <button onClick={()=>{ const rect = containerRef.current?.getBoundingClientRect(); if(rect) zoomAtPoint(scale*1.15, rect.left + rect.width/2, rect.top + rect.height/2) }} className="bg-white border p-2 rounded">+</button>
          <button onClick={()=>{ const rect = containerRef.current?.getBoundingClientRect(); if(rect) zoomAtPoint(scale/1.15, rect.left + rect.width/2, rect.top + rect.height/2) }} className="bg-white border p-2 rounded">−</button>
          <button onClick={()=>{ setScale(1); setOffset({x:0,y:0}); setTimeout(()=>recomputeRects(),50) }} className="bg-white border p-2 rounded">Reset</button>
        </div>
      
          {/* Node editor panel */}
          {editingNodeId && (()=>{
            const node = nodes.find(x=>x.id===editingNodeId)
            if(!node) return null
            const isStart = node.data?.type === 'start'
            const isLoop = node.data?.type === 'loop'
            const isConditional = node.data?.type === 'conditional' || node.data?.type === 'if'
            // Determine any assigned webhook for display. Prefer any
            // pre-assigned webhook on the node itself (config or input),
            // otherwise fall back to the persisted server-side workflow tasks.
            let assignedWebhook = ''
            if(node.data?.config && (node.data.config._webhook_url || node.data.config._webhookPath || node.data.config._webhook_path)){
              assignedWebhook = node.data.config._webhook_url || node.data.config._webhookPath || node.data.config._webhook_path || ''
            } else if(node.data?.input && (node.data.input._webhook_url || node.data.input._webhook_path)){
              assignedWebhook = node.data.input._webhook_url || node.data.input._webhook_path || ''
            } else {
              const wfTasksList = (workflow && (workflow.Tasks || workflow.tasks)) || null
              if(wfTasksList && Array.isArray(wfTasksList)){
                const found = wfTasksList.find(t => (t.TaskID && t.TaskID === node.id) || (t.task_id && t.task_id === node.id) || (t.taskId && t.taskId === node.id) || (t.taskID && t.taskID === node.id))
                if(found && found.Input){
                  assignedWebhook = (found.Input["_webhook_url"] || found.Input._webhook_url || '')
                }
              }
            }
            return (
              <div style={{position:'absolute', right:8, top:8, width:360}} className="bg-white border rounded p-3 shadow">
                <div className="flex items-center justify-between mb-2">
                  <div className="font-medium">{isStart ? 'Edit Start Node' : 'Edit Node'}</div>
                  <div className="text-xs text-slate-500">id: {node.id}</div>
                </div>

                {isStart ? (
                  <>
                    { assignedWebhook ? (
                      <>
                        <label className="text-xs">Assigned Webhook</label>
                        <div className="flex gap-2 mb-2">
                          <input readOnly className="w-full p-1 border rounded bg-slate-100 text-xs" value={assignedWebhook} />
                          <button onClick={()=>{ try{ navigator.clipboard.writeText(assignedWebhook); alert('Copied webhook URL') }catch(err){}}} className="px-3 py-1 bg-white border rounded text-xs">Copy</button>
                        </div>
                      </>
                    ) : (
                      <div className="text-xs text-slate-500 mb-2">No webhook assigned — start the workflow to generate one</div>
                    )}

                    <label className="text-xs">Webhook URL / Path</label>
                    <input className="w-full mb-2 p-1 border rounded" value={node.data.endpoint || ''} onChange={(e)=>{
                      const v = e.target.value
                      setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, endpoint: v}}) : n))
                    }} />

                    <label className="text-xs">Authorization</label>
                    <select className="w-full mb-2 p-1 border rounded" value={(node.data.auth && node.data.auth.type) || 'none'} onChange={(e)=>{
                      const t = e.target.value
                      setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, auth: (n.data.auth||{}), }}) : n))
                      // update type separately to avoid mutating
                      setNodes(ns=>ns.map(n=> {
                        if(n.id!==node.id) return n
                        const auth = {...(n.data.auth||{}), type: t}
                        return {...n, data: {...n.data, auth}}
                      }))
                    }}>
                      <option value="none">None</option>
                      <option value="bearer">Bearer Token</option>
                      <option value="basic">Basic Auth</option>
                      <option value="custom">Custom JSON</option>
                    </select>

                    {/* conditional auth fields */}
                    {node.data.auth && node.data.auth.type === 'bearer' && (
                      <>
                        <label className="text-xs">Token</label>
                        <input className="w-full mb-2 p-1 border rounded" value={node.data.auth.token || ''} onChange={(e)=>{
                          const v = e.target.value
                          setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, auth: {...(n.data.auth||{}), token: v}}}) : n))
                        }} />
                      </>
                    )}
                    {node.data.auth && node.data.auth.type === 'basic' && (
                      <>
                        <label className="text-xs">Username</label>
                        <input className="w-full mb-2 p-1 border rounded" value={node.data.auth.username || ''} onChange={(e)=>{
                          const v = e.target.value
                          setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, auth: {...(n.data.auth||{}), username: v}}}) : n))
                        }} />
                        <label className="text-xs">Password</label>
                        <input type="password" className="w-full mb-2 p-1 border rounded" value={node.data.auth.password || ''} onChange={(e)=>{
                          const v = e.target.value
                          setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, auth: {...(n.data.auth||{}), password: v}}}) : n))
                        }} />
                      </>
                    )}
                    {node.data.auth && node.data.auth.type === 'custom' && (
                      <>
                        <label className="text-xs">Auth JSON</label>
                        <textarea rows={4} className="w-full mb-2 p-1 border rounded font-mono text-xs" value={JSON.stringify(node.data.auth?.data || node.data.auth || {}, null, 2)} onChange={(e)=>{
                          try{ const v = JSON.parse(e.target.value); setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, auth: {...(n.data.auth||{}), data: v}}}) : n)) }catch(err){}
                        }} />
                      </>
                    )}

                    <label className="text-xs">Schema Mapper (JSON)</label>
                    <textarea rows={6} className="w-full mb-2 p-1 border rounded font-mono text-xs" value={JSON.stringify(node.data.schemaMapper||{}, null, 2)} onChange={(e)=>{
                      try{ const v = JSON.parse(e.target.value); setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, schemaMapper: v}}) : n)) }catch(err){}
                    }} />

                    <label className="text-xs">Config (JSON)</label>
                    <textarea rows={4} className="w-full mb-2 p-1 border rounded font-mono text-xs" value={JSON.stringify(node.data.config||{}, null, 2)} onChange={(e)=>{
                      try{ const v = JSON.parse(e.target.value); setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, config: v}}) : n)) }catch(err){}
                    }} />

                    <div className="flex gap-2">
                      <button onClick={()=>{ notify(nodes, edges); setEditingNodeId(null); recomputeRects() }} className="px-3 py-1 bg-primary text-white rounded">Save</button>
                      <button onClick={()=>{ if(confirm('Delete Start node?')){ setNodes(ns=>ns.filter(x=>x.id!==node.id)); setEdges(es=>es.filter(e=>e.source!==node.id && e.target!==node.id)); notify(nodes.filter(x=>x.id!==node.id), edges.filter(e=>e.source!==node.id && e.target!==node.id)); setEditingNodeId(null); recomputeRects() } }} className="px-3 py-1 bg-red-600 text-white rounded">Delete</button>
                      <button onClick={()=>{ setEditingNodeId(null) }} className="px-3 py-1 bg-amber-200 rounded">Close</button>
                    </div>
                  </>
                ) : isLoop ? (
                  <>
                    <label className="text-xs">Loop Mode</label>
                    <select className="w-full mb-2 p-1 border rounded" value={(node.data?.config?.loop?.mode) || 'count'} onChange={(e)=>{
                      const m = e.target.value
                      setNodes(ns=>ns.map(n=> {
                        if(n.id!==node.id) return n
                        const cfg = {...(n.data.config||{})}
                        cfg.loop = {...(cfg.loop||{}), mode: m}
                        return {...n, data: {...n.data, config: cfg}}
                      }))
                    }}>
                      <option value="count">Counter</option>
                      <option value="condition">Condition</option>
                    </select>

                    { ((node.data?.config?.loop?.mode) || 'count') === 'count' ? (
                      <>
                        <label className="text-xs">Count</label>
                        <input type="number" min={1} className="w-full mb-2 p-1 border rounded" value={(node.data?.config?.loop?.count) || 1} onChange={(e)=>{
                          const v = parseInt(e.target.value) || 0
                          setNodes(ns=>ns.map(n=> {
                            if(n.id!==node.id) return n
                            const cfg = {...(n.data.config||{})}
                            cfg.loop = {...(cfg.loop||{}), count: v}
                            return {...n, data: {...n.data, config: cfg}}
                          }))
                        }} />
                      </>
                    ) : (
                      <>
                        <label className="text-xs">Condition (expression)</label>
                        <div style={{position:'relative'}}>
                          <input ref={el=>{ if(el && document.activeElement===el) autocompleteInputRef.current = el }} className="w-full mb-2 p-1 border rounded" value={(node.data?.config?.loop?.condition) || ''} onFocus={(e)=>{ autocompleteInputRef.current = e.target; setAutocompleteOpen(true); setAutocompleteQuery(''); setEditingField('loop.condition') }} onBlur={()=>{ setTimeout(()=>{ setAutocompleteOpen(false); setEditingField(null) }, 150) }} onChange={(e)=>{
                            const v = e.target.value
                            setNodes(ns=>ns.map(n=> {
                              if(n.id!==node.id) return n
                              const cfg = {...(n.data.config||{})}
                              cfg.loop = {...(cfg.loop||{}), condition: v}
                              return {...n, data: {...n.data, config: cfg}}
                            }))
                            setAutocompleteQuery(v || '')
                          }} onKeyDown={(e)=>{ if(e.key === 'Escape') setAutocompleteOpen(false) }} />

                          { autocompleteOpen && editingNodeId===node.id && editingField === 'loop.condition' && (
                            <div className="absolute left-0 top-full mt-1 w-full bg-white border rounded shadow z-50" style={{maxHeight:220, overflowY:'auto'}}>
                              {autocompleteItems.filter(it=> it.label.toLowerCase().includes((autocompleteQuery||'').toLowerCase())).map(item=> (
                                <div key={item.id} className="p-2 border-b last:border-b-0">
                                  <div className="flex items-center justify-between">
                                    <div className="font-medium">{item.label}</div>
                                    <button className="text-xs text-slate-500" onMouseDown={(ev)=>{ ev.preventDefault(); insertSuggestionAtCursor(item.insert); setAutocompleteOpen(false) }}>
                                      Insert base
                                    </button>
                                  </div>
                                  <div className="text-xs text-slate-500 mb-2">"{item.label}".output</div>
                                  {item.fields && item.fields.length>0 && (
                                    <div className="grid grid-cols-1 gap-1">
                                      {item.fields.map(f => (
                                        <div key={f.path} className="text-xs p-1 rounded hover:bg-slate-100 cursor-pointer" onMouseDown={(ev)=>{ ev.preventDefault(); insertSuggestionAtCursor(f.insert); setAutocompleteOpen(false) }}>
                                          <strong className="mr-2">{f.path.split('.').slice(-1)[0]}</strong>
                                          <span className="text-slate-500">{`"${item.label}".output.${f.path}`}</span>
                                        </div>
                                      ))}
                                    </div>
                                  )}
                                </div>
                              ))}
                              {autocompleteItems.length===0 && (
                                <div className="p-2 text-xs text-slate-500">No previous actions available</div>
                              )}
                            </div>
                          ) }
                        </div>
                      </>
                    )}

                    <label className="text-xs">Config (JSON)</label>
                    <textarea rows={4} className="w-full mb-2 p-1 border rounded font-mono text-xs" value={JSON.stringify(node.data.config||{}, null, 2)} onChange={(e)=>{
                      try{ const v = JSON.parse(e.target.value); setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, config: v}}) : n)) }catch(err){}
                    }} />

                    <div className="flex gap-2">
                      <button onClick={()=>{ notify(nodes, edges); setEditingNodeId(null); recomputeRects() }} className="px-3 py-1 bg-primary text-white rounded">Save</button>
                      <button onClick={()=>{ setNodes(ns=>ns.filter(x=>x.id!==node.id)); setEdges(es=>es.filter(e=>e.source!==node.id && e.target!==node.id)); notify(nodes.filter(x=>x.id!==node.id), edges.filter(e=>e.source!==node.id && e.target!==node.id)); setEditingNodeId(null); recomputeRects() }} className="px-3 py-1 bg-red-600 text-white rounded">Delete</button>
                      <button onClick={()=>setEditingNodeId(null)} className="px-3 py-1 bg-amber-200 rounded">Close</button>
                    </div>
                  </>
                ) : isConditional ? (
                  <>
                    <label className="text-xs">Condition (expression)</label>
                    <div style={{position:'relative'}}>
                      <input ref={el=>{ if(el && document.activeElement===el) autocompleteInputRef.current = el }} className="w-full mb-2 p-1 border rounded" value={node.data.condition || ''} onFocus={(e)=>{ autocompleteInputRef.current = e.target; setAutocompleteOpen(true); setAutocompleteQuery(''); setEditingField('condition') }} onBlur={()=>{ setTimeout(()=>{ setAutocompleteOpen(false); setEditingField(null) }, 150) }} onChange={(e)=>{
                        const v = e.target.value
                        setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, condition: v}}) : n))
                        setAutocompleteQuery(v || '')
                      }} onKeyDown={(e)=>{ if(e.key === 'Escape') setAutocompleteOpen(false) }} />

                      { autocompleteOpen && editingNodeId===node.id && editingField === 'condition' && (
                        <div className="absolute left-0 top-full mt-1 w-full bg-white border rounded shadow z-50" style={{maxHeight:220, overflowY:'auto'}}>
                          {autocompleteItems.filter(it=> it.label.toLowerCase().includes((autocompleteQuery||'').toLowerCase())).map(item=> (
                            <div key={item.id} className="p-2 border-b last:border-b-0">
                              <div className="flex items-center justify-between">
                                <div className="font-medium">{item.label}</div>
                                <button className="text-xs text-slate-500" onMouseDown={(ev)=>{ ev.preventDefault(); insertSuggestionAtCursor(item.insert); setAutocompleteOpen(false) }}>
                                  Insert base
                                </button>
                              </div>
                              <div className="text-xs text-slate-500 mb-2">"{item.label}".output</div>
                              {item.fields && item.fields.length>0 && (
                                <div className="grid grid-cols-1 gap-1">
                                  {item.fields.map(f => (
                                    <div key={f.path} className="text-xs p-1 rounded hover:bg-slate-100 cursor-pointer" onMouseDown={(ev)=>{ ev.preventDefault(); insertSuggestionAtCursor(f.insert); setAutocompleteOpen(false) }}>
                                      <strong className="mr-2">{f.path.split('.').slice(-1)[0]}</strong>
                                      <span className="text-slate-500">{`"${item.label}".output.${f.path}`}</span>
                                    </div>
                                  ))}
                                </div>
                              )}
                            </div>
                          ))}
                          {autocompleteItems.length===0 && (
                            <div className="p-2 text-xs text-slate-500">No previous actions available</div>
                          )}
                        </div>
                      ) }
                    </div>

                    <label className="text-xs">True branch label</label>
                    <input className="w-full mb-2 p-1 border rounded" value={node.data.trueLabel || 'If'} onChange={(e)=>{
                      const v = e.target.value
                      setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, trueLabel: v}}) : n))
                    }} />

                    <label className="text-xs">False branch label</label>
                    <input className="w-full mb-2 p-1 border rounded" value={node.data.falseLabel || 'Else'} onChange={(e)=>{
                      const v = e.target.value
                      setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, falseLabel: v}}) : n))
                    }} />

                    <label className="text-xs">Config (JSON)</label>
                    <textarea rows={4} className="w-full mb-2 p-1 border rounded font-mono text-xs" value={JSON.stringify(node.data.config||{}, null, 2)} onChange={(e)=>{
                      try{ const v = JSON.parse(e.target.value); setNodes(ns=>ns.map(n=> n.id===node.id ? ({...n, data: {...n.data, config: v}}) : n)) }catch(err){}
                    }} />

                    <div className="flex gap-2">
                      <button onClick={()=>{ notify(nodes, edges); setEditingNodeId(null); recomputeRects() }} className="px-3 py-1 bg-primary text-white rounded">Save</button>
                      <button onClick={()=>{ setNodes(ns=>ns.filter(x=>x.id!==node.id)); setEdges(es=>es.filter(e=>e.source!==node.id && e.target!==node.id)); notify(nodes.filter(x=>x.id!==node.id), edges.filter(e=>e.source!==node.id && e.target!==node.id)); setEditingNodeId(null); recomputeRects() }} className="px-3 py-1 bg-red-600 text-white rounded">Delete</button>
                      <button onClick={()=>setEditingNodeId(null)} className="px-3 py-1 bg-amber-200 rounded">Close</button>
                    </div>
                  </>
                ) : (
                  <>
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
                      <button onClick={()=>{ notify(nodes, edges); setEditingNodeId(null); recomputeRects() }} className="px-3 py-1 bg-primary text-white rounded">Save</button>
                      <button onClick={()=>{ setNodes(ns=>ns.filter(x=>x.id!==node.id)); setEdges(es=>es.filter(e=>e.source!==node.id && e.target!==node.id)); notify(nodes.filter(x=>x.id!==node.id), edges.filter(e=>e.source!==node.id && e.target!==node.id)); setEditingNodeId(null); recomputeRects() }} className="px-3 py-1 bg-red-600 text-white rounded">Delete</button>
                      <button onClick={()=>setEditingNodeId(null)} className="px-3 py-1 bg-amber-200 rounded">Close</button>
                    </div>
                  </>
                )}
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
