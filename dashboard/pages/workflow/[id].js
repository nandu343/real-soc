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

  const [serverWorkflow, setServerWorkflow] = useState(workflow || null)
  const latest = serverWorkflow || workflow || null
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
  // Side panels
  const [leftOpen, setLeftOpen] = useState(false)
  const [rightOpen, setRightOpen] = useState(false)
  const [drawerOpen, setDrawerOpen] = useState(false)

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

  async function generateAndStart(){
    setLoading(true)
    try{
      const initial_state = generateWorkflow(canvasNodes, canvasEdges)
      if(flowName && flowName.trim()!==''){
        initial_state.workflow_id = flowName.trim()
      }
      const res = await fetch('/api/start-workflow', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ initial_state })
      })
      const data = await res.json().catch(()=>null)
      if(!res.ok){
        alert('Failed to start workflow: '+(data?.error || res.status))
        return
      }
      const wid = data?.workflow_id || initial_state.workflow_id
      setRunningWorkflowId(wid)
      // Fetch the freshly created workflow state so the UI can display
      // assigned webhook URLs on Start nodes
      try{
        const r2 = await fetch(`/api/get-workflow?id=${encodeURIComponent(wid)}`)
        if(r2.ok){
          const js = await r2.json().catch(()=>null)
          if(js) setServerWorkflow(js)
        }
      }catch(err){ /* ignore */ }
      alert('Workflow started: '+wid)
    }catch(err){
      alert('Error starting workflow: '+String(err))
    }finally{ setLoading(false) }
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
      const data = await res.json().catch(()=>null)
      setChatAnswer(data?.answer || String(data))
    }catch(err){
      setChatAnswer('Error: '+String(err))
    }finally{setLoading(false)}
  }

  async function restoreSnapshot(s){
    try{
      const res = await fetch('/api/restore-snapshot', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ workflowId: workflow?.workflow_id || workflowId, snapshotId: s.id }) })
      const data = await res.json().catch(()=>null)
      if(!res.ok){ alert('Restore failed: '+(data?.error||res.status)); return }
      alert('Snapshot restored')
      window.location.reload()
    }catch(err){ alert('Error: '+String(err)) }
  }

  async function emitSnapshot(s){
    try{
      const wid = workflow?.workflow_id || workflowId
      const res = await fetch('/api/emit-snapshot', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ workflowId: wid }) })
      const data = await res.json().catch(()=>null)
      if(!res.ok){ alert('Emit snapshot failed: '+(data?.error||res.status)); return }
      alert('Snapshot created')
      window.location.reload()
    }catch(err){ alert('Error: '+String(err)) }
  }

  return (
    <div className="min-h-screen bg-gradient-to-b from-surface to-white p-8 text-slate-800">
      <div className="max-w-7xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          <h1 className="text-2xl font-semibold">Workflow: {workflow?.workflow_id || workflow?.WorkflowID || workflow?.workflowID || (workflowId === 'new' ? 'New Flow (unsaved)' : 'unknown')}</h1>
          <Link href="/" className="text-sm text-primary">← Back</Link>
        </div>

        <div className="relative">
          <div className="w-full">
            <div className="flex items-center gap-2 mb-4">
              <button onClick={generateAndStart} className="btn-primary">Generate & Start</button>
              <button onClick={()=>{ navigator.clipboard && navigator.clipboard.writeText(JSON.stringify(generateWorkflow(canvasNodes, canvasEdges), null, 2)); alert('workflow JSON copied') }} className="btn-ghost">Copy JSON</button>
              <div className="text-sm text-slate-500">Tip: drag agents from the palette, then connect nodes.</div>
            </div>

            <div className="w-full h-[calc(100vh-6rem)]">
              <NodeCanvas initialNodes={canvasNodes} initialEdges={canvasEdges} onChange={handleCanvasChange} activeTaskId={activeTaskId} failedTaskErrors={failedMap} workflow={latest} />
            </div>

            {/* Left sliding palette */}
            <div className={`fixed top-0 left-0 bottom-0 z-50 w-80 transform transition-transform duration-300 ${leftOpen ? 'translate-x-0' : '-translate-x-full'}`}>
              <div className="h-full bg-white shadow-xl p-4">
                <div className="flex justify-end"><button onClick={()=>setLeftOpen(false)} className="text-sm p-1">✕</button></div>
                <NodePalette />
              </div>
            </div>
          </div>

          {/* Floating controls are moved below to avoid stacking/context issues */}

          {/* Bottom drawer console */}
          <div className={`fixed inset-0 z-40 ${drawerOpen ? 'block' : 'hidden'}`} onClick={()=>setDrawerOpen(false)} />
          <div className={`fixed left-0 bottom-0 w-full z-50 transition-transform duration-300 ${drawerOpen ? 'translate-y-0' : 'translate-y-full'}`}>
            <div className="max-w-7xl mx-auto p-6 bg-white/85 backdrop-blur-md rounded-t-3xl shadow-2xl border-t border-gray-200">
              <div className="flex justify-end"><button onClick={()=>setDrawerOpen(false)} className="text-sm p-1">✕</button></div>
              <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                <div className="md:col-span-1">
                  <h3 className="font-medium">Current State</h3>
                  <pre className="mt-2 max-h-64 overflow-auto bg-white p-3 rounded border">{latestPretty}</pre>
                </div>
                <div className="md:col-span-1">
                  <h3 className="font-medium">Snapshots</h3>
                  <div className="mt-2 card max-h-64 overflow-auto">
                    {(!snapshots || snapshots.length===0) && (<div className="text-sm text-slate-500 p-3">No snapshots found.</div>)}
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
                </div>
                <div className="md:col-span-1">
                  <h3 className="font-medium">Review & Chat</h3>
                  <div className="mt-2">
                    <div className="flex gap-2 mb-2">
                      <input value={reviewer} onChange={(e)=>setReviewer(e.target.value)} placeholder="Reviewer" className="border rounded p-2 flex-1" />
                      <input value={comment} onChange={(e)=>setComment(e.target.value)} placeholder="Comment" className="border rounded p-2 w-40" />
                    </div>
                    <div className="flex gap-2 mb-2">
                      <button onClick={()=>sendApproval(true)} disabled={approveLoading} className="bg-emerald-600 text-white px-4 py-2 rounded">Approve</button>
                      <button onClick={()=>sendApproval(false)} disabled={approveLoading} className="bg-red-600 text-white px-4 py-2 rounded">Reject</button>
                      <button onClick={()=>{ setShowEditor(!showEditor); setEditedStateText(latestPretty) }} className="bg-amber-200 px-4 py-2 rounded">{showEditor ? 'Close Editor' : 'Edit State'}</button>
                    </div>
                    {showEditor && (
                      <textarea value={editedStateText} onChange={(e)=>setEditedStateText(e.target.value)} rows={6} className="w-full border rounded p-2 font-mono text-sm" />
                    )}

                    <div className="mt-3">
                      <h4 className="font-medium">Chat</h4>
                      <textarea value={question} onChange={(e)=>setQuestion(e.target.value)} rows={3} className="w-full border rounded p-2 mt-2" placeholder="Ask about this workflow state..." />
                      <div className="flex gap-2 mt-2"><button onClick={ask} disabled={loading || !question} className="bg-sky-600 text-white px-4 py-2 rounded">{loading ? 'Thinking...' : 'Ask'}</button></div>
                      {chatAnswer && (<div className="mt-2"><pre className="p-2 bg-gray-50 rounded">{chatAnswer}</pre></div>)}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Floating controls: palette + console (top-level to avoid stacking context issues) */}
      <div className="fixed right-6 bottom-6 z-60 flex flex-col items-end gap-3">
        <button onClick={(e)=>{ e.stopPropagation(); setLeftOpen(prev=>!prev) }} className="bg-white border p-3 rounded-full shadow-lg hover:scale-105 transform transition">☰</button>
        <button onClick={(e)=>{ e.stopPropagation(); setDrawerOpen(!drawerOpen) }} className="bg-white border p-3 rounded-full shadow-lg hover:scale-105 transform transition">⚙</button>
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
