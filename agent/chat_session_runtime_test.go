package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

type chatRouterStub struct {
	selection core.RoutingSelection
}

func (r *chatRouterStub) Select(context.Context, core.Phase, string, core.TaskClass, []string) (core.RoutingSelection, error) {
	return r.selection, nil
}

type memoryStub struct {
	retrievals []core.RetrievalContext
	resultFn   func(input core.RetrievalContext) memory.RetrievalResult
}

func (m *memoryStub) Retrieve(_ context.Context, input core.RetrievalContext) (memory.RetrievalResult, error) {
	m.retrievals = append(m.retrievals, input)
	if m.resultFn != nil {
		return m.resultFn(input), nil
	}
	return memory.RetrievalResult{
		BuiltInRules:       "Be helpful.",
		Operator:           "operator context",
		Soul:               "soul context",
		RuntimeHostSummary: "Runtime host summary:\n- name: runtime",
		FallbackBrief:      "Fallback finding for runtime: baseline learned",
	}, nil
}

type sequenceProvider struct {
	call int
	fn   func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error)
}

func (p *sequenceProvider) ChatCompletion(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.call++
	return p.fn(p.call, req)
}

type approverStub struct {
	calls int
}

func (a *approverStub) Approve(context.Context, tools.ShellApprovalRequest) (tools.ShellApprovalDecision, error) {
	a.calls++
	return tools.ShellApprovalDecision{
		Approved: true,
		Mode:     tools.SafetyModeUserConfirm,
		Remember: false,
	}, nil
}

type failingTool struct{}

func (f failingTool) Name() string { return "fail_tool" }
func (f failingTool) Description() string {
	return "fails"
}
func (f failingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (f failingTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", fmt.Errorf("boom")
}

func TestChatConversationPreservesHistoryAndPinsModel(t *testing.T) {
	t.Parallel()

	provider := &sequenceProvider{
		fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			if req.Model != "pinned-model" {
				return nil, fmt.Errorf("expected pinned model, got %q", req.Model)
			}
			switch call {
			case 1:
				return &provider.ChatResponse{
					Model: "pinned-model",
					Message: provider.Message{
						Role:    "assistant",
						Content: "hello there",
					},
				}, nil
			case 2:
				var sawPriorUser, sawPriorAssistant, sawCurrentUser bool
				for _, msg := range req.Messages {
					if msg.Role == "user" && msg.Content == "hello" {
						sawPriorUser = true
					}
					if msg.Role == "assistant" && msg.Content == "hello there" {
						sawPriorAssistant = true
					}
					if msg.Role == "user" && msg.Content == "what did I just say?" {
						sawCurrentUser = true
					}
				}
				if !sawPriorUser || !sawPriorAssistant || !sawCurrentUser {
					return nil, fmt.Errorf("expected second turn to include prior history and current prompt")
				}
				return &provider.ChatResponse{
					Model: "pinned-model",
					Message: provider.Message{
						Role:    "assistant",
						Content: "you said hello",
					},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected call %d", call)
			}
		},
	}

	a := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  tools.NewRegistry(),
		DefaultModel:                  "default-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		Router:                        &chatRouterStub{selection: core.RoutingSelection{Phase: core.PhaseChat, Requested: core.NewModelRef("openrouter", "pinned-model")}},
		MemoryRetriever:               &memoryStub{},
	})

	session, selection, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned unexpected error: %v", err)
	}
	if selection.Requested.Model != "pinned-model" {
		t.Fatalf("expected pinned model selection, got %#v", selection)
	}

	first, err := session.Turn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("first Turn returned unexpected error: %v", err)
	}
	if first.Output != "hello there" {
		t.Fatalf("expected first output, got %q", first.Output)
	}

	second, err := session.Turn(context.Background(), "what did I just say?")
	if err != nil {
		t.Fatalf("second Turn returned unexpected error: %v", err)
	}
	if second.Output != "you said hello" {
		t.Fatalf("expected second output, got %q", second.Output)
	}
}

