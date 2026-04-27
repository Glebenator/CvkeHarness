package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/coolcake/cvkeharness/provider"
)

type scriptedProvider struct {
	t        *testing.T
	mu       sync.Mutex
	steps    []scriptedProviderStep
	requests []*provider.ChatRequest
}

type scriptedProviderStep struct {
	name   string
	expect func(*provider.ChatRequest) error
	resp   *provider.ChatResponse
	err    error
}

func newScriptedProvider(t *testing.T, steps ...scriptedProviderStep) *scriptedProvider {
	t.Helper()
	return &scriptedProvider{
		t:     t,
		steps: steps,
	}
}

func (p *scriptedProvider) ChatCompletion(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	stepIndex := len(p.requests)
	p.requests = append(p.requests, cloneChatRequest(req))
	if stepIndex >= len(p.steps) {
		p.t.Fatalf("unexpected provider request %d: model=%q messages=%d tools=%d", stepIndex+1, req.Model, len(req.Messages), len(req.Tools))
	}

	step := p.steps[stepIndex]
	if step.expect != nil {
		if err := step.expect(req); err != nil {
			p.t.Fatalf("scripted provider step %d %q failed: %v", stepIndex+1, step.name, err)
		}
	}
	return step.resp, step.err
}

func (p *scriptedProvider) Requests() []*provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]*provider.ChatRequest, len(p.requests))
	copy(out, p.requests)
	return out
}

func (p *scriptedProvider) AssertComplete(t *testing.T) {
	t.Helper()
	if got := len(p.Requests()); got != len(p.steps) {
		t.Fatalf("expected %d provider requests, got %d", len(p.steps), got)
	}
}

func expectModel(model string) func(*provider.ChatRequest) error {
	return func(req *provider.ChatRequest) error {
		if req.Model != model {
			return fmt.Errorf("expected model %q, got %q", model, req.Model)
		}
		return nil
	}
}

func expectLastMessage(role string, contains string) func(*provider.ChatRequest) error {
	return func(req *provider.ChatRequest) error {
		if len(req.Messages) == 0 {
			return fmt.Errorf("expected at least one message")
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != role {
			return fmt.Errorf("expected last message role %q, got %q", role, last.Role)
		}
		if contains != "" && !strings.Contains(last.Content, contains) {
			return fmt.Errorf("expected last %s message to contain %q, got %q", role, contains, last.Content)
		}
		return nil
	}
}

func expectToolNames(names ...string) func(*provider.ChatRequest) error {
	return func(req *provider.ChatRequest) error {
		seen := make(map[string]bool, len(req.Tools))
		for _, tool := range req.Tools {
			seen[tool.Function.Name] = true
		}
		for _, name := range names {
			if !seen[name] {
				return fmt.Errorf("expected tool %q to be present; saw %#v", name, seen)
			}
		}
		return nil
	}
}

func allRequestPredicates(predicates ...func(*provider.ChatRequest) error) func(*provider.ChatRequest) error {
	return func(req *provider.ChatRequest) error {
		for _, predicate := range predicates {
			if predicate == nil {
				continue
			}
			if err := predicate(req); err != nil {
				return err
			}
		}
		return nil
	}
}

func assistantText(content string) *provider.ChatResponse {
	return &provider.ChatResponse{
		Model: "test-model",
		Message: provider.Message{
			Role:    "assistant",
			Content: content,
		},
	}
}

func assistantToolCall(id, name, args string) *provider.ChatResponse {
	return &provider.ChatResponse{
		Model: "test-model",
		Message: provider.Message{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID:   id,
				Type: "function",
				Function: provider.ToolFunction{
					Name:      name,
					Arguments: args,
				},
			}},
		},
	}
}

func verifierJSON(status, reason string, missing []string, repair string) *provider.ChatResponse {
	payload := map[string]any{
		"status":             status,
		"reason":             reason,
		"missing_actions":    missing,
		"repair_instruction": repair,
	}
	data, _ := json.Marshal(payload)
	return assistantText(string(data))
}

func cloneChatRequest(req *provider.ChatRequest) *provider.ChatRequest {
	cloned := *req
	cloned.Messages = append([]provider.Message(nil), req.Messages...)
	cloned.Tools = append([]provider.ToolDef(nil), req.Tools...)
	return &cloned
}
