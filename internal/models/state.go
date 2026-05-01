// Package models defines the core data structures used across the Real-SOC pipeline.
// The WorkflowState struct is the central "source of truth" passed between every
// agent, conforming to the Agent2Agent (A2A) Protocol so any agent implementation
// (Python, Go, Rust …) can consume and produce it via a standardised JSON shape.
package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// --------------------------------------------------------------------------
// A2A Protocol primitives
// --------------------------------------------------------------------------

// AgentCard describes a registered agent that can participate in a workflow.
// It follows the A2A "capability card" convention: each agent self-declares
// its skills so the orchestrator can route tasks intelligently.
type AgentCard struct {
	// AgentID is a stable, human-readable identifier (e.g. "yara-scanner-v1").
	AgentID string `json:"agent_id"`
	// Image is the fully-qualified container image reference (registry/name:tag).
	Image string `json:"image"`
	// Capabilities lists the high-level skills this agent provides.
	Capabilities []string `json:"capabilities"`
	// MCPTools lists the MCP tool names this agent exposes to the host.
	MCPTools []string `json:"mcp_tools,omitempty"`
	// Outbound controls external network access for this agent's container.
	// Supported values:
	//   - "none": no internet access (default)
	//   - "proxy": agent may only reach the configured Proxy Agent
	//   - "direct": agent has direct outbound network access (discouraged)
	Outbound string `json:"outbound,omitempty"`
}

// TaskObject represents a discrete unit of work within the workflow.
// A workflow is composed of an ordered sequence of Tasks; each Task is
// executed by the agent referenced in AgentID.
type TaskObject struct {
	// TaskID is a UUID uniquely identifying this task within the workflow run.
	TaskID string `json:"task_id"`
	// AgentID references the AgentCard responsible for executing this task.
	AgentID string `json:"agent_id"`
	// Name is a short human-readable label (e.g. "triage", "sandbox-detonate").
	Name string `json:"name"`
	// Input is an arbitrary JSON payload consumed by the agent.
	Input map[string]any `json:"input"`
	// Output holds the agent's result after execution (nil until completed).
	Output map[string]any `json:"output,omitempty"`
	// Status is one of: pending | running | completed | failed | awaiting_approval.
	Status string `json:"status"`
	// OnHang determines how the orchestrator should respond when the task
	// appears to hang (heartbeat timeout or similar). Supported values:
	//   - "retry" (default): allow Temporal activity retries
	//   - "escalate": immediately escalate to a human (HITL) for approval
	// Set by the initial workflow input per-task.
	OnHang string `json:"on_hang,omitempty"`
	// Confidence is a normalised score [0.0, 1.0] indicating the agent's
	// certainty in its output. Values < 0.7 trigger a HITL pause.
	Confidence float64 `json:"confidence"`
	// Error contains a human-readable error message when Status == "failed".
	Error string `json:"error,omitempty"`
}

// Artifact represents an immutable file or data blob produced by an agent as
// part of its hand-off contract.  Artifacts are stored by-reference (URI) so
// large binaries do not bloat the state document.
type Artifact struct {
	// ArtifactID is a UUID identifying this artifact.
	ArtifactID string `json:"artifact_id"`
	// ProducedBy is the AgentID that created this artifact.
	ProducedBy string `json:"produced_by"`
	// Name is a short, filesystem-safe label (e.g. "updated_state.json").
	Name string `json:"name"`
	// MimeType describes the content type (e.g. "application/json").
	MimeType string `json:"mime_type"`
	// URI is a resolvable pointer to the artifact (file path, S3 URL, etc.).
	URI string `json:"uri"`
	// Checksum is the SHA-256 hex digest of the artifact contents used for
	// integrity verification during hand-off.
	Checksum string `json:"checksum,omitempty"`
}

// --------------------------------------------------------------------------
// WorkflowState — the A2A "source of truth"
// --------------------------------------------------------------------------

// WorkflowState is the single document that flows through every agent in the
// pipeline.  It is serialised to JSON and mounted into each container at
// /workspace/state.json.  After execution the container writes its updated
// copy back to the same path; the runner captures that file as an Artifact
// and deserialises it here before persisting to Postgres.
//
// Wire format is intentionally flat and self-describing so any language can
// parse it without a schema file.
type WorkflowState struct {
	// TraceID is a globally-unique identifier for this workflow run.
	// All log lines, database rows, and Temporal workflow executions share
	// this ID to enable end-to-end tracing.
	TraceID string `json:"trace_id" gorm:"primaryKey;column:trace_id"`

	// WorkflowID mirrors the Temporal workflow run ID for correlation.
	WorkflowID string `json:"workflow_id" gorm:"column:workflow_id;index"`

	// CurrentStep is the zero-based index of the task currently executing.
	CurrentStep int `json:"current_step" gorm:"column:current_step"`

	// Status is the overall pipeline status:
	//   pending | running | awaiting_approval | completed | failed
	Status string `json:"status" gorm:"column:status"`

	// AgentCards lists all agents registered for this workflow run.
	// Stored as JSONB in Postgres.
	AgentCards AgentCardList `json:"agent_cards" gorm:"column:agent_cards;type:jsonb"`

	// Tasks is the ordered list of work units for this run.
	// Stored as JSONB in Postgres.
	Tasks TaskList `json:"tasks" gorm:"column:tasks;type:jsonb"`

	// Artifacts accumulates every file produced during the run.
	// This forms the immutable audit trail / hand-off chain.
	// Stored as JSONB in Postgres.
	Artifacts ArtifactList `json:"artifacts" gorm:"column:artifacts;type:jsonb"`

	// Metadata holds arbitrary key-value pairs for pipeline-specific context
	// (e.g. alert source, severity, customer ID).
	Metadata map[string]any `json:"metadata,omitempty" gorm:"-"`

	// CreatedAt records when this workflow run was first initialised.
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`

	// UpdatedAt is refreshed on every state transition.
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
}

// TableName overrides the GORM table name.
func (WorkflowState) TableName() string { return "workflow_states" }

// --------------------------------------------------------------------------
// JSONB-compatible list types for GORM
// --------------------------------------------------------------------------

// AgentCardList is a slice of AgentCard that implements the GORM Scanner /
// Valuer interfaces so it can be stored as a Postgres JSONB column.
type AgentCardList []AgentCard

func (a AgentCardList) Value() (driver.Value, error) {
	return jsonMarshal(a)
}

func (a *AgentCardList) Scan(src any) error {
	return jsonUnmarshal(src, a)
}

// TaskList is a slice of TaskObject with JSONB support.
type TaskList []TaskObject

func (t TaskList) Value() (driver.Value, error) {
	return jsonMarshal(t)
}

func (t *TaskList) Scan(src any) error {
	return jsonUnmarshal(src, t)
}

// ArtifactList is a slice of Artifact with JSONB support.
type ArtifactList []Artifact

func (a ArtifactList) Value() (driver.Value, error) {
	return jsonMarshal(a)
}

func (a *ArtifactList) Scan(src any) error {
	return jsonUnmarshal(src, a)
}

// --------------------------------------------------------------------------
// JSONB helpers (used by all list types above)
// --------------------------------------------------------------------------

func jsonMarshal(v any) (driver.Value, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func jsonUnmarshal(src any, dst any) error {
	var b []byte
	switch s := src.(type) {
	case []byte:
		b = s
	case string:
		b = []byte(s)
	default:
		return errors.New("unsupported JSONB source type")
	}
	return json.Unmarshal(b, dst)
}
