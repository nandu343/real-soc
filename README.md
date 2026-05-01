# Real-SOC — Local-First Agentic SOC Pipeline

Real-SOC replaces AWS cloud services (API Gateway, Lambda, Step Functions) with a
fully local, open-source stack:

| AWS service | Real-SOC equivalent |
|---|---|
| API Gateway | **Go + Fiber** HTTP server |
| Lambda | **Docker Engine API** (moby/moby) agent containers |
| Step Functions | **Temporal.io** workflow engine |
| DynamoDB | **PostgreSQL + JSONB** (GORM) |

---

## Architecture

```
┌──────────────┐        ┌───────────────────────────┐
│  Operator /  │  HTTP  │   Real-SOC Orchestrator      │
│  SIEM alert  │───────▶│  (Go + Fiber, :8080)       │
└──────────────┘        │                           │
                        │  ┌───────────────────┐    │
                        │  │  Temporal Worker   │    │
                        │  │  AgentPipeline     │    │
                        │  │  Workflow          │    │
                        │  └─────────┬─────────┘    │
                        │            │ activities    │
                        │  ┌─────────▼─────────┐    │
                        │  │  Docker Runner     │    │
                        │  │  (agent sandbox)   │    │
                        │  └─────────┬─────────┘    │
                        │            │ tmpfs-backed  │
                        │  ┌─────────▼─────────┐    │
                        │  │  Agent Container   │    │
                        │  │  /workspace/       │    │
                        │  │  state.json        │    │
                        │  └───────────────────┘    │
                        │                           │
                        │  ┌───────────────────┐    │
                        │  │  MCP Tool Host     │    │
                        │  │  YARA / Sandbox /  │    │
                        │  │  Hash utilities    │    │
                        │  └───────────────────┘    │
                        └───────────────────────────┘
                                    │
                        ┌───────────▼───────────────┐
                        │  PostgreSQL JSONB store    │
                        │  (full audit trail)        │
                        └───────────────────────────┘
```

---

## Project Structure

```
real-soc/
├── cmd/
│   └── orchestrator/
│       └── main.go          — Entry point (wires Fiber, Temporal, Docker)
├── internal/
│   ├── models/
│   │   └── state.go         — WorkflowState A2A struct + JSONB types
│   ├── db/
│   │   └── postgres.go      — GORM store (audit trail)
│   ├── runner/
│   │   └── docker.go        — Docker SDK agent execution + sandboxing
│   └── workflow/
│       ├── activities.go    — Temporal Activities (RunAgent, PersistState)
│       └── workflow.go      — Temporal Workflow + HITL signal gate
├── api/
│   └── handler.go           — Fiber REST API handlers
├── mcp/
│   └── server.go            — MCP JSON-RPC 2.0 tool host
├── agents/
│   └── hello-world/
│       ├── Dockerfile        — Sample agent container image
│       └── agent.py          — Sample agent (reads/writes state.json)
├── docker-compose.yml        — Full local stack (Temporal + Postgres + Go)
├── Dockerfile                — Orchestrator container image
└── README.md
```

---

## Quick Start

### Prerequisites

* Docker ≥ 24 with Compose v2
* Go ≥ 1.22 (for local development)

### 1. Start the full stack

```bash
docker compose up --build -d
```

This starts:
* **PostgreSQL** on `localhost:5432`
* **Temporal** on `localhost:7233`
* **Temporal UI** on `http://localhost:8088`
* **Real-SOC Orchestrator** on `http://localhost:8080`

### 2. Start a workflow run

```bash
curl -s -X POST http://localhost:8080/api/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "initial_state": {
      "trace_id": "demo-001",
      "agent_cards": [
        {
          "agent_id": "hello-world",
          "image": "ghcr.io/nandu343/real-soc/hello-world:latest",
          "capabilities": ["increment_counter"]
        }
      ],
      "tasks": [
        {
          "task_id": "task-001",
          "agent_id": "hello-world",
          "name": "increment_counter",
          "status": "pending",
          "confidence": 0.0,
          "input": {"counter": 0}
        }
      ],
      "artifacts": [],
      "status": "pending",
      "current_step": 0
    }
  }'
```

### 3. Check workflow status

```bash
curl -s http://localhost:8080/api/v1/workflows/soc-pipeline-demo-001 | jq .
```

### 4. Approve a HITL gate (when confidence < 0.7)

