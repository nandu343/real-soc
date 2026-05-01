package runner

import (
	"context"
	"fmt"
)

// MultiRunner dispatches RunAgent calls to a concrete implementation based
// on the AgentCard.Type. It contains both a DockerRunner and an HTTPRunner
// instance and routes to the appropriate one.
type MultiRunner struct {
	Docker *DockerRunner
	HTTP   *HTTPRunner
}

// RunAgent routes execution to the proper backend based on agent type.
func (m *MultiRunner) RunAgent(ctx context.Context, input AgentRunInput) (AgentRunOutput, error) {
	t := input.AgentCard.Type
	switch t {
	case "remote", "api", "http":
		if m.HTTP == nil {
			return AgentRunOutput{}, fmt.Errorf("no http runner configured")
		}
		return m.HTTP.RunAgent(ctx, input)
	case "docker", "":
		if m.Docker == nil {
			return AgentRunOutput{}, fmt.Errorf("no docker runner configured")
		}
		return m.Docker.RunAgent(ctx, input)
	case "human":
		return AgentRunOutput{}, fmt.Errorf("human agents must be handled by workflow signal, not runner")
	default:
		// Fallback to docker runner for unknown types to preserve backwards compatibility.
		if m.Docker == nil {
			return AgentRunOutput{}, fmt.Errorf("no docker runner configured (unknown agent type %s)", t)
		}
		return m.Docker.RunAgent(ctx, input)
	}
}
