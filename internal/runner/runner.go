package runner

import "context"

// Runner is the minimal interface implemented by execution backends.
// Both DockerRunner and HTTPRunner satisfy this interface so the Temporal
// Activities may remain agnostic about the concrete execution mechanism.
type Runner interface {
	RunAgent(ctx context.Context, input AgentRunInput) (AgentRunOutput, error)
}
