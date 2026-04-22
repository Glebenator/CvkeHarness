package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

var userHostPattern = regexp.MustCompile(`\b[a-zA-Z0-9._-]+@[a-zA-Z0-9._:-]+\b`)

// Retrieve loads the current compact operational memory brief for a phase.
func (m *Manager) Retrieve(ctx context.Context, input core.RetrievalContext) (RetrievalResult, error) {
	return m.RetrievePlan(ctx, input)
}

// RetrievePlan returns the prompt-ready target-aware retrieval brief.
func (m *Manager) RetrievePlan(ctx context.Context, input core.RetrievalContext) (RetrievalResult, error) {
	if err := m.EnsureFiles(); err != nil {
		return RetrievalResult{}, err
	}

	operatorBytes, err := os.ReadFile(m.managedPath(OperatorFile))
	if err != nil {
		return RetrievalResult{}, err
	}
	soulBytes, err := os.ReadFile(m.managedPath(SoulFile))
	if err != nil {
		return RetrievalResult{}, err
	}

	resolution := TargetResolution{
		RuntimeHostID: strings.TrimSpace(input.RuntimeHostID),
		TargetID:      strings.TrimSpace(input.TargetID),
		TargetKind:    strings.TrimSpace(input.TargetKind),
	}
	if resolution.RuntimeHostID == "" || resolution.TargetID == "" {
		resolved, err := m.ResolveTarget(ctx, TargetResolutionInput{Task: input.Task})
		if err != nil {
			return RetrievalResult{}, err
		}
		if resolution.RuntimeHostID == "" {
			resolution.RuntimeHostID = resolved.RuntimeHostID
		}
		if resolution.TargetID == "" {
			resolution.TargetID = resolved.TargetID
		}
		if resolution.TargetKind == "" {
			resolution.TargetKind = resolved.TargetKind
		}
		if resolution.PrimaryName == "" {
			resolution.PrimaryName = resolved.PrimaryName
		}
	}

	mem, err := m.loadState(ctx)
	if err != nil {
		return RetrievalResult{}, err
	}

	intent := strings.TrimSpace(input.Intent)
	if intent == "" {
		intent = classifyIntent(input.Task)
	}
	primaryTool := primaryToolName(input)

	playbook, strength := selectPlaybook(mem, resolution.TargetID, intent, primaryTool)
	caution := selectCaution(mem, resolution.TargetID, intent, primaryTool)
	finding := selectFinding(mem, resolution.TargetID, intent, primaryTool)

	result := RetrievalResult{
		BuiltInRules:       builtInRules(),
		Operator:           formatOperatorContext(m.dir, string(operatorBytes)),
		Soul:               strings.TrimSpace(string(soulBytes)),
		RuntimeHostSummary: renderRuntimeHostSummary(mem, resolution.RuntimeHostID),
		TargetSummary:      renderTargetSummary(mem, resolution),
		CautionBrief:       renderCautionBrief(mem, caution),
	}

	if playbook != nil {
		result.PlaybookBrief = renderPlaybookBrief(mem, *playbook)
	}
	if strength < 3 {
		result.FallbackBrief = renderFindingBrief(mem, finding)
	}
	return result, nil
}

// LoadRuntimeHostProfile returns the runtime host target row plus verified facts.
func (m *Manager) LoadRuntimeHostProfile(ctx context.Context) (state.Target, []state.HostFact, error) {
	if err := m.EnsureFiles(); err != nil {
		return state.Target{}, nil, err
	}
	mem, err := m.loadState(ctx)
	if err != nil {
		return state.Target{}, nil, err
	}
	for _, target := range mem.Targets {
		if target.Target.ID == mem.RuntimeHostID {
			return target.Target, mergeFactLists(target.Facts, mem.RuntimeHostFacts), nil
		}
	}
	return state.Target{}, nil, fmt.Errorf("runtime host profile is missing")
}

// LoadTargetProfile returns one target profile by stable identifier.
func (m *Manager) LoadTargetProfile(ctx context.Context, targetID string) (state.Target, []state.HostFact, error) {
	if err := m.EnsureFiles(); err != nil {
		return state.Target{}, nil, err
	}
	mem, err := m.loadState(ctx)
	if err != nil {
		return state.Target{}, nil, err
	}
	for _, target := range mem.Targets {
		if target.Target.ID == targetID {
			return target.Target, target.Facts, nil
		}
	}
	return state.Target{}, nil, fmt.Errorf("target %q was not found", targetID)
}

