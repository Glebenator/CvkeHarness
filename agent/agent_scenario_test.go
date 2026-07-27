package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

type echoTool struct{}

func (echoTool) Name() string        { return "echo_tool" }
func (echoTool) Description() string { return "echoes the provided text" }
func (echoTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)
}
func (echoTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &payload); err != nil {
		return "", err
	}
	return payload.Text, nil
}

type runRecorderStub struct {
	records []state.RunRecord
}

func (r *runRecorderStub) RecordRun(_ context.Context, record state.RunRecord) error {
	r.records = append(r.records, record)
	return nil
}

func TestRunScenarioNormalToolCallThenFinalAnswer(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name:   "request tool",
			expect: allRequestPredicates(expectModel("test-model"), expectToolNames("echo_tool")),
			resp:   assistantToolCall("call-1", "echo_tool", `{"text":"tool result"}`),
		},
		scriptedProviderStep{
			name:   "final answer",
			expect: expectLastMessage("tool", "tool result"),
			resp:   assistantText("done"),
		},
	)
	recorder := &runRecorderStub{}
	registry := tools.NewRegistry()
	registry.Register(echoTool{})

	agent := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
		RunRecorder:                   recorder,
	})

	result, err := agent.Run(context.Background(), "use a tool")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	provider.AssertComplete(t)

	if result.Output != "done" {
		t.Fatalf("expected final output, got %q", result.Output)
	}
	if len(result.Run.Tools) != 1 || !result.Run.Tools[0].Success {
		t.Fatalf("expected one successful tool outcome, got %#v", result.Run.Tools)
	}
	if len(result.Run.Phases) != 1 || !result.Run.Phases[0].Success {
		t.Fatalf("expected successful execution phase, got %#v", result.Run.Phases)
	}
	if len(recorder.records) != 1 || !recorder.records[0].Success {
		t.Fatalf("expected run recorder to capture success, got %#v", recorder.records)
	}
}

func TestRunScenarioApprovalWaitBecomesBlockedStateWithoutVerifierLoop(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name:   "request shell command",
			expect: expectToolNames("shell_execute"),
			resp:   assistantToolCall("call-1", "shell_execute", `{"command":"echo hello"}`),
		},
	)
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	registry := tools.NewRegistry()
	registry.Register(tools.NewShellToolWithApprover([]string{"ps"}, tools.NewBlockingApprover(), "primary"))

	result, err := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
		BlockedWorkStore:              store,
	}).Run(context.Background(), "run echo")
	if err == nil {
		t.Fatal("expected approval wait to return a blocked error")
	}
	if result.Run.TaskState != state.TaskStateBlockedWaitingUser || result.Run.Success {
		t.Fatalf("expected blocked task state, got %#v", result.Run)
	}
	provider.AssertComplete(t)

	pending, listErr := store.ListBlockedWork(context.Background())
	if listErr != nil {
		t.Fatalf("ListBlockedWork returned error: %v", listErr)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one persisted blocked work item, got %#v", pending)
	}
	if pending[0].PendingApprovalPayload != "echo hello" || pending[0].ConversationSnapshot == "" || pending[0].ContinuationData == "" {
		t.Fatalf("expected resumable approval context to persist, got %#v", pending[0])
	}
}

