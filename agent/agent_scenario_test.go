package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/core"
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    registry,
		DefaultModel:    "test-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
		RunRecorder:     recorder,
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    registry,
		DefaultModel:    "test-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "test-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "test-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    registry,
		DefaultModel:    "test-model",
		MaxIterations:   2,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    registry,
		DefaultModel:    "requested-model",
		MaxIterations:   3,
		MaxTokens:       512,
		MemoryRetriever: &memoryStub{},
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "default-model",
		MaxIterations:   3,
		MaxTokens:       512,
		RoutingConfig:   core.RoutingConfig{Enabled: true},
		Router:          router,
		MemoryRetriever: &memoryStub{},
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
		Provider:        provider,
		ProviderName:    "openrouter",
		ToolRegistry:    tools.NewRegistry(),
		DefaultModel:    "default-model",
		MaxIterations:   3,
		MaxTokens:       512,
		Router:          &phaseRouterStub{selections: map[core.Phase]core.RoutingSelection{core.PhaseCuration: {Phase: core.PhaseCuration, Requested: core.NewModelRef("openrouter", "curation-model")}}},
		MemoryRetriever: &memoryStub{},
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