```bash
curl -s -X POST http://localhost:8080/api/v1/workflows/soc-pipeline-demo-001/approve \
  -H 'Content-Type: application/json' \
  -d '{
    "approved": true,
    "reviewer": "analyst@example.com",
    "comment": "Reviewed and confirmed — proceed."
  }'
```

---

## Agent Hand-off Contract

Every agent container MUST follow this contract to participate in the pipeline.

### Input

The orchestrator writes the current `WorkflowState` to `/workspace/state.json`
before starting the container.  The path is also available via the `STATE_FILE`
environment variable.

### WorkflowState JSON Schema

```json
{
  "trace_id":      "<globally unique run ID>",
  "workflow_id":   "<Temporal workflow ID>",
  "current_step":  0,
  "status":        "running",
  "agent_cards": [
    {
      "agent_id":     "my-agent",
      "image":        "ghcr.io/org/my-agent:sha256-...",
      "capabilities": ["triage", "enrich"],
      "mcp_tools":    ["yara_scan", "file_hash"]
    }
  ],
  "tasks": [
    {
      "task_id":    "<uuid>",
      "agent_id":   "my-agent",
      "name":       "triage",
      "status":     "running",
      "confidence": 0.0,
      "input":      { "alert_id": "ALT-001", "severity": "high" },
      "output":     null
    }
  ],
  "artifacts": [],
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z"
}
```

### Agent Responsibilities

1. **Read** `/workspace/state.json` (or `$STATE_FILE`).
2. **Locate** the task at `tasks[current_step]` that matches your `agent_id`.
3. **Execute** your logic using `input` as parameters.
4. **Write** your result into `tasks[current_step].output`.
5. **Set** `tasks[current_step].status` to `"completed"` or `"failed"`.
6. **Set** `tasks[current_step].confidence` to a value in `[0.0, 1.0]`.
   * `< 0.7` → the orchestrator **pauses** and waits for HITL approval.
   * `≥ 0.7` → the pipeline continues automatically.
7. **Increment** `current_step` by 1.
8. **Update** `updated_at` to the current UTC timestamp.
9. **Write** the updated JSON back to `$STATE_FILE`.
10. **Exit 0** on success.  Any non-zero exit code marks the task as failed.

### Calling MCP Tools

Agents may call local SOC tools via the MCP JSON-RPC 2.0 endpoint:

```
POST http://172.17.0.1:8081/mcp/rpc
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "yara_scan",
    "params": {
      "target_path": "/workspace/sample.exe",
      "rules_path": "/rules/malware.yar"
    }
  }
}
```

List available tools:
```
GET http://172.17.0.1:8081/mcp/tools
```

The host gateway IP (`172.17.0.1`) is the default Docker bridge address.
Set `MCP_PORT` (default `8081`) if you change the port.

---

## Security Sandboxing

Each agent container is hardened by the Docker runner:

| Control | Setting |
|---|---|
| Read-only root filesystem | `ReadonlyRootfs: true` |
| No privilege escalation | `no-new-privileges:true` SecurityOpt |
| Dropped capabilities | `CapDrop: ALL` |
| Network isolation | Dedicated bridge (`soc-agent-net`) |
| CPU limit | 1 logical CPU (1e9 nanoCPUs) |
| Memory limit | 512 MB |
| Image pinning | Resolved to immutable SHA-256 digest before start |
| Writable surface | Only `/workspace` (tmpfs inside the container; size-limited) |

See the package-level docblock in `internal/runner/docker.go` for the full
rationale.

---

## Traffic Controller & Proxy Agent

By default agent containers run on an internal Docker network with *no
internet access*. This prevents a malicious or buggy agent from reaching
external services or filling host disk. Outbound access is controlled in two
ways:

- Workspace isolation: the runner mounts a size-limited `tmpfs` at
  `/workspace` for each agent. This prevents container writes from
  consuming host disk space. The default tmpfs size is `64MB` and is
  configurable via `RUNNER_TMPFS_SIZE_MB`.

- Outbound policy: each `AgentCard` may set an `outbound` policy with
  values `none` (default), `proxy`, or `direct`. When set to `proxy`, the
  orchestrator exposes a controlled Proxy Agent service that forwards only
  to an allow-list of hosts. When set to `direct`, the container is
  granted direct bridge-network access only if `RUNNER_ALLOW_DIRECT=true`.

Proxy Agent (recommended for controlled outbound access)
---------------------------------------

The orchestrator can optionally start a simple Proxy Agent that forwards
HTTP requests from agents to allowed upstream hosts. Configure it via
environment variables at startup:

