// Proxy endpoint to start a workflow via the orchestrator API.
export default async function handler(req, res) {
  if (req.method !== 'POST') return res.status(405).end()
  const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const target = `${apiBase}/workflows`
  try {
    const r = await fetch(target, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req.body),
    })
    const data = await r.json().catch(()=>null)
    return res.status(r.status).json(data || { status: r.status })
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
