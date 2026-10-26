package turn

import (
	"context"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
)

// mockProvider scripts streaming + buffered responses per round.
type mockProvider struct {
	name        string
	streams     []streamScript
	complete    inference.ToolResponse
	completeErr error
	streamsErr  error
	calls       int
}

type streamScript struct {
	chunks []inference.Chunk
	err    error
}

func (m *mockProvider) Name() string            { return m.name }
func (m *mockProvider) ApiType() config.ApiType { return config.ApiTypeOpenai }
func (m *mockProvider) Model() string           { return "mock" }

func (m *mockProvider) CompleteTools(ctx context.Context, system string, messages []inference.LlmMessage, defs []inference.ToolDefinition, sessionID string) (inference.ToolResponse, error) {
	return m.complete, m.completeErr
}

func (m *mockProvider) StreamTools(ctx context.Context, system string, messages []inference.LlmMessage, defs []inference.ToolDefinition, sessionID string, tx chan<- inference.Chunk, done <-chan struct{}) error {
	idx := m.calls
	m.calls++
	if m.streamsErr != nil {
		return m.streamsErr
	}
	if idx < len(m.streams) {
		for _, c := range m.streams[idx].chunks {
			select {
			case tx <- c:
			case <-done:
				return inference.CancelledError()
			case <-ctx.Done():
				return inference.CancelledError()
			}
		}
		return m.streams[idx].err
	}
	return nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
