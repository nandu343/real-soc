#!/usr/bin/env python3
"""
Reasoning Agent for Real-SOC
=================================
Implements the Agent Hand-off Contract and uses `prompt.md` as the
model instruction. Behavior:

- Reads the workflow state from `$STATE_FILE` (default: `/workspace/state.json`).
- Loads `prompt.md` from the same folder and appends a small JSON context
  containing the task input and minimal workflow context.
- Calls the LLM via the OpenAI HTTP Chat Completions API when
  `OPENAI_API_KEY` is present.
- Expects the model to return a single JSON object (the `prompt.md`
  enforces this). The agent extracts the JSON, stores it under
  `task["analysis"]`, maps `analysis["output"]` -> `task["output"]`
  (fallback: whole object), marks the task `completed`, and advances
  the workflow step.

This file intentionally uses only the `requests` dependency for easy
containerization (see accompanying Dockerfile).
"""

import json
import os
import re
import sys
import uuid
from datetime import datetime, timezone

try:
    import requests
except Exception as exc:
    print(f"[ERROR] missing dependency 'requests': {exc}", file=sys.stderr)
    sys.exit(1)


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def read_prompt_md() -> str:
    here = os.path.dirname(os.path.abspath(__file__))
    path = os.path.join(here, "prompt.md")
    try:
        with open(path, "r", encoding="utf-8") as f:
            return f.read()
    except Exception as exc:
        print(f"[ERROR] Failed to read prompt.md: {exc}", file=sys.stderr)
        return ""


def call_openai_chat(prompt_text: str) -> str:
    api_key = os.environ.get("OPENAI_API_KEY")
    if not api_key:
        raise RuntimeError("OPENAI_API_KEY not set; cannot call LLM")
    model = os.environ.get("LLM_MODEL", "gpt-4o-mini")
    url = "https://api.openai.com/v1/chat/completions"
    headers = {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
    }
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt_text}],
        "temperature": 0.0,
        # Let the environment override if needed
    }
    timeout = int(os.environ.get("LLM_TIMEOUT", "120"))
    resp = requests.post(url, headers=headers, json=payload, timeout=timeout)
    if resp.status_code != 200:
        raise RuntimeError(f"LLM API error status={resp.status_code} body={resp.text}")
    data = resp.json()
    choices = data.get("choices")
    if not choices:
        raise RuntimeError(f"LLM response missing choices: {data}")
    # Safe extraction for the first choice
    first = choices[0]
    # Chat completion shape: message.content
    if "message" in first and isinstance(first["message"], dict):
        return first["message"].get("content", "")
    # Older APIs may use 'text'
    return first.get("text", "")


def _extract_text_from_response(data):
    """Extract a reasonable text/string from varied LLM sidecar responses."""
    if data is None:
        return ""
    if isinstance(data, str):
        return data
    if isinstance(data, dict):
        # common top-level fields
        for key in ("text", "response", "output", "result", "message"):
            if key in data and isinstance(data[key], (str, dict, list)):
                v = data[key]
                if isinstance(v, str):
                    return v
                # message may be a dict with content
                if isinstance(v, dict):
                    if "content" in v and isinstance(v["content"], str):
                        return v["content"]
                    # sometimes message -> {role, content}
                    if "content" in v and isinstance(v["content"], (list, dict)):
                        try:
                            return json.dumps(v["content"])
                        except Exception:
                            pass
        # choices style
        if "choices" in data and isinstance(data["choices"], list) and data["choices"]:
            first = data["choices"][0]
            if isinstance(first, dict):
                for key in ("text", "message", "content", "output"):
                    if key in first:
                        val = first[key]
                        if isinstance(val, str):
                            return val
                        if isinstance(val, dict) and "content" in val and isinstance(val["content"], str):
                            return val["content"]
            else:
                return str(first)
        # data list segments
        if "data" in data and isinstance(data["data"], list):
            parts = []
            for seg in data["data"]:
                if isinstance(seg, dict):
                    for key in ("text", "content"):
                        if key in seg and isinstance(seg[key], str):
                            parts.append(seg[key])
                            break
            if parts:
                return "".join(parts)
        try:
            return json.dumps(data)
        except Exception:
            return str(data)
    # fallback
    return str(data)


