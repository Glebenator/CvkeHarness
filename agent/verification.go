package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/glebenator/cvkeharness/core"
	"github.com/glebenator/cvkeharness/internal/promptdump"
	"github.com/glebenator/cvkeharness/internal/secrets"
	"github.com/glebenator/cvkeharness/internal/telemetry"
	"github.com/glebenator/cvkeharness/memory"
	"github.com/glebenator/cvkeharness/provider"
	"github.com/glebenator/cvkeharness/state"
	"github.com/glebenator/cvkeharness/tools"
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
	RepairAttempts        int
	RepairLimit           int
	CapabilitiesEvaluated bool
	CapabilitiesChanged   bool
	StopReason            tools.VerificationStopReason
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
	return "Completion verification did not pass. Continue the task instead of asking the user to proceed. The verifier identifies missing work but does not authorize any tool action; all normal capability and safety checks still apply.\n\n" + strings.Join(parts, "\n\n")
}

func repairFingerprint(output string, toolNames []string, observedCount int, verification CompletionVerification) string {
	payload := []any{
		strings.TrimSpace(output),
		core.ToolsetKey(toolNames),
		observedCount,
		verification.Status,
		strings.TrimSpace(verification.Reason),
		verification.MissingActions,
		strings.TrimSpace(verification.RepairInstruction),
	}
	data, _ := json.Marshal(payload)
	return hashJSON(data)
}

func emitRepairAttempt(ctx context.Context, attempt int, reason string, capabilitiesChanged, noProgress, capabilityUnavailable bool, toolNames []string) {
	payload, _ := json.Marshal(map[string]any{
		"attempt":                attempt,
		"reason":                 reason,
		"capabilities_changed":   capabilitiesChanged,
		"no_progress":            noProgress,
		"capability_unavailable": capabilityUnavailable,
		"tool_names":             toolNames,
	})
	_ = telemetry.Record(ctx, telemetry.Event{Type: telemetry.EventRepairAttempted, Payload: payload})
}

func emitVerificationActivity(ctx context.Context, verification CompletionVerification, phase tools.VerificationPhase, final bool) {
	activity := tools.VerificationActivity{
		Phase:                 phase,
		Status:                compactVerificationEventText(verification.Status, 32),
		RepairAttempt:         verification.RepairAttempts,
		RepairLimit:           verification.RepairLimit,
		Reason:                compactVerificationEventText(verification.Reason, 240),
		MissingActions:        compactVerificationMissingActions(verification.MissingActions),
		CapabilitiesEvaluated: verification.CapabilitiesEvaluated,
		CapabilitiesChanged:   verification.CapabilitiesChanged,
		StopReason:            verification.StopReason,
		Final:                 final,
	}
	tools.EmitEvent(ctx, tools.Event{Type: tools.EventVerificationActivity, Verification: activity})
}

func compactVerificationMissingActions(actions []string) []string {
	const maxActions = 3
	result := make([]string, 0, minInt(len(actions), maxActions))
	for _, action := range actions {
		if len(result) == maxActions {
			break
		}
		if compact := compactVerificationEventText(action, 160); compact != "" {
			result = append(result, compact)
		}
	}
	return result
}

