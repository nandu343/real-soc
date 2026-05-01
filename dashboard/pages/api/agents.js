// GET /api/agents - proxy to orchestrator /api/v1/agents
// Additionally, include local agents discovered in the workspace `../agents` folder
import fs from 'fs'
import path from 'path'

export default async function handler(req, res) {
  if (req.method !== 'GET') return res.status(405).end()

  const API_BASE = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'
  const url = `${API_BASE}/agents`

  // Fetch remote agents (orchestrator) if available
  let remote = []
  try {
    const r = await fetch(url)
    const text = await r.text()
    if (r.ok) {
      try { remote = JSON.parse(text) } catch (e) { remote = [] }
    }
  } catch (err) {
    // ignore remote errors; we'll still return local agents
    remote = []
  }

  // Discover local agents in the workspace ../agents folder
  const localAgents = []
  try {
    const agentsRoot = path.join(process.cwd(), '..', 'agents')
    if (fs.existsSync(agentsRoot)) {
      const entries = fs.readdirSync(agentsRoot)
      for (const name of entries) {
        try {
          const full = path.join(agentsRoot, name)
          const st = fs.statSync(full)
          if (!st.isDirectory()) continue
          // Try to load metadata if agent.json exists
          let image = ''
          let capabilities = []
          let type = 'docker'
          const manifest = path.join(full, 'agent.json')
          if (fs.existsSync(manifest)) {
            try {
              const j = JSON.parse(fs.readFileSync(manifest, 'utf8'))
              image = j.image || image
              capabilities = j.capabilities || capabilities
              type = j.type || type
            } catch (e) {}
          }
          // Heuristic: if prompt.md exists, mark capability 'reasoning'
          if (fs.existsSync(path.join(full, 'prompt.md'))) capabilities.push('reasoning')
          // Add a minimal agent descriptor
          localAgents.push({ agent_id: name, image, capabilities, type })
        } catch (e) {
          // ignore entry errors
        }
      }
    }
  } catch (err) {
    // ignore fs errors
  }

  // Merge remote + local by agent_id (local entries first)
  const mergedMap = new Map()
  ;(localAgents || []).forEach(a => mergedMap.set(a.agent_id, a))
  ;(remote || []).forEach(a => { if (a && a.agent_id && !mergedMap.has(a.agent_id)) mergedMap.set(a.agent_id, a) })

  const out = Array.from(mergedMap.values())
  return res.status(200).json(out)
}
