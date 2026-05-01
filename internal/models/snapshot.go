package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// WorkflowStateSnapshot stores an immutable JSON snapshot of a WorkflowState
// at a particular point in time (e.g., before/after an agent run).  This
// enables a time-machine style audit trail independent of the primary
// workflow_states table.
type WorkflowStateSnapshot struct {
	ID           string    `json:"id" gorm:"primaryKey;column:id"`
	WorkflowID   string    `json:"workflow_id" gorm:"column:workflow_id;index"`
	TraceID      string    `json:"trace_id" gorm:"column:trace_id;index"`
	Step         int       `json:"step" gorm:"column:step"`
	SnapshotType string    `json:"snapshot_type" gorm:"column:snapshot_type"` // e.g. "before" | "after"
	StateJSON    string    `json:"state" gorm:"column:state;type:jsonb"`
	CreatedAt    time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

// TableName overrides the GORM table name used for snapshots.
func (WorkflowStateSnapshot) TableName() string { return "workflow_state_snapshots" }

// NewSnapshotFromState builds a WorkflowStateSnapshot from a WorkflowState.
func NewSnapshotFromState(state *WorkflowState, step int, snapType string) (*WorkflowStateSnapshot, error) {
	b, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return &WorkflowStateSnapshot{
		ID:           uuid.NewString(),
		WorkflowID:   state.WorkflowID,
		TraceID:      state.TraceID,
		Step:         step,
		SnapshotType: snapType,
		StateJSON:    string(b),
	}, nil
}
