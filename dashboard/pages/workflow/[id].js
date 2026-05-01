import { useState, useEffect, useRef } from 'react'
import JsonDiff from '../../components/JsonDiff'
import Link from 'next/link'
import NodePalette from '../../components/NodePalette'
import NodeCanvas from '../../components/NodeCanvas'
import generateWorkflow from '../../utils/generateWorkflow'

export default function WorkflowPage({ workflow, snapshots, apiBase, workflowId }){
  const [question, setQuestion] = useState('')
  const [chatAnswer, setChatAnswer] = useState(null)
  const [loading, setLoading] = useState(false)

  // Canvas state
  const [canvasNodes, setCanvasNodes] = useState([])
  const [canvasEdges, setCanvasEdges] = useState([])
  const [flowName, setFlowName] = useState('')
  const [savedFlows, setSavedFlows] = useState([])
  const [showSaved, setShowSaved] = useState(false)

  // Running workflow being polled for Pulse
  const [runningWorkflowId, setRunningWorkflowId] = useState(null)
  const [activeTaskId, setActiveTaskId] = useState(null)
  const [failedMap, setFailedMap] = useState({})
  const pollRef = useRef(null)

  const latest = workflow || null
  const prev = (snapshots && snapshots.length>1) ? JSON.parse(snapshots[1].state) : null
  const prevPretty = prev ? JSON.stringify(prev, null, 2) : ''
  const latestPretty = latest ? JSON.stringify(latest, null, 2) : ''

  // Review/approve UI state
  const [showEditor, setShowEditor] = useState(false)
  const [editedStateText, setEditedStateText] = useState(latestPretty)
  const [reviewer, setReviewer] = useState('')
  const [comment, setComment] = useState('')
  const [approveLoading, setApproveLoading] = useState(false)
  const [approveResult, setApproveResult] = useState(null)

  // Handle canvas changes from NodeCanvas
  function handleCanvasChange({ nodes, edges }){
    setCanvasNodes(nodes)
    setCanvasEdges(edges)
  }

  // Local flow persistence (localStorage key: real-soc:flows)
  const loadSavedFlows = ()=>{
    try{
      const arr = JSON.parse(localStorage.getItem('real-soc:flows')||'[]')
      setSavedFlows(arr || [])
    }catch(err){ setSavedFlows([]) }
  }

  useEffect(()=>{ if(typeof window !== 'undefined') loadSavedFlows() }, [])

  const saveFlowToLocal = ()=>{
    if(!flowName || flowName.trim()===''){ alert('Please enter a flow name'); return }
    const id = (typeof crypto !== 'undefined' && crypto.randomUUID) ? crypto.randomUUID() : ('flow-'+Date.now())
    const item = { id, name: flowName, nodes: canvasNodes, edges: canvasEdges, createdAt: new Date().toISOString() }
    const existing = JSON.parse(localStorage.getItem('real-soc:flows')||'[]')
    existing.unshift(item)
    localStorage.setItem('real-soc:flows', JSON.stringify(existing))
    setSavedFlows(existing)
    setFlowName('')
    alert('Saved flow')
  }

  const loadFlow = (f)=>{
    if(!f) return
    setCanvasNodes(f.nodes || [])
    setCanvasEdges(f.edges || [])
    setShowSaved(false)
    alert('Loaded flow: '+(f.name||f.id))
  }

  const deleteFlow = (id)=>{
    const remaining = (savedFlows || []).filter(s=>s.id!==id)
    localStorage.setItem('real-soc:flows', JSON.stringify(remaining))
    setSavedFlows(remaining)
  }

  const downloadFlow = (f)=>{
    const blob = new Blob([JSON.stringify(f, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = (f.name || 'flow') + '.json'
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  async function generateAndStart(){
    try{
      const wf = generateWorkflow(canvasNodes, canvasEdges)
      const res = await fetch('/api/start-workflow', {
        method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ initial_state: wf })
      })
      if(!res.ok){
        const txt = await res.text().catch(()=>null)
        alert('start failed: '+ (txt || res.status))
        return
      }
      const data = await res.json()
      const id = data.workflow_id || data.WorkflowID || data.workflowID
      setRunningWorkflowId(id)
      // start polling immediately
      pollOnce(id)
      alert('workflow started: '+id)
    }catch(err){
      alert('start error: '+String(err))
    }
  }

  async function emitSnapshot(snapshot){
    // Start a new workflow run from this snapshot (emit)
    try{
      const state = JSON.parse(snapshot.state)
      const res = await fetch('/api/start-workflow', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ initial_state: state }) })
      if(!res.ok){ alert('emit failed'); return }
      const data = await res.json()
      const id = data.workflow_id || data.WorkflowID || data.workflowID
      setRunningWorkflowId(id)
      pollOnce(id)
      alert('emitted new workflow: '+id)
    }catch(err){ alert('emit error: '+String(err)) }
  }

  async function restoreSnapshot(snapshot){
    try{
      const res = await fetch('/api/restore-snapshot', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ workflowId: workflowId, snapshotId: snapshot.id, reviewer: 'ui' }) })
      if(!res.ok){ alert('restore failed'); return }
      alert('restore signalled')
    }catch(err){ alert('restore error: '+String(err)) }
  }

  // Poll once for active task and failed map
  async function pollOnce(id){
    try{
      const r = await fetch(`/api/query-workflow?id=${encodeURIComponent(id)}`)
      if(!r.ok) return
      const s = await r.json()
      if(!s) return
      const cs = s.current_step ?? s.CurrentStep ?? 0
      let active = null
      if(Array.isArray(s.tasks)){
        const t = s.tasks[cs]
        if(t) active = t.task_id || t.TaskID || null
      }
      setActiveTaskId(active)
      const fm = {}
      if(Array.isArray(s.tasks)){
        s.tasks.forEach(t=>{ const id = t.task_id || t.TaskID; if(t.status === 'failed' || t.error) fm[id] = t.error || 'failed' })
      }
      setFailedMap(fm)
    }catch(err){ /* ignore */ }
  }

  // Polling effect for Pulse
  useEffect(()=>{
    const id = runningWorkflowId || (workflow && (workflow.workflow_id || workflow.WorkflowID || workflow.workflowID))
    if(!id) return
    // clear previous
    if(pollRef.current) clearInterval(pollRef.current)
    pollRef.current = setInterval(()=>pollOnce(id), 3000)
    // initial poll
    pollOnce(id)
    return ()=>{ if(pollRef.current) clearInterval(pollRef.current) }
  }, [runningWorkflowId, workflow])

  async function ask(){
    setLoading(true)
    setChatAnswer(null)
    try{
      const wid = workflow?.workflow_id || workflow?.WorkflowID || workflow?.workflowId || workflowId
      if(!wid || wid === 'new'){
        setChatAnswer('Cannot chat: workflow not started or open a saved workflow first.')
        setLoading(false)
        return
      }
      const res = await fetch('/api/chat', {
        method: 'POST',
        headers: {'Content-Type':'application/json'},
        body: JSON.stringify({ workflowId: wid, question })
      })
      const data = await res.json()
      setChatAnswer(data.answer)
    }catch(err){
      setChatAnswer('Error: '+String(err))
    }finally{setLoading(false)}
  }

  async function sendApproval(approved){
    if(!reviewer || reviewer.trim()===''){
      setApproveResult({ ok:false, error: 'Reviewer is required' })
      return
    }
    setApproveLoading(true)
    setApproveResult(null)
    try{
      let editedState = null
      if(showEditor){
        try{
          editedState = JSON.parse(editedStateText)
        }catch(err){
          setApproveResult({ ok:false, error: 'Edited state is not valid JSON: '+String(err) })
          setApproveLoading(false)
          return
        }
      }

      const res = await fetch('/api/approve', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workflowId: workflowId || (workflow && (workflow.workflow_id || workflow.WorkflowID || workflow.workflowID)), approved, reviewer, comment, editedState })
      })
      const data = await res.json().catch(()=>null)
      setApproveResult({ ok: res.ok, status: res.status, data, text: !data ? await res.text().catch(()=>null) : null })
    }catch(err){
      setApproveResult({ ok:false, error: String(err) })
    }finally{
      setApproveLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-amber-50 p-8 text-slate-800">
      <div className="max-w-7xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          <h1 className="text-2xl font-semibold">Workflow: {workflow?.workflow_id || workflow?.WorkflowID || workflow?.workflowID || (workflowId === 'new' ? 'New Flow (unsaved)' : 'unknown')}</h1>
          <Link href="/" className="text-sm text-amber-700">← Back</Link>
        </div>

        <div className="flex gap-4">
          <NodePalette />
          <div className="flex-1 space-y-6">
            <div className="flex gap-4">
              <div className="flex-1">
                <div className="flex items-center gap-2 mb-2">
                  <button onClick={generateAndStart} className="bg-amber-600 text-white px-4 py-2 rounded">Generate & Start</button>
                  <button onClick={()=>{ navigator.clipboard && navigator.clipboard.writeText(JSON.stringify(generateWorkflow(canvasNodes, canvasEdges), null, 2)); alert('workflow JSON copied') }} className="bg-amber-200 px-3 py-2 rounded">Copy JSON</button>
                  <div className="text-sm text-slate-500">Tip: click a node then another to connect.</div>
                </div>

                <div className="flex items-center gap-2 mb-4">
                  <input value={flowName} onChange={(e)=>setFlowName(e.target.value)} placeholder="Flow name" className="border rounded p-2" />
                  <button onClick={saveFlowToLocal} className="bg-amber-200 px-3 py-2 rounded">Save Flow</button>
                  <button onClick={()=>setShowSaved(!showSaved)} className="bg-amber-50 border px-3 py-2 rounded">Saved Flows ({savedFlows.length})</button>
                </div>

                {showSaved && (
                  <div className="mb-4 border rounded p-2 bg-white max-h-52 overflow-auto">
                    {(savedFlows||[]).length===0 && (<div className="text-sm text-slate-500">No saved flows</div>)}
                    {(savedFlows||[]).map(f => (
                      <div key={f.id} className="flex items-center justify-between gap-2 p-2 border-b last:border-b-0">
                        <div className="text-sm">
                          <div className="font-medium">{f.name}</div>
                          <div className="text-xs text-slate-500">{f.createdAt}</div>
                        </div>
                        <div className="flex gap-2">
                            <button onClick={()=>loadFlow(f)} className="px-2 py-1 bg-emerald-600 text-white rounded text-sm">Load</button>
                            <button onClick={()=>downloadFlow(f)} className="px-2 py-1 bg-amber-600 text-white rounded text-sm">Download</button>
                            <button onClick={()=>deleteFlow(f.id)} className="px-2 py-1 bg-red-600 text-white rounded text-sm">Delete</button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}

                <NodeCanvas initialNodes={canvasNodes} initialEdges={canvasEdges} onChange={handleCanvasChange} activeTaskId={activeTaskId} failedTaskErrors={failedMap} />
              </div>
              <div className="w-1/2">
                <section className="mb-6">
                  <h2 className="font-medium">Current State</h2>
                  <pre className="mt-2">{latestPretty}</pre>
                </section>

                <section className="mb-6">
                  <h2 className="font-medium">Snapshots (Emit / Restore)</h2>
                  <div className="mt-2 border rounded p-2 bg-amber-50 max-h-48 overflow-auto">
                    {(!snapshots || snapshots.length===0) && (<div className="text-sm text-slate-500">No snapshots found.</div>)}
                    {(snapshots||[]).map(s => (
                      <div key={s.id} className="p-2 border-b last:border-b-0 flex items-start justify-between gap-2">
                        <div className="text-xs">
                          <div><strong>Step:</strong> {s.step}</div>
                          <div className="text-slate-600 truncate">{s.created_at || s.createdAt}</div>
                        </div>
                        <div className="flex gap-2">
                          <button onClick={()=>emitSnapshot(s)} className="bg-amber-600 text-white px-3 py-1 rounded text-sm">Emit</button>
                          <button onClick={()=>restoreSnapshot(s)} className="bg-amber-600 text-white px-3 py-1 rounded text-sm">Restore</button>
                        </div>
                      </div>
                    ))}
                  </div>
                </section>

                <section className="mb-6">
                  <h2 className="font-medium">JSON Diff (previous → current)</h2>
                    <div className="mt-2 border rounded p-2 bg-amber-50">
                    {prev ? (
                      <div style={{width:'100%'}}>
                        <JsonDiff oldValue={prev} newValue={latest} />
                      </div>
                    ) : (
                      <div className="text-sm text-slate-500">No previous snapshot to diff against.</div>
                    )}
                  </div>
                </section>

                <section className="mb-6">
                  <h2 className="font-medium">Review & Approve</h2>
                    <div className="mt-2 border rounded p-4 bg-amber-50">
                    <div className="grid grid-cols-1 gap-2 md:grid-cols-3 mb-2">
                      <input value={reviewer} onChange={(e)=>setReviewer(e.target.value)} placeholder="Reviewer name*" className="border rounded p-2" />
                      <input value={comment} onChange={(e)=>setComment(e.target.value)} placeholder="Comment (optional)" className="border rounded p-2" />
                      <div className="flex gap-2">
                        <button onClick={()=>sendApproval(true)} disabled={approveLoading} className="bg-emerald-600 text-white px-4 py-2 rounded">Approve</button>
                        <button onClick={()=>sendApproval(false)} disabled={approveLoading} className="bg-red-600 text-white px-4 py-2 rounded">Reject</button>
                        <button onClick={()=>{ setShowEditor(!showEditor); setEditedStateText(latestPretty) }} className="bg-amber-200 px-4 py-2 rounded">{showEditor ? 'Close Editor' : 'Edit State'}</button>
                      </div>
                    </div>

                    {showEditor && (
                      <div className="mt-2">
                        <label className="block text-sm font-medium mb-1">Edited Workflow State (JSON)</label>
                        <textarea value={editedStateText} onChange={(e)=>setEditedStateText(e.target.value)} rows={12} className="w-full border rounded p-2 font-mono text-sm" />
                      </div>
                    )}

                    {approveLoading && (<div className="mt-2 text-sm text-slate-600">Sending signal...</div>)}
                    {approveResult && (
                      <div className="mt-2 p-2 rounded border text-sm">
                        <div><strong>Result:</strong> {approveResult.ok ? 'ok' : 'failed'} {approveResult.status ? `({approveResult.status})` : ''}</div>
                        {approveResult.error && (<pre className="mt-1 text-red-600">{approveResult.error}</pre>)}
                        {approveResult.data && (<pre className="mt-1">{JSON.stringify(approveResult.data, null, 2)}</pre>)}
                        {approveResult.text && (<pre className="mt-1">{approveResult.text}</pre>)}
                      </div>
                    )}
                  </div>
                </section>

                <section className="mb-6">
                  <h2 className="font-medium">Chat with State</h2>
                  <div className="mt-2">
                    <textarea value={question} onChange={(e)=>setQuestion(e.target.value)} rows={4} className="w-full border rounded p-2" placeholder="Ask about this workflow state..." />
                    <div className="flex gap-2 mt-2">
                      <button onClick={ask} disabled={loading || !question} className="bg-sky-600 text-white px-4 py-2 rounded">{loading ? 'Thinking...' : 'Ask'}</button>
                    </div>
                    {chatAnswer && (
                      <div className="mt-4">
                        <h3 className="font-semibold">Answer</h3>
                        <pre className="mt-2">{chatAnswer}</pre>
                      </div>
                    )}
                  </div>
                </section>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

export async function getServerSideProps(ctx){
  const id = ctx.params.id
  const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'

  const fetchState = async (url)=>{
    try{
      const r = await fetch(url)
      if(!r.ok) return null
      return await r.json()
    }catch(err){
      return null
    }
  }

  // If creating a new flow in the UI, skip fetching from the API
  if(id === 'new'){
    return { props: { workflow: null, snapshots: [], apiBase, workflowId: id } }
  }

  const workflow = await fetchState(`${apiBase}/workflows/${encodeURIComponent(id)}`)
  const snapshots = await fetchState(`${apiBase}/workflows/${encodeURIComponent(id)}/snapshots`)

  return { props: { workflow, snapshots: snapshots||[], apiBase, workflowId: id } }
}
