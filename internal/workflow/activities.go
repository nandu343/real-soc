// Package workflow contains the Temporal Activities used by the Real-SOC pipeline.
//
// An Activity is an ordinary Go function that performs a single, retryable unit
// of work.  Activities are registered with a Temporal Worker and scheduled by
// the Workflow (see workflow.go).  Any panic or returned error will cause
// Temporal to retry the Activity according to its RetryPolicy.
package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/nandu343/real-soc/internal/db"
	"github.com/nandu343/real-soc/internal/models"
	"github.com/nandu343/real-soc/internal/runner"
	"go.temporal.io/sdk/activity"
	templog "go.temporal.io/sdk/log"
)

// Activities bundles all dependencies needed by the activity functions.
// A single instance is registered with the Temporal worker at startup.
type Activities struct {
	Runner *runner.DockerRunner
	Store  *db.Store
}

// --------------------------------------------------------------------------
// RunAgentActivity
// --------------------------------------------------------------------------

// RunAgentInput carries the parameters for the RunAgent activity.
type RunAgentInput struct {
	State     *models.WorkflowState
	AgentCard models.AgentCard
	TaskIndex int
}

// RunAgentOutput carries the result from the RunAgent activity.
type RunAgentOutput struct {
	UpdatedState *models.WorkflowState
	Artifact     models.Artifact
	Logs         string
}

// RunAgent executes a single agent container via the DockerRunner and
// persists the updated state to Postgres.
//
// This function is registered as a Temporal Activity; Temporal will call it
// inside a worker goroutine and handle retries on transient failures.
func (a *Activities) RunAgent(ctx context.Context, input RunAgentInput) (RunAgentOutput, error) {
	baseLogger := activity.GetLogger(ctx)
	var logger templog.Logger
	if wl, ok := baseLogger.(templog.WithLogger); ok {
		logger = wl.With("trace_id", input.State.TraceID)
	} else {
		logger = baseLogger
	}
	logger.Info("RunAgent started",
		"agent", input.AgentCard.AgentID,
		"image", input.AgentCard.Image,
		"task_index", input.TaskIndex,
	)

	// Heartbeat so Temporal knows the activity is still alive during a long pull.
	activity.RecordHeartbeat(ctx, "pulling image")

	// Persist a "before" snapshot so we can reconstruct the state prior
	// to agent execution (time-machine capability).
	if err := a.Store.SaveSnapshot(input.State, input.TaskIndex, "before"); err != nil {
		return RunAgentOutput{}, fmt.Errorf("activity RunAgent: save snapshot (before): %w", err)
	}

	out, err := a.Runner.RunAgent(ctx, runner.AgentRunInput{
		State:     input.State,
		AgentCard: input.AgentCard,
		TaskIndex: input.TaskIndex,
	})
	if err != nil {
		return RunAgentOutput{}, fmt.Errorf("activity RunAgent: %w", err)
	}

	activity.RecordHeartbeat(ctx, "persisting state")

	// Append the artifact to the updated state before persisting.
	out.UpdatedState.Artifacts = append(out.UpdatedState.Artifacts, out.Artifact)
	out.UpdatedState.UpdatedAt = time.Now().UTC()

	if err := a.Store.Save(out.UpdatedState); err != nil {
		return RunAgentOutput{}, fmt.Errorf("activity RunAgent: persist state: %w", err)
	}

	// Persist an "after" snapshot capturing the exact state produced by the
	// agent.  This is independent of the primary workflow_states row and
	// creates the append-only timeline used for auditing / time travel.
	if err := a.Store.SaveSnapshot(out.UpdatedState, input.TaskIndex, "after"); err != nil {
		return RunAgentOutput{}, fmt.Errorf("activity RunAgent: save snapshot (after): %w", err)
	}

	logger.Info("RunAgent completed",
		"agent", input.AgentCard.AgentID,
		"checksum", out.Artifact.Checksum,
		"confidence", out.UpdatedState.Tasks[input.TaskIndex].Confidence,
	)

	return RunAgentOutput{
		UpdatedState: out.UpdatedState,
		Artifact:     out.Artifact,
		Logs:         out.Logs,
	}, nil
}

// --------------------------------------------------------------------------
// PersistStateActivity
// --------------------------------------------------------------------------

// PersistState saves an arbitrary WorkflowState snapshot to Postgres.
// This is called at the start and end of the workflow, and after any HITL
// approval, to ensure a complete audit trail.
func (a *Activities) PersistState(ctx context.Context, state *models.WorkflowState) error {
	if err := a.Store.Save(state); err != nil {
		return fmt.Errorf("activity PersistState: %w", err)
	}
	base := activity.GetLogger(ctx)
	if wl, ok := base.(templog.WithLogger); ok {
		wl.With("trace_id", state.TraceID).Info("state persisted", "status", state.Status)
	} else {
		base.Info("state persisted", "trace_id", state.TraceID, "status", state.Status)
	}
	return nil
}