func TestResumeBlockedWorkCompletesAfterApproval(t *testing.T) {
	t.Parallel()

	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	now := time.Now().UTC()
	target := state.Target{
		ID: "runtime-1", Kind: memory.TargetKindRuntime, Environment: state.EnvironmentRuntime,
		PrimaryName: "runtime.local", Transport: "local", RemoteIdentity: "local:runtime-1",
		Status: state.MemoryStatusActive, FirstSeenAt: now, LastSeenAt: now, VerifiedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.ReplaceOperationalMemory(context.Background(), state.OperationalMemory{Targets: []state.Target{target}}); err != nil {
		t.Fatalf("ReplaceOperationalMemory returned error: %v", err)
	}
	targetMemory := &memoryStub{resolution: memory.TargetResolution{
		RuntimeHostID: target.ID, TargetID: target.ID, TargetKind: target.Kind, Environment: target.Environment,
	}}
	blockingProvider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "request shell command",
			resp: assistantToolCall("call-1", "shell_execute", `{"command":"echo hello"}`),
		},
	)
	blockingRegistry := tools.NewRegistry()
	blockingRegistry.Register(tools.NewShellToolWithApprover([]string{"ps"}, tools.NewBlockingApprover(), "primary"))
	blockingAgent := New(Options{
		Provider:                      blockingProvider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  blockingRegistry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               targetMemory,
		BlockedWorkStore:              store,
	})
	if _, err := blockingAgent.Run(context.Background(), "run echo"); err == nil {
		t.Fatal("expected initial run to block")
	}

	pending, err := store.ListBlockedWork(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("expected one pending work item, got pending=%#v err=%v", pending, err)
	}
	if err := store.SaveCommandApproval(context.Background(), state.CommandApproval{
		TargetID: target.ID, Environment: target.Environment, RemoteIdentity: target.RemoteIdentity,
		Command: "echo hello", Action: "echo",
		Status: state.ApprovalStatusApproved, Source: "cli_policy", Rationale: "operator approved resumed action",
		PolicyVersion: state.CommandApprovalPolicyVersion, ApprovedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveCommandApproval returned error: %v", err)
	}
	resumeProvider := newScriptedProvider(t,
		scriptedProviderStep{name: "request shell command again", resp: assistantToolCall("call-1", "shell_execute", `{"command":"echo hello"}`)},
		scriptedProviderStep{name: "final after approved command", expect: expectLastMessage("tool", "hello"), resp: assistantText("done")},
	)
	resumeRegistry := tools.NewRegistry()
	resumeRegistry.Register(tools.NewShellToolWithApprovalStore([]string{"ps"}, tools.NewBlockingApprover(), "primary", store))
	resumeAgent := New(Options{
		Provider:                      resumeProvider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  resumeRegistry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               targetMemory,
		BlockedWorkStore:              store,
	})

	result, err := resumeAgent.ResumeBlockedWork(context.Background(), pending[0])
	if err != nil {
		t.Fatalf("ResumeBlockedWork returned error: %v", err)
	}
	if result.Run.TaskState != state.TaskStateCompleted || !result.Run.Success {
		t.Fatalf("expected resumed run to complete, got %#v", result.Run)
	}
	resumeProvider.AssertComplete(t)
}

func TestRunScenarioVerificationSatisfied(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "final answer",
			resp: assistantText("done"),
		},
		scriptedProviderStep{
			name:   "verification satisfied",
			expect: allRequestPredicates(expectModel("test-model"), expectLastMessage("user", "assistant_final_output")),
			resp:   verifierJSON(verificationSatisfied, "The final answer satisfies the request.", nil, ""),
		},
	)

	result, err := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "test-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
	}).Run(context.Background(), "finish cleanly")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	provider.AssertComplete(t)
	if result.Output != "done" || result.Verification.Status != verificationSatisfied {
		t.Fatalf("expected satisfied verification, got output=%q verification=%#v", result.Output, result.Verification)
	}
	if len(result.Run.Phases) != 2 || result.Run.Phases[1].Phase != core.PhaseVerification || !result.Run.Phases[1].Success {
		t.Fatalf("expected successful verification phase, got %#v", result.Run.Phases)
	}
}

func TestRunScenarioVerificationRepairsIncompleteConditionalTask(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "premature final answer",
			resp: assistantText("Docker is not running. I can start it for you."),
		},
		scriptedProviderStep{
			name: "verification unsatisfied",
			resp: verifierJSON(verificationUnsatisfied, "The assistant checked Docker but did not start it.", []string{"Start Docker because it is not running."}, "Start Docker now, then report the result."),
		},
		scriptedProviderStep{
			name:   "repair sees verifier instruction",
			expect: expectLastMessage("system", "Start Docker now"),
			resp:   assistantToolCall("call-1", "echo_tool", `{"text":"Docker started"}`),
		},
		scriptedProviderStep{
			name:   "final after repair",
			expect: expectLastMessage("tool", "Docker started"),
			resp:   assistantText("Docker was not running, so I started it."),
		},
		scriptedProviderStep{
			name: "verification satisfied",
			resp: verifierJSON(verificationSatisfied, "The conditional action was completed.", nil, ""),
		},
	)
	registry := tools.NewRegistry()
	registry.Register(echoTool{})

	result, err := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    registry,
		DefaultModel:    "test-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
	}).Run(context.Background(), "check if docker is running, and if not, start it")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	provider.AssertComplete(t)
	if !result.Verification.RepairTriggered || result.Verification.Status != verificationSatisfied {
		t.Fatalf("expected repair-triggered satisfied verification, got %#v", result.Verification)
	}
	if len(result.Run.Tools) != 1 || !result.Run.Tools[0].Success {
		t.Fatalf("expected repair tool call to be recorded, got %#v", result.Run.Tools)
	}
}

