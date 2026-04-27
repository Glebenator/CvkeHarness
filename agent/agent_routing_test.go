package agent

import (
	"context"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

type capturingRouter struct {
	toolsets map[core.Phase][]string
}

func (c *capturingRouter) Select(_ context.Context, phase core.Phase, _ string, _ core.TaskClass, toolNames []string) (core.RoutingSelection, error) {
	if c.toolsets == nil {
		c.toolsets = make(map[core.Phase][]string)
	}
	c.toolsets[phase] = append([]string(nil), toolNames...)
	return core.RoutingSelection{
		Phase:     phase,
		Requested: core.NewModelRef("openrouter", "test-model"),
	}, nil
}

type routingTestProvider struct {
	callCount int
}

func (p *routingTestProvider) ChatCompletion(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.callCount++
	last := req.Messages[len(req.Messages)-1]

	switch p.callCount {
	case 1:
		return &provider.ChatResponse{
			Model: "test-model",
			Message: provider.Message{
				Role:    "assistant",
				Content: "1. Inspect. 2. Use one tool. 3. Summarize.",
			},
		}, nil
	case 2:
		return &provider.ChatResponse{
			Model: "test-model",
			Message: provider.Message{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: provider.ToolFunction{
						Name:      "shell_execute",
						Arguments: `{"command":"ps"}`,
					},
				}},
			},
		}, nil
	case 3:
		if last.Role != "tool" {
			return nil, context.Canceled
		}
		return &provider.ChatResponse{
			Model: "test-model",
			Message: provider.Message{
				Role:    "assistant",
				Content: last.Content,
			},
		}, nil
	default:
		return &provider.ChatResponse{
			Model: "test-model",
			Message: provider.Message{
				Role:    "assistant",
				Content: "done",
			},
		}, nil
	}
}

func TestPlanningRoutingUsesEmptyToolsetProfile(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry()
	provider := &routingTestProvider{}
	registry.Register(tools.NewShellTool([]string{"ps"}, provider, "safety", "primary"))

	router := &capturingRouter{}
	agent := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		RoutingConfig:                 core.RoutingConfig{Enabled: true},
		Router:                        router,
	})

	if _, err := agent.Run(context.Background(), "inspect process list"); err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	if got := len(router.toolsets[core.PhasePlanning]); got != 0 {
		t.Fatalf("expected planning route lookup to use empty toolset profile, got %v", router.toolsets[core.PhasePlanning])
	}
	if got := len(router.toolsets[core.PhaseExecution]); got == 0 {
		t.Fatal("expected execution route lookup to still see available tools")
	}
}
