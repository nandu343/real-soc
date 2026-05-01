package runner

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

	"github.com/nandu343/real-soc/internal/models"
)

// HTTPRunner posts the pruned WorkflowState to a remote HTTP endpoint and
// expects a JSON WorkflowState in response. It's used for agents that run
// on external servers rather than as local containers.
type HTTPRunner struct {
	client  *http.Client
	timeout time.Duration
}

// NewHTTPRunner creates a configured HTTPRunner. Timeout is applied to the
// underlying HTTP client requests.
func NewHTTPRunner(timeout time.Duration) *HTTPRunner {
	return &HTTPRunner{client: &http.Client{Timeout: timeout}, timeout: timeout}
}

// RunAgent implements Runner.RunAgent for remote HTTP agents.
func (h *HTTPRunner) RunAgent(ctx context.Context, input AgentRunInput) (AgentRunOutput, error) {
	// Determine endpoint
	endpoint := input.AgentCard.Endpoint
	if endpoint == "" {
		return AgentRunOutput{}, fmt.Errorf("http runner: agent %s has no endpoint configured", input.AgentCard.AgentID)
	}

	// Prepare pruned state payload similar to DockerRunner.
	pruned := PruneWorkflowStateForAgent(input.State)
	stateBytes, err := json.Marshal(pruned)
	if err != nil {
		return AgentRunOutput{}, fmt.Errorf("http runner: marshal state: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(stateBytes))
	if err != nil {
		return AgentRunOutput{}, fmt.Errorf("http runner: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "real-soc-http-runner/1.0")

	// Optional auth: currently supports bearer token via AgentCard.Auth["bearer"].
	if input.AgentCard.Auth != nil {
		if b, ok := input.AgentCard.Auth["bearer"].(string); ok && b != "" {
			req.Header.Set("Authorization", "Bearer "+b)
		}
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return AgentRunOutput{}, fmt.Errorf("http runner: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return AgentRunOutput{}, fmt.Errorf("http runner: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return AgentRunOutput{}, fmt.Errorf("http runner: remote agent returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var updated models.WorkflowState
	if err := json.Unmarshal(respBytes, &updated); err != nil {
		return AgentRunOutput{}, fmt.Errorf("http runner: unmarshal response: %w", err)
	}

	// Compute checksum for artifact record
	sum := sha256.Sum256(respBytes)
	checksum := hex.EncodeToString(sum[:])
	artifact := models.Artifact{
		ArtifactID: newUUID(),
		ProducedBy: input.AgentCard.AgentID,
		Name:       "state.json",
		MimeType:   "application/json",
		URI:        fmt.Sprintf("%s#response", endpoint),
		Checksum:   checksum,
	}

	return AgentRunOutput{UpdatedState: &updated, Artifact: artifact, Logs: string(respBytes)}, nil
}