- `PROXY_AGENT_IMAGE` — Docker image for the proxy agent (optional).
- `PROXY_ALLOWED_HOSTS` — Comma-separated list of allowed hostnames
  (e.g. `virustotal.com`) for the proxy to forward to.

When a container's `AgentCard.Outbound == "proxy"`, the runner injects
`PROXY_URL=http://proxy-agent:8080/forward` into the agent environment.
Agents must POST JSON payloads to this endpoint (see `agents/proxy-agent`
for example).

Block I/O / IOPS hard limits
----------------------------

You can apply additional block I/O throttling via environment variables
when launching the orchestrator. These settings are passed through to
Docker's cgroup controls and are optional:

- `RUNNER_BLKIO_DEVICE` — the host block device path to throttle
  (e.g. `/dev/sda`).
- `RUNNER_BLKIO_READ_IOPS` / `RUNNER_BLKIO_WRITE_IOPS` — maximum IOPS
  for reads/writes on the device.
- `RUNNER_BLKIO_READ_BPS` / `RUNNER_BLKIO_WRITE_BPS` — maximum bytes-per-second
  for reads/writes on the device.

If no device-specific limits are set, the runner sets a conservative
`BlkioWeight` to limit relative block I/O priority for agent containers.

Environment variables summary (new)
----------------------------------

| Variable | Default | Description |
|---|---:|---|
| `PROXY_AGENT_IMAGE` | _(empty)_ | Optional proxy agent image to start at orchestrator boot |
| `PROXY_ALLOWED_HOSTS` | `virustotal.com` | Comma-separated allowed hosts for the proxy |
| `RUNNER_TMPFS_SIZE_MB` | `64` | Size (MB) of the per-agent tmpfs mounted at `/workspace` |
| `RUNNER_ALLOW_DIRECT` | `false` | If `true`, allows agents with `outbound==direct` to use bridge network |
| `RUNNER_BLKIO_DEVICE` | _(empty)_ | Block device path for I/O throttling (optional) |
| `RUNNER_BLKIO_READ_IOPS` | _(empty)_ | Read IOPS limit for the device |
| `RUNNER_BLKIO_WRITE_IOPS` | _(empty)_ | Write IOPS limit for the device |
| `RUNNER_BLKIO_READ_BPS` | _(empty)_ | Read bytes-per-second limit for the device |
| `RUNNER_BLKIO_WRITE_BPS` | _(empty)_ | Write bytes-per-second limit for the device |


---

## MCP Tools

The following tools are pre-registered with the MCP host.  Replace the stub
implementations in `mcp/server.go` with real integrations.

| Tool name | Description |
|---|---|
| `yara_scan` | Scan a file against YARA rules |
| `file_hash` | Compute SHA-256 and MD5 hashes |
| `sandbox_detonate` | Submit to a malware sandbox (Cuckoo, ANY.RUN, …) |

---

## API Reference

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/health` | Liveness probe |
| `POST` | `/api/v1/workflows` | Start a new pipeline run |
| `GET` | `/api/v1/workflows/:id` | Get latest state for a run |
| `POST` | `/api/v1/workflows/:id/approve` | HITL approval signal |
| `GET` | `/mcp/tools` | List available MCP tools |
| `POST` | `/mcp/rpc` | JSON-RPC 2.0 tool call |

---

## Building the Hello-World Agent

```bash
cd agents/hello-world
docker build -t Real-SOC/hello-world:latest .

# Test locally
echo '{"trace_id":"test","current_step":0,"tasks":[{"task_id":"t1","agent_id":"hello-world","name":"greet","status":"pending","confidence":0,"input":{"counter":0}}],"artifacts":[],"agent_cards":[]}' \
  > /tmp/state.json

docker run --rm \
  -v /tmp/state.json:/workspace/state.json \
  Real-SOC/hello-world:latest

cat /tmp/state.json | python3 -m json.tool
```

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `POSTGRES_HOST` | `localhost` | Postgres host |
| `POSTGRES_PORT` | `5432` | Postgres port |
| `POSTGRES_USER` | `socuser` | Postgres user |
| `POSTGRES_PASSWORD` | `socpass` | Postgres password |
| `POSTGRES_DB` | `socdb` | Postgres database |
| `TEMPORAL_ADDRESS` | `localhost:7233` | Temporal gRPC address |
| `DOCKER_AGENT_NETWORK` | `soc-agent-net` | Docker network for agents |
| `REGISTRY_TOKEN` | _(empty)_ | Base64 auth for private registries |