def call_ollama(prompt_text: str) -> str:
    base = os.environ.get("OLLAMA_URL", "http://ollama:11434")
    model = os.environ.get("LLM_MODEL", "llama2")
    timeout = int(os.environ.get("LLM_TIMEOUT", "120"))
    max_tokens = int(os.environ.get("LLM_MAX_TOKENS", "2048"))

    # Try a few known Ollama endpoints; some deployments stream by default.
    endpoints = [
        "/api/generate?stream=false",
        "/api/generate",
        "/generate?stream=false",
        "/generate",
    ]
    last_err = None
    for ep in endpoints:
        url = base.rstrip("/") + ep
        payload = {"model": model, "prompt": prompt_text, "max_tokens": max_tokens}
        try:
            resp = requests.post(url, json=payload, timeout=timeout)
        except Exception as exc:
            last_err = exc
            continue
        if resp.status_code >= 400:
            last_err = RuntimeError(f"ollama error status={resp.status_code} body={resp.text}")
            continue
        # try parse
        try:
            data = resp.json()
            return _extract_text_from_response(data)
        except Exception:
            # not JSON — return raw text
            return resp.text
    raise RuntimeError(f"ollama call failed: {last_err}")


def call_vllm(prompt_text: str) -> str:
    base = os.environ.get("VLLM_URL", "http://vllm:8000")
    model = os.environ.get("LLM_MODEL", "llama")
    timeout = int(os.environ.get("LLM_TIMEOUT", "120"))
    max_tokens = int(os.environ.get("LLM_MAX_TOKENS", "2048"))

    endpoints = [
        "/v1/generate",
        "/generate",
        "/v1/completions",
        "/completions",
    ]
    last_err = None
    for ep in endpoints:
        url = base.rstrip("/") + ep
        payload = {"model": model, "prompt": prompt_text, "max_tokens": max_tokens}
        try:
            resp = requests.post(url, json=payload, timeout=timeout)
        except Exception as exc:
            last_err = exc
            continue
        if resp.status_code >= 400:
            last_err = RuntimeError(f"vllm error status={resp.status_code} body={resp.text}")
            continue
        try:
            data = resp.json()
            return _extract_text_from_response(data)
        except Exception:
            return resp.text
    raise RuntimeError(f"vllm call failed: {last_err}")


def call_llm(prompt_text: str) -> str:
    backend = os.environ.get("LLM_BACKEND", "openai").lower()
    if backend == "openai":
        return call_openai_chat(prompt_text)
    if backend == "ollama":
        return call_ollama(prompt_text)
    if backend == "vllm":
        return call_vllm(prompt_text)
    raise RuntimeError(f"unsupported LLM_BACKEND: {backend}")


def extract_json_from_text(text: str):
    """Try to parse text as JSON, or find the first balanced JSON
    object/array substring and parse that. Returns (parsed, error_str).
    """
    s = text.strip()
    if not s:
        return None, "empty model response"
    # Quick attempt: direct parse
    try:
        return json.loads(s), None
    except Exception:
        pass

    # Find first '{' or '[' and attempt to extract balanced substring
    first_obj = s.find("{")
    first_arr = s.find("[")
    if first_obj == -1 and first_arr == -1:
        return None, "no JSON object or array found in model output"

    if first_obj == -1:
        start = first_arr
        open_c, close_c = "[", "]"
    elif first_arr == -1:
        start = first_obj
        open_c, close_c = "{", "}"
    else:
        # pick whichever comes first
        if first_obj < first_arr:
            start = first_obj
            open_c, close_c = "{", "}"
        else:
            start = first_arr
            open_c, close_c = "[", "]"

    depth = 0
    in_string = False
    esc = False
    for i in range(start, len(s)):
        ch = s[i]
        if ch == '"' and not esc:
            in_string = not in_string
        if ch == '\\' and not esc:
            esc = True
            continue
        else:
            esc = False
        if in_string:
            continue
        if ch == open_c:
            depth += 1
        elif ch == close_c:
            depth -= 1
            if depth == 0:
                candidate = s[start : i + 1]
                try:
                    return json.loads(candidate), None
                except Exception as exc:
                    return None, f"json decode error: {exc}"

    return None, "incomplete JSON in model output"


