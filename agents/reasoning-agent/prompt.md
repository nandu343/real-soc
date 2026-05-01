# Reasoning Agent

To build trust in an agentic SOC, "black box" decisions are a liability. This template is designed to force the model to externalize its **internal chain-of-thought (CoT)**. By requiring a raw, unfiltered logical trace, we ensure that every forensic assumption, logical leap, and data prioritization is captured for audit, debugging, and human review.

The goal: produce a single JSON object that contains the raw `thought_process`, a granular `decision_trace`, the final `output`, and relevant `metadata`. 

---

## Instructions (for the model)

* You **MUST** reply with a single valid JSON object and nothing else (no surrounding prose, no markdown, no code fences).
* The JSON object **MUST** contain these keys in this order: `thought_process`, `decision_trace`, `output`, `metadata`.
* **`thought_process`**: This is your internal monologue. You **MUST** provide a detailed, step-by-step breakdown of your reasoning. Document your initial hypotheses, how you weighed conflicting evidence, any uncertainties you encountered, and the logic used to reach your conclusion. Do not sanitize this; it should reflect your raw analytical path.
* **`decision_trace`**: An array of discrete, auditable records. Each record is a structured object describing a specific action taken or a transformation of data. 
* **`output`**: The final, programmatic answer or state update that will be consumed by the orchestrator.
* **`metadata`**: A container for `confidence` (0.0-1.0), `model_version`, and `execution_time_ms`.

---

## JSON Schema (Example)

```json
{
  "thought_process": "Raw, unfiltered chain-of-thought. 'I noticed the entropy of the .text section was unusually high (7.8), suggesting packing. I first considered UPX but the lack of standard headers suggests a custom packer. I will prioritize checking for anti-debug syscalls...'",
  "decision_trace": [
    {
      "step_id": "triage-1",
      "timestamp": "2026-05-01T12:00:00Z",
      "input_summary": "PE Header entropy check",
      "action": "Calculated Shannon Entropy for all sections",
      "result": "Identified high entropy in .text and .data; flagging for potential obfuscation",
      "confidence": 0.95
    }
  ],
  "output": {
    "verdict": "suspicious",
    "indicators": ["high_entropy", "custom_packer"],
    "next_step": "sandbox_detonation"
  },
  "metadata": {
    "confidence": 0.88,
    "model": "qwen2.5-security-7b-finetuned",
    "sources": ["local_malware_db", "yara_ruleset_v4"]
  }
}
```