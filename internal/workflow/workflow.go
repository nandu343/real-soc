// Package workflow contains the Temporal Workflow definition for the SOC-OS
// agentic pipeline.
//
// Workflow overview
// ─────────────────
// The AgentPipelineWorkflow orchestrates a sequence of agent containers.
// Each step:
//   1. Executes the agent via the RunAgent activity.
//   2. Inspects the confidence score of the completed task.
//   3. If confidence < HITLThreshold (0.7), pauses and waits for a human
//      operator to send an ApproveStep signal with an optional JSON edit to
//      the state.  This is the "Human-in-the-Loop" (HITL) gate.
//   4. Advances to the next agent in the pipeline.
//
// Temporal guarantees that all state is durably checkpointed between steps,
// so the workflow survives worker restarts without losing progress.
package workflow

import (
	"time"

	"github.com/nandu343/real-soc/internal/models"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// TaskQueueName is the Temporal task queue that the worker listens on.
	TaskQueueName = "soc-os-task-queue"

	// HITLThreshold is the minimum confidence score required to skip the
	// Human-in-the-Loop approval gate.  Tasks with a lower score pause
	// the workflow until an operator sends the ApproveStep signal.
	HITLThreshold = 0.7

	// ApproveStepSignal is the name of the Temporal signal that an operator
	// sends to resume a paused workflow step.
	ApproveStepSignal = "ApproveStep"

	// HITLTimeout is the maximum time the workflow will wait for an operator
	// approval before failing the run.  In production, adjust to match your
	// on-call SLA.
	HITLTimeout = 24 * time.Hour
)

// --------------------------------------------------------------------------
// Signal payloads
// --------------------------------------------------------------------------

// ApproveStepPayload is the JSON body that an operator sends via the
// ApproveStep signal.
//
// The operator may optionally provide an EditedState to apply manual
// corrections to the agent's output before the pipeline continues.
type ApproveStepPayload struct {
	// Approved must be true for the pipeline to continue.
	// If false, the workflow transitions to "failed".
	Approved bool `json:"approved"`
	// EditedState is an optional replacement for the current WorkflowState.
	// Use this to fix agent errors before the next step runs.
	EditedState *models.WorkflowState `json:"edited_state,omitempty"`
	// Reviewer is the identity (username / email) of the approving operator.
	Reviewer string `json:"reviewer"`
	// Comment is a free-text audit note.
	Comment string `json:"comment,omitempty"`
}

// --------------------------------------------------------------------------
// Workflow input / output
// --------------------------------------------------------------------------

// PipelineInput carries the initial WorkflowState when starting a new run.
type PipelineInput struct {
	InitialState *models.WorkflowState
}

// PipelineOutput carries the terminal WorkflowState when the run completes.
type PipelineOutput struct {
	FinalState *models.WorkflowState
	StepCount  int
}

// --------------------------------------------------------------------------
// Workflow definition
// --------------------------------------------------------------------------

