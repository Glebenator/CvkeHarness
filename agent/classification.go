package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
)

const classifierTimeout = 5 * time.Second

type classificationContext struct {
	PreviousActionablePrompt string
	PreviousActionableClass  core.TaskClass
	PreviousToolNames        []string
	RepairInstruction        string
}

type taskClassification struct {
	Class          core.TaskClass
	Actionable     bool
	Source         string
	Model          string
	FallbackReason string
}

func (a *Agent) classifyTask(ctx context.Context, prompt string, prior classificationContext) taskClassification {
	deterministic := contextualDeterministicClassification(prompt, prior)
	decision := taskClassification{
		Class:      deterministic,
		Actionable: actionableTaskClass(deterministic),
		Source:     "deterministic",
	}
	if a == nil || a.opts.SafetyMode != tools.SafetyModeLLMJudge || a.opts.ClassifierProvider == nil || strings.TrimSpace(a.opts.SafetyModel) == "" {
		a.emitClassification(ctx, decision)
		return decision
	}

	callCtx, cancel := context.WithTimeout(ctx, classifierTimeout)
	defer cancel()
	req := &provider.ChatRequest{
		Model: a.opts.SafetyModel,
		Messages: []provider.Message{
			{Role: "system", Content: "Classify the user's current task for CvkeHarness. Follow-ups such as again, retry, repeat, retest, and do that again inherit the prior actionable task. Return exactly one JSON object and no prose. task_class must be one of general, inspection, debugging, shell_heavy, policy_sensitive, long_horizon, summarization."},
			{Role: "user", Content: classifierPrompt(prompt, prior)},
		},
		Temperature: 0,
		MaxTokens:   160,
	}
	resp, err := a.opts.ClassifierProvider.ChatCompletion(callCtx, req)
	if err != nil {
		decision.Source = "deterministic_fallback"
		decision.Model = a.opts.SafetyModel
		decision.FallbackReason = err.Error()
		a.emitClassification(ctx, decision)
		return decision
	}
	if resp == nil {
		decision.Source = "deterministic_fallback"
		decision.Model = a.opts.SafetyModel
		decision.FallbackReason = "classifier returned no response"
		a.emitClassification(ctx, decision)
		return decision
	}
	parsed, err := parseTaskClassification(resp.Message.Content)
	if err != nil {
		decision.Source = "deterministic_fallback"
		decision.Model = a.opts.SafetyModel
		decision.FallbackReason = "invalid classifier response: " + err.Error()
		a.emitClassification(ctx, decision)
		return decision
	}
	decision = parsed
	decision.Actionable = decision.Actionable || actionableTaskClass(decision.Class)
	decision.Source = "llm_judge"
	decision.Model = resp.Model
	if strings.TrimSpace(decision.Model) == "" {
		decision.Model = a.opts.SafetyModel
	}
	// Follow-up inheritance is a deterministic conversation contract. The
	// judge improves ambiguous classification but cannot erase that contract.
	if isRepeatFollowUp(prompt) && prior.PreviousActionableClass != "" {
		decision.Class = prior.PreviousActionableClass
		decision.Actionable = true
	}
	a.emitClassification(ctx, decision)
	return decision
}

func contextualDeterministicClassification(prompt string, prior classificationContext) core.TaskClass {
	if isRepeatFollowUp(prompt) && prior.PreviousActionableClass != "" {
		return prior.PreviousActionableClass
	}
	combined := strings.TrimSpace(prompt + "\n" + prior.RepairInstruction)
	return core.ClassifyTask(combined)
}

func isRepeatFollowUp(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	for _, phrase := range []string{"again", "retry", "repeat", "retest", "re-test", "do that again", "test again", "try again"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func actionableTaskClass(class core.TaskClass) bool {
	return class != core.TaskClassGeneral && class != core.TaskClassSummarization
}

func classifierPrompt(prompt string, prior classificationContext) string {
	payload := map[string]any{
		"current_user_prompt":        strings.TrimSpace(prompt),
		"previous_actionable_prompt": truncateForVerification(prior.PreviousActionablePrompt, 600),
		"previous_actionable_class":  prior.PreviousActionableClass,
		"previous_tools_used":        prior.PreviousToolNames,
		"repair_instruction":         truncateForVerification(prior.RepairInstruction, 600),
		"required_json_shape": map[string]any{
			"task_class": "enum listed in system message",
			"actionable": "boolean",
		},
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func parseTaskClassification(raw string) (taskClassification, error) {
	var parsed struct {
		TaskClass  string `json:"task_class"`
		Actionable *bool  `json:"actionable"`
	}
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return taskClassification{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return taskClassification{}, fmt.Errorf("multiple JSON values")
	} else if err != io.EOF {
		return taskClassification{}, fmt.Errorf("trailing classifier content: %w", err)
	}
	if parsed.Actionable == nil {
		return taskClassification{}, fmt.Errorf("missing actionable")
	}
	class := core.TaskClass(strings.TrimSpace(parsed.TaskClass))
	switch class {
	case core.TaskClassGeneral, core.TaskClassInspection, core.TaskClassDebugging, core.TaskClassShellHeavy,
		core.TaskClassPolicySensitive, core.TaskClassLongHorizon, core.TaskClassSummarization:
	default:
		return taskClassification{}, fmt.Errorf("invalid task_class %q", parsed.TaskClass)
	}
	return taskClassification{Class: class, Actionable: *parsed.Actionable}, nil
}

func uniqueObservedToolNames(observed []memory.ObservedToolCall) []string {
	seen := make(map[string]bool)
	var names []string
	for _, call := range observed {
		if call.ToolName != "" && !seen[call.ToolName] {
			seen[call.ToolName] = true
			names = append(names, call.ToolName)
		}
	}
	return names
}

func (a *Agent) emitClassification(ctx context.Context, decision taskClassification) {
	payload, _ := json.Marshal(map[string]any{
		"source":          decision.Source,
		"model":           decision.Model,
		"result":          decision.Class,
		"actionable":      decision.Actionable,
		"fallback_reason": decision.FallbackReason,
	})
	_ = telemetry.Record(ctx, telemetry.Event{Type: telemetry.EventTaskClassified, Payload: payload})
}
