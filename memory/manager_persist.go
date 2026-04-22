package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

// PersistLessons keeps the ad hoc finding tool working on top of the new model.
func (m *Manager) PersistLessons(ctx context.Context, lessons []Lesson) error {
	if err := m.EnsureFiles(); err != nil {
		return err
	}
	mem, err := m.loadState(ctx)
	if err != nil {
		return err
	}
	m.ensureRuntimeBootstrap(&mem)
	now := m.now()

	changed := false
	for _, lesson := range lessons {
		body := strings.TrimSpace(lesson.Body)
		if body == "" {
			continue
		}
		confidence := lesson.Confidence
		if confidence <= 0 {
			confidence = 0.65
		}
		finding := state.Finding{
			ID:         findingID(mem.RuntimeHostID+"|ad_hoc", body),
			TargetID:   mem.RuntimeHostID,
			Intent:     IntentGeneral,
			ToolName:   strings.TrimSpace(lesson.ToolName),
			Status:     "active",
			Origin:     "ad_hoc",
			Body:       body,
			Confidence: confidence,
			SeenCount:  1,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		mem.Findings, changed = upsertFinding(mem.Findings, finding)
	}
	if !changed {
		return nil
	}
	return m.writeAllState(ctx, mem, "record ad hoc finding")
}

// CurateRunOutcome deterministically promotes proven target knowledge.
func (m *Manager) CurateRunOutcome(ctx context.Context, outcome RunOutcome) error {
	if err := m.EnsureFiles(); err != nil {
		return err
	}
	mem, err := m.loadState(ctx)
	if err != nil {
		return err
	}
	m.ensureRuntimeBootstrap(&mem)

	intent := strings.TrimSpace(outcome.Intent)
	if intent == "" {
		intent = classifyIntent(outcome.Task)
	}

	resolution := outcome.Target
	if resolution.RuntimeHostID == "" || resolution.TargetID == "" {
		resolved, err := m.ResolveTarget(ctx, TargetResolutionInput{Task: outcome.Task})
		if err != nil {
			return err
		}
		if resolution.RuntimeHostID == "" {
			resolution.RuntimeHostID = resolved.RuntimeHostID
		}
		if resolution.TargetID == "" {
			resolution.TargetID = resolved.TargetID
			resolution.TargetKind = resolved.TargetKind
			resolution.PrimaryName = resolved.PrimaryName
		}
	}

	now := m.now()
	targetID := resolution.TargetID
	changed := false

	for _, call := range outcome.ToolCalls {
		callTargetID := targetID
		callResolution, hasSpecificTarget := m.resolveToolCallTarget(ctx, call)
		if hasSpecificTarget {
			callTargetID = callResolution.TargetID
			targetID = callTargetID
			resolution = callResolution
		}
		changed = m.applyObservedFacts(&mem, callTargetID, call, now) || changed
		if call.Success {
			changed = markTargetVerified(&mem, callTargetID, now) || changed
		} else {
			changed = applyPlaybookFailure(&mem, callTargetID, intent, call, now) || changed
			changed = applyCaution(&mem, callTargetID, intent, call, now) || changed
		}
	}

	successfulCommands := successfulShellCommands(outcome.ToolCalls, targetID)
	if len(successfulCommands) > 0 && strings.TrimSpace(outcome.ExecutionError) == "" {
		changed = applyPlaybook(&mem, targetID, intent, successfulCommands, outcome.Task, now) || changed
	}

	if len(successfulCommands) == 0 && strings.TrimSpace(outcome.ExecutionError) == "" && targetID != "" {
		summary := strings.TrimSpace(outcome.Output)
		if summary != "" {
			finding := state.Finding{
				ID:         findingID(targetID+"|run", summary),
				TargetID:   targetID,
				Intent:     intent,
				ToolName:   "",
				Status:     "active",
				Origin:     "run_outcome",
				Body:       oneSentence(summary),
				Confidence: 0.65,
				SeenCount:  1,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			mem.Findings, changed = upsertFinding(mem.Findings, finding)
		}
	}

	if !changed {
		return nil
	}
	return m.writeAllState(ctx, mem, "curate run outcome")
}

func (m *Manager) resolveToolCallTarget(ctx context.Context, call ObservedToolCall) (TargetResolution, bool) {
	if strings.TrimSpace(call.Command) == "" {
		return TargetResolution{}, false
	}
	resolution, err := m.ResolveTarget(ctx, TargetResolutionInput{Command: call.Command})
	if err != nil {
		return TargetResolution{}, false
	}
	if resolution.TargetID == "" || resolution.TargetID == resolution.RuntimeHostID {
		return resolution, false
	}
	return resolution, true
}

func successfulShellCommands(calls []ObservedToolCall, targetID string) []ObservedToolCall {
	var out []ObservedToolCall
	for _, call := range calls {
		if call.ToolName != "shell_execute" || !call.Success || strings.TrimSpace(call.Command) == "" {
			continue
		}
		out = append(out, call)
	}
	return out
}

func applyPlaybook(mem *fileState, targetID, intent string, calls []ObservedToolCall, task string, now time.Time) bool {
	if targetID == "" {
		return false
	}
	verifySteps, actionSteps, successChecks := splitPlaybookSteps(calls)
	preconditions := inferredPreconditions(*mem, targetID)
	playbook := state.Playbook{
		ID:             playbookID(targetID, intent, "shell_execute"),
		TargetID:       targetID,
		Intent:         intent,
		ToolName:       "shell_execute",
		Status:         "active",
		Title:          playbookTitle(intent, targetName(*mem, targetID)),
		Confidence:     0.82,
		SuccessCount:   1,
		FailureCount:   0,
		LastVerifiedAt: now,
		LastUsedAt:     now,
		CreatedAt:      now,
		UpdatedAt:      now,
		MatchTerms:     matchTerms(intent, task),
		Preconditions:  preconditions,
		VerifySteps:    verifySteps,
		ActionSteps:    actionSteps,
		SuccessChecks:  successChecks,
		Notes:          "",
	}

	changed := false
	for i, existing := range mem.Playbooks {
		if existing.ID != playbook.ID {
			continue
		}
		playbook.CreatedAt = existing.CreatedAt
		playbook.SuccessCount = existing.SuccessCount + 1
		playbook.FailureCount = existing.FailureCount
		playbook.Confidence = minFloat(0.97, maxFloat(existing.Confidence, 0.82)+0.05)
		if len(playbook.VerifySteps) == 0 {
			playbook.VerifySteps = existing.VerifySteps
		}
		if len(playbook.ActionSteps) == 0 {
			playbook.ActionSteps = existing.ActionSteps
		}
		if len(playbook.SuccessChecks) == 0 {
			playbook.SuccessChecks = existing.SuccessChecks
		}
		mem.Playbooks[i] = playbook
		return true
	}
	mem.Playbooks = append(mem.Playbooks, playbook)
	changed = true
	return changed
}

func applyPlaybookFailure(mem *fileState, targetID, intent string, call ObservedToolCall, now time.Time) bool {
	if call.ToolName != "shell_execute" || targetID == "" {
		return false
	}
	id := playbookID(targetID, intent, call.ToolName)
	for i, playbook := range mem.Playbooks {
		if playbook.ID != id {
			continue
		}
		playbook.FailureCount++
		playbook.Confidence = maxFloat(0.35, playbook.Confidence-0.15)
		playbook.LastUsedAt = now
		playbook.UpdatedAt = now
		mem.Playbooks[i] = playbook
		return true
	}
	return false
}

func applyCaution(mem *fileState, targetID, intent string, call ObservedToolCall, now time.Time) bool {
	if targetID == "" {
		return false
	}
	body := cautionBody(call)
	if body == "" {
		return false
	}
	caution := state.Caution{
		ID:           cautionID(targetID, intent, call.ToolName),
		TargetID:     targetID,
		Intent:       intent,
		ToolName:     call.ToolName,
		Status:       "active",
		Body:         body,
		Confidence:   0.78,
		FailureCount: 1,
		LastSeenAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	for i, existing := range mem.Cautions {
		if existing.ID != caution.ID {
			continue
		}
		caution.CreatedAt = existing.CreatedAt
		caution.FailureCount = existing.FailureCount + 1
		caution.Confidence = minFloat(0.95, maxFloat(existing.Confidence, 0.78)+0.03)
		mem.Cautions[i] = caution
		return true
	}
	mem.Cautions = append(mem.Cautions, caution)
	return true
}

func cautionBody(call ObservedToolCall) string {
	switch {
	case call.PolicyDenied:
		return "This command path was denied by policy or approval gates; verify approval before retrying. Last command: " + strings.TrimSpace(call.Command)
	case strings.TrimSpace(call.Result) != "":
		return "A recent attempt failed: " + oneSentence(call.Result)
	default:
		return ""
	}
}

func (m *Manager) applyObservedFacts(mem *fileState, targetID string, call ObservedToolCall, now time.Time) bool {
	if targetID == "" || call.ToolName != "shell_execute" || !call.Success {
		return false
	}
	facts := extractFacts(call.Command, call.Result, targetID, now)
	if len(facts) == 0 {
		return false
	}
	idx := targetIndex(*mem, targetID)
	if idx < 0 {
		return false
	}
	target := mem.Targets[idx]
	before := len(target.Facts)
	for _, fact := range facts {
		target.Facts = upsertFact(target.Facts, fact)
		switch fact.Key {
		case "hostname":
			target.Hostnames = append(target.Hostnames, strings.ToLower(fact.Value))
			if target.Target.PrimaryName == "" {
				target.Target.PrimaryName = fact.Value
			}
		}
	}
	target.Facts = dedupeFacts(target.Facts)
	target.Hostnames = dedupeStrings(target.Hostnames)
	mem.Targets[idx] = target
	if targetID == mem.RuntimeHostID {
		mem.RuntimeHostFacts = mergeFactLists(mem.RuntimeHostFacts, facts)
	}
	return len(target.Facts) != before || len(facts) > 0
}

func markTargetVerified(mem *fileState, targetID string, now time.Time) bool {
	idx := targetIndex(*mem, targetID)
	if idx < 0 {
		return false
	}
	target := mem.Targets[idx]
	changed := false
	if target.Target.Status != "active" {
		target.Target.Status = "active"
		changed = true
	}
	if target.Target.Confidence < 0.85 {
		target.Target.Confidence = 0.85
		changed = true
	}
	if target.Target.LastSeenAt.Before(now) {
		target.Target.LastSeenAt = now
		changed = true
	}
	mem.Targets[idx] = target
	return changed
}

func targetIndex(mem fileState, targetID string) int {
	for i, target := range mem.Targets {
		if target.Target.ID == targetID {
			return i
		}
	}
	return -1
}

func splitPlaybookSteps(calls []ObservedToolCall) ([]string, []string, []string) {
	if len(calls) == 0 {
		return nil, nil, nil
	}
	var verify, action, checks []string
	for i, call := range calls {
		command := strings.TrimSpace(call.Command)
		if command == "" {
			continue
		}
		switch {
		case looksLikeVerifyCommand(command) && len(action) == 0:
			verify = append(verify, command)
		case i == len(calls)-1 && looksLikeVerifyCommand(command) && len(calls) > 1:
			checks = append(checks, command)
		default:
			action = append(action, command)
		}
	}
	if len(action) == 0 {
		action = append(action, strings.TrimSpace(calls[len(calls)-1].Command))
	}
	return dedupeStrings(verify), dedupeStrings(action), dedupeStrings(checks)
}

func looksLikeVerifyCommand(command string) bool {
	lower := strings.ToLower(command)
	verifyTokens := []string{
		"status",
		"is-active",
		"journalctl",
		"hostname",
		"uname",
		"docker ps",
		"go test",
		"curl ",
	}
	for _, token := range verifyTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func inferredPreconditions(mem fileState, targetID string) []string {
	facts := factsForTarget(mem, targetID)
	var out []string
	for _, fact := range facts {
		switch fact.Key {
		case "package_manager", "service_manager", "container_runtime", "os_distribution":
			out = append(out, fact.Key+"="+fact.Value)
		}
	}
	return dedupeStrings(out)
}

func matchTerms(intent, task string) []string {
	items := []string{intent}
	for _, token := range strings.Fields(strings.ToLower(task)) {
		token = strings.Trim(token, "`'\",.:;()[]{}")
		if len(token) < 4 {
			continue
		}
		items = append(items, token)
		if len(items) >= 6 {
			break
		}
	}
	return dedupeStrings(items)
}

func playbookTitle(intent, target string) string {
	switch intent {
	case IntentInspectLogs:
		return "Inspect Logs On " + target
	case IntentInspectServiceStatus:
		return "Inspect Service Status On " + target
	case IntentRestartService:
		return "Restart Service On " + target
	case IntentInstallDependency:
		return "Install Dependency On " + target
	case IntentDockerRecovery:
		return "Docker Recovery On " + target
	case IntentPortConflict:
		return "Port Conflict Recovery On " + target
	case IntentNetworkDebug:
		return "Network Debug On " + target
	case IntentBuildFix:
		return "Build Fix On " + target
	case IntentTestFix:
		return "Test Fix On " + target
	case IntentConfigEdit:
		return "Config Edit On " + target
	case IntentSSHConnectivity:
		return "SSH Connectivity On " + target
	default:
		return "General Procedure On " + target
	}
}

func extractFacts(command, result, targetID string, now time.Time) []state.HostFact {
	commandLower := strings.ToLower(command)
	result = strings.TrimSpace(result)
	var out []state.HostFact
	add := func(key, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		out = append(out, state.HostFact{
			HostID:     targetID,
			Key:        key,
			Value:      value,
			Confidence: 1,
			VerifiedAt: now,
			UpdatedAt:  now,
		})
	}

	if strings.Contains(commandLower, "hostname") || strings.Contains(commandLower, "uname -n") {
		if line := firstOutputLine(result); line != "" {
			add("hostname", strings.ToLower(line))
		}
	}
	if strings.Contains(result, "PRETTY_NAME=") || strings.Contains(result, "\nID=") || strings.HasPrefix(result, "ID=") {
		for _, raw := range strings.Split(result, "\n") {
			line := strings.TrimSpace(raw)
			switch {
			case strings.HasPrefix(line, "ID="):
				add("os_distribution", strings.Trim(line[3:], `"`))
			case strings.HasPrefix(line, "PRETTY_NAME="):
				add("os_pretty_name", strings.Trim(line[len("PRETTY_NAME="):], `"`))
			}
		}
	}
	switch {
	case strings.Contains(commandLower, "apt-get") || strings.Contains(commandLower, " apt "):
		add("package_manager", "apt")
	case strings.Contains(commandLower, "dnf"):
		add("package_manager", "dnf")
	case strings.Contains(commandLower, "yum"):
		add("package_manager", "yum")
	case strings.Contains(commandLower, "apk"):
		add("package_manager", "apk")
	case strings.Contains(commandLower, "brew"):
		add("package_manager", "brew")
	case strings.Contains(commandLower, "pacman"):
		add("package_manager", "pacman")
	}
	if strings.Contains(commandLower, "systemctl") {
		add("service_manager", "systemd")
	}
	if strings.Contains(commandLower, "docker") {
		add("container_runtime", "docker")
	}
	return dedupeFacts(out)
}

func firstOutputLine(result string) string {
	for _, raw := range strings.Split(result, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" {
			return line
		}
	}
	return ""
}

func oneSentence(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexAny(text, "\n"); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 220 {
		text = strings.TrimSpace(text[:220])
	}
	return text
}

func upsertFinding(existing []state.Finding, finding state.Finding) ([]state.Finding, bool) {
	for i, item := range existing {
		if item.ID != finding.ID {
			continue
		}
		finding.CreatedAt = item.CreatedAt
		finding.SeenCount = item.SeenCount + 1
		finding.Confidence = maxFloat(item.Confidence, finding.Confidence)
		existing[i] = finding
		return existing, true
	}
	return append(existing, finding), true
}

func playbookID(targetID, intent, toolName string) string {
	return stableID("playbook", targetID, intent, toolName)
}

func cautionID(targetID, intent, toolName string) string {
	return stableID("caution", targetID, intent, toolName)
}

func findingID(seed, body string) string {
	return stableID("finding", seed, normalize(body))
}

func stableID(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func normalize(body string) string {
	body = strings.ToLower(strings.TrimSpace(body))
	body = strings.NewReplacer(",", "", ".", "", ":", "", ";", "", "`", "", "\"", "", "'", "").Replace(body)
	body = strings.Join(strings.Fields(body), " ")
	return body
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