def main() -> int:
    state_file = os.environ.get("STATE_FILE", "/workspace/state.json")

    try:
        with open(state_file, "r", encoding="utf-8") as f:
            state = json.load(f)
    except FileNotFoundError:
        print(f"[ERROR] State file not found: {state_file}", file=sys.stderr)
        return 1
    except json.JSONDecodeError as exc:
        print(f"[ERROR] Invalid JSON in state file: {exc}", file=sys.stderr)
        return 1

    print(f"[INFO] Read state: trace_id={state.get('trace_id')} step={state.get('current_step')}")

    current_step = state.get("current_step", 0)
    tasks = state.get("tasks", [])

    if current_step >= len(tasks):
        print(f"[ERROR] current_step {current_step} out of range (tasks={len(tasks)})", file=sys.stderr)
        return 1

    task = tasks[current_step]
    print(f"[INFO] Executing task: name={task.get('name')} agent={task.get('agent_id')}")

    task_input = task.get("input", {})

    # Load the prompt template
    prompt_md = read_prompt_md()
    if not prompt_md:
        print("[ERROR] prompt.md is empty or missing", file=sys.stderr)
        return 1

    # Build prompt: include prompt.md and a compact context block
    context = {"task_input": task_input, "trace_id": state.get("trace_id"), "current_step": state.get("current_step")}
    full_prompt = prompt_md + "\n\n---\nContext:\n" + json.dumps(context, indent=2)

    # Call LLM (backend selectable via LLM_BACKEND env: openai, ollama, vllm)
    try:
        response_text = call_llm(full_prompt)
    except Exception as exc:
        print(f"[ERROR] LLM call failed: {exc}", file=sys.stderr)
        return 1

    print(f"[INFO] LLM response length={len(response_text)}")

    parsed, err = extract_json_from_text(response_text)
    if parsed is None:
        print(f"[ERROR] Failed to parse JSON from model output: {err}", file=sys.stderr)
        # Save the raw output for debugging and mark failed
        task["status"] = "failed"
        task["analysis_raw"] = response_text
        state["updated_at"] = now_iso()
        try:
            with open(state_file, "w", encoding="utf-8") as f:
                json.dump(state, f, indent=2)
        except Exception as exc:
            print(f"[ERROR] Failed to write state file: {exc}", file=sys.stderr)
        return 1

    # Attach parsed analysis and map the programmatic output
    task["analysis"] = parsed
    if isinstance(parsed, dict) and "output" in parsed:
        task["output"] = parsed.get("output")
    else:
        task["output"] = parsed

    task["status"] = "completed"
    if isinstance(parsed, dict):
        task["confidence"] = parsed.get("metadata", {}).get("confidence", 0.0)

    state["current_step"] = current_step + 1
    state["updated_at"] = now_iso()

    artifact = {
        "artifact_id": str(uuid.uuid4()),
        "produced_by": task.get("agent_id", "reasoning-agent"),
        "name": "analysis.json",
        "mime_type": "application/json",
        "uri": f"file://{state_file}",
        "checksum": "",
    }
    state.setdefault("artifacts", []).append(artifact)

    try:
        with open(state_file, "w", encoding="utf-8") as f:
            json.dump(state, f, indent=2)
    except OSError as exc:
        print(f"[ERROR] Failed to write state file: {exc}", file=sys.stderr)
        return 1

    print(f"[INFO] State written successfully. New step={state['current_step']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
