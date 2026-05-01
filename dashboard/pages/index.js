import Link from 'next/link'
import { useState } from 'react'

export default function Home(){
  const [id, setId] = useState('')
  return (
    <div className="min-h-screen bg-slate-50 p-8">
      <div className="max-w-3xl mx-auto">
        <h1 className="text-2xl font-semibold mb-4">Real-SOC — Chain of Thought Dashboard</h1>
        <p className="mb-6 text-slate-600">Enter a workflow ID to inspect its state, snapshots, and chat with the pipeline.</p>

        <div className="flex gap-2">
          <input value={id} onChange={(e)=>setId(e.target.value)} placeholder="workflow id (e.g. soc-pipeline-local-test)" className="flex-1 border rounded p-2" />
          <Link href={`/workflow/${encodeURIComponent(id)}`} className="bg-sky-600 text-white px-4 py-2 rounded">Open</Link>
          <Link href={`/workflow/new`} className="bg-emerald-600 text-white px-4 py-2 rounded">Create New</Link>
        </div>

        <div className="mt-8 text-sm text-slate-500">
          Tip: workflow IDs are returned when starting a workflow via the orchestrator API.
        </div>
      </div>
    </div>
  )
}
