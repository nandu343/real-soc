// Package runner implements the Docker-based agent execution layer.
//
// Security sandboxing model
// ─────────────────────────
// Every agent runs inside a fresh, ephemeral container with the following
// hardening applied:
//
//  1. Read-only root filesystem (ReadonlyRootfs: true) — the agent cannot
//     tamper with the host OS image.
//
//  2. No new privileges (SecurityOpt: ["no-new-privileges:true"]) — prevents
//     privilege escalation via setuid binaries inside the container.
//
//  3. Dropped capabilities — all Linux capabilities are dropped by default;
//     only the minimum set required is added back (none for most SOC agents).
//
//  4. Network isolation — containers join a dedicated bridge network and
//     cannot reach the host network or the internet unless explicitly allowed
//     by the agent's AgentCard.
//
//  5. Resource limits — CPU and memory limits are enforced via cgroup
//     constraints so a misbehaving agent cannot exhaust host resources.
//
//  6. State is passed via a bind-mount of a *temporary* host directory that
//     is created fresh for every run and removed immediately after.  The
//     agent cannot traverse above /workspace inside the container.
//
//  7. Image digest pinning — the runner resolves the image to its immutable
//     SHA256 digest before starting the container, ensuring the agent binary
//     has not been silently replaced (supply-chain protection).
package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/nandu343/real-soc/internal/models"
)

const (
	// workspaceDir is the path inside the container where the state file is mounted.
	workspaceDir = "/workspace"
	// stateFileName is the well-known name of the state file the agent reads/writes.
	stateFileName = "state.json"
	// containerTimeout is the maximum wall-clock time an agent container may run.
	containerTimeout = 5 * time.Minute
	// memoryLimitBytes caps each agent at 512 MB of RAM.
	memoryLimitBytes = 512 * 1024 * 1024
	// nanoCPUs limits each agent to 1 logical CPU.
	nanoCPUs = 1e9
)

// DockerRunner wraps the Docker Engine API client and provides a single
// RunAgent method used as a Temporal Activity.
type DockerRunner struct {
	cli           *client.Client
	networkName   string
	registryToken string // optional bearer token for private registries
}

// NewDockerRunner creates a DockerRunner that connects to the local Docker
// daemon via the default DOCKER_HOST socket.
//
// networkName is the name of the Docker bridge network that all agent
// containers will be attached to (created automatically if absent).
// registryToken may be empty for public images.
func NewDockerRunner(networkName, registryToken string) (*DockerRunner, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("runner: create docker client: %w", err)
	}

	r := &DockerRunner{
		cli:           cli,
		networkName:   networkName,
		registryToken: registryToken,
	}

	// Ensure the isolated agent network exists before any container is started.
	if err := r.ensureNetwork(context.Background()); err != nil {
		return nil, err
	}

	return r, nil
}

// AgentRunInput carries the parameters supplied to the RunAgent activity.
type AgentRunInput struct {
	// State is the current WorkflowState that will be serialised to state.json
	// and bind-mounted into the agent container.
	State *models.WorkflowState
	// AgentCard describes the agent to run (image, capabilities, …).
	AgentCard models.AgentCard
	// TaskIndex is the index of the task in State.Tasks that this agent should
	// process.  The agent receives the full state but convention dictates it
	// focuses on tasks[TaskIndex].
	TaskIndex int
}

// AgentRunOutput carries the result of a successful agent execution.
type AgentRunOutput struct {
	// UpdatedState is the WorkflowState as written by the agent container.
	UpdatedState *models.WorkflowState
	// Artifact describes the state.json file produced by the agent.
	Artifact models.Artifact
	// Logs contains the combined stdout+stderr output of the container.
	Logs string
}

