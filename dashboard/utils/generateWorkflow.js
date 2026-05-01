import { v4 as uuidv4 } from 'uuid'

// Convert a node/edge graph (XyFlow/ReactFlow style) into a WorkflowState
// JSON object compatible with the orchestrator StartWorkflow API.
export function generateWorkflow(nodes = [], edges = [], opts = {}){
  const trace_id = opts.trace_id || uuidv4()

  // Build AgentCard registry from nodes (dedupe by agent id)
  const agentMap = new Map()
  nodes.forEach(node => {
    const agentId = node.data?.agentId || node.data?.agent_id || node.agent_id || node.data?.agentID
    if(!agentId) return
    if(!agentMap.has(agentId)){
      agentMap.set(agentId, {
        agent_id: agentId,
        image: node.data?.image || node.image || null,
        capabilities: node.data?.capabilities || [],
        outbound: node.data?.outbound || 'none',
      })
    }
  })

  // Helper to find outgoing edge target
  const nextFor = (nodeId)=>{
    const e = edges.find(en => en.source === nodeId)
    return e ? e.target : null
  }

  // Build tasks array. Use node.id as task_id when present.
  const tasks = nodes.map(node => {
    const id = node.id || uuidv4()
    const agentId = node.data?.agentId || node.data?.agent_id || node.agent_id || node.data?.agentID || 'unknown'
    return {
      task_id: id,
      agent_id: agentId,
      name: node.data?.taskName || node.data?.name || node.name || agentId,
      input: node.data?.config || node.data?.input || {},
      status: 'pending',
      confidence: 0.0,
      next: nextFor(id) || '',
    }
  })

  const state = {
    trace_id,
    workflow_id: opts.workflow_id || `soc-pipeline-${trace_id}`,
    current_step: 0,
    status: 'pending',
    agent_cards: Array.from(agentMap.values()),
    tasks,
    artifacts: [],
    metadata: opts.metadata || {},
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }

  return state
}

export default generateWorkflow
