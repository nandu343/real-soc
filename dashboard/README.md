Real-SOC Dashboard
==================

Minimal Next.js + Tailwind dashboard for visualizing workflows, viewing JSON diffs,
and chatting with the current workflow state.

Quick start (local development):

1. Install dependencies:

```bash
cd dashboard
npm install --legacy-peer-deps
```

2. Set environment variables for the orchestrator API and LLM backend:

```bash
export NEXT_PUBLIC_API_BASE=http://localhost:8080/api/v1
export LLM_BACKEND=ollama # or openai|vllm
# set OLLAMA_URL or OPENAI_API_KEY as required
```

3. Run the dev server:

```bash
npm run dev
```

Open http://localhost:3001 and enter a workflow id.

Notes:
- The dashboard uses `jsondiffpatch` to compute JSON diffs and render a compact delta view.
- If you previously relied on `react-diff-viewer`, it has been removed for React 18 compatibility.

Local agents & human agent template:
- The Node Palette can fetch agents from the orchestrator (`/api/v1/agents`). If none are present, use the "Create" button in the palette to add a local agent stored in `localStorage`.
- Use the "Add Human" quick button to insert a preconfigured human analyst agent (type `human`) which pauses workflows for manual input.
- Local agents are persisted to `localStorage` under the key `real-soc:local_agents` and can be deleted or exported from the palette UI.
