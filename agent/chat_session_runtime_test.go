package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
		Guidance:           "guidance context",
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

type fixedOutputTool struct {
	name   string
	output string
}

func (t fixedOutputTool) Name() string        { return t.name }
func (t fixedOutputTool) Description() string { return t.name }
func (t fixedOutputTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t fixedOutputTool) Execute(context.Context, json.RawMessage) (string, error) {
	return t.output, nil
}

func TestChatSpeedTestFollowUpKeepsShellCapability(t *testing.T) {
	p := &sequenceProvider{fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		switch call {
		case 1:
			if err := expectToolNames("shell_execute")(req); err != nil {
				return nil, err
			}
			return assistantText("Your speed is 2 Mbps."), nil
		case 2:
			if err := expectToolNames("shell_execute")(req); err != nil {
				return nil, err
			}
			return assistantText("That does seem very low."), nil
		case 3:
			if err := expectToolNames("shell_execute")(req); err != nil {
				return nil, err
			}
			return assistantToolCall("speed-2", "shell_execute", `{}`), nil
		case 4:
			return assistantText("The retest is 95 Mbps."), nil
		default:
			return nil, fmt.Errorf("unexpected call %d", call)
		}
	}}
	registry := tools.NewRegistry()
	registry.Register(fixedOutputTool{name: "shell_execute", output: "95 Mbps"})
	a := New(Options{Provider: p, ProviderName: "test", ToolRegistry: registry, DefaultModel: "model", MaxIterations: 3, MaxTokens: 256, DisableCompletionVerification: true})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.Turn(context.Background(), "what is my internet speed"); err != nil {
		t.Fatal(err)
	}
	if _, err = session.Turn(context.Background(), "that seems very low"); err != nil {
		t.Fatal(err)
	}
	result, err := session.Turn(context.Background(), "test again")
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskClass != core.TaskClassInspection || len(result.Tools) != 1 || result.Tools[0].ToolName != "shell_execute" {
		t.Fatalf("expected inherited shell-backed retest, got class=%s tools=%#v", result.TaskClass, result.Tools)
	}
	if result.Tools[0].OutputInline != "95 Mbps" || result.Tools[0].OutputStoredBytes == 0 || result.Tools[0].OutputDigest == "" {
		t.Fatalf("expected durable output metadata with prompt dumps disabled, got %#v", result.Tools[0])
	}
}

