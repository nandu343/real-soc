// Proxy to call the orchestrator Temporal query endpoint for a running workflow.
export default async function handler(req, res) {
  const id = req.query.id || req.body?.id
  if (!id) return res.status(400).json({ error: 'workflow id is required' })
  const q = req.query.q || 'getState'
  const run_id = req.query.run_id || ''
  const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const url = `${apiBase}/workflows/${encodeURIComponent(id)}/query?q=${encodeURIComponent(q)}&run_id=${encodeURIComponent(run_id)}`
  try {
    const r = await fetch(url)
    const data = await r.json().catch(()=>null)
    return res.status(r.status).json(data || { status: r.status })
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