// AgentPipelineWorkflow is the top-level Temporal Workflow function.
//
// It iterates over the tasks defined in the initial state, executing each one
// via the RunAgent activity, and inserts a HITL gate whenever the agent's
// confidence score drops below HITLThreshold.
//
// The function signature matches the Temporal SDK's workflow registration
// requirement: (workflow.Context, <input>) (<output>, error).
func AgentPipelineWorkflow(ctx workflow.Context, input PipelineInput) (PipelineOutput, error) {
	logger := workflow.GetLogger(ctx)
	state := input.InitialState

	// Register a query handler so external callers can inspect the live state
	// without interrupting the workflow.
	if err := workflow.SetQueryHandler(ctx, "getState", func() (*models.WorkflowState, error) {
		return state, nil
	}); err != nil {
		return PipelineOutput{}, err
	}

	// Persist the initial state via the PersistState activity.
	state.Status = "running"
	if err := persistState(ctx, state); err != nil {
		return PipelineOutput{}, err
	}

	// Iterate through each task in order.
	for i := range state.Tasks {
		task := &state.Tasks[i]
		task.Status = "running"
		logger.Info("executing task", "index", i, "name", task.Name, "agent", task.AgentID)

		// Find the AgentCard for this task.
		card, err := findAgentCard(state, task.AgentID)
		if err != nil {
			task.Status = "failed"
			task.Error = err.Error()
			state.Status = "failed"
			_ = persistState(ctx, state)
			return PipelineOutput{FinalState: state, StepCount: i}, err
		}

		// Execute the agent container via the RunAgent activity.
		activityOptions := workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Minute,
			HeartbeatTimeout:    30 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    5 * time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    60 * time.Second,
				MaximumAttempts:    3,
			},
		}
		actCtx := workflow.WithActivityOptions(ctx, activityOptions)

		var agentOut RunAgentOutput
		err = workflow.ExecuteActivity(actCtx, (*Activities).RunAgent, RunAgentInput{
			State:     state,
			AgentCard: card,
			TaskIndex: i,
		}).Get(actCtx, &agentOut)
		if err != nil {
			task.Status = "failed"
			task.Error = err.Error()
			state.Status = "failed"
			_ = persistState(ctx, state)
			return PipelineOutput{FinalState: state, StepCount: i}, err
		}

		// Merge the agent's updated state back into our local copy.
		state = agentOut.UpdatedState
		state.CurrentStep = i + 1

		// ----------------------------------------------------------------
		// HITL gate: pause if confidence < HITLThreshold
		// ----------------------------------------------------------------
		if task.Confidence < HITLThreshold {
			logger.Info("low confidence — waiting for HITL approval",
				"task", task.Name,
				"confidence", task.Confidence,
			)

			task.Status = "awaiting_approval"
			state.Status = "awaiting_approval"
			if err := persistState(ctx, state); err != nil {
				return PipelineOutput{FinalState: state, StepCount: i}, err
			}

			// Block on the ApproveStep signal.
			var approval ApproveStepPayload
			signalCh := workflow.GetSignalChannel(ctx, ApproveStepSignal)

			// Use a selector so we can also handle the timeout case.
			selector := workflow.NewSelector(ctx)
			timerFired := false

			selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) {
				ch.Receive(ctx, &approval)
			})
			selector.AddFuture(workflow.NewTimer(ctx, HITLTimeout), func(f workflow.Future) {
				timerFired = true
			})

			selector.Select(ctx)

			if timerFired {
				state.Status = "failed"
				_ = persistState(ctx, state)
				return PipelineOutput{FinalState: state, StepCount: i},
					temporal.NewApplicationError("HITL timeout: no approval received", "HITLTimeout")
			}

			if !approval.Approved {
				state.Status = "failed"
				_ = persistState(ctx, state)
				return PipelineOutput{FinalState: state, StepCount: i},
					temporal.NewApplicationError("HITL rejected by "+approval.Reviewer, "HITLRejected")
			}

			// Apply the operator's manual edits if provided.
			if approval.EditedState != nil {
				state = approval.EditedState
				logger.Info("HITL state edit applied", "reviewer", approval.Reviewer)
			}

			task.Status = "completed"
			state.Status = "running"
			logger.Info("HITL approved", "reviewer", approval.Reviewer, "comment", approval.Comment)
		} else {
			task.Status = "completed"
		}

		if err := persistState(ctx, state); err != nil {
			return PipelineOutput{FinalState: state, StepCount: i}, err
		}
	}

	state.Status = "completed"
	if err := persistState(ctx, state); err != nil {
		return PipelineOutput{FinalState: state, StepCount: len(state.Tasks)}, err
	}

	logger.Info("pipeline completed", "trace_id", state.TraceID, "steps", len(state.Tasks))
	return PipelineOutput{FinalState: state, StepCount: len(state.Tasks)}, nil
}

// --------------------------------------------------------------------------
// Private helpers
// --------------------------------------------------------------------------

// persistState calls the PersistState activity with a short timeout.
func persistState(ctx workflow.Context, state *models.WorkflowState) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 5,
		},
	}
	return workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, ao),
		(*Activities).PersistState,
		state,
	).Get(ctx, nil)
}

// findAgentCard searches the state's AgentCards for the card with the given ID.
func findAgentCard(state *models.WorkflowState, agentID string) (models.AgentCard, error) {
	for _, card := range state.AgentCards {
		if card.AgentID == agentID {
			return card, nil
		}
	}
	return models.AgentCard{}, temporal.NewApplicationError(
		"agent card not found: "+agentID, "AgentNotFound",
	)
}
