// GET /api/agents - proxy to orchestrator /api/v1/agents

export default async function handler(req, res) {
  if (req.method !== 'GET') return res.status(405).end()

  const API_BASE = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const url = `${API_BASE}/agents`

  try {
    const r = await fetch(url)
    const text = await r.text()
    if (!r.ok) {
      try { return res.status(r.status).json(JSON.parse(text)) } catch (e) { return res.status(r.status).send(text) }
    }
    try { return res.status(200).json(JSON.parse(text)) } catch (e) { return res.status(200).send(text) }
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
