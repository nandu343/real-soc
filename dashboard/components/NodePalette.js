import { useEffect, useState } from 'react'

const STORAGE_KEY = 'real-soc:local_agents'

export default function NodePalette(){
  const [agents, setAgents] = useState([])
  const [localAgents, setLocalAgents] = useState([])
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ agent_id: '', image: '', capabilities: '', type: 'docker', endpoint: '', auth: '' })
  const [repoUrl, setRepoUrl] = useState('')
  const [importLoading, setImportLoading] = useState(false)
  const [importError, setImportError] = useState(null)
  const [collapsed, setCollapsed] = useState(false)

  useEffect(()=>{
    async function load(){
      try{
        const r = await fetch('/api/agents')
        if(!r.ok) return
        const data = await r.json()
        setAgents(Array.isArray(data) ? data : [])
      }catch(err){ /* ignore */ }
    }
    load()
  },[])

  useEffect(()=>{
    try{
      const raw = localStorage.getItem(STORAGE_KEY) || '[]'
      const parsed = JSON.parse(raw)
      setLocalAgents(Array.isArray(parsed) ? parsed : [])
    }catch(err){ setLocalAgents([]) }
  },[])

  const saveLocal = (arr)=>{ localStorage.setItem(STORAGE_KEY, JSON.stringify(arr)); setLocalAgents(arr) }

  const createAgent = ()=>{
    const id = (form.agent_id || '').trim()
    if(!id){ alert('agent_id is required'); return }
    const exists = localAgents.find(a=>a.agent_id === id) || agents.find(a=>a.agent_id === id)
    if(exists){ if(!confirm('An agent with this id already exists. Create anyway?')) return }
    const obj = {
      agent_id: id,
      image: form.image || '',
      endpoint: form.endpoint || '',
      auth: form.auth ? (function(){ try{ return JSON.parse(form.auth) }catch(e){ return {} } })() : {},
      capabilities: (form.capabilities || '').split(',').map(s=>s.trim()).filter(Boolean),
      type: form.type || 'docker'
    }
    const next = [obj, ...localAgents]
    saveLocal(next)
    setForm({ agent_id: '', image: '', capabilities: '', type: 'docker', endpoint: '', auth: '' })
    setShowCreate(false)
  }

  const addHumanQuick = ()=>{
    const id = 'human-analyst-'+Date.now().toString().slice(-6)
    const obj = { agent_id: id, image: '', capabilities: ['human'], type: 'human' }
    saveLocal([obj, ...localAgents])
    alert('Added human agent: '+id)
  }

  const addRemoteQuick = ()=>{
    const url = prompt('Remote agent endpoint (e.g. https://host:8080/run)')
    if(!url) return
    const id = 'remote-'+Date.now().toString().slice(-6)
    const obj = { agent_id: id, image: '', endpoint: url, auth: {}, capabilities: ['remote'], type: 'remote' }
    saveLocal([obj, ...localAgents])
    alert('Added remote agent: '+id)
  }

  const deleteLocal = (id)=>{
    if(!confirm('Delete local agent '+id+'?')) return
    const next = localAgents.filter(a=>a.agent_id !== id)
    saveLocal(next)
  }

  // Merge local + remote and dedupe by agent_id (local wins)
  const combined = (() => {
    const seen = new Set()
    const out = []
    ;[...localAgents, ...(agents || [])].forEach(a => {
      const id = a && (a.agent_id || a.AgentID || a.agentId)
      if (!id) return
      if (!seen.has(id)) { seen.add(id); out.push(a) }
    })
    return out
  })()

  return (
    <aside className={`border-r p-3 ${collapsed ? 'w-16' : 'w-72'} bg-amber-50 border-amber-200 transition-all`}>
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2">
          <button onClick={()=>setCollapsed(!collapsed)} className="text-sm px-2 py-1 bg-amber-200 border rounded">{collapsed ? '→' : '←'}</button>
          {!collapsed && <h3 className="font-semibold">Node Palette</h3>}
        </div>
        <div className="flex gap-2">
          {!collapsed && <button onClick={()=>setShowCreate(!showCreate)} className="text-xs px-2 py-1 bg-white border rounded">{showCreate? 'Close':'Create'}</button>}
          {!collapsed && <button onClick={addHumanQuick} className="text-xs px-2 py-1 bg-amber-100 border rounded">Add Human</button>}
          {!collapsed && <button onClick={addRemoteQuick} className="text-xs px-2 py-1 bg-indigo-100 border rounded">Add Remote</button>}
        </div>
      </div>

      {showCreate && !collapsed && (
        <div className="mb-3 border rounded p-2 bg-amber-50">
          <input className="w-full mb-2 p-1 border rounded" placeholder="agent_id" value={form.agent_id} onChange={(e)=>setForm({...form, agent_id: e.target.value})} />
          <input className="w-full mb-2 p-1 border rounded" placeholder="image (optional)" value={form.image} onChange={(e)=>setForm({...form, image: e.target.value})} />
          {(form.type === 'api' || form.type === 'remote') && (
            <>
              <input className="w-full mb-2 p-1 border rounded" placeholder="endpoint (https://host:8080/run)" value={form.endpoint || ''} onChange={(e)=>setForm({...form, endpoint: e.target.value})} />
              <textarea rows={3} className="w-full mb-2 p-1 border rounded font-mono text-xs" placeholder='auth JSON (e.g. {"bearer":"TOKEN"})' value={form.auth || ''} onChange={(e)=>setForm({...form, auth: e.target.value})} />
            </>
          )}
          <input className="w-full mb-2 p-1 border rounded" placeholder="capabilities (comma separated)" value={form.capabilities} onChange={(e)=>setForm({...form, capabilities: e.target.value})} />
          <div className="flex gap-2">
            <select value={form.type} onChange={(e)=>setForm({...form, type: e.target.value})} className="p-1 border rounded">
              <option value="docker">docker</option>
              <option value="human">human</option>
              <option value="api">api</option>
              <option value="remote">remote</option>
            </select>
            <button onClick={createAgent} className="px-3 py-1 bg-amber-600 text-white rounded">Create</button>
          </div>
        </div>
      )}

      {!collapsed && (
      <div className="mb-3 border rounded p-2 bg-amber-50">
        <label className="text-xs font-medium">Import from GitHub</label>
        <input className="w-full mb-2 p-1 border rounded" placeholder="https://github.com/owner/repo" value={repoUrl} onChange={(e)=>setRepoUrl(e.target.value)} />
        <div className="flex gap-2">
          <button disabled={importLoading} onClick={async ()=>{
            if(!repoUrl) { setImportError('repo url required'); return }
            setImportLoading(true); setImportError(null)
            try{
              const r = await fetch('/api/agents/import', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ repoUrl }) })
              const j = await r.json()
              if(!r.ok) { setImportError(j?.error || 'import failed') }
              else { alert('Imported agent: '+ (j.agent?.agent_id || 'ok')); const r2 = await fetch('/api/agents'); if(r2.ok){ const arr = await r2.json(); setAgents(Array.isArray(arr)?arr:[]) } }
            }catch(err){ setImportError(String(err)) }
            setImportLoading(false); setRepoUrl('')
          }} className="px-3 py-1 bg-amber-600 text-white rounded">{importLoading ? 'Importing...' : 'Import'}</button>
          <div className="text-xs text-red-600">{importError}</div>
        </div>
      </div>
      )}

      <div className={`space-y-2 overflow-auto transition-all ${collapsed ? 'hidden' : ''}`} style={{maxHeight: '60vh'}}>
        {combined.length===0 && <div className="text-sm text-slate-500">No agents found</div>}
        {combined.map(a=> (
          <div key={a.agent_id}
               draggable
               onDragStart={(e)=>{ e.dataTransfer.setData('application/json', JSON.stringify(a)); e.dataTransfer.effectAllowed='copy'; }}
               className="p-2 border rounded cursor-grab bg-amber-50 hover:bg-amber-100 flex items-center justify-between">
            <div>
              <div className="font-medium">{a.agent_id} {a.type === 'human' ? <span className="text-xs text-amber-600">(human)</span> : null}</div>
              <div className="text-xs text-slate-500 truncate">{(a.capabilities||[]).join(', ')}</div>
            </div>
            {localAgents.find(l=>l.agent_id===a.agent_id) ? (
              <div className="flex flex-col items-end">
                <button onClick={(e)=>{ e.stopPropagation(); deleteLocal(a.agent_id) }} className="text-xs text-red-600">Delete</button>
                <div className="text-xs text-slate-400">Local</div>
              </div>
            ) : null}
          </div>
        ))}
      </div>
    </aside>
  )
}