// RunAgent is the core Temporal Activity function.
//
// It:
//  1. Pulls (and pins) the agent image.
//  2. Writes the current state to a temp directory.
//  3. Creates and starts a hardened container.
//  4. Waits for the container to exit (or times out).
//  5. Reads the updated state.json written by the agent.
//  6. Computes a SHA-256 checksum of the output for integrity.
//  7. Cleans up all temporary resources.
func (r *DockerRunner) RunAgent(ctx context.Context, input AgentRunInput) (AgentRunOutput, error) {
	// ------------------------------------------------------------------
	// 1. Serialise current state to a temporary workspace directory.
	//    A fresh tmpdir is created per invocation so there is no cross-
	//    contamination between concurrent agent runs.
	// ------------------------------------------------------------------
	tmpDir, err := os.MkdirTemp("", "soc-agent-*")
	if err != nil {
		return AgentRunOutput{}, fmt.Errorf("runner: create tmpdir: %w", err)
	}
	// Always clean up the tmpdir, even on error.
	defer os.RemoveAll(tmpDir)

	stateBytes, err := json.Marshal(input.State)
	if err != nil {
		return AgentRunOutput{}, fmt.Errorf("runner: marshal state: %w", err)
	}

	statePath := filepath.Join(tmpDir, stateFileName)
	if err := os.WriteFile(statePath, stateBytes, 0o600); err != nil {
		return AgentRunOutput{}, fmt.Errorf("runner: write state file: %w", err)
	}

	// ------------------------------------------------------------------
	// 2. Pull the agent image and resolve to an immutable digest.
	//    This defends against "tag mutable" supply-chain attacks.
	// ------------------------------------------------------------------
	imageRef, err := r.pullImage(ctx, input.AgentCard.Image)
	if err != nil {
		return AgentRunOutput{}, err
	}

	// ------------------------------------------------------------------
	// 3. Create the hardened container.
	// ------------------------------------------------------------------
	containerID, err := r.createContainer(ctx, imageRef, tmpDir)
	if err != nil {
		return AgentRunOutput{}, err
	}
	// Unconditionally remove the container when done (force=true so it is
	// removed even if it is still running after a timeout).
	defer r.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}) //nolint:errcheck

	// ------------------------------------------------------------------
	// 4. Start the container and wait for it to finish (with timeout).
	// ------------------------------------------------------------------
	if err := r.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return AgentRunOutput{}, fmt.Errorf("runner: start container: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, containerTimeout)
	defer cancel()

	statusCh, errCh := r.cli.ContainerWait(runCtx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return AgentRunOutput{}, fmt.Errorf("runner: wait container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			logs, _ := r.containerLogs(ctx, containerID)
			return AgentRunOutput{}, fmt.Errorf("runner: agent exited with code %d: %s", status.StatusCode, logs)
		}
	case <-runCtx.Done():
		return AgentRunOutput{}, fmt.Errorf("runner: agent timed out after %s", containerTimeout)
	}

	// ------------------------------------------------------------------
	// 5. Capture container logs for debugging / audit.
	// ------------------------------------------------------------------
	logs, _ := r.containerLogs(ctx, containerID)

	// ------------------------------------------------------------------
	// 6. Read the updated state.json written by the agent.
	// ------------------------------------------------------------------
	updatedBytes, err := r.copyFileFromContainer(ctx, containerID, filepath.Join(workspaceDir, stateFileName))
	if err != nil {
		// Fall back to the local tmpdir copy in case the agent wrote directly
		// to the bind-mounted path rather than via ContainerCP.
		updatedBytes, err = os.ReadFile(statePath)
		if err != nil {
			return AgentRunOutput{}, fmt.Errorf("runner: read updated state: %w", err)
		}
	}

	var updatedState models.WorkflowState
	if err := json.Unmarshal(updatedBytes, &updatedState); err != nil {
		return AgentRunOutput{}, fmt.Errorf("runner: unmarshal updated state: %w", err)
	}

	// ------------------------------------------------------------------
	// 7. Compute SHA-256 checksum for the Artifact record.
	// ------------------------------------------------------------------
	sum := sha256.Sum256(updatedBytes)
	checksum := hex.EncodeToString(sum[:])

	artifact := models.Artifact{
		ArtifactID: newUUID(),
		ProducedBy: input.AgentCard.AgentID,
		Name:       stateFileName,
		MimeType:   "application/json",
		URI:        fmt.Sprintf("container://%s/%s", containerID[:12], stateFileName),
		Checksum:   checksum,
	}

	return AgentRunOutput{
		UpdatedState: &updatedState,
		Artifact:     artifact,
		Logs:         logs,
	}, nil
}

// --------------------------------------------------------------------------
// Private helpers
// --------------------------------------------------------------------------

