#!/usr/bin/env python3
"""
Hello-World Real-SOC Agent
========================
This is the reference implementation of the Agent Hand-off Contract.

Every Real-SOC agent MUST:
1. Read the WorkflowState JSON from the path in $STATE_FILE.
2. Locate its own TaskObject (matched by agent_id in the AgentCards list).
3. Perform its work.
4. Write the updated WorkflowState JSON back to $STATE_FILE.
5. Exit 0 on success, non-zero on failure.

The orchestrator captures the updated $STATE_FILE as an Artifact and
records its SHA-256 checksum for integrity.
"""

import json
import os
import sys
import uuid
from datetime import datetime, timezone


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def main() -> int:
    state_file = os.environ.get("STATE_FILE", "/workspace/state.json")

    # ── 1. Read the current WorkflowState ────────────────────────────────────
    try:
        with open(state_file, "r") as f:
            state = json.load(f)
    except FileNotFoundError:
        print(f"[ERROR] State file not found: {state_file}", file=sys.stderr)
        return 1
    except json.JSONDecodeError as exc:
        print(f"[ERROR] Invalid JSON in state file: {exc}", file=sys.stderr)
        return 1

    print(f"[INFO] Read state: trace_id={state.get('trace_id')} step={state.get('current_step')}")

    # ── 2. Find the task assigned to this agent ───────────────────────────────
    # Convention: this agent processes tasks[current_step].
    current_step = state.get("current_step", 0)
    tasks = state.get("tasks", [])

    if current_step >= len(tasks):
        print(f"[ERROR] current_step {current_step} out of range (tasks={len(tasks)})", file=sys.stderr)
        return 1

    task = tasks[current_step]
    print(f"[INFO] Executing task: name={task.get('name')} agent={task.get('agent_id')}")

    # ── 3. Perform work ───────────────────────────────────────────────────────
    # This hello-world agent simply increments a counter in the task input
    # and echoes the input back as output.
    task_input = task.get("input", {})
    counter = task_input.get("counter", 0) + 1

    print(f"[INFO] Hello from Real-SOC hello-world agent! counter={counter}")

    # ── 4. Update the task with results ──────────────────────────────────────
    task["output"] = {
        "message": f"Hello from step {current_step}!",
        "counter": counter,
        "processed_at": now_iso(),
    }
    task["status"] = "completed"
    # Confidence 1.0 = high certainty, no HITL required.
    task["confidence"] = 1.0

    # Increment the step counter so the orchestrator advances.
    state["current_step"] = current_step + 1
    state["updated_at"] = now_iso()

    # Append a self-describing artifact entry (optional but recommended).
    artifact = {
        "artifact_id": str(uuid.uuid4()),
        "produced_by": task.get("agent_id", "hello-world"),
        "name": "state.json",
        "mime_type": "application/json",
        "uri": f"file://{state_file}",
        "checksum": "",  # The orchestrator will overwrite this with the real SHA-256.
    }
    state.setdefault("artifacts", []).append(artifact)

    # ── 5. Write the updated state back ──────────────────────────────────────
    try:
        with open(state_file, "w") as f:
            json.dump(state, f, indent=2)
    except OSError as exc:
        print(f"[ERROR] Failed to write state file: {exc}", file=sys.stderr)
        return 1

    print(f"[INFO] State written successfully. New step={state['current_step']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
