import { useState } from 'react'

export default function NodeCanvas({ initialNodes, onChange, activeTaskId, failedTaskErrors }){
  const [nodes, setNodes] = useState(initialNodes || [])
  const [edges, setEdges] = useState([])
  const [selection, setSelection] = useState(null)

  const notify = (ns, es) => {
    if(onChange) onChange({ nodes: ns, edges: es })
  }

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
          taskName: agent.name || agent.agent_id,
          config: agent.config || {}
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

  const onNodeClick = (node)=>{
    if(!selection){
      setSelection(node.id)
      return
    }
    if(selection === node.id){
      setSelection(null)
      return
    }
    // create edge from selection -> node
    const edge = { id: Date.now().toString(), source: selection, target: node.id }
    setEdges(es => {
      const next = [...es, edge]
      notify(nodes, next)
      return next
    })
    setSelection(null)
  }

  return (
    <div className="flex-1">
      <div onDragOver={(e)=>e.preventDefault()} onDrop={onDrop} className="h-[60vh] bg-slate-100 p-4 relative">
        {nodes.map(n=> {
          const isActive = activeTaskId && (n.id === activeTaskId || n.data?.taskId === activeTaskId)
          const failed = failedTaskErrors && (failedTaskErrors[n.id] || failedTaskErrors[n.data?.taskId])
          return (
            <div key={n.id}
                 onClick={()=>onNodeClick(n)}
                 title={failed ? (failed || 'error') : ''}
                 style={{position:'absolute', left:n.x, top:n.y}}
                 className={`w-40 p-2 bg-white rounded shadow cursor-pointer border ${isActive? 'border-emerald-600': ''} ${failed? 'border-red-600': 'border-slate-200'}`}>
              <div className="font-semibold text-sm">{n.data?.taskName || n.data?.agentId}</div>
              <div className="text-xs text-slate-500">{n.data?.image}</div>
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
      </div>

      <div className="mt-2 bg-white p-2 rounded border">
        <div className="text-sm text-slate-600">Connect nodes: click source node, then click target node.</div>
        <div className="mt-2 text-sm">
          {edges.length===0 ? (<div className="text-slate-500">No edges</div>) : (
            edges.map(e => (<div key={e.id} className="text-xs">{e.source} → {e.target}</div>))
          )}
        </div>
      </div>
    </div>
  )
}