// ResolveTarget finds or creates a stable target record from prompt or command hints.
func (m *Manager) ResolveTarget(ctx context.Context, input TargetResolutionInput) (TargetResolution, error) {
	if err := m.EnsureFiles(); err != nil {
		return TargetResolution{}, err
	}
	mem, err := m.loadState(ctx)
	if err != nil {
		return TargetResolution{}, err
	}

	resolution := TargetResolution{
		RuntimeHostID: mem.RuntimeHostID,
		TargetID:      mem.RuntimeHostID,
		TargetKind:    TargetKindRuntime,
		PrimaryName:   runtimePrimaryName(mem),
	}

	hint := firstTargetHint(input.Command)
	if hint == nil {
		hint = firstTargetHint(input.Task)
	}
	if hint == nil {
		return resolution, nil
	}

	targetIdx := findTargetByHint(mem, *hint)
	now := m.now()
	changed := false
	if targetIdx < 0 {
		record := targetRecord{
			Target: state.Target{
				ID:          targetIDFromHint(*hint),
				Kind:        hint.Kind,
				PrimaryName: hint.Host,
				Transport:   transportForTargetKind(hint.Kind),
				Confidence:  0.6,
				Status:      "provisional",
				FirstSeenAt: now,
				LastSeenAt:  now,
			},
		}
		applyHintToTarget(&record, *hint)
		mem.Targets = append(mem.Targets, record)
		targetIdx = len(mem.Targets) - 1
		changed = true
	} else {
		record := mem.Targets[targetIdx]
		if record.Target.LastSeenAt.Before(now) {
			record.Target.LastSeenAt = now
			changed = true
		}
		beforeAliases := fmt.Sprintf("%v|%v|%v", record.Aliases, record.Hostnames, record.IPs)
		applyHintToTarget(&record, *hint)
		afterAliases := fmt.Sprintf("%v|%v|%v", record.Aliases, record.Hostnames, record.IPs)
		if beforeAliases != afterAliases {
			changed = true
		}
		mem.Targets[targetIdx] = record
	}

	target := mem.Targets[targetIdx].Target
	resolution.TargetID = target.ID
	resolution.TargetKind = target.Kind
	resolution.PrimaryName = target.PrimaryName

	if changed {
		if err := m.writeAllState(ctx, mem, "resolve target identity"); err != nil {
			return TargetResolution{}, err
		}
	}
	return resolution, nil
}

type playbookCandidate struct {
	Playbook state.Playbook
	Score    float64
	Strength int
}

func selectPlaybook(mem fileState, targetID, intent, toolName string) (*state.Playbook, int) {
	var candidates []playbookCandidate
	now := time.Now().UTC()
	for _, playbook := range mem.Playbooks {
		if playbook.TargetID != targetID || playbook.Status == "disabled" {
			continue
		}
		strength := 0
		switch {
		case playbook.Intent == intent && playbook.ToolName != "" && playbook.ToolName == toolName && toolName != "":
			strength = 4
		case playbook.Intent == intent && playbook.Intent != "":
			strength = 3
		case playbook.ToolName != "" && playbook.ToolName == toolName && toolName != "":
			strength = 2
		default:
			continue
		}
		score := float64(strength)*10 + playbook.Confidence*5 + float64(playbook.SuccessCount-playbook.FailureCount)
		score += freshnessScore(now, playbook.LastVerifiedAt)
		candidates = append(candidates, playbookCandidate{
			Playbook: playbook,
			Score:    score,
			Strength: strength,
		})
	}
	if len(candidates) == 0 {
		return nil, 0
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Playbook.UpdatedAt.After(candidates[j].Playbook.UpdatedAt)
		}
		return candidates[i].Score > candidates[j].Score
	})
	return &candidates[0].Playbook, candidates[0].Strength
}

