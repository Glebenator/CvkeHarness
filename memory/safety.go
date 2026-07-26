package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

const (
	candidateTTL = 14 * 24 * time.Hour
	activeTTL    = 30 * 24 * time.Hour
)

func evidenceHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func factIntegrity(item state.HostFact) string {
	return evidenceHash(
		item.Source,
		item.HostID,
		item.Environment,
		item.Key,
		item.Value,
		item.EvidenceRef,
		fmt.Sprintf("%.6f", item.Confidence),
	)
}

func playbookIntegrity(item state.Playbook) string {
	return evidenceHash(
		item.Source,
		item.TargetID,
		item.Environment,
		item.Intent,
		item.ToolName,
		item.Title,
		strings.Join(item.MatchTerms, "\n"),
		strings.Join(item.Preconditions, "\n"),
		strings.Join(item.VerifySteps, "\n"),
		strings.Join(item.ActionSteps, "\n"),
		strings.Join(item.SuccessChecks, "\n"),
		item.Notes,
		item.EvidenceRef,
		fmt.Sprintf("%.6f", item.Confidence),
		fmt.Sprintf("%d", item.SuccessCount),
		fmt.Sprintf("%d", item.FailureCount),
	)
}

func findingIntegrity(item state.Finding) string {
	return evidenceHash(
		item.Source,
		item.TargetID,
		item.Environment,
		item.Intent,
		item.ToolName,
		item.Body,
		item.EvidenceRef,
		fmt.Sprintf("%.6f", item.Confidence),
		fmt.Sprintf("%d", item.SeenCount),
	)
}

func cautionIntegrity(item state.Caution) string {
	return evidenceHash(
		item.Source,
		item.TargetID,
		item.Environment,
		item.Intent,
		item.ToolName,
		item.Body,
		item.EvidenceRef,
		fmt.Sprintf("%.6f", item.Confidence),
		fmt.Sprintf("%d", item.FailureCount),
	)
}

func targetEnvironment(st fileState, targetID string) string {
	for _, target := range st.Targets {
		if target.Target.ID == targetID {
			return firstNonEmpty(target.Target.Environment, state.EnvironmentUnknown)
		}
	}
	return state.EnvironmentUnknown
}

func liveTargetEnvironment(st fileState, targetID string, now time.Time) (string, bool) {
	for _, target := range st.Targets {
		item := target.Target
		if item.ID != targetID {
			continue
		}
		if item.Status != state.MemoryStatusActive ||
			item.Environment == "" ||
			item.Environment == state.EnvironmentUnknown ||
			item.ExpiresAt.IsZero() ||
			!item.ExpiresAt.After(now) ||
			(item.Kind != TargetKindRuntime && item.RemoteIdentity == "") {
			return state.EnvironmentUnknown, false
		}
		return item.Environment, true
	}
	return state.EnvironmentUnknown, false
}

func validMemoryStatus(status string) bool {
	switch status {
	case state.MemoryStatusCandidate,
		state.MemoryStatusActive,
		state.MemoryStatusRejected,
		state.MemoryStatusRevoked,
		state.MemoryStatusExpired:
		return true
	default:
		return false
	}
}

func validMemoryTrust(trust string) bool {
	switch trust {
	case state.MemoryTrustUntrusted, state.MemoryTrustOperator, state.MemoryTrustVerified:
		return true
	default:
		return false
	}
}

func liveOperationalItem(status, trust, environment, targetEnvironment string, expiresAt, now time.Time) bool {
	if status != state.MemoryStatusActive {
		return false
	}
	if trust != state.MemoryTrustOperator && trust != state.MemoryTrustVerified {
		return false
	}
	if environment == "" || environment == state.EnvironmentUnknown || environment != targetEnvironment {
		return false
	}
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return false
	}
	return true
}

