// Package workflow contains the Temporal Workflow definition for the Real-SOC
// agentic pipeline.
//
// Workflow overview
// ─────────────────
// The AgentPipelineWorkflow orchestrates a sequence of agent containers.
// Each step:
//  1. Executes the agent via the RunAgent activity.
//  2. Inspects the confidence score of the completed task.
//  3. If confidence < HITLThreshold (0.7), pauses and waits for a human
//     operator to send an ApproveStep signal with an optional JSON edit to
//     the state.  This is the "Human-in-the-Loop" (HITL) gate.
//  4. Advances to the next agent in the pipeline.
//
// Temporal guarantees that all state is durably checkpointed between steps,
// so the workflow survives worker restarts without losing progress.
package workflow

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nandu343/real-soc/internal/models"
	templog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// TaskQueueName is the Temporal task queue that the worker listens on.
	TaskQueueName = "real-soc-task-queue"

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

// HumanInputSignal is the name of the Temporal signal used to deliver a
// human-edited WorkflowState back into a paused workflow step.
const HumanInputSignal = "HumanInputSignal"

// PausedForHumanStatus is the workflow state used when execution is paused
// awaiting a human analyst to edit the state.json.
const PausedForHumanStatus = "paused_for_human"

// humanResponseTimeout reads HUMAN_RESPONSE_TIMEOUT_SECONDS from the
// environment (in seconds) and returns a duration. If unset or invalid
// it falls back to 24 hours.
func humanResponseTimeout() time.Duration {
	s := os.Getenv("HUMAN_RESPONSE_TIMEOUT_SECONDS")
	if s == "" {
		return 24 * time.Hour
	}
	sec, err := strconv.Atoi(s)
	if err != nil || sec <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(sec) * time.Second
}

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
	// Attach a workflow-scoped logger and include the TraceID so every
	// workflow log line contains the trace identifier.
	state := input.InitialState
	baseLogger := workflow.GetLogger(ctx)
	var logger templog.Logger
	if wl, ok := baseLogger.(templog.WithLogger); ok {
		logger = wl.With("trace_id", state.TraceID)
	} else {
		logger = baseLogger
	}

	// Register a query handler so external callers can inspect the live state
	// without interrupting the workflow.
	if err := workflow.SetQueryHandler(ctx, "getState", func() (*models.WorkflowState, error) {
		return state, nil
	}); err != nil {
		return PipelineOutput{}, err
	}

	// Listen for RestoreState signals so external actors can replace the
	// in-flight WorkflowState.  We spawn a non-blocking goroutine that
	// receives any RestoreState payloads and persists them.
	restoreCh := workflow.GetSignalChannel(ctx, "RestoreState")
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var restored models.WorkflowState
			restoreCh.Receive(ctx, &restored)
			logger.Info("RestoreState signal received", "trace_id", restored.TraceID)
			state = &restored
			if err := persistState(ctx, state); err != nil {
				logger.Error("failed to persist restored state", "error", err)
			}
		}
	})

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

		// If the agent card is a human analyst, pause the workflow and wait
		// for a HumanInputSignal containing the edited WorkflowState. We also
		// start a timer (SLA) so the workflow can escalate if the analyst
		// doesn't respond within the configured window.
		if strings.EqualFold(card.Type, "human") {
			logger.Info("pausing for human input", "task", task.Name, "index", i)

			task.Status = "paused_for_human"
			state.Status = PausedForHumanStatus
			state.CurrentStep = i
			if err := persistState(ctx, state); err != nil {
				return PipelineOutput{FinalState: state, StepCount: i}, err
			}

			// Block on HumanInputSignal or timer
			var humanState models.WorkflowState
			signalCh := workflow.GetSignalChannel(ctx, HumanInputSignal)
			selector := workflow.NewSelector(ctx)
			timerFired := false

			selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) {
				ch.Receive(ctx, &humanState)
			})
			selector.AddFuture(workflow.NewTimer(ctx, humanResponseTimeout()), func(f workflow.Future) {
				timerFired = true
			})

			selector.Select(ctx)

			if timerFired {
				// Escalation: mark the task as skipped and continue.
				logger.Warn("human response timeout — escalating/skip step", "task", task.Name, "index", i)
				task.Status = "skipped"
				task.Error = "human input timeout"
				state.Status = "running"
				if err := persistState(ctx, state); err != nil {
					return PipelineOutput{FinalState: state, StepCount: i}, err
				}
				// continue to next task
				continue
			}

			// Apply the human-edited state received via signal.
			state = &humanState
			state.CurrentStep = i
			task.Status = "completed"
			state.Status = "running"
			logger.Info("human input applied — resuming", "task", task.Name, "index", i)
			if err := persistState(ctx, state); err != nil {
				return PipelineOutput{FinalState: state, StepCount: i}, err
			}
			// Move to next task
			continue
		}

		// Execute the agent. For `api` typed AgentCards we perform a generic
		// HTTP call activity (CallAPI). Otherwise we use the RunAgent activity
		// which drives container-based or remote-agent runners.
		maxAttempts := int32(3)
		if task.OnHang == "escalate" {
			maxAttempts = 1
		}
		activityOptions := workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Minute,
			HeartbeatTimeout:    30 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    5 * time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    60 * time.Second,
				MaximumAttempts:    maxAttempts,
			},
		}
		actCtx := workflow.WithActivityOptions(ctx, activityOptions)

		// Expand task inputs by resolving any dot-notation references to
		// previous task outputs ("name".output.x). This allows tasks to
		// reference earlier outputs in their input payloads.
		if err := ExpandInputs(task, state); err != nil {
			// Log and continue — make expansion failures surface as task errors
			task.Status = "failed"
			task.Error = err.Error()
			state.Status = "failed"
			_ = persistState(ctx, state)
			return PipelineOutput{FinalState: state, StepCount: i}, err
		}

		// Branch for API nodes
		if strings.EqualFold(card.Type, "api") || strings.EqualFold(card.Type, "http") {
			var apiOut CallAPIOutput
			err = workflow.ExecuteActivity(actCtx, (*Activities).CallAPI, CallAPIInput{
				State:     state,
				AgentCard: card,
				TaskIndex: i,
			}).Get(actCtx, &apiOut)
			if err != nil {
				if task.OnHang == "escalate" {
					logger.Info("escalating task to HITL due to activity error", "task", task.Name, "error", err)

					task.Status = "awaiting_approval"
					task.Error = err.Error()
					state.Status = "awaiting_approval"
					if err2 := persistState(ctx, state); err2 != nil {
						return PipelineOutput{FinalState: state, StepCount: i}, err2
					}

					var approval ApproveStepPayload
					signalCh := workflow.GetSignalChannel(ctx, ApproveStepSignal)
					selector := workflow.NewSelector(ctx)
					timerFired := false

					selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) { ch.Receive(ctx, &approval) })
					selector.AddFuture(workflow.NewTimer(ctx, HITLTimeout), func(f workflow.Future) { timerFired = true })

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

					if approval.EditedState != nil {
						state = approval.EditedState
						logger.Info("HITL state edit applied (escalation)", "reviewer", approval.Reviewer)
					}

					task.Status = "completed"
					state.Status = "running"
					logger.Info("HITL approved (escalation)", "reviewer", approval.Reviewer, "comment", approval.Comment)

				} else {
					task.Status = "failed"
					task.Error = err.Error()
					state.Status = "failed"
					_ = persistState(ctx, state)
					return PipelineOutput{FinalState: state, StepCount: i}, err
				}
			} else {
				// Merge updated state onto our running state so that agents
				// that return a partial state do not clobber previous outputs.
				state = MergeWorkflowStates(state, apiOut.UpdatedState)
				state.CurrentStep = i + 1
			}
		} else {
			var agentOut RunAgentOutput
			err = workflow.ExecuteActivity(actCtx, (*Activities).RunAgent, RunAgentInput{
				State:     state,
				AgentCard: card,
				TaskIndex: i,
			}).Get(actCtx, &agentOut)
			if err != nil {
				if task.OnHang == "escalate" {
					logger.Info("escalating task to HITL due to activity error", "task", task.Name, "error", err)

					task.Status = "awaiting_approval"
					task.Error = err.Error()
					state.Status = "awaiting_approval"
					if err2 := persistState(ctx, state); err2 != nil {
						return PipelineOutput{FinalState: state, StepCount: i}, err2
					}

					var approval ApproveStepPayload
					signalCh := workflow.GetSignalChannel(ctx, ApproveStepSignal)
					selector := workflow.NewSelector(ctx)
					timerFired := false

					selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) { ch.Receive(ctx, &approval) })
					selector.AddFuture(workflow.NewTimer(ctx, HITLTimeout), func(f workflow.Future) { timerFired = true })

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

					if approval.EditedState != nil {
						state = approval.EditedState
						logger.Info("HITL state edit applied (escalation)", "reviewer", approval.Reviewer)
					}

					task.Status = "completed"
					state.Status = "running"
					logger.Info("HITL approved (escalation)", "reviewer", approval.Reviewer, "comment", approval.Comment)

				} else {
					task.Status = "failed"
					task.Error = err.Error()
					state.Status = "failed"
					_ = persistState(ctx, state)
					return PipelineOutput{FinalState: state, StepCount: i}, err
				}
			}

			// Merge the agent's updated state back into our local copy so
			// previous task outputs are preserved and new values overlay.
			state = MergeWorkflowStates(state, agentOut.UpdatedState)
			state.CurrentStep = i + 1
		}

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

