// POST /api/approve
// Body: { workflowId, approved, reviewer, comment, editedState }

export default async function handler(req, res) {
  if (req.method !== 'POST') return res.status(405).end()

  const { workflowId, approved, reviewer, comment, editedState } = req.body || {}
  if (!workflowId || typeof approved !== 'boolean') {
    return res.status(400).json({ error: 'workflowId and approved (boolean) are required' })
  }

  const API_BASE = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const url = `${API_BASE}/workflows/${encodeURIComponent(workflowId)}/approve`

  const payload = {
    approved: approved,
    reviewer: reviewer || 'dashboard',
    comment: comment || '',
  }
  if (editedState) payload.edited_state = editedState

  try {
    const r = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })

    const text = await r.text()
    if (!r.ok) {
      // forward error body
      try {
        return res.status(r.status).json(JSON.parse(text))
      } catch (e) {
        return res.status(r.status).send(text)
      }
    }

    try {
      return res.status(200).json(JSON.parse(text))
    } catch (e) {
      return res.status(200).send(text)
    }
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
