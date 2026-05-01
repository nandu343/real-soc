// Package workflow contains the Temporal Activities used by the Real-SOC pipeline.
//
// An Activity is an ordinary Go function that performs a single, retryable unit
// of work.  Activities are registered with a Temporal Worker and scheduled by
// the Workflow (see workflow.go).  Any panic or returned error will cause
// Temporal to retry the Activity according to its RetryPolicy.
package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Runner runner.Runner
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

// CallAPIInput carries the parameters for executing a generic API node.
type CallAPIInput struct {
	State     *models.WorkflowState
	AgentCard models.AgentCard
	TaskIndex int
}

// CallAPIOutput carries the result from an API call activity.
type CallAPIOutput struct {
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

// CallAPI performs an HTTP request for an `api` node. It sends the task
// input as JSON payload to the configured AgentCard.Endpoint and expects
// an application/json response which will be stored in the task's Output.
func (a *Activities) CallAPI(ctx context.Context, input CallAPIInput) (CallAPIOutput, error) {
	baseLogger := activity.GetLogger(ctx)
	var logger templog.Logger
	if wl, ok := baseLogger.(templog.WithLogger); ok {
		logger = wl.With("trace_id", input.State.TraceID)
	} else {
		logger = baseLogger
	}

	logger.Info("CallAPI started",
		"agent", input.AgentCard.AgentID,
		"endpoint", input.AgentCard.Endpoint,
		"task_index", input.TaskIndex,
	)

	activity.RecordHeartbeat(ctx, "calling api")

	// Persist a before snapshot for time-travel/audit.
	if err := a.Store.SaveSnapshot(input.State, input.TaskIndex, "before_api"); err != nil {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: save snapshot (before): %w", err)
	}

	// Build request method and headers from task input and agent card.
	method := "POST"
	headers := map[string]string{"Content-Type": "application/json", "User-Agent": "real-soc-api-node/1.0"}
	if input.State.Tasks[input.TaskIndex].Input != nil {
		if m, ok := input.State.Tasks[input.TaskIndex].Input["_method"].(string); ok && m != "" {
			method = m
		}
		if hh, ok := input.State.Tasks[input.TaskIndex].Input["_headers"].(map[string]any); ok {
			for k, v := range hh {
				headers[k] = fmt.Sprint(v)
			}
		}
	}
	// AgentCard auth (bearer) takes precedence if present.
	if input.AgentCard.Auth != nil {
		if b, ok := input.AgentCard.Auth["bearer"].(string); ok && b != "" {
			headers["Authorization"] = "Bearer " + b
		}
	}

	if input.AgentCard.Endpoint == "" {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: agent %s has no endpoint configured", input.AgentCard.AgentID)
	}

	// Prepare body payload: copy task input minus control fields.
	bodyMap := map[string]any{}
	if input.State.Tasks[input.TaskIndex].Input != nil {
		for k, v := range input.State.Tasks[input.TaskIndex].Input {
			if k == "_method" || k == "_headers" {
				continue
			}
			bodyMap[k] = v
		}
	}

	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, input.AgentCard.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: new request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: remote returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed map[string]any
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		parsed = map[string]any{"text": string(respBytes)}
	}

	// Update state with output
	updated := input.State
	if updated.Tasks == nil || input.TaskIndex < 0 || input.TaskIndex >= len(updated.Tasks) {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: invalid task index %d", input.TaskIndex)
	}
	updated.Tasks[input.TaskIndex].Output = parsed
	updated.Tasks[input.TaskIndex].Status = "completed"
	updated.Tasks[input.TaskIndex].Confidence = 1.0

	// Compute checksum and create artifact
	sum := sha256.Sum256(respBytes)
	checksum := hex.EncodeToString(sum[:])
	artifact := models.Artifact{
		ArtifactID: fmt.Sprintf("api-%d", time.Now().UnixNano()),
		ProducedBy: input.AgentCard.AgentID,
		Name:       "api-response.json",
		MimeType:   "application/json",
		URI:        fmt.Sprintf("%s#response", input.AgentCard.Endpoint),
		Checksum:   checksum,
	}

	updated.Artifacts = append(updated.Artifacts, artifact)
	updated.UpdatedAt = time.Now().UTC()

	if err := a.Store.Save(updated); err != nil {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: save state: %w", err)
	}

	if err := a.Store.SaveSnapshot(updated, input.TaskIndex, "after_api"); err != nil {
		return CallAPIOutput{}, fmt.Errorf("activity CallAPI: save snapshot (after): %w", err)
	}

	logger.Info("CallAPI completed", "agent", input.AgentCard.AgentID, "status", resp.StatusCode)

	return CallAPIOutput{UpdatedState: updated, Artifact: artifact, Logs: string(respBytes)}, nil
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