func TestRunScenarioVerificationRepairExhaustion(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{name: "premature final", resp: assistantText("Docker is not running. I can start it for you.")},
		scriptedProviderStep{name: "unsatisfied", resp: verifierJSON(verificationUnsatisfied, "Not started.", []string{"Start Docker."}, "Start Docker.")},
		scriptedProviderStep{name: "still incomplete", resp: assistantText("Docker is still not running.")},
		scriptedProviderStep{name: "unsatisfied again", resp: verifierJSON(verificationUnsatisfied, "Still not started.", []string{"Start Docker."}, "Start Docker.")},
	)

	result, err := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "test-model",
		MaxIterations:   2,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
	}).Run(context.Background(), "check if docker is running, and if not, start it")
	if err == nil || !strings.Contains(err.Error(), "task incomplete after verification repair") {
		t.Fatalf("expected incomplete-task error, got %v", err)
	}
	if result.Run.Success || result.Verification.Status != verificationUnsatisfied {
		t.Fatalf("expected failed run with unsatisfied verification, got run=%#v verification=%#v", result.Run, result.Verification)
	}
}

func TestRunScenarioVerificationRepairRetriesWithinIterationBudget(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{name: "premature final", resp: assistantText("Docker is not running. I can start it for you.")},
		scriptedProviderStep{name: "unsatisfied", resp: verifierJSON(verificationUnsatisfied, "Not started.", []string{"Start Docker."}, "Start Docker now.")},
		scriptedProviderStep{
			name:   "first repair still incomplete",
			expect: expectLastMessage("system", "Start Docker now"),
			resp:   assistantText("I will start Docker next."),
		},
		scriptedProviderStep{name: "unsatisfied again", resp: verifierJSON(verificationUnsatisfied, "Still not started.", []string{"Start Docker."}, "Stop promising and start Docker now.")},
		scriptedProviderStep{
			name:   "second repair completes",
			expect: expectLastMessage("system", "Stop promising and start Docker now"),
			resp:   assistantText("Docker was not running, so I started it."),
		},
		scriptedProviderStep{name: "verification satisfied", resp: verifierJSON(verificationSatisfied, "The second repair completed the action.", nil, "")},
	)

	result, err := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "test-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
	}).Run(context.Background(), "check if docker is running, and if not, start it")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	provider.AssertComplete(t)
	if !result.Verification.RepairTriggered || result.Verification.Status != verificationSatisfied {
		t.Fatalf("expected retry repair to satisfy run, got verification=%#v", result.Verification)
	}
}

func TestRunScenarioMalformedVerificationJSONFailsAfterRepair(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{name: "final", resp: assistantText("possibly done")},
		scriptedProviderStep{name: "malformed verifier", resp: assistantText("not json")},
		scriptedProviderStep{name: "repair final", resp: assistantText("possibly done after repair")},
		scriptedProviderStep{name: "malformed verifier again", resp: assistantText("not json")},
	)

	result, err := New(Options{
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "test-model",
		MaxIterations:   2,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
	}).Run(context.Background(), "do a task")
	if err == nil || !strings.Contains(err.Error(), "task incomplete after verification repair") {
		t.Fatalf("expected incomplete-task error, got %v", err)
	}
	if !result.Verification.MalformedVerifierJSON || result.Verification.Status != verificationUncertain {
		t.Fatalf("expected malformed verifier JSON to be recorded as uncertain, got %#v", result.Verification)
	}
}

func TestRunScenarioMalformedToolCallJSON(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "request malformed tool",
			resp: assistantToolCall("call-1", "echo_tool", `{"text":`),
		},
		scriptedProviderStep{
			name:   "final answer after tool error",
			expect: expectLastMessage("tool", "Error executing tool:"),
			resp:   assistantText("reported tool error"),
		},
	)
	registry := tools.NewRegistry()
	registry.Register(echoTool{})

	result, err := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
	}).Run(context.Background(), "call malformed tool")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if len(result.Run.Tools) != 1 || result.Run.Tools[0].Success || result.Run.Tools[0].ErrorMessage == "" {
		t.Fatalf("expected one failed tool outcome, got %#v", result.Run.Tools)
	}
}