func TestTranscriptToStateMessagesMasksPopulatedToolAndAssistantFields(t *testing.T) {
	argumentSecret := "sk-argumentsecretvalue123456789"
	toolSecret := "sk-tooloutputsecretvalue123456789"
	assistantSecret := "sk-assistantsecretvalue123456789"
	messages := TranscriptToStateMessages(1, 2, 0, time.Now(), []provider.Message{
		{
			Role:    "assistant",
			Content: "calling with " + assistantSecret,
			ToolCalls: []provider.ToolCall{{
				ID: "call-1",
				Function: provider.ToolFunction{
					Name:      "shell_execute",
					Arguments: `{"command":"echo ` + argumentSecret + `"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "result " + toolSecret},
	})
	if len(messages) != 2 {
		t.Fatalf("expected two messages, got %#v", messages)
	}
	all := messages[0].Content + messages[0].ToolArguments + messages[0].ToolCallsJSON + messages[1].Content
	for _, raw := range []string{argumentSecret, toolSecret, assistantSecret} {
		if strings.Contains(all, raw) {
			t.Fatalf("raw secret %q survived transcript conversion: %#v", raw, messages)
		}
	}
	if strings.Count(all, "[REDACTED]") < 4 {
		t.Fatalf("expected redaction in content, arguments, JSON, and tool output, got %#v", messages)
	}
}

func TestChatRepairRebuildsCapabilitiesAndExecutesNewTool(t *testing.T) {
	p := newScriptedProvider(t,
		scriptedProviderStep{name: "premature answer", expect: expectToolNames("shell_execute"), resp: assistantText("I cannot look that up.")},
		scriptedProviderStep{name: "verification requests web", resp: verifierJSON(verificationUnsatisfied, "Current information is missing.", []string{"Use web_search."}, "Use web_search and answer from its result.")},
		scriptedProviderStep{name: "repair gains web", expect: expectToolNames("shell_execute", "web_search"), resp: assistantToolCall("web-1", "web_search", `{}`)},
		scriptedProviderStep{name: "answer from tool", expect: expectLastMessage("tool", "current result"), resp: assistantText("The current result is available.")},
		scriptedProviderStep{name: "satisfied", resp: verifierJSON(verificationSatisfied, "Used the newly available capability.", nil, "")},
	)
	registry := tools.NewRegistry()
	registry.Register(fixedOutputTool{name: "shell_execute", output: "unused"})
	registry.Register(fixedOutputTool{name: "web_search", output: "current result"})
	a := New(Options{Provider: p, ProviderName: "test", ToolRegistry: registry, DefaultModel: "model", MaxIterations: 4, MaxTokens: 256, MemoryRetriever: &memoryStub{}})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Turn(context.Background(), "tell me the answer")
	if err != nil {
		t.Fatal(err)
	}
	p.AssertComplete(t)
	if len(result.Tools) != 1 || result.Tools[0].ToolName != "web_search" || !result.Verification.RepairTriggered {
		t.Fatalf("expected capability-aware repair tool use, got tools=%#v verification=%#v", result.Tools, result.Verification)
	}
}

func TestChatRepairStopsOnNoProgress(t *testing.T) {
	p := newScriptedProvider(t,
		scriptedProviderStep{name: "refusal", resp: assistantText("I cannot do that.")},
		scriptedProviderStep{name: "uncertain", resp: verifierJSON(verificationUnsatisfied, "Action missing.", []string{"Complete it."}, "Complete it.")},
		scriptedProviderStep{name: "same refusal", resp: assistantText("I cannot do that.")},
		scriptedProviderStep{name: "same verifier", resp: verifierJSON(verificationUnsatisfied, "Action missing.", []string{"Complete it."}, "Complete it.")},
	)
	a := New(Options{Provider: p, ProviderName: "test", ToolRegistry: tools.NewRegistry(), DefaultModel: "model", MaxIterations: 25, MaxTokens: 256, MemoryRetriever: &memoryStub{}})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Turn(context.Background(), "do the task")
	if err == nil || !strings.Contains(err.Error(), "task incomplete") {
		t.Fatalf("expected bounded incomplete result, got %v", err)
	}
	p.AssertComplete(t)
	assistantMessages := 0
	for _, message := range result.Transcript {
		if message.Role == "assistant" {
			assistantMessages++
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("expected only final refusal persisted, transcript=%#v", result.Transcript)
	}
}

func (f failingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (f failingTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", fmt.Errorf("boom")
}

func TestStartChatCreatesIndependentConversationHistory(t *testing.T) {
	t.Parallel()

	a := New(Options{
		ProviderName: "openrouter",
		DefaultModel: "test-model",
	})
	first, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("first StartChat returned unexpected error: %v", err)
	}
	first.history.Add(provider.Message{Role: "user", Content: "old conversation"})

	second, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("second StartChat returned unexpected error: %v", err)
	}
	if got := second.History(); len(got) != 0 {
		t.Fatalf("expected new conversation not to inherit transcript, got %#v", got)
	}
	if got := first.History(); len(got) != 1 {
		t.Fatalf("expected original conversation to remain independently owned, got %#v", got)
	}
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

func TestChatConversationToolsReturnsRegisteredCapabilities(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry()
	registry.Register(failingTool{})
	a := New(Options{
		Provider:      &sequenceProvider{},
		ProviderName:  "openrouter",
		ToolRegistry:  registry,
		DefaultModel:  "test-model",
		MaxIterations: 1,
		MaxTokens:     128,
	})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatalf("StartChat returned unexpected error: %v", err)
	}

	got := session.Tools()
	if len(got) != 1 || got[0].Name != "fail_tool" || got[0].Description == "" {
		t.Fatalf("unexpected chat tools: %#v", got)
	}
}

func TestChatConversationRefreshesLearnedContextAfterRepeatedToolFailure(t *testing.T) {
	t.Parallel()

	mem := &memoryStub{
		resultFn: func(input core.RetrievalContext) memory.RetrievalResult {
			result := memory.RetrievalResult{
				BuiltInRules:       "Be helpful.",
				Guidance:           "guidance context",
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
