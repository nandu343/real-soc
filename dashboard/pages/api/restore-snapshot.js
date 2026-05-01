// Proxy to orchestrator snapshot restore API.
export default async function handler(req, res) {
  if (req.method !== 'POST') return res.status(405).end()
  const { workflowId, snapshotId, reviewer, comment } = req.body || {}
  if (!workflowId || !snapshotId) return res.status(400).json({ error: 'workflowId and snapshotId are required' })
  const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const url = `${apiBase}/workflows/${encodeURIComponent(workflowId)}/restore`
  try {
    const r = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ snapshot_id: snapshotId, reviewer: reviewer || 'ui', comment: comment || '' }),
    })
    const data = await r.json().catch(()=>null)
    return res.status(r.status).json(data || { status: r.status })
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
