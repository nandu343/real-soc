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
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/blkiodev"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
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
	// proxyContainerID holds the container id of a long-running Proxy Agent
	// service when one has been started via StartProxyService.
	proxyContainerID string
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
	// 1. Serialise current state to an in-memory tar archive which will be
	//    copied into a size-limited tmpfs inside the container.  Using a
	//    tmpfs prevents container writes from consuming host disk space.
	// ------------------------------------------------------------------
	stateBytes, err := json.Marshal(input.State)
	if err != nil {
		return AgentRunOutput{}, fmt.Errorf("runner: marshal state: %w", err)
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
	// 3. Create the hardened container (with tmpfs workspace).
	// ------------------------------------------------------------------
	containerID, err := r.createContainer(ctx, imageRef, input.AgentCard)
	if err != nil {
		return AgentRunOutput{}, err
	}
	// Unconditionally remove the container when done (force=true so it is
	// removed even if it is still running after a timeout).
	defer r.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}) //nolint:errcheck

	// Copy the initial state.json into the container workspace before start.
	if err := r.copyToContainer(ctx, containerID, stateFileName, stateBytes); err != nil {
		return AgentRunOutput{}, fmt.Errorf("runner: copy initial state into container: %w", err)
	}

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
		return AgentRunOutput{}, fmt.Errorf("runner: read updated state: %w", err)
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
func (r *DockerRunner) createContainer(ctx context.Context, imageRef string, agentCard models.AgentCard) (string, error) {
	// Determine network mode based on agent outbound policy.
	// Default: internal isolated network (no internet).
	networkMode := container.NetworkMode(r.networkName)
	env := []string{"STATE_FILE=" + workspaceDir + "/" + stateFileName}

	// If agent requests proxy access, ensure proxy service is running and
	// expose PROXY_URL via environment so the agent can use it.
	if agentCard.Outbound == "proxy" {
		if r.proxyContainerID == "" {
			return "", fmt.Errorf("runner: agent %s requested proxy outbound but proxy service is not running", agentCard.AgentID)
		}
		env = append(env, "PROXY_URL=http://proxy-agent:8080/forward")
	}

	// If agent requests direct outbound, only allow when explicitly enabled
	// via environment variable RUNNER_ALLOW_DIRECT=true to avoid accidental
	// internet access in secure deployments.
	if agentCard.Outbound == "direct" {
		if os.Getenv("RUNNER_ALLOW_DIRECT") != "true" {
			return "", fmt.Errorf("runner: direct outbound requested by %s but RUNNER_ALLOW_DIRECT is not set", agentCard.AgentID)
		}
		networkMode = container.NetworkMode("bridge")
	}

	// Build cgroup I/O throttles from environment variables (optional).
	res := container.Resources{
		Memory:      memoryLimitBytes,
		NanoCPUs:    nanoCPUs,
		BlkioWeight: 10,
	}

	if dev := os.Getenv("RUNNER_BLKIO_DEVICE"); dev != "" {
		if v := os.Getenv("RUNNER_BLKIO_READ_IOPS"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				res.BlkioDeviceReadIOps = []*blkiodev.ThrottleDevice{{Path: dev, Rate: n}}
			}
		}
		if v := os.Getenv("RUNNER_BLKIO_WRITE_IOPS"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				res.BlkioDeviceWriteIOps = []*blkiodev.ThrottleDevice{{Path: dev, Rate: n}}
			}
		}
		if v := os.Getenv("RUNNER_BLKIO_READ_BPS"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				res.BlkioDeviceReadBps = []*blkiodev.ThrottleDevice{{Path: dev, Rate: n}}
			}
		}
		if v := os.Getenv("RUNNER_BLKIO_WRITE_BPS"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				res.BlkioDeviceWriteBps = []*blkiodev.ThrottleDevice{{Path: dev, Rate: n}}
			}
		}
	}

	// tmpfs size (MB) default 64MB, overridable via RUNNER_TMPFS_SIZE_MB
	tmpfsMb := 64
	if v := os.Getenv("RUNNER_TMPFS_SIZE_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tmpfsMb = n
		}
	}

	resp, err := r.cli.ContainerCreate(
		ctx,
		&container.Config{
			Image: imageRef,
			// The agent is responsible for reading /workspace/state.json
			// and writing its result back to the same path.
			Env: env,
			// Disable attaching a terminal — agents should be headless.
			AttachStdout: true,
			AttachStderr: true,
		},
		&container.HostConfig{
			// Security hardening — see package docblock for full rationale.
			ReadonlyRootfs: true,
			SecurityOpt:    []string{"no-new-privileges:true"},
			CapDrop:        []string{"ALL"},
			// NetworkMode is chosen above based on outbound policy.
			NetworkMode: networkMode,
			Resources:   res,
			Tmpfs: map[string]string{
				workspaceDir: fmt.Sprintf("size=%dm", tmpfsMb),
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

// copyToContainer copies a single file into the container at the provided
// destination directory. The content will appear as destDir/fileName.
func (r *DockerRunner) copyToContainer(ctx context.Context, containerID, fileName string, content []byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: fileName,
		Mode: 0o600,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}

	return r.cli.CopyToContainer(ctx, containerID, workspaceDir, &buf, container.CopyToContainerOptions{AllowOverwriteDirWithFile: true})
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

	// Create an "internal" attachable bridge network so containers can
	// communicate with each other (e.g. agent <-> proxy) but cannot reach
	// external networks (host/internet) unless explicitly connected.
	_, err = r.cli.NetworkCreate(ctx, r.networkName, network.CreateOptions{
		Driver:     "bridge",
		Attachable: true,
		Internal:   true,
	})
	if err != nil {
		return fmt.Errorf("runner: create network %s: %w", r.networkName, err)
	}
	return nil
}

// StartProxyService starts a long-running proxy container that is attached to
// both the isolated agent network (so agents can reach it) and the default
// bridge network (so it may access the internet). The proxy will be started
// detached and its container ID returned.
func (r *DockerRunner) StartProxyService(ctx context.Context, proxyImage string, allowedHosts []string) (string, error) {
	// Pull and resolve the image first.
	imageRef, err := r.pullImage(ctx, proxyImage)
	if err != nil {
		return "", fmt.Errorf("runner: pull proxy image: %w", err)
	}

	// Prepare environment for the proxy container.
	env := []string{fmt.Sprintf("ALLOWED_HOSTS=%s", strings.Join(allowedHosts, ","))}

	resp, err := r.cli.ContainerCreate(ctx, &container.Config{
		Image: imageRef,
		Env:   env,
	}, &container.HostConfig{
		// Start on the default bridge network so the proxy has outbound
		// connectivity; we'll additionally connect it to the isolated
		// agent network so agents can reach it.
		NetworkMode: container.NetworkMode("bridge"),
	}, &network.NetworkingConfig{}, nil, "soc-proxy-agent")
	if err != nil {
		return "", fmt.Errorf("runner: create proxy container: %w", err)
	}

	// Connect the proxy into the isolated agent network so agents can
	// address it by name / alias from the same network.
	if err := r.cli.NetworkConnect(ctx, r.networkName, resp.ID, &network.EndpointSettings{Aliases: []string{"proxy-agent"}}); err != nil {
		// Cleanup the created container on failure.
		_ = r.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("runner: connect proxy to agent network: %w", err)
	}

	if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = r.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("runner: start proxy container: %w", err)
	}

	r.proxyContainerID = resp.ID
	return resp.ID, nil
}

// StopProxyService stops and removes the running proxy container started by
// StartProxyService. It's safe to call multiple times.
func (r *DockerRunner) StopProxyService(ctx context.Context) error {
	if r.proxyContainerID == "" {
		return nil
	}
	err := r.cli.ContainerRemove(ctx, r.proxyContainerID, container.RemoveOptions{Force: true})
	if err != nil {
		return fmt.Errorf("runner: remove proxy container: %w", err)
	}
	r.proxyContainerID = ""
	return nil
}

// newUUID returns a random UUID string (version 4).
// We inline a minimal implementation here so the runner package does not
// depend on an external UUID library.  The bytes are read from /dev/urandom
// via readRandom, which uses os.Open — fully available in this package.
func newUUID() string {
	b := make([]byte, 16)
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