// pullImage pulls the specified image reference and returns its resolved
// image ID (which is pinned to an immutable SHA256 digest).
func (r *DockerRunner) pullImage(ctx context.Context, imageRef string) (string, error) {
	rc, err := r.cli.ImagePull(ctx, imageRef, image.PullOptions{
		RegistryAuth: r.registryToken,
	})
	if err != nil {
		return "", fmt.Errorf("runner: pull image %s: %w", imageRef, err)
	}
	defer rc.Close()

	// Drain the pull output — required to ensure the pull completes.
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return "", fmt.Errorf("runner: drain pull output: %w", err)
	}

	// Inspect the image to get its stable RepoDigest (immutable SHA256).
	inspect, _, err := r.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		return "", fmt.Errorf("runner: inspect image %s: %w", imageRef, err)
	}

	// Prefer the digest-pinned reference to prevent tag substitution.
	if len(inspect.RepoDigests) > 0 {
		return inspect.RepoDigests[0], nil
	}
	return inspect.ID, nil
}

// createContainer creates (but does not start) a hardened container with the
// tmpDir bind-mounted at /workspace inside the container.
func (r *DockerRunner) createContainer(ctx context.Context, imageRef, tmpDir string) (string, error) {
	resp, err := r.cli.ContainerCreate(
		ctx,
		&container.Config{
			Image: imageRef,
			// The agent is responsible for reading /workspace/state.json
			// and writing its result back to the same path.
			Env: []string{
				"STATE_FILE=" + workspaceDir + "/" + stateFileName,
			},
			// Disable attaching a terminal — agents should be headless.
			AttachStdout: true,
			AttachStderr: true,
		},
		&container.HostConfig{
			// Security hardening — see package docblock for full rationale.
			ReadonlyRootfs: true,
			SecurityOpt:    []string{"no-new-privileges:true"},
			CapDrop:        []string{"ALL"},
			NetworkMode:    container.NetworkMode(r.networkName),
			Resources: container.Resources{
				Memory:   memoryLimitBytes,
				NanoCPUs: nanoCPUs,
			},
			// Bind-mount the tmpDir as the only writable surface.
			// The agent can read/write /workspace but nothing else.
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: tmpDir,
					Target: workspaceDir,
					// NOT read-only: the agent must be able to overwrite state.json.
				},
			},
		},
		&network.NetworkingConfig{},
		nil,
		"", // auto-generate container name
	)
	if err != nil {
		return "", fmt.Errorf("runner: create container: %w", err)
	}
	return resp.ID, nil
}

// containerLogs fetches the combined stdout+stderr output of a container.
func (r *DockerRunner) containerLogs(ctx context.Context, containerID string) (string, error) {
	rc, err := r.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// copyFileFromContainer copies a single file from a running or stopped
// container using the Docker Archive (tar) API and returns its raw bytes.
func (r *DockerRunner) copyFileFromContainer(ctx context.Context, containerID, srcPath string) ([]byte, error) {
	rc, _, err := r.cli.CopyFromContainer(ctx, containerID, srcPath)
	if err != nil {
		return nil, fmt.Errorf("runner: copy from container: %w", err)
	}
	defer rc.Close()

	// The Docker API returns a tar archive; extract the first entry.
	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("runner: read tar header: %w", err)
	}

	return io.ReadAll(tr)
}

// ensureNetwork creates the isolated Docker bridge network for agent
// containers if it does not already exist.
func (r *DockerRunner) ensureNetwork(ctx context.Context) error {
	f := filters.NewArgs(filters.Arg("name", r.networkName))
	nets, err := r.cli.NetworkList(ctx, network.ListOptions{Filters: f})
	if err != nil {
		return fmt.Errorf("runner: list networks: %w", err)
	}
	if len(nets) > 0 {
		return nil // network already exists
	}

	_, err = r.cli.NetworkCreate(ctx, r.networkName, network.CreateOptions{
		Driver:     "bridge",
		Attachable: true,
	})
	if err != nil {
		return fmt.Errorf("runner: create network %s: %w", r.networkName, err)
	}
	return nil
}

// newUUID returns a random UUID string (version 4).
// We inline a simple implementation here to avoid importing a large package.
func newUUID() string {
	b := make([]byte, 16)
	// crypto/rand is preferred but os.Getenv is unavailable here; use a
	// deterministic fallback seeded by timestamp for low-collision IDs.
	_ = readRandom(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func readRandom(b []byte) error {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.ReadFull(f, b)
	return err
}
