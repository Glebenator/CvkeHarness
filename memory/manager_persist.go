package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/telemetry"
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
	targetID := strings.TrimSpace(telemetry.FieldsFromContext(ctx).TargetID)
	if targetID == "" {
		targetID = mem.RuntimeHostID
	} else if targetIndex(mem, targetID) < 0 {
		return fmt.Errorf("cannot persist memory candidate for unknown target %q", targetID)
	}

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
			ID:          findingID(targetID+"|model_candidate", body),
			TargetID:    targetID,
			Environment: targetEnvironment(mem, targetID),
			Intent:      IntentGeneral,
			ToolName:    strings.TrimSpace(lesson.ToolName),
			Status:      state.MemoryStatusCandidate,
			Origin:      "model_suggestion",
			Source:      "memory_record_finding",
			EvidenceRef: "model-authored candidate",
			Trust:       state.MemoryTrustUntrusted,
			Body:        redactSensitiveText(body),
			Confidence:  confidence,
			SeenCount:   1,
			ObservedAt:  now,
			ExpiresAt:   now.Add(candidateTTL),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		finding.EvidenceHash = findingIntegrity(finding)
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
	changed := false
	successfulByTarget := make(map[string][]ObservedToolCall)

	for _, call := range outcome.ToolCalls {
		if isWebResearchTool(call.ToolName) {
			continue
		}
		callTargetID := strings.TrimSpace(call.TargetID)
		if callTargetID != "" && targetIndex(mem, callTargetID) < 0 {
			callTargetID = ""
		}
		if callTargetID == "" {
			callResolution, hasSpecificTarget := m.resolveToolCallTarget(ctx, call)
			if callResolution.Ambiguous {
				continue
			}
			if hasSpecificTarget {
				callTargetID = callResolution.TargetID
			} else {
				callTargetID = resolution.TargetID
			}
		}
		if callTargetID == "" || targetIndex(mem, callTargetID) < 0 {
			continue
		}
		changed = m.applyObservedFacts(&mem, callTargetID, call, now) || changed
		if !call.Success {
			changed = applyPlaybookFailure(&mem, callTargetID, intent, call, now) || changed
			changed = applyCaution(&mem, callTargetID, intent, call, now) || changed
		} else if call.ToolName == "shell_execute" && strings.TrimSpace(call.Command) != "" {
			successfulByTarget[callTargetID] = append(successfulByTarget[callTargetID], call)
		}
	}

	if strings.TrimSpace(outcome.ExecutionError) == "" {
		for callTargetID, successfulCommands := range successfulByTarget {
			changed = applyPlaybook(&mem, callTargetID, intent, successfulCommands, outcome.Task, outcome.VerifiedOutcome, outcome.VerificationEvidence, now) || changed
		}
	}

	if !changed {
		return nil
	}
	return m.writeAllState(ctx, mem, "curate run outcome")
}

func isWebResearchTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "web_search", "web_fetch":
		return true
	default:
		return false
	}
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

