// Proxy to orchestrator snapshot-create API.
export default async function handler(req, res) {
  if (req.method !== 'POST') return res.status(405).end()
  const { workflowId } = req.body || {}
  if (!workflowId) return res.status(400).json({ error: 'workflowId is required' })
  const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const url = `${apiBase}/workflows/${encodeURIComponent(workflowId)}/snapshots`
  try {
    const r = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    })
    const data = await r.json().catch(()=>null)
    return res.status(r.status).json(data || { status: r.status })
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