func selectCaution(mem fileState, targetID, intent, toolName string) *state.Caution {
	type cautionCandidate struct {
		Caution state.Caution
		Score   float64
	}
	var candidates []cautionCandidate
	for _, caution := range mem.Cautions {
		if caution.TargetID != targetID || caution.Status == "inactive" {
			continue
		}
		score := caution.Confidence + float64(caution.FailureCount)
		switch {
		case caution.Intent == intent && caution.ToolName == toolName && toolName != "":
			score += 4
		case caution.Intent == intent && caution.Intent != "":
			score += 3
		case caution.ToolName == toolName && caution.ToolName != "" && toolName != "":
			score += 2
		default:
			score += 1
		}
		candidates = append(candidates, cautionCandidate{Caution: caution, Score: score})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	return &candidates[0].Caution
}

func selectFinding(mem fileState, targetID, intent, toolName string) *state.Finding {
	type findingCandidate struct {
		Finding state.Finding
		Score   float64
	}
	var candidates []findingCandidate
	for _, finding := range mem.Findings {
		if finding.Status == "inactive" {
			continue
		}
		score := finding.Confidence + float64(finding.SeenCount)
		if finding.TargetID == targetID {
			score += 4
		} else if finding.TargetID == "unknown" {
			score += 1
		} else {
			continue
		}
		if finding.Intent == intent && finding.Intent != "" {
			score += 2
		}
		if finding.ToolName != "" && finding.ToolName == toolName && toolName != "" {
			score += 1.5
		}
		candidates = append(candidates, findingCandidate{Finding: finding, Score: score})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	return &candidates[0].Finding
}

func builtInRules() string {
	return `You are CvkeHarness.
Keep the runtime rules compact and invariant.
Distinguish the runtime host from the active target system.
Use target-aware memory conservatively: prefer one verified playbook over many weak hints.
If required tooling is missing, confirm the missing dependency, ask before installing or mutating the system, and after approval perform the install instead of only handing the user manual steps.`
}

func formatOperatorContext(dir, operator string) string {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return ""
	}
	parts := []string{
		"Runtime file locations:",
		"- Memory directory: " + dir,
		"- operator.md: " + filepath.Join(dir, OperatorFile),
		"- soul.md: " + filepath.Join(dir, SoulFile),
		"- targets.md: " + filepath.Join(dir, TargetsFile),
		"- host.md: " + filepath.Join(dir, HostFile),
		"- playbooks.md: " + filepath.Join(dir, PlaybooksFile),
		"- findings.md: " + filepath.Join(dir, FindingsFile),
		"- cautions.md: " + filepath.Join(dir, CautionsFile),
		"",
		operator,
	}
	return strings.Join(parts, "\n")
}

func renderRuntimeHostSummary(mem fileState, runtimeHostID string) string {
	var lines []string
	lines = append(lines, "Runtime host summary:")
	lines = append(lines, "- id: "+runtimeHostID)
	lines = append(lines, "- name: "+runtimePrimaryName(mem))
	for _, fact := range prioritizedFacts(mergeFactLists(mem.RuntimeHostFacts, factsForTarget(mem, runtimeHostID)), 3) {
		lines = append(lines, fmt.Sprintf("- %s: %s", fact.Key, fact.Value))
	}
	return clampRenderedText(strings.Join(lines, "\n"), 6, 420)
}

func renderTargetSummary(mem fileState, resolution TargetResolution) string {
	if resolution.TargetID == "" || resolution.TargetID == resolution.RuntimeHostID {
		return ""
	}
	for _, target := range mem.Targets {
		if target.Target.ID != resolution.TargetID {
			continue
		}
		var lines []string
		lines = append(lines, "Target summary:")
		lines = append(lines, "- id: "+target.Target.ID)
		lines = append(lines, "- kind: "+target.Target.Kind)
		lines = append(lines, "- name: "+firstNonEmpty(target.Target.PrimaryName, resolution.PrimaryName))
		if len(target.Aliases) > 0 {
			lines = append(lines, "- aliases: "+strings.Join(target.Aliases, ", "))
		}
		for _, fact := range prioritizedFacts(target.Facts, 2) {
			lines = append(lines, fmt.Sprintf("- %s: %s", fact.Key, fact.Value))
		}
		return clampRenderedText(strings.Join(lines, "\n"), 6, 420)
	}
	return ""
}

func renderPlaybookBrief(mem fileState, playbook state.Playbook) string {
	targetName := targetName(mem, playbook.TargetID)
	mode := "verify-first"
	if playbookDirectUseAllowed(playbook) {
		mode = "direct-use allowed"
	}
	var lines []string
	lines = append(lines, "Primary playbook:")
	lines = append(lines, "- title: "+playbook.Title)
	lines = append(lines, fmt.Sprintf("- applicability: target=%s intent=%s tool=%s", targetName, playbook.Intent, firstNonEmpty(playbook.ToolName, "shell_execute")))
	lines = append(lines, "- freshness: "+freshnessLabel(playbook.LastVerifiedAt)+"; mode="+mode)
	if len(playbook.VerifySteps) > 0 {
		lines = append(lines, "Verify:")
		for _, step := range playbook.VerifySteps {
			lines = append(lines, "- "+step)
		}
	}
	if len(playbook.ActionSteps) > 0 {
		lines = append(lines, "Action:")
		for _, step := range playbook.ActionSteps {
			lines = append(lines, "- "+step)
		}
	}
	if len(playbook.SuccessChecks) > 0 {
		lines = append(lines, "Success checks:")
		for _, step := range playbook.SuccessChecks {
			lines = append(lines, "- "+step)
		}
	}
	return clampRenderedText(strings.Join(lines, "\n"), 16, 900)
}

func renderCautionBrief(mem fileState, caution *state.Caution) string {
	if caution == nil {
		return ""
	}
	text := fmt.Sprintf("Caution: target %s has a known risk for %s. %s",
		targetName(mem, caution.TargetID),
		firstNonEmpty(caution.Intent, "general work"),
		strings.TrimSpace(caution.Body),
	)
	return clampRenderedText(text, 2, 220)
}

func renderFindingBrief(mem fileState, finding *state.Finding) string {
	if finding == nil {
		return ""
	}
	text := fmt.Sprintf("Fallback finding for %s: %s", targetName(mem, finding.TargetID), strings.TrimSpace(finding.Body))
	return clampRenderedText(text, 2, 220)
}

func factsForTarget(mem fileState, targetID string) []state.HostFact {
	for _, target := range mem.Targets {
		if target.Target.ID == targetID {
			return target.Facts
		}
	}
	return nil
}

func prioritizedFacts(facts []state.HostFact, max int) []state.HostFact {
	facts = dedupeFacts(facts)
	sort.SliceStable(facts, func(i, j int) bool {
		return factPriority(facts[i].Key) < factPriority(facts[j].Key)
	})
	if len(facts) > max {
		facts = facts[:max]
	}
	return facts
}

func factPriority(key string) int {
	switch key {
	case "hostname":
		return 0
	case "os_distribution":
		return 1
	case "package_manager":
		return 2
	case "service_manager":
		return 3
	case "container_runtime":
		return 4
	default:
		return 10
	}
}

func playbookDirectUseAllowed(playbook state.Playbook) bool {
	if freshnessLabel(playbook.LastVerifiedAt) != "fresh" {
		return false
	}
	if playbook.Confidence < 0.82 {
		return false
	}
	return playbook.SuccessCount >= 1 && playbook.FailureCount < playbook.SuccessCount
}

func freshnessLabel(ts time.Time) string {
	if ts.IsZero() {
		return "cold"
	}
	age := time.Since(ts)
	switch {
	case age <= 30*24*time.Hour:
		return "fresh"
	case age <= 90*24*time.Hour:
		return "stale"
	default:
		return "cold"
	}
}

func freshnessScore(now, ts time.Time) float64 {
	if ts.IsZero() {
		return -2
	}
	age := now.Sub(ts)
	switch {
	case age <= 30*24*time.Hour:
		return 3
	case age <= 90*24*time.Hour:
		return 1
	default:
		return -2
	}
}

func clampRenderedText(text string, maxLines, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	text = strings.Join(lines, "\n")
	if len(text) > maxChars {
		text = strings.TrimSpace(text[:maxChars-3]) + "..."
	}
	return text
}

type targetHint struct {
	Raw  string
	User string
	Host string
	Kind string
}

func firstTargetHint(text string) *targetHint {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if hint := parseCommandTargetHint(text); hint != nil {
		return hint
	}
	for _, match := range userHostPattern.FindAllString(text, -1) {
		if hint := parseTargetToken(match, TargetKindSSH); hint != nil {
			return hint
		}
	}
	return nil
}

func parseCommandTargetHint(command string) *targetHint {
	fields := strings.Fields(command)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "ssh":
			for j := i + 1; j < len(fields); j++ {
				if token := strings.TrimSpace(fields[j]); token != "" {
					if expectsSSHFlagValue(fields[j-1]) {
						continue
					}
					if strings.HasPrefix(token, "-") {
						continue
					}
					return parseTargetToken(token, TargetKindSSH)
				}
			}
		case "scp", "rsync":
			for j := i + 1; j < len(fields); j++ {
				token := strings.TrimSpace(fields[j])
				if token == "" || strings.HasPrefix(token, "-") {
					continue
				}
				if strings.Contains(token, ":") {
					token = strings.SplitN(token, ":", 2)[0]
				}
				if hint := parseTargetToken(token, TargetKindSSH); hint != nil {
					return hint
				}
			}
		}
	}
	return nil
}