func validateImportedState(st fileState, now time.Time) error {
	targets := make(map[string]state.Target, len(st.Targets))
	for _, record := range st.Targets {
		target := record.Target
		if strings.TrimSpace(target.ID) == "" {
			return fmt.Errorf("import rejected: target_id is required")
		}
		if _, exists := targets[target.ID]; exists {
			return fmt.Errorf("import rejected: duplicate target_id %q", target.ID)
		}
		if target.Environment == "" {
			return fmt.Errorf("import rejected: target %q is missing environment", target.ID)
		}
		if target.Status == state.MemoryStatusActive && target.Kind != TargetKindRuntime {
			if target.Environment == state.EnvironmentUnknown || strings.TrimSpace(target.RemoteIdentity) == "" {
				return fmt.Errorf("import rejected: active remote target %q needs environment and remote_identity", target.ID)
			}
		}
		targets[target.ID] = target
	}

	validateScope := func(kind, id, targetID, environment, status, trust, evidence string, expiresAt time.Time) error {
		target, ok := targets[targetID]
		if !ok {
			return fmt.Errorf("import rejected: %s %q references unknown target %q", kind, id, targetID)
		}
		if environment == "" || environment != target.Environment {
			return fmt.Errorf("import rejected: %s %q environment does not match target", kind, id)
		}
		if !validMemoryStatus(status) || !validMemoryTrust(trust) {
			return fmt.Errorf("import rejected: %s %q has invalid status or trust", kind, id)
		}
		if strings.TrimSpace(evidence) == "" {
			return fmt.Errorf("import rejected: %s %q is missing evidence_hash", kind, id)
		}
		if status == state.MemoryStatusActive && (expiresAt.IsZero() || !expiresAt.After(now)) {
			return fmt.Errorf("import rejected: active %s %q is expired or has no expiry", kind, id)
		}
		return nil
	}

	for _, record := range st.Targets {
		for _, item := range record.Facts {
			if err := validateScope("fact", item.Key, item.HostID, item.Environment, item.Status, item.Trust, item.EvidenceHash, item.ExpiresAt); err != nil {
				return err
			}
			if item.EvidenceHash != factIntegrity(item) {
				return fmt.Errorf("import rejected: fact %q failed integrity validation", item.Key)
			}
		}
	}
	for _, item := range st.Playbooks {
		if err := validateScope("playbook", item.ID, item.TargetID, item.Environment, item.Status, item.Trust, item.EvidenceHash, item.ExpiresAt); err != nil {
			return err
		}
		if item.EvidenceHash != playbookIntegrity(item) {
			return fmt.Errorf("import rejected: playbook %q failed integrity validation", item.ID)
		}
		if item.Status == state.MemoryStatusActive && len(item.SuccessChecks) == 0 {
			return fmt.Errorf("import rejected: active playbook %q has no success check", item.ID)
		}
	}
	for _, item := range st.Findings {
		if err := validateScope("finding", item.ID, item.TargetID, item.Environment, item.Status, item.Trust, item.EvidenceHash, item.ExpiresAt); err != nil {
			return err
		}
		if item.EvidenceHash != findingIntegrity(item) {
			return fmt.Errorf("import rejected: finding %q failed integrity validation", item.ID)
		}
	}
	for _, item := range st.Cautions {
		if err := validateScope("caution", item.ID, item.TargetID, item.Environment, item.Status, item.Trust, item.EvidenceHash, item.ExpiresAt); err != nil {
			return err
		}
		if item.EvidenceHash != cautionIntegrity(item) {
			return fmt.Errorf("import rejected: caution %q failed integrity validation", item.ID)
		}
	}
	return nil
}

