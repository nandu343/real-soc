package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/nandu343/real-soc/internal/models"
)

const (
	// When the number of tasks exceeds this value, we perform a pruning
	// pass so agent-context doesn't include the full history.
	DefaultMaxExpandedTasks     = 30
	DefaultKeepRecentTasks      = 10
	DefaultMaxArtifactsToKeep   = 5
	DefaultSummaryMaxCharacters = 3000
)

// PruneWorkflowStateForAgent returns a deep-copied WorkflowState that has
// older completed steps condensed into a single summary task and keeps only
// a small tail of recent tasks and artifacts. This reduces LLM/context size
// while preserving important pending/failed tasks.
func PruneWorkflowStateForAgent(orig *models.WorkflowState) *models.WorkflowState {
	if orig == nil {
		return nil
	}
	if len(orig.Tasks) <= DefaultMaxExpandedTasks {
		return deepCopyState(orig)
	}

	total := len(orig.Tasks)
	keep := DefaultKeepRecentTasks
	if keep >= total {
		return deepCopyState(orig)
	}

	prefixCount := total - keep

	var sb strings.Builder
	for i := 0; i < prefixCount; i++ {
		t := orig.Tasks[i]
		sb.WriteString(fmt.Sprintf("[%d] %s by %s: status=%s conf=%.2f", i, t.Name, t.AgentID, t.Status, t.Confidence))
		if t.Error != "" {
			sb.WriteString(" error=")
			sb.WriteString(t.Error)
		} else if t.Output != nil {
			if s := summarizeOutputOneLine(t.Output); s != "" {
				sb.WriteString(" output=")
				sb.WriteString(s)
			}
		}
		sb.WriteString("\n")
		if sb.Len() > DefaultSummaryMaxCharacters {
			break
		}
	}
	summary := sb.String()
	if len(summary) > DefaultSummaryMaxCharacters {
		summary = summary[:DefaultSummaryMaxCharacters]
	}

	summaryTask := models.TaskObject{
		TaskID:     "summary-" + newUUID(),
		AgentID:    "system.summarizer",
		Name:       "summary: older steps",
		Output:     map[string]any{"summary": summary},
		Status:     "completed",
		Confidence: 1.0,
	}

	// Build new task list: keep any non-completed older tasks, then the
	// summary task, then all recent tasks.
	newTasks := make(models.TaskList, 0, keep+1)
	for i := 0; i < prefixCount; i++ {
		t := orig.Tasks[i]
		if t.Status != "completed" {
			newTasks = append(newTasks, t)
		}
	}
	newTasks = append(newTasks, summaryTask)
	newTasks = append(newTasks, orig.Tasks[prefixCount:]...)

	// Keep only the most recent artifacts.
	var newArtifacts models.ArtifactList
	if len(orig.Artifacts) > DefaultMaxArtifactsToKeep {
		newArtifacts = orig.Artifacts[len(orig.Artifacts)-DefaultMaxArtifactsToKeep:]
	} else {
		newArtifacts = make(models.ArtifactList, len(orig.Artifacts))
		copy(newArtifacts, orig.Artifacts)
	}

	copyState := deepCopyState(orig)
	copyState.Tasks = newTasks
	copyState.Artifacts = newArtifacts
	if copyState.Metadata == nil {
		copyState.Metadata = map[string]any{}
	}
	copyState.Metadata["pruned_for_agent"] = true
	copyState.Metadata["pruned_at"] = time.Now().UTC().Format(time.RFC3339)
	return copyState
}

func summarizeOutputOneLine(out map[string]any) string {
	if out == nil {
		return ""
	}
	if s, ok := out["summary"].(string); ok && s != "" {
		return truncateOneLine(s, 200)
	}
	if s, ok := out["text"].(string); ok && s != "" {
		return truncateOneLine(s, 200)
	}
	if b, err := json.Marshal(out); err == nil {
		return truncateOneLine(string(b), 200)
	}
	return ""
}

func truncateOneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func deepCopyState(orig *models.WorkflowState) *models.WorkflowState {
	b, err := json.Marshal(orig)
	if err != nil {
		s := *orig
		return &s
	}
	var dst models.WorkflowState
	if err := json.Unmarshal(b, &dst); err != nil {
		s := *orig
		return &s
	}
	return &dst
}

// runSummarizerContainer runs a specialised summarizer agent container that
// is expected to return a pruned/condensed WorkflowState. This is used when
// SUMMARIZER_AGENT_IMAGE is configured and the history is large.
func (r *DockerRunner) runSummarizerContainer(ctx context.Context, imageRef string, state *models.WorkflowState) (*models.WorkflowState, error) {
	// Marshal state
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("runner: marshal state for summarizer: %w", err)
	}

	// Pull the image and resolve to a digest
	imagePinned, err := r.pullImage(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("runner: pull summarizer image: %w", err)
	}

	// Create a minimal AgentCard for the summarizer run.
	card := models.AgentCard{AgentID: "summarizer", Image: imagePinned}

	containerID, err := r.createContainer(ctx, imagePinned, card)
	if err != nil {
		return nil, fmt.Errorf("runner: create summarizer container: %w", err)
	}
	// Ensure container is removed when done
	defer r.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}) //nolint:errcheck

	if err := r.copyToContainer(ctx, containerID, stateFileName, stateBytes); err != nil {
		return nil, fmt.Errorf("runner: copy initial state into summarizer container: %w", err)
	}

	manifestBytes, err := r.fetchToolManifest(ctx)
	if err != nil {
		fallback := map[string]any{"error": "failed_fetch_manifest", "detail": err.Error()}
		if b, jerr := json.Marshal(fallback); jerr == nil {
			manifestBytes = b
		} else {
			manifestBytes = []byte(`{"error":"failed_generate_manifest"}`)
		}
	}
	if err := r.copyToContainer(ctx, containerID, "tool_manifest.json", manifestBytes); err != nil {
		return nil, fmt.Errorf("runner: copy manifest into summarizer container: %w", err)
	}

	if err := r.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("runner: start summarizer container: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, containerTimeout)
	defer cancel()

	statusCh, errCh := r.cli.ContainerWait(runCtx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("runner: wait summarizer container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			logs, _ := r.containerLogs(ctx, containerID)
			return nil, fmt.Errorf("runner: summarizer exited with code %d: %s", status.StatusCode, logs)
		}
	case <-runCtx.Done():
		return nil, fmt.Errorf("runner: summarizer timed out after %s", containerTimeout)
	}

	updatedBytes, err := r.copyFileFromContainer(ctx, containerID, filepath.Join(workspaceDir, stateFileName))
	if err != nil {
		return nil, fmt.Errorf("runner: read updated state from summarizer container: %w", err)
	}

	var updatedState models.WorkflowState
	if err := json.Unmarshal(updatedBytes, &updatedState); err != nil {
		return nil, fmt.Errorf("runner: unmarshal updated state from summarizer: %w", err)
	}

	return &updatedState, nil
}