func TestChatConversationRunsCompletionVerification(t *testing.T) {
	t.Parallel()

	provider := &sequenceProvider{
		fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			switch call {
			case 1:
				return &provider.ChatResponse{
					Model: "pinned-model",
					Message: provider.Message{
						Role:    "assistant",
						Content: "done",
					},
				}, nil
			case 2:
				last := req.Messages[len(req.Messages)-1]
				if last.Role != "user" || !strings.Contains(last.Content, "assistant_final_output") {
					return nil, fmt.Errorf("expected verification prompt, got role=%q content=%q", last.Role, last.Content)
				}
				return verifierJSON(verificationSatisfied, "The answer satisfies the turn.", nil, ""), nil
			default:
				return nil, fmt.Errorf("unexpected call %d", call)
			}
		},
	}

	a := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "default-model",
		MaxIterations:   3,
		MaxTokens:       512,
		Router:          &chatRouterStub{selection: core.RoutingSelection{Phase: core.PhaseChat, Requested: core.NewModelRef("openrouter", "pinned-model")}},
		MemoryRetriever: &memoryStub{},
	})

	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned unexpected error: %v", err)
	}
	result, err := session.Turn(context.Background(), "finish")
	if err != nil {
		t.Fatalf("Turn returned unexpected error: %v", err)
	}
	if result.Verification.Status != verificationSatisfied || result.VerificationPhase.Phase != core.PhaseVerification {
		t.Fatalf("expected chat verification metadata, got verification=%#v phase=%#v", result.Verification, result.VerificationPhase)
	}
}

func TestChatConversationVerificationRepair(t *testing.T) {
	t.Parallel()

	provider := &sequenceProvider{
		fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			switch call {
			case 1:
				return &provider.ChatResponse{Model: "pinned-model", Message: provider.Message{Role: "assistant", Content: "I can do that next."}}, nil
			case 2:
				return verifierJSON(verificationUncertain, "The answer proposes next steps instead of completing them.", []string{"Complete the requested work."}, "Complete the requested work now."), nil
			case 3:
				last := req.Messages[len(req.Messages)-1]
				if last.Role != "system" || !strings.Contains(last.Content, "Complete the requested work now") {
					return nil, fmt.Errorf("expected verifier repair system prompt, got role=%q content=%q", last.Role, last.Content)
				}
				return &provider.ChatResponse{Model: "pinned-model", Message: provider.Message{Role: "assistant", Content: "done now"}}, nil
			case 4:
				return verifierJSON(verificationSatisfied, "The repair completed the work.", nil, ""), nil
			default:
				return nil, fmt.Errorf("unexpected call %d", call)
			}
		},
	}

	a := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "default-model",
		MaxIterations:   3,
		MaxTokens:       512,
		Router:          &chatRouterStub{selection: core.RoutingSelection{Phase: core.PhaseChat, Requested: core.NewModelRef("openrouter", "pinned-model")}},
		MemoryRetriever: &memoryStub{},
	})

	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned unexpected error: %v", err)
	}
	result, err := session.Turn(context.Background(), "finish")
	if err != nil {
		t.Fatalf("Turn returned unexpected error: %v", err)
	}
	if !result.Verification.RepairTriggered || result.Output != "done now" {
		t.Fatalf("expected repaired chat turn, got output=%q verification=%#v", result.Output, result.Verification)
	}
}

func TestChatConversationVerificationRepairRetriesWithinIterationBudget(t *testing.T) {
	t.Parallel()

	provider := &sequenceProvider{
		fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			switch call {
			case 1:
				return &provider.ChatResponse{Model: "pinned-model", Message: provider.Message{Role: "assistant", Content: "I can do that next."}}, nil
			case 2:
				return verifierJSON(verificationUnsatisfied, "The answer only promises future work.", []string{"Complete the requested work."}, "Complete the requested work now."), nil
			case 3:
				last := req.Messages[len(req.Messages)-1]
				if last.Role != "system" || !strings.Contains(last.Content, "Complete the requested work now") {
					return nil, fmt.Errorf("expected first verifier repair prompt, got role=%q content=%q", last.Role, last.Content)
				}
				return &provider.ChatResponse{Model: "pinned-model", Message: provider.Message{Role: "assistant", Content: "I am still about to do it."}}, nil
			case 4:
				return verifierJSON(verificationUnsatisfied, "The repair still only promised future work.", []string{"Complete the requested work."}, "Stop promising; complete the work now."), nil
			case 5:
				last := req.Messages[len(req.Messages)-1]
				if last.Role != "system" || !strings.Contains(last.Content, "Stop promising; complete the work now") {
					return nil, fmt.Errorf("expected second verifier repair prompt, got role=%q content=%q", last.Role, last.Content)
				}
				return &provider.ChatResponse{Model: "pinned-model", Message: provider.Message{Role: "assistant", Content: "done now"}}, nil
			case 6:
				return verifierJSON(verificationSatisfied, "The second repair completed the work.", nil, ""), nil
			default:
				return nil, fmt.Errorf("unexpected call %d", call)
			}
		},
	}

	a := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "default-model",
		MaxIterations:   3,
		MaxTokens:       512,
		Router:          &chatRouterStub{selection: core.RoutingSelection{Phase: core.PhaseChat, Requested: core.NewModelRef("openrouter", "pinned-model")}},
		MemoryRetriever: &memoryStub{},
	})

	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned unexpected error: %v", err)
	}
	result, err := session.Turn(context.Background(), "finish")
	if err != nil {
		t.Fatalf("Turn returned unexpected error: %v", err)
	}
	if !result.Verification.RepairTriggered || result.Output != "done now" || result.Verification.Status != verificationSatisfied {
		t.Fatalf("expected retry repair to satisfy the turn, got output=%q verification=%#v", result.Output, result.Verification)
	}
}