func TestRunScenarioUnknownTool(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "request unknown tool",
			resp: assistantToolCall("call-1", "missing_tool", `{}`),
		},
		scriptedProviderStep{
			name:   "final answer after unknown tool",
			expect: expectLastMessage("tool", "unknown tool: missing_tool"),
			resp:   assistantText("unknown tool handled"),
		},
	)

	result, err := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  tools.NewRegistry(),
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
	}).Run(context.Background(), "call unknown tool")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if len(result.Run.Tools) != 1 || result.Run.Tools[0].ToolName != "missing_tool" || result.Run.Tools[0].Success {
		t.Fatalf("expected failed unknown-tool outcome, got %#v", result.Run.Tools)
	}
}

func TestRunScenarioProviderErrorOnFirstIteration(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "provider error",
			err:  fmt.Errorf("temporary outage"),
		},
	)

	result, err := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  tools.NewRegistry(),
		DefaultModel:                  "test-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
	}).Run(context.Background(), "fail early")
	if err == nil || !strings.Contains(err.Error(), "LLM API error on iteration 1") {
		t.Fatalf("expected first-iteration provider error, got %v", err)
	}
	if result.Run.Success {
		t.Fatalf("expected failed run record, got %#v", result.Run)
	}
}

func TestRunScenarioMaxIterationExhaustion(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{name: "tool 1", resp: assistantToolCall("call-1", "echo_tool", `{"text":"one"}`)},
		scriptedProviderStep{name: "tool 2", resp: assistantToolCall("call-2", "echo_tool", `{"text":"two"}`)},
	)
	registry := tools.NewRegistry()
	registry.Register(echoTool{})

	result, err := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "test-model",
		MaxIterations:                 2,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
	}).Run(context.Background(), "never finish")
	if err == nil || !strings.Contains(err.Error(), "agent exceeded max iterations") {
		t.Fatalf("expected max-iteration exhaustion, got %v", err)
	}
	if len(result.Run.Tools) != 2 {
		t.Fatalf("expected two tool outcomes before exhaustion, got %#v", result.Run.Tools)
	}
}

func TestRunScenarioActualModelAliasing(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "actual model differs",
			resp: &provider.ChatResponse{
				Model: "actual-model-alias",
				Message: provider.Message{
					Role: "assistant",
					ToolCalls: []provider.ToolCall{{
						ID:   "call-1",
						Type: "function",
						Function: provider.ToolFunction{
							Name:      "echo_tool",
							Arguments: `{"text":"ok"}`,
						},
					}},
				},
			},
		},
		scriptedProviderStep{
			name: "final",
			resp: &provider.ChatResponse{
				Message: provider.Message{
					Role:    "assistant",
					Content: "done",
				},
			},
		},
	)
	registry := tools.NewRegistry()
	registry.Register(echoTool{})

	result, err := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  registry,
		DefaultModel:                  "requested-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		MemoryRetriever:               &memoryStub{},
	}).Run(context.Background(), "observe actual model")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if got := result.Run.Phases[0].ActualModel; got != "actual-model-alias" {
		t.Fatalf("expected phase actual model alias, got %q", got)
	}
	if got := result.Run.Tools[0].Model; got != "actual-model-alias" {
		t.Fatalf("expected tool outcome to use actual model alias, got %q", got)
	}
}

func TestRunScenarioRoutingThroughPlanningAndExecutionModels(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name:   "planning",
			expect: expectModel("planning-model"),
			resp:   assistantText("plan"),
		},
		scriptedProviderStep{
			name:   "execution",
			expect: expectModel("execution-model"),
			resp:   assistantText("done"),
		},
	)
	router := &phaseRouterStub{
		selections: map[core.Phase]core.RoutingSelection{
			core.PhasePlanning: {
				Phase:     core.PhasePlanning,
				Requested: core.NewModelRef("openrouter", "planning-model"),
				Reason:    "test planning route",
			},
			core.PhaseExecution: {
				Phase:     core.PhaseExecution,
				Requested: core.NewModelRef("openrouter", "execution-model"),
				Reason:    "test execution route",
			},
		},
	}

	result, err := New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  tools.NewRegistry(),
		DefaultModel:                  "default-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		RoutingConfig:                 core.RoutingConfig{Enabled: true},
		Router:                        router,
		MemoryRetriever:               &memoryStub{},
	}).Run(context.Background(), "route models")
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if len(result.Routing) != 2 {
		t.Fatalf("expected planning and execution routing selections, got %#v", result.Routing)
	}
	if result.Routing[0].Requested.Model != "planning-model" || result.Routing[1].Requested.Model != "execution-model" {
		t.Fatalf("unexpected routing selections: %#v", result.Routing)
	}
}

