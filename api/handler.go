// Package api implements the Fiber HTTP API for the SOC-OS orchestrator.
//
// Endpoints
// ─────────
//   POST /api/v1/workflows          — Start a new agent pipeline run.
//   GET  /api/v1/workflows/:id      — Get the current state of a workflow run.
//   POST /api/v1/workflows/:id/approve — Send an ApproveStep HITL signal.
//   GET  /api/v1/health             — Liveness probe.
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/nandu343/real-soc/internal/db"
	"github.com/nandu343/real-soc/internal/models"
	"github.com/nandu343/real-soc/internal/workflow"
	"go.temporal.io/sdk/client"
)

// Handler groups all HTTP handler dependencies.
type Handler struct {
	store          *db.Store
	temporalClient client.Client
}

// NewHandler creates a Handler wired to the given store and Temporal client.
func NewHandler(store *db.Store, temporalClient client.Client) *Handler {
	return &Handler{
		store:          store,
		temporalClient: temporalClient,
	}
}

// Register mounts all API routes on the given Fiber app.
func (h *Handler) Register(app *fiber.App) {
	v1 := app.Group("/api/v1")

	v1.Get("/health", h.Health)
	v1.Post("/workflows", h.StartWorkflow)
	v1.Get("/workflows/:id", h.GetWorkflow)
	v1.Post("/workflows/:id/approve", h.ApproveStep)
}

// --------------------------------------------------------------------------
// Request / response types
// --------------------------------------------------------------------------

// StartWorkflowRequest is the JSON body for POST /api/v1/workflows.
type StartWorkflowRequest struct {
	// InitialState is the bootstrap WorkflowState for the new pipeline run.
	// At minimum it must have a TraceID, AgentCards, and at least one Task.
	InitialState *models.WorkflowState `json:"initial_state"`
}

// StartWorkflowResponse is returned after successfully scheduling a workflow.
type StartWorkflowResponse struct {
	WorkflowID string    `json:"workflow_id"`
	RunID      string    `json:"run_id"`
	TraceID    string    `json:"trace_id"`
	StartedAt  time.Time `json:"started_at"`
}

// ApproveStepRequest is the JSON body for POST /api/v1/workflows/:id/approve.
type ApproveStepRequest struct {
	Approved    bool                  `json:"approved"`
	EditedState *models.WorkflowState `json:"edited_state,omitempty"`
	Reviewer    string                `json:"reviewer"`
	Comment     string                `json:"comment,omitempty"`
}

// --------------------------------------------------------------------------
// Handlers
// --------------------------------------------------------------------------

// Health returns 200 OK as a simple liveness probe.
func (h *Handler) Health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok", "ts": time.Now().UTC()})
}

// StartWorkflow validates the request, schedules a new AgentPipelineWorkflow
// via Temporal, and returns the workflow/run IDs.
func (h *Handler) StartWorkflow(c *fiber.Ctx) error {
	var req StartWorkflowRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(http.StatusBadRequest, "invalid JSON body: "+err.Error())
	}

	if req.InitialState == nil {
		return fiber.NewError(http.StatusBadRequest, "initial_state is required")
	}
	if req.InitialState.TraceID == "" {
		return fiber.NewError(http.StatusBadRequest, "initial_state.trace_id is required")
	}
	if len(req.InitialState.Tasks) == 0 {
		return fiber.NewError(http.StatusBadRequest, "initial_state must contain at least one task")
	}
	if len(req.InitialState.AgentCards) == 0 {
		return fiber.NewError(http.StatusBadRequest, "initial_state must contain at least one agent_card")
	}

	// Stamp the workflow ID into the state so it is propagated to all agents.
	workflowID := fmt.Sprintf("soc-pipeline-%s", req.InitialState.TraceID)
	req.InitialState.WorkflowID = workflowID

	opts := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: workflow.TaskQueueName,
	}

	run, err := h.temporalClient.ExecuteWorkflow(
		c.Context(),
		opts,
		workflow.AgentPipelineWorkflow,
		workflow.PipelineInput{InitialState: req.InitialState},
	)
	if err != nil {
		return fiber.NewError(http.StatusInternalServerError, "failed to start workflow: "+err.Error())
	}

	return c.Status(http.StatusAccepted).JSON(StartWorkflowResponse{
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
		TraceID:    req.InitialState.TraceID,
		StartedAt:  time.Now().UTC(),
	})
}

// GetWorkflow returns the current WorkflowState for the given workflow ID.
// It queries the Postgres store (not Temporal) for the last persisted snapshot.
func (h *Handler) GetWorkflow(c *fiber.Ctx) error {
	workflowID := c.Params("id")
	if workflowID == "" {
		return fiber.NewError(http.StatusBadRequest, "workflow id is required")
	}

	states, err := h.store.ListByWorkflowID(workflowID)
	if err != nil {
		return fiber.NewError(http.StatusInternalServerError, "db error: "+err.Error())
	}
	if len(states) == 0 {
		return fiber.NewError(http.StatusNotFound, "workflow not found")
	}

	// Return the most recently updated snapshot (first result, ordered DESC).
	return c.JSON(states[0])
}

// ApproveStep sends the ApproveStep Temporal signal to a paused workflow.
// This is the HITL gate: an operator calls this endpoint to resume execution
// (optionally providing a corrected state JSON).
func (h *Handler) ApproveStep(c *fiber.Ctx) error {
	workflowID := c.Params("id")
	if workflowID == "" {
		return fiber.NewError(http.StatusBadRequest, "workflow id is required")
	}

	var req ApproveStepRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(http.StatusBadRequest, "invalid JSON body: "+err.Error())
	}
	if req.Reviewer == "" {
		return fiber.NewError(http.StatusBadRequest, "reviewer is required")
	}

	payload := workflow.ApproveStepPayload{
		Approved:    req.Approved,
		EditedState: req.EditedState,
		Reviewer:    req.Reviewer,
		Comment:     req.Comment,
	}

	err := h.temporalClient.SignalWorkflow(
		c.Context(),
		workflowID,
		"", // latest run
		workflow.ApproveStepSignal,
		payload,
	)
	if err != nil {
		return fiber.NewError(http.StatusInternalServerError, "signal error: "+err.Error())
	}

	return c.JSON(fiber.Map{
		"status":      "signal sent",
		"workflow_id": workflowID,
		"reviewer":    req.Reviewer,
		"approved":    req.Approved,
	})
}
