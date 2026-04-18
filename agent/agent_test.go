package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

type fakeProvider struct {
	callCount int
}

func (f *fakeProvider) ChatCompletion(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	f.callCount++

	switch f.callCount {
	case 1:
		return &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: provider.ToolFunction{
							Name:      "shell_execute",
							Arguments: `{"command":"ps; whoami"}`,
						},
					},
				},
			},
		}, nil
	case 2:
		if len(req.Messages) == 0 {
			return nil, fmt.Errorf("expected tool result in second request")
		}

		lastMessage := req.Messages[len(req.Messages)-1]
		if lastMessage.Role != "tool" {
			return nil, fmt.Errorf("expected last message to be a tool result, got %q", lastMessage.Role)
		}

		return &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: lastMessage.Content,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unexpected provider call %d", f.callCount)
	}
}

func TestRun_RejectsUnsafeShellToolCall(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry()
	registry.Register(tools.NewShellTool([]string{"ps"}))

	provider := &fakeProvider{}
	agent := New(provider, registry, "test-model", 3, 512)

	result, err := agent.Run(context.Background(), "inspect process list")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	if provider.callCount != 2 {
		t.Fatalf("expected 2 provider calls, got %d", provider.callCount)
	}

	if !strings.Contains(result, "Error executing tool:") {
		t.Fatalf("expected tool execution error in final result, got %q", result)
	}

	if !strings.Contains(result, `blocked shell syntax ";"`) {
		t.Fatalf("expected blocked shell syntax in final result, got %q", result)
	}
}

