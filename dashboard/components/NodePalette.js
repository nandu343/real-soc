import { useEffect, useState } from 'react'

export default function NodePalette(){
  const [agents, setAgents] = useState([])

  useEffect(()=>{
    async function load(){
      try{
        const r = await fetch('/api/agents')
        if(!r.ok) return
        const data = await r.json()
        setAgents(data)
      }catch(err){
        // ignore
      }
    }
    load()
  },[])

  return (
    <aside className="w-64 border-r p-4 bg-white">
      <h3 className="font-semibold mb-2">Node Palette</h3>
      <div className="space-y-2 overflow-auto" style={{maxHeight: '60vh'}}>
        {agents.length===0 && <div className="text-sm text-slate-500">No agents found</div>}
        {agents.map(a=> (
          <div key={a.agent_id}
               draggable
               onDragStart={(e)=>{ e.dataTransfer.setData('application/json', JSON.stringify(a)); e.dataTransfer.effectAllowed='copy'; }}
               className="p-2 border rounded cursor-grab bg-slate-50 hover:bg-slate-100">
            <div className="font-medium">{a.agent_id}</div>
            <div className="text-xs text-slate-500 truncate">{(a.capabilities||[]).join(', ')}</div>
          </div>
        ))}
      </div>
    </aside>
  )
}
