Local LLM sidecar guide
=======================

This agent supports multiple LLM backends via the `LLM_BACKEND` env var:

- `openai` (default): calls the OpenAI Chat Completions API. Requires `OPENAI_API_KEY`.
- `ollama`: calls a local Ollama sidecar. Configure `OLLAMA_URL` (default: `http://ollama:11434`).
- `vllm`: calls a local vLLM HTTP server. Configure `VLLM_URL` (default: `http://vllm:8000`).

Environment variables summary:

- `LLM_BACKEND`: `openai|ollama|vllm` (default: `openai`)
- `LLM_MODEL`: model name (backend-specific)
- `OPENAI_API_KEY`: required when `LLM_BACKEND=openai`
- `OLLAMA_URL`: URL to Ollama sidecar (default `http://ollama:11434`)
- `VLLM_URL`: URL to vLLM sidecar (default `http://vllm:8000`)
- `LLM_TIMEOUT`: request timeout in seconds (default `120`)
- `LLM_MAX_TOKENS`: max tokens to request (default `2048`)

Docker-compose notes
--------------------

Examples for adding a local sidecar are included as commented templates in the
project `docker-compose.yml`. Uncomment and adjust the service you want to use.

Quick local run (agent-only):

```bash
# Run an Ollama sidecar separately (see Ollama docs) or use docker-compose.
export STATE_FILE=/tmp/state.json
export LLM_BACKEND=ollama
export OLLAMA_URL=http://localhost:11434
# optional: export LLM_MODEL=llama2
python3 agents/reasoning-agent/agent.py
```

If you run the agent inside Docker with compose and a sidecar service, ensure the
agent container can reach the sidecar by service name (e.g. `ollama:11434`) and
set `LLM_BACKEND`/`OLLAMA_URL` accordingly.
