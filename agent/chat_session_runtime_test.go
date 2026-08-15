package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
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

type contextSequenceProvider struct {
	call int
	fn   func(ctx context.Context, call int, req *provider.ChatRequest) (*provider.ChatResponse, error)
}

func (p *contextSequenceProvider) ChatCompletion(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.call++
	return p.fn(ctx, p.call, req)
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

type approvalCountingTool struct{ calls int }

func (t *approvalCountingTool) Name() string        { return "schedule_manage" }
func (t *approvalCountingTool) Description() string { return "changes a scheduled job" }
func (t *approvalCountingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"}}}`)
}
func (t *approvalCountingTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.calls++
	return "scheduled once", nil
}

type approvalEventObserver struct{ events chan tools.Event }

func (o approvalEventObserver) Observe(event tools.Event) { o.events <- event }

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

func TestChatWeatherLocationFollowUpKeepsShellCapability(t *testing.T) {
	p := &sequenceProvider{fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		switch call {
		case 1:
			if err := expectToolNames("shell_execute")(req); err != nil {
				return nil, err
			}
			return assistantText("What city should I check?"), nil
		case 2:
			if err := expectToolNames("shell_execute")(req); err != nil {
				return nil, err
			}
			return assistantText("Vancouver, BC or Vancouver, Washington?"), nil
		case 3:
			if err := expectToolNames("shell_execute")(req); err != nil {
				return nil, err
			}
			return assistantToolCall("weather-1", "shell_execute", `{}`), nil
		case 4:
			return assistantText("Vancouver, BC weather is available."), nil
		default:
			return nil, fmt.Errorf("unexpected call %d", call)
		}
	}}
	registry := tools.NewRegistry()
	registry.Register(fixedOutputTool{name: "shell_execute", output: "weather result"})
	a := New(Options{Provider: p, ProviderName: "test", ToolRegistry: registry, DefaultModel: "model", MaxIterations: 3, MaxTokens: 256, DisableCompletionVerification: true})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"what is the weather like today", "in vancouver", "bc"} {
		result, turnErr := session.Turn(context.Background(), prompt)
		if turnErr != nil {
			t.Fatalf("turn %q failed: %v", prompt, turnErr)
		}
		if prompt == "bc" && (len(result.Tools) != 1 || result.Tools[0].ToolName != "shell_execute") {
			t.Fatalf("expected location follow-up to execute shell, got %#v", result.Tools)
		}
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
	observer := &memoryEventObserver{}
	p := newScriptedProvider(t,
		scriptedProviderStep{name: "refusal", resp: assistantText("I cannot do that.")},
		scriptedProviderStep{name: "uncertain", resp: verifierJSON(verificationUnsatisfied, "Action missing.", []string{"Complete it."}, "Complete it.")},
		scriptedProviderStep{name: "same refusal", resp: assistantText("I cannot do that.")},
		scriptedProviderStep{name: "same verifier", resp: verifierJSON(verificationUnsatisfied, "Action missing.", []string{"Complete it."}, "Complete it.")},
	)
	a := New(Options{Provider: p, ProviderName: "test", ToolRegistry: tools.NewRegistry(), EventObserver: observer, DefaultModel: "model", MaxIterations: 25, MaxTokens: 256, MemoryRetriever: &memoryStub{}})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Turn(context.Background(), "do the task")
	if err == nil || !strings.Contains(err.Error(), "task incomplete") || !strings.Contains(err.Error(), "no progress") {
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
	if result.Verification.StopReason != tools.VerificationStopNoProgress || result.Verification.RepairAttempts != 1 || result.Verification.RepairLimit != 2 {
		t.Fatalf("expected explicit no-progress stop after repair 1/2, got %#v", result.Verification)
	}
	var final tools.VerificationActivity
	for _, event := range observer.events {
		if event.Type == tools.EventVerificationActivity && event.Verification.Final {
			final = event.Verification
		}
	}
	if final.Phase != tools.VerificationPhaseStopped || final.StopReason != tools.VerificationStopNoProgress {
		t.Fatalf("expected final no-progress verifier activity, got %#v", final)
	}
}

func TestChatVerificationActivityIsCorrelatedBoundedAndRedacted(t *testing.T) {
	observer := &memoryEventObserver{}
	p := newScriptedProvider(t,
		scriptedProviderStep{name: "premature answer", resp: assistantText("candidate-answer-must-not-enter-events")},
		scriptedProviderStep{name: "unsatisfied", resp: verifierJSON(
			verificationUnsatisfied,
			"Need api_key=supersecretvalue before completion.",
			[]string{"Use password=anothersecretvalue to finish the requested action."},
			"Finish the action.",
		)},
		scriptedProviderStep{name: "repaired answer", resp: assistantText("done")},
		scriptedProviderStep{name: "satisfied", resp: verifierJSON(verificationSatisfied, "The requested action is complete.", nil, "")},
	)
	a := New(Options{
		Provider:        p,
		ProviderName:    "test",
		ToolRegistry:    tools.NewRegistry(),
		EventObserver:   observer,
		DefaultModel:    "model",
		MaxIterations:   3,
		MaxTokens:       256,
		MemoryRetriever: &memoryStub{},
	})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := telemetry.WithFields(context.Background(), telemetry.Fields{SessionID: "session-visible", TurnID: "turn-visible"})
	result, err := session.Turn(ctx, "finish the task")
	if err != nil {
		t.Fatal(err)
	}
	p.AssertComplete(t)

	var activities []tools.Event
	for _, event := range observer.events {
		if event.Type != tools.EventVerificationActivity {
			continue
		}
		activities = append(activities, event)
		if event.SessionID != "session-visible" || event.TurnID != "turn-visible" {
			t.Fatalf("verification event lost turn correlation: %#v", event)
		}
		if event.Command != "" || event.Output != "" || event.ErrorMessage != "" {
			t.Fatalf("verification event exposed unrestricted runtime detail: %#v", event)
		}
		serialized := fmt.Sprintf("%#v", event.Verification)
		for _, forbidden := range []string{"candidate-answer-must-not-enter-events", "supersecretvalue", "anothersecretvalue"} {
			if strings.Contains(serialized, forbidden) {
				t.Fatalf("verification event exposed %q: %s", forbidden, serialized)
			}
		}
	}
	if len(activities) != 5 {
		t.Fatalf("expected checking, repair planning/update, checking, and final activity snapshots, got %d: %#v", len(activities), activities)
	}
	if got := activities[0].Verification; got.Phase != tools.VerificationPhaseChecking || got.RepairAttempt != 0 || got.RepairLimit != 2 {
		t.Fatalf("unexpected initial checking activity: %#v", got)
	}
	capabilitySnapshot := activities[2].Verification
	if capabilitySnapshot.Phase != tools.VerificationPhaseRepairing || !capabilitySnapshot.CapabilitiesEvaluated || capabilitySnapshot.CapabilitiesChanged || capabilitySnapshot.RepairAttempt != 1 {
		t.Fatalf("expected repair 1/2 with unchanged capabilities, got %#v", capabilitySnapshot)
	}
	if !strings.Contains(capabilitySnapshot.Reason, "[REDACTED]") || len(capabilitySnapshot.MissingActions) != 1 || !strings.Contains(capabilitySnapshot.MissingActions[0], "[REDACTED]") {
		t.Fatalf("expected bounded redacted verifier summary, got %#v", capabilitySnapshot)
	}
	final := activities[len(activities)-1].Verification
	if !final.Final || final.Phase != tools.VerificationPhaseCompleted || final.Status != verificationSatisfied || final.RepairAttempt != 1 || final.RepairLimit != 2 {
		t.Fatalf("unexpected final verifier activity: %#v", final)
	}
	if result.Verification.RepairAttempts != 1 || result.Verification.RepairLimit != 2 || !result.Verification.CapabilitiesEvaluated || result.Verification.CapabilitiesChanged {
		t.Fatalf("final result did not reconcile verifier progress: %#v", result.Verification)
	}
}

func TestChatVerificationStopsWhenRequiredCapabilityIsUnavailable(t *testing.T) {
	observer := &memoryEventObserver{}
	p := newScriptedProvider(t,
		scriptedProviderStep{name: "answer without lookup", resp: assistantText("I cannot look that up.")},
		scriptedProviderStep{name: "verification requires web", resp: verifierJSON(
			verificationUnsatisfied,
			"Current information is missing.",
			[]string{"Use web_search."},
			"Use web_search and answer from its result.",
		)},
	)
	a := New(Options{
		Provider:        p,
		ProviderName:    "test",
		ToolRegistry:    tools.NewRegistry(),
		EventObserver:   observer,
		DefaultModel:    "model",
		MaxIterations:   25,
		MaxTokens:       256,
		MemoryRetriever: &memoryStub{},
	})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Turn(context.Background(), "look up the current result")
	if err == nil || !strings.Contains(err.Error(), "task incomplete") || !strings.Contains(err.Error(), "required capability was unavailable") {
		t.Fatalf("expected capability-unavailable incomplete result, got %v", err)
	}
	p.AssertComplete(t)
	if result.Verification.StopReason != tools.VerificationStopCapabilityUnavailable || result.Verification.RepairAttempts != 1 || !result.Verification.CapabilitiesEvaluated {
		t.Fatalf("expected capability-unavailable stop on repair 1/2, got %#v", result.Verification)
	}
	var final tools.VerificationActivity
	for _, event := range observer.events {
		if event.Type == tools.EventVerificationActivity && event.Verification.Final {
			final = event.Verification
		}
	}
	if final.Phase != tools.VerificationPhaseStopped || final.StopReason != tools.VerificationStopCapabilityUnavailable {
		t.Fatalf("expected final capability-unavailable activity, got %#v", final)
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

func TestChatConversationContinuesExactApprovedToolCallWithPriorContext(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	policy, err := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}

	providerCalls := 0
	p := &sequenceProvider{fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		providerCalls = call
		hasEarlierUser := false
		hasEarlierAssistant := false
		for _, message := range req.Messages {
			hasEarlierUser = hasEarlierUser || (message.Role == "user" && message.Content == "remember target alpha")
			hasEarlierAssistant = hasEarlierAssistant || (message.Role == "assistant" && message.Content == "I will keep target alpha in context.")
		}
		switch call {
		case 1:
			return assistantText("I will keep target alpha in context."), nil
		case 2:
			if !hasEarlierUser || !hasEarlierAssistant {
				return nil, fmt.Errorf("approved turn lost prior conversation context")
			}
			return assistantToolCall("approval-call", "schedule_manage", `{"action":"add"}`), nil
		case 3:
			if !hasEarlierUser || !hasEarlierAssistant {
				return nil, fmt.Errorf("post-approval continuation lost prior conversation context")
			}
			last := req.Messages[len(req.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "approval-call" || last.Content != "scheduled once" {
				return nil, fmt.Errorf("expected exact approved tool result, got %#v", last)
			}
			return assistantText("Target alpha was scheduled."), nil
		default:
			return nil, fmt.Errorf("unexpected provider call %d", call)
		}
	}}

	countingTool := &approvalCountingTool{}
	registry := tools.NewRegistry()
	registry.ConfigureSecurityWithStore(policy, tools.NewBlockingApprover(), nil, store)
	registry.Register(countingTool)
	events := make(chan tools.Event, 16)
	a := New(Options{
		Provider:                      p,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		EventObserver:                 approvalEventObserver{events: events},
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		MemoryRetriever:               &memoryStub{},
		BlockedWorkStore:              store,
		AwaitManualApprovals:          true,
		DisableCompletionVerification: true,
	})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Turn(context.Background(), "remember target alpha"); err != nil {
		t.Fatal(err)
	}

	type turnOutcome struct {
		result ChatTurnResult
		err    error
	}
	turnDone := make(chan turnOutcome, 1)
	go func() {
		result, turnErr := session.Turn(context.Background(), "schedule it now")
		turnDone <- turnOutcome{result: result, err: turnErr}
	}()

	var approvalEvent tools.Event
	waitDeadline := time.After(2 * time.Second)
	waiting := true
	for waiting {
		select {
		case event := <-events:
			if event.Type == tools.EventApprovalRequired {
				approvalEvent = event
				waiting = false
			}
		case <-waitDeadline:
			t.Fatal("timed out waiting for inline approval request")
		}
	}
	if approvalEvent.BlockedWorkID == "" || approvalEvent.ToolCallID != "approval-call" {
		t.Fatalf("approval event did not identify the blocked call: %#v", approvalEvent)
	}
	if providerCalls != 2 || countingTool.calls != 0 {
		t.Fatalf("tool must remain unexecuted while approval is pending: provider_calls=%d tool_calls=%d", providerCalls, countingTool.calls)
	}

	grant, err := tools.ApproveBlockedWork(context.Background(), store, policy, approvalEvent.BlockedWorkID, 15*time.Minute, "test-tui")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ResumeApproval(approvalEvent.BlockedWorkID); err != nil {
		t.Fatal(err)
	}

	select {
	case outcome := <-turnDone:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.Output != "Target alpha was scheduled." || len(outcome.result.Tools) != 1 || !outcome.result.Tools[0].Success {
			t.Fatalf("unexpected continued turn result: %#v", outcome.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approved turn to continue")
	}
	if providerCalls != 3 {
		t.Fatalf("approval replayed the prompt instead of continuing the pending call: provider_calls=%d", providerCalls)
	}
	if countingTool.calls != 1 {
		t.Fatalf("approved action executed %d times, want exactly once", countingTool.calls)
	}
	if history := session.History(); len(history) != 6 || history[3].Role != "assistant" || history[4].Role != "tool" {
		t.Fatalf("conversation history was not continued in place: %#v", history)
	}
	grants, err := store.ListSecurityActionGrants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].Digest != grant.Digest || grants[0].RemainingUses != 0 || grants[0].UsedAt.IsZero() {
		t.Fatalf("exact one-use grant was not consumed by the pending action: %#v", grants)
	}
}

func TestChatConversationExecutesApprovedShellCallOnceWithoutPromptReplay(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	policy, err := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "approval-marker")
	command := fmt.Sprintf("sh -c %q", fmt.Sprintf("printf x >> %q", marker))
	assessment, err := tools.AssessShellCommand(command, policy)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Decision != securitypolicy.DecisionAsk {
		t.Fatalf("test shell command must require approval, got %s: %s", assessment.Decision, assessment.Reason)
	}
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}

	p := &sequenceProvider{fn: func(call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		switch call {
		case 1:
			return assistantToolCall("shell-approval-call", "shell_execute", string(args)), nil
		case 2:
			last := req.Messages[len(req.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "shell-approval-call" {
				return nil, fmt.Errorf("expected continued shell tool result, got %#v", last)
			}
			return assistantText("The approved shell action completed."), nil
		default:
			return nil, fmt.Errorf("approval replayed the prompt: provider call %d", call)
		}
	}}
	registry, err := tools.NewDefaultRegistryFromOptions(tools.DefaultRegistryOptions{
		Store:                store,
		BlockManualApprovals: true,
		SecurityPolicy:       &policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan tools.Event, 32)
	a := New(Options{
		Provider:                      p,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		EventObserver:                 approvalEventObserver{events: events},
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		MemoryRetriever:               &memoryStub{},
		BlockedWorkStore:              store,
		AwaitManualApprovals:          true,
		DisableCompletionVerification: true,
	})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	type turnOutcome struct {
		result ChatTurnResult
		err    error
	}
	turnDone := make(chan turnOutcome, 1)
	go func() {
		result, turnErr := session.Turn(context.Background(), "append the marker")
		turnDone <- turnOutcome{result: result, err: turnErr}
	}()

	var approvalEvent tools.Event
	waitDeadline := time.After(2 * time.Second)
	waiting := true
	for waiting {
		select {
		case event := <-events:
			if event.Type == tools.EventApprovalRequired {
				approvalEvent = event
				waiting = false
			}
		case <-waitDeadline:
			t.Fatal("timed out waiting for shell approval request")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell action ran before approval: %v", err)
	}
	if _, err := tools.ApproveBlockedWork(context.Background(), store, policy, approvalEvent.BlockedWorkID, 15*time.Minute, "test-tui"); err != nil {
		t.Fatal(err)
	}
	if err := session.ResumeApproval(approvalEvent.BlockedWorkID); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-turnDone:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.Output != "The approved shell action completed." || len(outcome.result.Tools) != 1 || !outcome.result.Tools[0].Success {
			t.Fatalf("unexpected approved shell result: %#v", outcome.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approved shell continuation")
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "x" || p.call != 2 {
		t.Fatalf("approved shell action was replayed: contents=%q provider_calls=%d", contents, p.call)
	}
}

func TestChatConversationCancellationCompletesPendingToolMessage(t *testing.T) {
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	policy, err := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}

	p := &contextSequenceProvider{fn: func(ctx context.Context, call int, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		switch call {
		case 1:
			return assistantToolCall("cancel-call", "schedule_manage", `{"action":"add"}`), nil
		case 2:
			last := req.Messages[len(req.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "cancel-call" || !strings.Contains(last.Content, "approval wait interrupted") {
				return nil, fmt.Errorf("canceled approval left an invalid tool sequence: %#v", last)
			}
			return nil, ctx.Err()
		case 3:
			roles := make([]string, 0, len(req.Messages))
			for _, message := range req.Messages {
				roles = append(roles, message.Role)
			}
			if !strings.Contains(strings.Join(roles, ","), "assistant,tool,user") {
				return nil, fmt.Errorf("next turn did not receive a completed canceled tool sequence: %v", roles)
			}
			return assistantText("The approval was canceled safely."), nil
		default:
			return nil, fmt.Errorf("unexpected provider call %d", call)
		}
	}}
	registry := tools.NewRegistry()
	registry.ConfigureSecurityWithStore(policy, tools.NewBlockingApprover(), nil, store)
	registry.Register(&approvalCountingTool{})
	events := make(chan tools.Event, 16)
	a := New(Options{
		Provider:                      p,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		EventObserver:                 approvalEventObserver{events: events},
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		MemoryRetriever:               &memoryStub{},
		BlockedWorkStore:              store,
		AwaitManualApprovals:          true,
		DisableCompletionVerification: true,
	})
	session, _, err := a.StartChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	turnCtx, cancel := context.WithCancel(context.Background())
	type turnError struct{ err error }
	turnDone := make(chan turnError, 1)
	go func() {
		_, turnErr := session.Turn(turnCtx, "schedule this")
		turnDone <- turnError{err: turnErr}
	}()

	waitDeadline := time.After(2 * time.Second)
	waiting := true
	for waiting {
		select {
		case event := <-events:
			if event.Type == tools.EventApprovalRequired {
				waiting = false
			}
		case <-waitDeadline:
			t.Fatal("timed out waiting for approval before cancellation")
		}
	}
	cancel()
	select {
	case outcome := <-turnDone:
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("expected canceled approval turn, got %v", outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out canceling approval wait")
	}
	if history := session.History(); len(history) != 3 || history[2].Role != "tool" || history[2].ToolCallID != "cancel-call" {
		t.Fatalf("canceled approval left incomplete conversation history: %#v", history)
	}
	result, err := session.Turn(context.Background(), "what happened?")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "The approval was canceled safely." {
		t.Fatalf("unexpected follow-up after canceled approval: %#v", result)
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
