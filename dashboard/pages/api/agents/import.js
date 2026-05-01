import { exec as _exec } from 'child_process'
import { promisify } from 'util'
import fs from 'fs'
import path from 'path'

const exec = promisify(_exec)

export default async function handler(req, res) {
  if (req.method !== 'POST') return res.status(405).end()

  const { repoUrl, name } = req.body || {}
  if (!repoUrl) return res.status(400).json({ error: 'repoUrl is required' })

  try {
    const agentsRoot = path.join(process.cwd(), '..', 'agents')
    if (!fs.existsSync(agentsRoot)) fs.mkdirSync(agentsRoot, { recursive: true })

    // derive folder name from repo if not provided
    let folderName = name && String(name).trim()
    if (!folderName) {
      // try to parse owner/repo from URL
      const parts = repoUrl.replace(/\.git$/, '').split('/').filter(Boolean)
      folderName = parts.slice(-2).join('-') || `agent-${Date.now()}`
    }

    let target = path.join(agentsRoot, folderName)
    if (fs.existsSync(target)) {
      target = path.join(agentsRoot, folderName + '-' + Date.now())
    }

    // Attempt a shallow git clone. This requires `git` to be available.
    await exec(`git clone --depth 1 ${repoUrl} ${target}`, { timeout: 120000 })

    // remove .git folder to keep workspace clean
    const gitDir = path.join(target, '.git')
    if (fs.existsSync(gitDir)) {
      try { await exec(`rm -rf ${gitDir}`) } catch (e) { /* best-effort */ }
    }

    // discover optional manifest and prompt
    let manifest = null
    const manifestPath = path.join(target, 'agent.json')
    if (fs.existsSync(manifestPath)) {
      try { manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8')) } catch (e) { manifest = null }
    }

    const capabilities = manifest?.capabilities || []
    if (fs.existsSync(path.join(target, 'prompt.md')) && !capabilities.includes('reasoning')) capabilities.push('reasoning')

    const out = { agent_id: path.basename(target), image: manifest?.image || '', capabilities, type: manifest?.type || 'docker' }
    return res.status(200).json({ ok: true, agent: out })
  } catch (err) {
    return res.status(500).json({ error: String(err) })
  }
}
