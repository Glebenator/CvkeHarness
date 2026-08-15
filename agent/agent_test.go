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
							Arguments: `{"command":"ps && whoami"}`,
						},
					},
				},
			},
		}, nil
	case 2:
		// It might be the judge model calling to verify.
		lastMessage := req.Messages[len(req.Messages)-1]
		if strings.Contains(lastMessage.Content, "Is this command safe") {
			return &provider.ChatResponse{
				Message: provider.Message{
					Role:    "assistant",
					Content: "DANGEROUS",
				},
			}, nil
		}

		if lastMessage.Role != "tool" {
			return nil, fmt.Errorf("expected last message to be a tool result, got %q", lastMessage.Role)
		}

		return &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: lastMessage.Content,
			},
		}, nil
	case 3:
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
	provider := &fakeProvider{}
	registry.Register(tools.NewShellTool([]string{"ps"}, provider, "safety", "primary"))
	agent := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
	})

	result, err := agent.Run(context.Background(), "inspect process list")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	if provider.callCount != 3 {
		t.Fatalf("expected 3 provider calls, got %d", provider.callCount)
	}

	if !strings.Contains(result.Output, "Error executing tool:") {
		t.Fatalf("expected tool execution error in final result, got %q", result.Output)
	}

	if !strings.Contains(result.Output, `supervisor model deemed this command dangerous`) {
		t.Fatalf("expected judge rejection error in final result, got %q", result.Output)
	}
}

func TestClassifyPolicyDenialIgnoresWebValidationErrors(t *testing.T) {
	t.Parallel()

	denied, denialClass := classifyPolicyDenial(fmt.Errorf("url host \"localhost\" is not allowed for web_fetch"))
	if denied || denialClass != "" {
		t.Fatalf("expected web validation error to avoid shell policy classification, got denied=%v class=%q", denied, denialClass)
	}
}

func TestRedactedBlockedApprovalMasksOperatorFacingFields(t *testing.T) {
	t.Parallel()

	secret := "sk-blockedapprovalvalue123456789"
	approval := redactedBlockedApproval(tools.ShellApprovalRequest{
		Command:         "curl https://example.com api_key=" + secret,
		ValidationError: "credential " + secret + " requires approval",
		Effects: []tools.ShellEffect{{
			Setting: "network_access",
			Detail:  "send token " + secret,
			Target:  "example.com/" + secret,
		}},
	})
	serialized := fmt.Sprintf("%#v", approval)
	if strings.Contains(serialized, secret) {
		t.Fatalf("blocked approval leaked secret: %s", serialized)
	}
	if !strings.Contains(serialized, "[REDACTED]") {
		t.Fatalf("expected blocked approval to preserve a redaction marker: %s", serialized)
	}
}
