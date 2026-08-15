package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

type classifierProviderStub struct {
	content string
	err     error
	req     *provider.ChatRequest
}

func (p *classifierProviderStub) ChatCompletion(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.req = req
	if p.err != nil {
		return nil, p.err
	}
	return &provider.ChatResponse{Model: "actual-safety", Message: provider.Message{Role: "assistant", Content: p.content}}, nil
}

func TestJudgeTaskClassifierSuccess(t *testing.T) {
	judge := &classifierProviderStub{content: `{"task_class":"shell_heavy","actionable":true}`}
	a := New(Options{SafetyMode: tools.SafetyModeLLMJudge, SafetyModel: "safety-model", ClassifierProvider: judge})
	got := a.classifyTask(context.Background(), "do it", classificationContext{PreviousActionableClass: core.TaskClassInspection})
	if got.Class != core.TaskClassShellHeavy || got.Source != "llm_judge" || got.Model != "actual-safety" {
		t.Fatalf("unexpected judge classification: %#v", got)
	}
	if judge.req == nil || judge.req.Model != "safety-model" || judge.req.MaxTokens > 160 {
		t.Fatalf("expected short configured-safety-model call, got %#v", judge.req)
	}
}

func TestJudgeTaskClassifierFailureAndMalformedFallBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		err     error
	}{
		{name: "provider failure", err: fmt.Errorf("offline")},
		{name: "malformed", content: `{"task_class":"invented","actionable":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			judge := &classifierProviderStub{content: tc.content, err: tc.err}
			a := New(Options{SafetyMode: tools.SafetyModeLLMJudge, SafetyModel: "safety-model", ClassifierProvider: judge})
			got := a.classifyTask(context.Background(), "inspect status", classificationContext{})
			if got.Class != core.TaskClassInspection || got.Source != "deterministic_fallback" || got.FallbackReason == "" {
				t.Fatalf("expected deterministic fallback, got %#v", got)
			}
		})
	}
}

func TestDeterministicRepeatInheritsPreviousActionableClass(t *testing.T) {
	a := New(Options{})
	got := a.classifyTask(context.Background(), "please test again", classificationContext{
		PreviousActionablePrompt: "what is my internet speed",
		PreviousActionableClass:  core.TaskClassInspection,
		PreviousToolNames:        []string{"shell_execute"},
	})
	if got.Class != core.TaskClassInspection || !got.Actionable {
		t.Fatalf("expected contextual follow-up inheritance, got %#v", got)
	}
}