func applyPlaybook(mem *fileState, targetID, intent string, calls []ObservedToolCall, task string, verifiedOutcome bool, verificationEvidence string, now time.Time) bool {
	if targetID == "" || containsSensitiveCalls(calls) {
		return false
	}
	verifySteps, actionSteps, successChecks := splitPlaybookSteps(calls)
	if len(actionSteps) == 0 {
		return false
	}
	preconditions := inferredPreconditions(*mem, targetID)
	playbook := state.Playbook{
		ID:            playbookID(targetID, intent, "shell_execute"),
		TargetID:      targetID,
		Environment:   targetEnvironment(*mem, targetID),
		Intent:        intent,
		ToolName:      "shell_execute",
		Status:        state.MemoryStatusCandidate,
		Source:        "verified_run_observation",
		EvidenceRef:   redactSensitiveText(strings.TrimSpace(verificationEvidence)),
		Trust:         state.MemoryTrustUntrusted,
		Title:         playbookTitle(intent, targetName(*mem, targetID)),
		Confidence:    0.65,
		SuccessCount:  1,
		FailureCount:  0,
		ObservedAt:    now,
		LastUsedAt:    now,
		CreatedAt:     now,
		UpdatedAt:     now,
		MatchTerms:    matchTerms(intent, task),
		Preconditions: preconditions,
		VerifySteps:   verifySteps,
		ActionSteps:   actionSteps,
		SuccessChecks: successChecks,
		Notes:         "",
	}
	if verifiedOutcome && len(successChecks) > 0 {
		playbook.LastVerifiedAt = now
		playbook.Confidence = 0.75
	}
	playbook.ExpiresAt = now.Add(candidateTTL)
	playbook.EvidenceHash = playbookIntegrity(playbook)

	changed := false
	for i, existing := range mem.Playbooks {
		if existing.ID != playbook.ID {
			continue
		}
		playbook.CreatedAt = existing.CreatedAt
		playbook.SuccessCount = existing.SuccessCount + 1
		playbook.FailureCount = existing.FailureCount
		playbook.Confidence = minFloat(0.85, maxFloat(existing.Confidence, playbook.Confidence)+0.03)
		if len(playbook.VerifySteps) == 0 {
			playbook.VerifySteps = existing.VerifySteps
		}
		if len(playbook.ActionSteps) == 0 {
			playbook.ActionSteps = existing.ActionSteps
		}
		if len(playbook.SuccessChecks) == 0 {
			playbook.SuccessChecks = existing.SuccessChecks
		}
		playbook.EvidenceHash = playbookIntegrity(playbook)
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
		playbook.EvidenceHash = playbookIntegrity(playbook)
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
		Environment:  targetEnvironment(*mem, targetID),
		Intent:       intent,
		ToolName:     call.ToolName,
		Status:       state.MemoryStatusCandidate,
		Source:       "failed_tool_observation",
		EvidenceRef:  firstNonEmpty(call.DenialClass, "tool failure"),
		Trust:        state.MemoryTrustUntrusted,
		Body:         body,
		Confidence:   0.55,
		FailureCount: 1,
		ObservedAt:   now,
		ExpiresAt:    now.Add(7 * 24 * time.Hour),
		LastSeenAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	caution.EvidenceHash = cautionIntegrity(caution)
	for i, existing := range mem.Cautions {
		if existing.ID != caution.ID {
			continue
		}
		caution.CreatedAt = existing.CreatedAt
		caution.FailureCount = existing.FailureCount + 1
		caution.Confidence = minFloat(0.75, maxFloat(existing.Confidence, 0.55)+0.03)
		caution.EvidenceHash = cautionIntegrity(caution)
		mem.Cautions[i] = caution
		return true
	}
	mem.Cautions = append(mem.Cautions, caution)
	return true
}

func cautionBody(call ObservedToolCall) string {
	switch {
	case call.PolicyDenied:
		return "A command path was denied by policy or approval gates. Review policy before retrying: " + redactSensitiveText(strings.TrimSpace(call.Command))
	case strings.TrimSpace(call.Result) != "":
		return "A recent attempt failed: " + redactSensitiveText(oneSentence(call.Result))
	default:
		return ""
	}
}

func (m *Manager) applyObservedFacts(mem *fileState, targetID string, call ObservedToolCall, now time.Time) bool {
	if targetID == "" || call.ToolName != "shell_execute" || !call.Success || containsSensitiveCommand(call.Command) {
		return false
	}
	environment := targetEnvironment(*mem, targetID)
	facts := extractFacts(call.Command, call.Result, targetID, environment, now)
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
	}
	target.Facts = dedupeFacts(target.Facts)
	target.Hostnames = dedupeStrings(target.Hostnames)
	mem.Targets[idx] = target
	return len(target.Facts) != before || len(facts) > 0
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

func extractFacts(command, result, targetID, environment string, now time.Time) []state.HostFact {
	commandLower := strings.TrimSpace(strings.ToLower(command))
	result = strings.TrimSpace(result)
	var out []state.HostFact
	add := func(key, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		item := state.HostFact{
			HostID:      targetID,
			Environment: firstNonEmpty(environment, state.EnvironmentUnknown),
			Key:         key,
			Value:       value,
			Status:      state.MemoryStatusCandidate,
			Source:      "typed_shell_probe",
			EvidenceRef: redactSensitiveText(command),
			Trust:       state.MemoryTrustUntrusted,
			Confidence:  1,
			ObservedAt:  now,
			VerifiedAt:  now,
			ExpiresAt:   now.Add(candidateTTL),
			UpdatedAt:   now,
		}
		item.EvidenceHash = factIntegrity(item)
		out = append(out, item)
	}

	if exactProbe(commandLower, "hostname") || exactProbe(commandLower, "uname -n") {
		if line := firstOutputLine(result); line != "" {
			add("hostname", strings.ToLower(line))
		}
	}
	if exactProbe(commandLower, "cat /etc/os-release") {
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
	case isToolProbe(commandLower, "apt") || isToolProbe(commandLower, "apt-get"):
		add("package_manager", "apt")
	case isToolProbe(commandLower, "dnf"):
		add("package_manager", "dnf")
	case isToolProbe(commandLower, "yum"):
		add("package_manager", "yum")
	case isToolProbe(commandLower, "apk"):
		add("package_manager", "apk")
	case isToolProbe(commandLower, "brew"):
		add("package_manager", "brew")
	case isToolProbe(commandLower, "pacman"):
		add("package_manager", "pacman")
	}
	if exactProbe(commandLower, "systemctl --version") {
		add("service_manager", "systemd")
	}
	if exactProbe(commandLower, "docker --version") {
		add("container_runtime", "docker")
	}
	return dedupeFacts(out)
}

func exactProbe(command, probe string) bool {
	command = strings.TrimSpace(command)
	probe = strings.TrimSpace(probe)
	if command == probe {
		return true
	}
	fields := strings.Fields(command)
	return len(fields) >= 3 &&
		fields[0] == "ssh" &&
		strings.Join(fields[len(fields)-len(strings.Fields(probe)):], " ") == probe
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