func compactVerificationEventText(value string, limit int) string {
	value = strings.Join(strings.Fields(secrets.Mask(value)), " ")
	if value == "" || limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func verificationStopReason(noProgress bool, repairAttempts, repairLimit, iteration, iterationLimit int) tools.VerificationStopReason {
	switch {
	case noProgress:
		return tools.VerificationStopNoProgress
	case repairLimit > 0 && repairAttempts >= repairLimit:
		return tools.VerificationStopRepairLimit
	case iteration >= iterationLimit:
		return tools.VerificationStopIterationLimit
	default:
		return tools.VerificationStopIterationLimit
	}
}

func requiredCapabilityUnavailable(verification CompletionVerification, toolNames []string) bool {
	text := strings.ToLower(strings.Join(append(append([]string{}, verification.MissingActions...), verification.RepairInstruction), " "))
	available := make(map[string]bool, len(toolNames))
	for _, name := range toolNames {
		available[name] = true
	}
	if (strings.Contains(text, "shell_execute") || strings.Contains(text, "run a shell") || strings.Contains(text, "execute a command")) && !available["shell_execute"] {
		return true
	}
	for _, name := range []string{"web_search", "web_fetch", "schedule_manage", "system_cron_manage", "memory_record_finding", "memory_remember_target"} {
		if strings.Contains(text, name) && !available[name] {
			return true
		}
	}
	return false
}

type incompleteTaskError struct {
	verification CompletionVerification
}

func (e incompleteTaskError) Error() string {
	reason := strings.TrimSpace(e.verification.Reason)
	if reason == "" {
		reason = "completion verification did not pass"
	}
	stop := ""
	switch e.verification.StopReason {
	case tools.VerificationStopNoProgress:
		stop = "no progress was detected"
	case tools.VerificationStopCapabilityUnavailable:
		stop = "a required capability was unavailable"
	case tools.VerificationStopRepairLimit:
		stop = "the repair limit was reached"
	case tools.VerificationStopIterationLimit:
		stop = "the iteration limit was reached"
	}
	if stop != "" {
		return "task incomplete after verification repair; stopped because " + stop + ": " + reason
	}
	return "task incomplete after verification repair: " + reason
}

func (a *Agent) verifyCompletion(ctx context.Context, selection core.RoutingSelection, taskClass core.TaskClass, prompt, output string, observed []memory.ObservedToolCall, execErr error) (CompletionVerification, state.PhaseRecord, error) {
	explanation := "same execution model verified whether the user request was satisfied"
	if !a.opts.VerifierModel.IsZero() {
		selection.Requested = a.opts.VerifierModel
		explanation = "configured verifier model checked whether the user request was satisfied"
	}
	p, err := a.resolveModelProvider(selection.Requested)
	if err != nil {
		return CompletionVerification{}, state.PhaseRecord{}, err
	}

	record := state.PhaseRecord{
		Connection:     selection.Requested.Connection,
		Phase:          core.PhaseVerification,
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		ActualModel:    selection.Requested.Model,
		Explanation:    selectionExplanation(core.RoutingSelection{Requested: selection.Requested, Reason: explanation}),
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
				Content: verificationPromptWithContext(ctx, prompt, output, observed, execErr),
			},
		},
		Temperature: 0,
		MaxTokens:   minInt(a.opts.MaxTokens, 1024),
	}
	verificationPlan := promptPlan{
		PrefixHash: hashJSON([]any{req.Messages[:1]}),
		PromptHash: hashJSON([]any{req.Messages, req.Tools}),
	}
	emitPromptPlanned(ctx, core.PhaseVerification, 0, selection.Requested.Provider, selection.Requested.Model, verificationPlan, len(req.Messages), selection.Requested.Connection)
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
	payload, _ := json.Marshal(map[string]any{
		"status":             decision.Status,
		"reason":             decision.Reason,
		"missing_actions":    decision.MissingActions,
		"repair_triggered":   decision.RepairTriggered,
		"malformed_response": decision.MalformedVerifierJSON,
	})
	_ = telemetry.Record(telemetry.WithFields(ctx, telemetry.Fields{
		Phase:          string(core.PhaseVerification),
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		ActualModel:    record.ActualModel,
	}), telemetry.Event{
		Type:           telemetry.EventVerificationCompleted,
		Phase:          string(core.PhaseVerification),
		Provider:       selection.Requested.Provider,
		RequestedModel: selection.Requested.Model,
		ActualModel:    record.ActualModel,
		Payload:        payload,
	})
	return decision, record, nil
}

func verificationPrompt(prompt, output string, observed []memory.ObservedToolCall, execErr error) string {
	return verificationPromptWithContext(context.Background(), prompt, output, observed, execErr)
}
func verificationPromptWithContext(ctx context.Context, prompt, output string, observed []memory.ObservedToolCall, execErr error) string {
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
	if request, ok := ctx.Value(chatRequestKey{}).(*chatRequestContext); ok {
		payload["conversation_context"] = request
		if request.PendingRequest != "" {
			payload["user_request"] = request.PendingRequest
		}
		if request.Memory != nil {
			payload["memory_completion_rule"] = "Memory is already handled by the runtime. Evaluate remaining execution only. Never request another memory write or overturn memory_result."
		}
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Review this run summary. The assistant must have completed the user's requested actions, not merely reported that more work could be done. If the user asked for a conditional action, checking the condition without performing the required action is unsatisfied. A successful memory_remember_target receipt with status=saved_and_recallable and readback_verified=true completes a request to remember a server endpoint. That declaration does not require live host verification, operational-memory promotion, or command approval. A memory_record_finding candidate alone does not establish future recall.\n\nReturn JSON only.\n\n" + string(data)
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
		if call.PrerequisiteRejected {
			item["prerequisite_rejected"] = true
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

// Use the latest outcome for a required tool: a later success clears an earlier
// rejection. Ordinary argument errors are not immutable prerequisites.
func requiredPrerequisiteRejected(v CompletionVerification, observed []memory.ObservedToolCall) bool {
	required := strings.ToLower(strings.Join(v.MissingActions, " ") + " " + v.RepairInstruction)
	seen := map[string]bool{}
	for i := len(observed) - 1; i >= 0; i-- {
		call := observed[i]
		if seen[call.ToolName] {
			continue
		}
		seen[call.ToolName] = true
		if call.PrerequisiteRejected && strings.Contains(required, call.ToolName) {
			return true
		}
	}
	return false
}