// InterpreterWorkflow runs a graph-based workflow where tasks reference the
// next task via the TaskObject.Next field. This allows dynamic, non-linear
// pipelines (XyFlow-style graphs) to be executed by the same RunAgent
// activity used by the linear pipeline.
func InterpreterWorkflow(ctx workflow.Context, input PipelineInput) (PipelineOutput, error) {
	state := input.InitialState
	baseLogger := workflow.GetLogger(ctx)
	var logger templog.Logger
	if wl, ok := baseLogger.(templog.WithLogger); ok {
		logger = wl.With("trace_id", state.TraceID)
	} else {
		logger = baseLogger
	}

	if err := workflow.SetQueryHandler(ctx, "getState", func() (*models.WorkflowState, error) {
		return state, nil
	}); err != nil {
		return PipelineOutput{}, err
	}

	// RestoreState signal handling (same as linear workflow)
	restoreCh := workflow.GetSignalChannel(ctx, "RestoreState")
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var restored models.WorkflowState
			restoreCh.Receive(ctx, &restored)
			logger.Info("RestoreState signal received", "trace_id", restored.TraceID)
			state = &restored
			if err := persistState(ctx, state); err != nil {
				logger.Error("failed to persist restored state", "error", err)
			}
		}
	})

	// Persist initial state
	state.Status = "running"
	if err := persistState(ctx, state); err != nil {
		return PipelineOutput{}, err
	}

	// Build lookup map from task_id -> index
	idToIndex := make(map[string]int)
	for i := range state.Tasks {
		idToIndex[state.Tasks[i].TaskID] = i
	}

	if len(state.Tasks) == 0 {
		state.Status = "completed"
		_ = persistState(ctx, state)
		return PipelineOutput{FinalState: state, StepCount: 0}, nil
	}

	// Determine start node: prefer state.CurrentStep if valid, else first task
	var currentTaskID string
	if state.CurrentStep >= 0 && state.CurrentStep < len(state.Tasks) {
		currentTaskID = state.Tasks[state.CurrentStep].TaskID
	} else {
		currentTaskID = state.Tasks[0].TaskID
	}

	// visited tracks how many times we've executed a task id. We allow
	// revisiting a task when it is explicitly configured as a loop.
	visited := make(map[string]int)
	steps := 0

	for currentTaskID != "" {
		idx, ok := idToIndex[currentTaskID]
		if !ok {
			logger.Error("task id not found in state", "task_id", currentTaskID)
			state.Status = "failed"
			_ = persistState(ctx, state)
			return PipelineOutput{FinalState: state, StepCount: steps}, temporal.NewApplicationError("task id not found: "+currentTaskID, "TaskNotFound")
		}

		if visited[currentTaskID] > 0 {
			// If this task is not configured as a loop, treat revisiting as
			// a cycle error. Loop tasks are allowed to be re-entered.
			if !(state.Tasks[idx].Input != nil && state.Tasks[idx].Input["loop"] != nil) {
				logger.Error("cycle detected in workflow graph", "task_id", currentTaskID)
				state.Status = "failed"
				_ = persistState(ctx, state)
				return PipelineOutput{FinalState: state, StepCount: steps}, temporal.NewApplicationError("cycle detected in graph", "GraphCycle")
			}
		}
		visited[currentTaskID]++

		task := &state.Tasks[idx]
		task.Status = "running"
		logger.Info("interpreting task", "index", idx, "task_id", task.TaskID, "agent", task.AgentID)

		// Find AgentCard
		card, err := findAgentCard(state, task.AgentID)
		if err != nil {
			task.Status = "failed"
			task.Error = err.Error()
			state.Status = "failed"
			_ = persistState(ctx, state)
			return PipelineOutput{FinalState: state, StepCount: steps}, err
		}

		// If this task is a human agent, pause and wait for human input
		if strings.EqualFold(card.Type, "human") {
			logger.Info("interpreting task requires human input — pausing", "task", task.Name, "task_id", task.TaskID)

			task.Status = "paused_for_human"
			state.Status = PausedForHumanStatus
			state.CurrentStep = idx
			if err := persistState(ctx, state); err != nil {
				return PipelineOutput{FinalState: state, StepCount: steps}, err
			}

			var humanState models.WorkflowState
			signalCh := workflow.GetSignalChannel(ctx, HumanInputSignal)
			selector := workflow.NewSelector(ctx)
			timerFired := false
			selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) { ch.Receive(ctx, &humanState) })
			selector.AddFuture(workflow.NewTimer(ctx, humanResponseTimeout()), func(f workflow.Future) { timerFired = true })
			selector.Select(ctx)

			if timerFired {
				logger.Warn("human response timeout — skipping task", "task_id", task.TaskID)
				task.Status = "skipped"
				task.Error = "human input timeout"
				state.Status = "running"
				if err := persistState(ctx, state); err != nil {
					return PipelineOutput{FinalState: state, StepCount: steps}, err
				}
				// move to next task in graph
				steps++
				nextID := state.Tasks[idx].Next
				if nextID == "" {
					break
				}
				currentTaskID = nextID
				continue
			}

			// Human provided a modified state
			state = &humanState
			state.CurrentStep = idx
			task.Status = "completed"
			state.Status = "running"
			if err := persistState(ctx, state); err != nil {
				return PipelineOutput{FinalState: state, StepCount: steps}, err
			}
			// Continue following the Next pointer from the current task
			steps++
			nextID := state.Tasks[idx].Next
			if nextID == "" {
				break
			}
			currentTaskID = nextID
			continue
		}

		// Execute activity
		maxAttempts := int32(3)
		if task.OnHang == "escalate" {
			maxAttempts = 1
		}
		activityOptions := workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Minute,
			HeartbeatTimeout:    30 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    5 * time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    60 * time.Second,
				MaximumAttempts:    maxAttempts,
			},
		}
		actCtx := workflow.WithActivityOptions(ctx, activityOptions)

		var agentOut RunAgentOutput
		err = workflow.ExecuteActivity(actCtx, (*Activities).RunAgent, RunAgentInput{
			State:     state,
			AgentCard: card,
			TaskIndex: idx,
		}).Get(actCtx, &agentOut)
		if err != nil {
			// Use existing escalation/HITL handling if configured
			if task.OnHang == "escalate" {
				logger.Info("escalating task to HITL due to activity error", "task", task.Name, "error", err)

				task.Status = "awaiting_approval"
				task.Error = err.Error()
				state.Status = "awaiting_approval"
				if err2 := persistState(ctx, state); err2 != nil {
					return PipelineOutput{FinalState: state, StepCount: steps}, err2
				}

				var approval ApproveStepPayload
				signalCh := workflow.GetSignalChannel(ctx, ApproveStepSignal)
				selector := workflow.NewSelector(ctx)
				timerFired := false
				selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) { ch.Receive(ctx, &approval) })
				selector.AddFuture(workflow.NewTimer(ctx, HITLTimeout), func(f workflow.Future) { timerFired = true })
				selector.Select(ctx)

				if timerFired {
					state.Status = "failed"
					_ = persistState(ctx, state)
					return PipelineOutput{FinalState: state, StepCount: steps}, temporal.NewApplicationError("HITL timeout: no approval received", "HITLTimeout")
				}

				if !approval.Approved {
					state.Status = "failed"
					_ = persistState(ctx, state)
					return PipelineOutput{FinalState: state, StepCount: steps}, temporal.NewApplicationError("HITL rejected by "+approval.Reviewer, "HITLRejected")
				}

				if approval.EditedState != nil {
					state = approval.EditedState
					logger.Info("HITL state edit applied (escalation)", "reviewer", approval.Reviewer)
				}

				task.Status = "completed"
				state.Status = "running"
				logger.Info("HITL approved (escalation)", "reviewer", approval.Reviewer, "comment", approval.Comment)

			} else {
				task.Status = "failed"
				task.Error = err.Error()
				state.Status = "failed"
				_ = persistState(ctx, state)
				return PipelineOutput{FinalState: state, StepCount: steps}, err
			}
		}

		// Merge agent state (do not clobber previous outputs; overlay them)
		state = MergeWorkflowStates(state, agentOut.UpdatedState)
		state.CurrentStep = idx

		// Check confidence
		if task.Confidence < HITLThreshold {
			logger.Info("low confidence — waiting for HITL approval", "task", task.Name, "confidence", task.Confidence)
			task.Status = "awaiting_approval"
			state.Status = "awaiting_approval"
			if err := persistState(ctx, state); err != nil {
				return PipelineOutput{FinalState: state, StepCount: steps}, err
			}

			var approval ApproveStepPayload
			signalCh := workflow.GetSignalChannel(ctx, ApproveStepSignal)
			selector := workflow.NewSelector(ctx)
			timerFired := false
			selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) { ch.Receive(ctx, &approval) })
			selector.AddFuture(workflow.NewTimer(ctx, HITLTimeout), func(f workflow.Future) { timerFired = true })
			selector.Select(ctx)

			if timerFired {
				state.Status = "failed"
				_ = persistState(ctx, state)
				return PipelineOutput{FinalState: state, StepCount: steps}, temporal.NewApplicationError("HITL timeout: no approval received", "HITLTimeout")
			}

			if !approval.Approved {
				state.Status = "failed"
				_ = persistState(ctx, state)
				return PipelineOutput{FinalState: state, StepCount: steps}, temporal.NewApplicationError("HITL rejected by "+approval.Reviewer, "HITLRejected")
			}

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
			return PipelineOutput{FinalState: state, StepCount: steps}, err
		}

		steps++

		// Loop handling: if this task contains a `loop` configuration in its
		// input, interpret it here and decide whether to repeat (point Next
		// back to self) or advance to the configured Next task.
		nextID := state.Tasks[idx].Next
		if state.Tasks[idx].Input != nil {
			if lraw, ok := state.Tasks[idx].Input["loop"]; ok {
				// Expect loop config to be a map[string]any with keys `mode`,
				// `count` or `condition`.
				if lcfg, ok2 := lraw.(map[string]any); ok2 {
					mode := "count"
					if m, ok3 := lcfg["mode"].(string); ok3 && m != "" {
						mode = m
					}

					// Ensure output map exists so we can persist iteration state
					if state.Tasks[idx].Output == nil {
						state.Tasks[idx].Output = make(map[string]any)
					}

					if mode == "count" {
						// Read configured count (may be float64 from JSON)
						var maxIter int
						switch v := lcfg["count"].(type) {
						case float64:
							maxIter = int(v)
						case int:
							maxIter = v
						case string:
							if parsed, err := strconv.Atoi(v); err == nil {
								maxIter = parsed
							}
						default:
							maxIter = 0
						}
						// read current iteration
						cur := 0
						if x, ok := state.Tasks[idx].Output["_loop_iter"]; ok {
							switch vv := x.(type) {
							case float64:
								cur = int(vv)
							case int:
								cur = vv
							}
						}
						cur++
						state.Tasks[idx].Output["_loop_iter"] = cur

						if maxIter > 0 && cur < maxIter {
							// repeat
							nextID = state.Tasks[idx].TaskID
						} else {
							// finished looping — advance to configured Next
							// (nextID already holds state.Tasks[idx].Next)
						}
					} else if mode == "condition" {
						// condition is expected to be a dot-notated reference
						// like `"OtherTask".output.some.path` which we can
						// resolve using ResolveDotReference. If it resolves to
						// truthy value we repeat; otherwise advance.
						if condRaw, ok := lcfg["condition"].(string); ok && condRaw != "" {
							val, rerr := ResolveDotReference(condRaw, state)
							if rerr == nil {
								// interpret truthiness
								doLoop := false
								switch tv := val.(type) {
								case bool:
									doLoop = tv
								case float64:
									doLoop = tv != 0
								case int:
									doLoop = tv != 0
								case string:
									doLoop = tv != "" && tv != "0"
								default:
									doLoop = tv != nil
								}
								if doLoop {
									nextID = state.Tasks[idx].TaskID
								}
							}
						}
					}
				}
			}
		}

		if nextID == "" {
			// no next -> terminate
			break
		}
		currentTaskID = nextID
	}

	state.Status = "completed"
	if err := persistState(ctx, state); err != nil {
		return PipelineOutput{FinalState: state, StepCount: steps}, err
	}

	logger.Info("interpreter pipeline completed", "trace_id", state.TraceID, "steps", steps)
	return PipelineOutput{FinalState: state, StepCount: steps}, nil
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