func TestCurationScenarioValidJSON(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name:   "curation",
			expect: allRequestPredicates(expectModel("curation-model"), expectLastMessage("user", "Return JSON")),
			resp: &provider.ChatResponse{
				Model: "actual-curation-model",
				Message: provider.Message{
					Role:    "assistant",
					Content: `{"lessons":[{"body":"Use echo_tool only for echo checks.","scope":"tool","tool_name":"echo_tool","confidence":0.8}]}`,
				},
			},
		},
	)
	agent := curationTestAgent(provider)

	selection, record, lessons, err := agent.runCurationPhase(context.Background(), "curate", core.TaskClassGeneral, "done", nil, nil)
	if err != nil {
		t.Fatalf("runCurationPhase returned unexpected error: %v", err)
	}
	if selection.Requested.Model != "curation-model" {
		t.Fatalf("expected curation selection, got %#v", selection)
	}
	if !record.Success || record.ActualModel != "actual-curation-model" {
		t.Fatalf("expected successful curation phase record with actual model, got %#v", record)
	}
	if len(lessons) != 1 {
		t.Fatalf("expected one lesson, got %#v", lessons)
	}
	if lessons[0].ToolName != "echo_tool" || lessons[0].Phase != core.PhaseCuration || lessons[0].Model != "actual-curation-model" {
		t.Fatalf("unexpected lesson shape: %#v", lessons[0])
	}
}

func TestCurationScenarioInvalidJSON(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "invalid curation",
			resp: &provider.ChatResponse{
				Model: "actual-curation-model",
				Message: provider.Message{
					Role:    "assistant",
					Content: `not json`,
				},
			},
		},
	)
	agent := curationTestAgent(provider)

	_, _, lessons, err := agent.runCurationPhase(context.Background(), "curate", core.TaskClassGeneral, "done", nil, nil)
	if err == nil {
		t.Fatal("expected invalid JSON to return an error")
	}
	if len(lessons) != 0 {
		t.Fatalf("expected no lessons on invalid JSON, got %#v", lessons)
	}
}

func TestCurationScenarioEmptyLessons(t *testing.T) {
	t.Parallel()

	provider := newScriptedProvider(t,
		scriptedProviderStep{
			name: "empty curation",
			resp: &provider.ChatResponse{
				Model: "actual-curation-model",
				Message: provider.Message{
					Role:    "assistant",
					Content: `{"lessons":[{"body":"   ","scope":"global","confidence":0.1}]}`,
				},
			},
		},
	)
	agent := curationTestAgent(provider)

	_, record, lessons, err := agent.runCurationPhase(context.Background(), "curate", core.TaskClassGeneral, "done", nil, nil)
	if err != nil {
		t.Fatalf("runCurationPhase returned unexpected error: %v", err)
	}
	if !record.Success {
		t.Fatalf("expected successful curation phase record, got %#v", record)
	}
	if len(lessons) != 0 {
		t.Fatalf("expected empty/blank lessons to be ignored, got %#v", lessons)
	}
}

func curationTestAgent(provider provider.Provider) *Agent {
	return New(Options{
		Provider:                      provider,
		ProviderName:                  "openrouter",
		ToolRegistry:                  tools.NewRegistry(),
		DefaultModel:                  "default-model",
		MaxIterations:                 3,
		MaxTokens:                     512,
		DisableCompletionVerification: true,
		Router:                        &phaseRouterStub{selections: map[core.Phase]core.RoutingSelection{core.PhaseCuration: {Phase: core.PhaseCuration, Requested: core.NewModelRef("openrouter", "curation-model")}}},
		MemoryRetriever:               &memoryStub{},
	})
}

type phaseRouterStub struct {
	selections map[core.Phase]core.RoutingSelection
}

func (r *phaseRouterStub) Select(_ context.Context, phase core.Phase, _ string, _ core.TaskClass, _ []string) (core.RoutingSelection, error) {
	selection, ok := r.selections[phase]
	if !ok {
		return core.RoutingSelection{}, fmt.Errorf("missing routing selection for phase %s", phase)
	}
	return selection, nil
}