func TestChatConversationRunsToolCallsWithApprovalPath(t *testing.T) {
	t.Parallel()

	provider := &sequenceProvider{
		fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			switch call {
			case 1:
				return &provider.ChatResponse{
					Model: "test-model",
					Message: provider.Message{
						Role: "assistant",
						ToolCalls: []provider.ToolCall{{
							ID:   "call-1",
							Type: "function",
							Function: provider.ToolFunction{
								Name:      "shell_execute",
								Arguments: `{"command":"echo hello"}`,
							},
						}},
					},
				}, nil
			case 2:
				last := req.Messages[len(req.Messages)-1]
				if last.Role != "tool" {
					return nil, fmt.Errorf("expected tool result message, got %q", last.Role)
				}
				return &provider.ChatResponse{
					Model: "test-model",
					Message: provider.Message{
						Role:    "assistant",
						Content: last.Content,
					},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected call %d", call)
			}
		},
	}

	registry := tools.NewRegistry()
	approver := &approverStub{}
	registry.Register(tools.NewShellToolWithApprover([]string{"ps"}, approver, "primary"))
	a := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
	})

	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned unexpected error: %v", err)
	}
	result, err := session.Turn(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Turn returned unexpected error: %v", err)
	}
	if approver.calls != 1 {
		t.Fatalf("expected shell approval path to be used once, got %d", approver.calls)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("expected tool-backed output, got %q", result.Output)
	}
}

func TestChatConversationRefreshesLearnedContextAfterRepeatedToolFailure(t *testing.T) {
	t.Parallel()

	mem := &memoryStub{
		resultFn: func(input core.RetrievalContext) memory.RetrievalResult {
			result := memory.RetrievalResult{
				BuiltInRules:       "Be helpful.",
				Operator:           "operator context",
				Soul:               "soul context",
				RuntimeHostSummary: "Runtime host summary:\n- name: runtime",
				FallbackBrief:      "Fallback finding for runtime: baseline learned",
			}
			if input.Trouble != nil {
				result.FallbackBrief = "Fallback finding for runtime: retry hint"
			}
			return result
		},
	}
	provider := &sequenceProvider{
		fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
			switch call {
			case 1, 2:
				return &provider.ChatResponse{
					Model: "test-model",
					Message: provider.Message{
						Role: "assistant",
						ToolCalls: []provider.ToolCall{{
							ID:   fmt.Sprintf("call-%d", call),
							Type: "function",
							Function: provider.ToolFunction{
								Name:      "fail_tool",
								Arguments: `{}`,
							},
						}},
					},
				}, nil
			case 3:
				var sawRefresh bool
				for _, msg := range req.Messages {
					if msg.Role == "system" && strings.Contains(msg.Content, "retry hint") {
						sawRefresh = true
						break
					}
				}
				if !sawRefresh {
					return nil, fmt.Errorf("expected refreshed learned context to be injected")
				}
				return &provider.ChatResponse{
					Model: "test-model",
					Message: provider.Message{
						Role:    "assistant",
						Content: "recovered",
					},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected call %d", call)
			}
		},
	}

	registry := tools.NewRegistry()
	registry.Register(failingTool{})
	a := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 4,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               mem,
	})

	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned unexpected error: %v", err)
	}
	result, err := session.Turn(context.Background(), "try the failing tool")
	if err != nil {
		t.Fatalf("Turn returned unexpected error: %v", err)
	}
	if result.Output != "recovered" {
		t.Fatalf("expected recovery output, got %q", result.Output)
	}
}
