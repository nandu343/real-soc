// POST /api/chat
// Body: { workflowId: string, question: string }

export default async function handler(req, res){
  if(req.method !== 'POST') return res.status(405).end()
  const { workflowId, question } = req.body || {}
  if(!workflowId || !question) return res.status(400).json({ error: 'workflowId and question required' })

  const API_BASE = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080/api/v1'

  // fetch current workflow state from orchestrator
  let state = null
  try{
    const r = await fetch(`${API_BASE}/workflows/${encodeURIComponent(workflowId)}`)
    if(r.ok) state = await r.json()
  }catch(err){
    // ignore
  }

  const prompt = `You are an analyst assistant. A human asks: "${question}"\n\nHere is the current workflow state (JSON):\n${state ? JSON.stringify(state,null,2) : '<unavailable>'}\n\nProvide a concise, actionable answer.`

  try{
    const answer = await callLLMFromEnv(prompt)
    return res.status(200).json({ answer })
  }catch(err){
    return res.status(500).json({ error: String(err) })
  }
}

async function callLLMFromEnv(prompt){
  const backend = (process.env.LLM_BACKEND || 'openai').toLowerCase()
  if(backend === 'openai') return callOpenAI(prompt)
  if(backend === 'ollama') return callOllama(prompt)
  if(backend === 'vllm') return callVLLM(prompt)
  throw new Error('unsupported LLM_BACKEND: '+backend)
}

async function callOpenAI(prompt){
  const apiKey = process.env.OPENAI_API_KEY
  if(!apiKey) throw new Error('OPENAI_API_KEY not set')
  const model = process.env.LLM_MODEL || 'gpt-4o-mini'
  const url = 'https://api.openai.com/v1/chat/completions'
  const payload = { model, messages: [{role:'user', content: prompt}], temperature: 0 }
  const r = await fetch(url, { method: 'POST', headers: { 'Authorization': `Bearer ${apiKey}`, 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
  if(!r.ok) throw new Error('openai error: '+(await r.text()))
  const data = await r.json()
  const choice = (data.choices && data.choices[0])
  if(!choice) throw new Error('no choices in openai response')
  if(choice.message && choice.message.content) return choice.message.content
  return choice.text || JSON.stringify(choice)
}

async function callOllama(prompt){
  const base = process.env.OLLAMA_URL || 'http://ollama:11434'
  const model = process.env.LLM_MODEL || 'llama2'
  const url = base.replace(/\/$/, '') + '/api/generate?stream=false'
  const payload = { model, prompt, max_tokens: parseInt(process.env.LLM_MAX_TOKENS||'2048') }
  const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
  if(!r.ok) throw new Error('ollama error: '+(await r.text()))
  try{
    const data = await r.json()
    // try common shapes
    if(typeof data === 'string') return data
    if(data && data.output) return (typeof data.output === 'string') ? data.output : JSON.stringify(data.output)
    if(Array.isArray(data)) return data.map(x=>typeof x === 'string' ? x : JSON.stringify(x)).join('\n')
    if(data && data.text) return data.text
    return JSON.stringify(data)
  }catch(err){
    return await r.text()
  }
}

async function callVLLM(prompt){
  const base = process.env.VLLM_URL || 'http://vllm:8000'
  const model = process.env.LLM_MODEL || 'llama'
  const url = base.replace(/\/$/, '') + '/v1/generate'
  const payload = { model, prompt, max_tokens: parseInt(process.env.LLM_MAX_TOKENS||'2048') }
  const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
  if(!r.ok) throw new Error('vllm error: '+(await r.text()))
  try{
    const data = await r.json()
    if(data && data.output) return (typeof data.output === 'string') ? data.output : JSON.stringify(data.output)
    if(data && data.choices && data.choices[0]){
      const c = data.choices[0]
      if(c.text) return c.text
      if(c.message && c.message.content) return c.message.content
    }
    return JSON.stringify(data)
  }catch(err){
    return await r.text()
  }
}