func expectsSSHFlagValue(flag string) bool {
	switch flag {
	case "-i", "-p", "-l", "-o", "-F", "-J", "-b", "-c", "-D", "-E", "-L", "-m", "-R", "-S", "-W":
		return true
	default:
		return false
	}
}

func parseTargetToken(token, kind string) *targetHint {
	token = strings.TrimSpace(strings.Trim(token, "`'\",()[]"))
	if token == "" {
		return nil
	}
	token = strings.TrimSuffix(token, ":")
	user := ""
	host := token
	if strings.Contains(token, "@") {
		parts := strings.SplitN(token, "@", 2)
		user = parts[0]
		host = parts[1]
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	host = strings.TrimSuffix(host, ":")
	if host == "" {
		return nil
	}
	return &targetHint{
		Raw:  token,
		User: user,
		Host: strings.ToLower(host),
		Kind: kind,
	}
}

func findTargetByHint(mem fileState, hint targetHint) int {
	for i, target := range mem.Targets {
		if strings.EqualFold(target.Target.PrimaryName, hint.Host) {
			return i
		}
		for _, alias := range append(append([]string{}, target.Aliases...), append(target.Hostnames, target.IPs...)...) {
			if strings.EqualFold(alias, hint.Host) || strings.EqualFold(alias, hint.Raw) {
				return i
			}
		}
		if hint.User != "" {
			userHost := hint.User + "@" + hint.Host
			for _, alias := range target.Aliases {
				if strings.EqualFold(alias, userHost) {
					return i
				}
			}
		}
	}
	return -1
}

func applyHintToTarget(target *targetRecord, hint targetHint) {
	target.Target.Kind = firstNonEmpty(target.Target.Kind, hint.Kind)
	target.Target.Transport = firstNonEmpty(target.Target.Transport, transportForTargetKind(hint.Kind))
	target.Target.PrimaryName = firstNonEmpty(target.Target.PrimaryName, hint.Host)
	if isIPAddress(hint.Host) {
		target.IPs = append(target.IPs, hint.Host)
	} else if strings.Contains(hint.Host, ".") {
		target.Hostnames = append(target.Hostnames, hint.Host)
		target.Aliases = append(target.Aliases, hint.Host)
	} else {
		target.Aliases = append(target.Aliases, hint.Host)
	}
	if hint.User != "" {
		target.Aliases = append(target.Aliases, hint.User+"@"+hint.Host)
	}
	target.Aliases = dedupeStrings(target.Aliases)
	target.Hostnames = dedupeStrings(target.Hostnames)
	target.IPs = dedupeStrings(target.IPs)
}

func targetIDFromHint(hint targetHint) string {
	return "target-" + shortHash(hint.Kind+"|"+hint.Host+"|"+hint.User)
}

func transportForTargetKind(kind string) string {
	switch kind {
	case TargetKindSSH:
		return "ssh"
	case TargetKindLocalContainer:
		return "container"
	case TargetKindRuntime:
		return "local"
	default:
		return "unknown"
	}
}

func classifyIntent(task string) string {
	lower := strings.ToLower(task)
	switch {
	case strings.Contains(lower, "journalctl") || strings.Contains(lower, "logs"):
		return IntentInspectLogs
	case strings.Contains(lower, "systemctl status") || strings.Contains(lower, "service status") || strings.Contains(lower, "is-active"):
		return IntentInspectServiceStatus
	case strings.Contains(lower, "restart") && strings.Contains(lower, "service"):
		return IntentRestartService
	case strings.Contains(lower, "install") || strings.Contains(lower, "apt") || strings.Contains(lower, "brew") || strings.Contains(lower, "pip "):
		return IntentInstallDependency
	case strings.Contains(lower, "docker"):
		return IntentDockerRecovery
	case strings.Contains(lower, "port") && (strings.Contains(lower, "conflict") || strings.Contains(lower, "in use")):
		return IntentPortConflict
	case strings.Contains(lower, "network") || strings.Contains(lower, "dns"):
		return IntentNetworkDebug
	case strings.Contains(lower, "build"):
		return IntentBuildFix
	case strings.Contains(lower, "test"):
		return IntentTestFix
	case strings.Contains(lower, "config"):
		return IntentConfigEdit
	case strings.Contains(lower, "ssh"):
		return IntentSSHConnectivity
	default:
		return IntentGeneral
	}
}

func primaryToolName(input core.RetrievalContext) string {
	if input.Trouble != nil && input.Trouble.Tool != "" {
		return input.Trouble.Tool
	}
	if len(input.ToolNames) > 0 {
		return input.ToolNames[0]
	}
	return ""
}

func targetName(mem fileState, targetID string) string {
	for _, target := range mem.Targets {
		if target.Target.ID == targetID {
			return firstNonEmpty(target.Target.PrimaryName, target.Target.ID)
		}
	}
	return targetID
}

func isIPAddress(host string) bool {
	for _, r := range host {
		if (r >= '0' && r <= '9') || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return host != ""
}
