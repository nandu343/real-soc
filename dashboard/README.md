Real-SOC Dashboard
==================

Minimal Next.js + Tailwind dashboard for visualizing workflows, viewing JSON diffs,
and chatting with the current workflow state.

Quick start (local development):

1. Install dependencies:

```bash
cd dashboard
npm install
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
