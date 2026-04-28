package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
)

const (
	verificationSatisfied   = "satisfied"
	verificationUnsatisfied = "unsatisfied"
	verificationUncertain   = "uncertain"
)

// CompletionVerification captures the verifier's compact, persisted summary.
type CompletionVerification struct {
	Status                string
	Reason                string
	MissingActions        []string
	RepairInstruction     string
	RepairTriggered       bool
	MalformedVerifierJSON bool
}

func (v CompletionVerification) missingActionsText() string {
	return strings.Join(v.MissingActions, "\n")
}

func (v CompletionVerification) satisfied() bool {
	return v.Status == verificationSatisfied
}

func (v CompletionVerification) repairPrompt() string {
	var parts []string
	if strings.TrimSpace(v.Reason) != "" {
		parts = append(parts, "Verifier reason: "+strings.TrimSpace(v.Reason))
	}
	if len(v.MissingActions) > 0 {
		parts = append(parts, "Missing actions:\n- "+strings.Join(v.MissingActions, "\n- "))
	}
	if strings.TrimSpace(v.RepairInstruction) != "" {
		parts = append(parts, "Repair instruction:\n"+strings.TrimSpace(v.RepairInstruction))
	}
	if len(parts) == 0 {
		parts = append(parts, "The verifier was uncertain whether the user's request was satisfied. Re-read the request and complete any missing work.")
	}
	return "Completion verification did not pass. Continue the task instead of asking the user to proceed.\n\n" + strings.Join(parts, "\n\n")
}

type incompleteTaskError struct {
	verification CompletionVerification
}

func (e incompleteTaskError) Error() string {
	reason := strings.TrimSpace(e.verification.Reason)
	if reason == "" {
		reason = "completion verification did not pass"
	}
	return "task incomplete after verification repair: " + reason
}

func (a *Agent) verifyCompletion(ctx context.Context, selection core.RoutingSelection, taskClass core.TaskClass, prompt, output string, observed []memory.ObservedToolCall, execErr error) (CompletionVerification, state.PhaseRecord, error) {
	p, err := a.resolveProvider(selection.Requested.Provider)
	if err != nil {
		return CompletionVerification{}, state.PhaseRecord{}, err
	}

	record := state.PhaseRecord{
		Phase:          core.PhaseVerification,
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		ActualModel:    selection.Requested.Model,
		Explanation:    "same execution model verified whether the user request was satisfied",
	}

	req := &provider.ChatRequest{
		Model: selection.Requested.Model,
		Messages: []provider.Message{
			{
				Role:    "system",
				Content: "You are CvkeHarness's completion verifier. Decide whether the assistant's final output satisfies the user's request. Return strict JSON only. Valid status values are satisfied, unsatisfied, and uncertain.",
			},
			{
				Role:    "user",
				Content: verificationPrompt(prompt, output, observed, execErr),
			},
		},
		Temperature: 0,
		MaxTokens:   minInt(a.opts.MaxTokens, 1024),
	}
	dump := a.dumpPrompt(ctx, promptdump.Metadata{
		Phase:     core.PhaseVerification,
		Provider:  selection.Requested.Provider,
		Model:     selection.Requested.Model,
		TaskClass: taskClass,
		Label:     "completion-verification",
	}, req)

	start := time.Now()
	resp, err := p.ChatCompletion(ctx, req)
	a.finishPromptDump(dump, resp, err)
	record.LatencyMs = time.Since(start).Milliseconds()
	if resp != nil {
		record.PromptTokens = resp.Usage.PromptTokens
		record.CompletionTokens = resp.Usage.CompletionTokens
		record.TotalTokens = resp.Usage.TotalTokens
		if cachedTokens, ok := resp.Usage.CachedTokens(); ok {
			record.CachedTokens = cachedTokens
			record.CachedTokensKnown = true
		}
		if strings.TrimSpace(resp.Model) != "" {
			record.ActualModel = resp.Model
		}
	}
	if err != nil {
		return CompletionVerification{}, record, err
	}

	decision, parseErr := parseVerification(resp.Message.Content)
	if parseErr != nil {
		decision = CompletionVerification{
			Status:                verificationUncertain,
			Reason:                "verifier returned malformed JSON: " + parseErr.Error(),
			RepairInstruction:     "Re-read the original request and complete any missing work before giving a final answer.",
			MalformedVerifierJSON: true,
		}
	}
	record.Success = decision.satisfied()
	return decision, record, nil
}

func verificationPrompt(prompt, output string, observed []memory.ObservedToolCall, execErr error) string {
	payload := map[string]any{
		"user_request":           prompt,
		"assistant_final_output": output,
		"execution_error":        errString(execErr),
		"tool_events":            summarizeObservedToolCalls(observed),
		"required_json_shape": map[string]any{
			"status":             "satisfied|unsatisfied|uncertain",
			"reason":             "concise explanation",
			"missing_actions":    []string{"concrete remaining action"},
			"repair_instruction": "what the agent should do next if not satisfied",
		},
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Review this run summary. The assistant must have completed the user's requested actions, not merely reported that more work could be done. If the user asked for a conditional action, checking the condition without performing the required action is unsatisfied.\n\nReturn JSON only.\n\n" + string(data)
}

func summarizeObservedToolCalls(observed []memory.ObservedToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(observed))
	for _, call := range observed {
		item := map[string]any{
			"tool_name": call.ToolName,
			"command":   call.Command,
			"success":   call.Success,
			"result":    truncateForVerification(call.Result, 900),
		}
		if call.PolicyDenied {
			item["policy_denied"] = true
			item["denial_class"] = call.DenialClass
		}
		out = append(out, item)
	}
	return out
}

func parseVerification(raw string) (CompletionVerification, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var parsed struct {
		Status            string   `json:"status"`
		Reason            string   `json:"reason"`
		MissingActions    []string `json:"missing_actions"`
		RepairInstruction string   `json:"repair_instruction"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return CompletionVerification{}, err
	}

	status := strings.ToLower(strings.TrimSpace(parsed.Status))
	switch status {
	case verificationSatisfied, verificationUnsatisfied, verificationUncertain:
	default:
		return CompletionVerification{}, fmt.Errorf("invalid status %q", parsed.Status)
	}

	var missing []string
	for _, action := range parsed.MissingActions {
		if trimmed := strings.TrimSpace(action); trimmed != "" {
			missing = append(missing, trimmed)
		}
	}
	return CompletionVerification{
		Status:            status,
		Reason:            strings.TrimSpace(parsed.Reason),
		MissingActions:    missing,
		RepairInstruction: strings.TrimSpace(parsed.RepairInstruction),
	}, nil
}

func truncateForVerification(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...[truncated]"
}
