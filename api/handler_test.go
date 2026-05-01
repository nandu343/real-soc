package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/nandu343/real-soc/internal/models"
	"go.temporal.io/sdk/client"
)

// fakeStore implements the Store interface for tests.
type fakeStore struct {
	snaps      []models.WorkflowStateSnapshot
	savedState *models.WorkflowState
}

func (f *fakeStore) Save(s *models.WorkflowState) error {
	f.savedState = s
	return nil
}
func (f *fakeStore) ListByWorkflowID(workflowID string) ([]models.WorkflowState, error) {
	// not needed for these tests
	return []models.WorkflowState{}, nil
}
func (f *fakeStore) ListSnapshots(workflowID string) ([]models.WorkflowStateSnapshot, error) {
	return f.snaps, nil
}
func (f *fakeStore) GetSnapshotByID(id string) (*models.WorkflowStateSnapshot, error) {
	for _, s := range f.snaps {
		if s.ID == id {
			copy := s
			return &copy, nil
		}
	}
	return nil, nil
}
func (f *fakeStore) SaveSnapshot(state *models.WorkflowState, step int, snapType string) error {
	// nop
	return nil
}

// fakeTemporal implements the minimal TemporalClient used by the handlers.
type fakeTemporal struct {
	signalled bool
	lastID    string
	lastName  string
	lastArg   interface{}
}

func (f *fakeTemporal) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	return nil, nil
}
func (f *fakeTemporal) SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error {
	f.signalled = true
	f.lastID = workflowID
	f.lastName = signalName
	f.lastArg = arg
	return nil
}

// (no additional stubs needed)

func TestListSnapshots(t *testing.T) {
	app := fiber.New()
	fs := &fakeStore{
		snaps: []models.WorkflowStateSnapshot{
			{
				ID:           "s1",
				WorkflowID:   "w1",
				TraceID:      "t1",
				Step:         0,
				SnapshotType: "before",
				StateJSON:    "{}",
				CreatedAt:    time.Now(),
			},
		},
	}
	h := NewHandler(fs, nil)
	h.Register(app)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/w1/snapshots", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out []models.WorkflowStateSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(out) != 1 || out[0].ID != "s1" {
		t.Fatalf("unexpected snapshots: %+v", out)
	}
}

func TestRestoreSnapshot(t *testing.T) {
	app := fiber.New()
	// Build a minimal WorkflowState JSON for the snapshot.
	ws := models.WorkflowState{
		TraceID:     "t-restore",
		WorkflowID:  "w-restore",
		CurrentStep: 1,
		Status:      "running",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	b, _ := json.Marshal(ws)

	fs := &fakeStore{
		snaps: []models.WorkflowStateSnapshot{
			{
				ID:           "snap-1",
				WorkflowID:   "w-restore",
				TraceID:      "t-restore",
				Step:         1,
				SnapshotType: "before",
				StateJSON:    string(b),
				CreatedAt:    time.Now(),
			},
		},
	}
	ft := &fakeTemporal{}
	h := NewHandler(fs, ft)
	h.Register(app)

	body, _ := json.Marshal(map[string]string{"snapshot_id": "snap-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/w-restore/restore", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if out["status"] != "restored" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if !ft.signalled {
		t.Fatalf("expected temporal signal to be sent")
	}
}
