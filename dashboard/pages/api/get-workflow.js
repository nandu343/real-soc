// Proxy to fetch a workflow state from the orchestrator.
export default async function handler(req, res) {
  if (req.method !== 'GET') return res.status(405).end()
  const id = req.query.id || req.query.workflowId || ''
  if (!id) return res.status(400).json({ error: 'id query parameter is required' })
  const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const url = `${apiBase}/workflows/${encodeURIComponent(id)}`
  try {
    const r = await fetch(url)
    const data = await r.json().catch(()=>null)
    return res.status(r.status).json(data || null)
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