func prepareImportedState(st *fileState, now time.Time) error {
	if st == nil {
		return fmt.Errorf("import rejected: operational state is required")
	}
	rejectSensitive := func(kind, id string, values ...string) error {
		for _, value := range values {
			if containsSensitiveText(value) {
				return fmt.Errorf("import rejected: %s %q contains a credential or secret marker", kind, id)
			}
		}
		return nil
	}
	for targetIdx := range st.Targets {
		target := &st.Targets[targetIdx].Target
		if err := rejectSensitive("target", target.ID,
			target.ID,
			target.PrimaryName,
			target.Environment,
			target.Transport,
			target.RemoteIdentity,
			strings.Join(st.Targets[targetIdx].Aliases, "\n"),
			strings.Join(st.Targets[targetIdx].Hostnames, "\n"),
			strings.Join(st.Targets[targetIdx].IPs, "\n"),
		); err != nil {
			return err
		}
		for factIdx := range st.Targets[targetIdx].Facts {
			item := &st.Targets[targetIdx].Facts[factIdx]
			if err := rejectSensitive("fact", item.Key,
				item.Key, item.Value, item.Status, item.Source, item.EvidenceRef, item.Trust,
			); err != nil {
				return err
			}
			if item.EvidenceHash != factIntegrity(*item) {
				canonicalizeImportedMetadata(&item.Source, &item.EvidenceRef, &item.Trust, &item.ObservedAt, now)
				item.EvidenceHash = factIntegrity(*item)
			}
		}
	}
	for idx := range st.Playbooks {
		item := &st.Playbooks[idx]
		if err := rejectSensitive("playbook", item.ID,
			item.ID, item.TargetID, item.Environment, item.Intent, item.ToolName, item.Status,
			item.Source, item.EvidenceRef, item.Trust, item.Title,
			strings.Join(item.MatchTerms, "\n"),
			strings.Join(item.Preconditions, "\n"),
			strings.Join(item.VerifySteps, "\n"),
			strings.Join(item.ActionSteps, "\n"),
			strings.Join(item.SuccessChecks, "\n"),
			item.Notes,
		); err != nil {
			return err
		}
		if item.EvidenceHash != playbookIntegrity(*item) {
			canonicalizeImportedMetadata(&item.Source, &item.EvidenceRef, &item.Trust, &item.ObservedAt, now)
			item.EvidenceHash = playbookIntegrity(*item)
		}
	}
	for idx := range st.Findings {
		item := &st.Findings[idx]
		if err := rejectSensitive("finding", item.ID,
			item.ID, item.TargetID, item.Environment, item.Intent, item.ToolName, item.Status,
			item.Origin, item.Source, item.EvidenceRef, item.Trust, item.Body,
		); err != nil {
			return err
		}
		if item.EvidenceHash != findingIntegrity(*item) {
			canonicalizeImportedMetadata(&item.Source, &item.EvidenceRef, &item.Trust, &item.ObservedAt, now)
			item.EvidenceHash = findingIntegrity(*item)
		}
	}
	for idx := range st.Cautions {
		item := &st.Cautions[idx]
		if err := rejectSensitive("caution", item.ID,
			item.ID, item.TargetID, item.Environment, item.Intent, item.ToolName, item.Status,
			item.Source, item.EvidenceRef, item.Trust, item.Body,
		); err != nil {
			return err
		}
		if item.EvidenceHash != cautionIntegrity(*item) {
			canonicalizeImportedMetadata(&item.Source, &item.EvidenceRef, &item.Trust, &item.ObservedAt, now)
			item.EvidenceHash = cautionIntegrity(*item)
		}
	}
	return nil
}

func canonicalizeImportedMetadata(source, evidenceRef, trust *string, observedAt *time.Time, now time.Time) {
	*source = "operator_import"
	*evidenceRef = "validated_markdown_import"
	if *trust == "" || *trust == state.MemoryTrustUntrusted {
		*trust = state.MemoryTrustOperator
	}
	if observedAt.IsZero() {
		*observedAt = now
	}
}

func containsSensitiveCalls(calls []ObservedToolCall) bool {
	for _, call := range calls {
		if containsSensitiveCommand(call.Command) {
			return true
		}
	}
	return false
}

func containsSensitiveCommand(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{
		"authorization:",
		"bearer ",
		"password=",
		"passwd=",
		"token=",
		"api_key=",
		"apikey=",
		"access_key=",
		"secret_access_key=",
		"secret=",
		"private_key",
		"private key-----",
		"client_secret",
		"ssh-rsa ",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func containsSensitiveText(text string) bool {
	return containsSensitiveCommand(strings.ToLower(text))
}

func redactSensitiveText(text string) string {
	words := strings.Fields(text)
	for i, word := range words {
		lower := strings.ToLower(word)
		for _, marker := range []string{"password=", "passwd=", "token=", "api_key=", "apikey=", "secret=", "client_secret="} {
			if strings.HasPrefix(lower, marker) {
				words[i] = word[:len(marker)] + "[REDACTED]"
				break
			}
		}
		if strings.HasPrefix(lower, "authorization:") {
			words[i] = "Authorization:[REDACTED]"
		}
	}
	return strings.Join(words, " ")
}

func isToolProbe(command, tool string) bool {
	command = strings.TrimSpace(strings.ToLower(command))
	tool = strings.TrimSpace(strings.ToLower(tool))
	return strings.Contains(command, "command -v "+tool) ||
		strings.Contains(command, "which "+tool) ||
		strings.Contains(command, tool+" --version") ||
		strings.Contains(command, tool+" -v")
}
