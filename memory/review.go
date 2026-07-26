package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/state"
)

// ReviewInbox returns all candidate operational knowledge in a compact,
// operator-readable form. Candidates are never eligible for retrieval.
func (m *Manager) ReviewInbox(ctx context.Context) (string, error) {
	if err := m.EnsureFiles(); err != nil {
		return "", err
	}
	st, err := m.loadState(ctx)
	if err != nil {
		return "", err
	}
	type row struct {
		kind, id, targetID, environment, source, evidenceRef, evidenceHash, trust, summary, successChecks string
		observedAt, expiresAt                                                                             time.Time
	}
	var rows []row
	for _, record := range st.Targets {
		for _, item := range record.Facts {
			if item.Status == state.MemoryStatusCandidate {
				rows = append(rows, row{
					kind: "fact", id: item.HostID + ":" + item.Key, targetID: item.HostID, environment: item.Environment,
					source: item.Source, evidenceRef: item.EvidenceRef, evidenceHash: item.EvidenceHash, trust: item.Trust,
					summary: item.Key + "=" + item.Value, observedAt: item.ObservedAt, expiresAt: item.ExpiresAt,
				})
			}
		}
	}
	for _, item := range st.Playbooks {
		if item.Status == state.MemoryStatusCandidate {
			rows = append(rows, row{
				kind: "playbook", id: item.ID, targetID: item.TargetID, environment: item.Environment,
				source: item.Source, evidenceRef: item.EvidenceRef, evidenceHash: item.EvidenceHash, trust: item.Trust,
				summary: item.Title, successChecks: strings.Join(item.SuccessChecks, "; "),
				observedAt: item.ObservedAt, expiresAt: item.ExpiresAt,
			})
		}
	}
	for _, item := range st.Findings {
		if item.Status == state.MemoryStatusCandidate {
			rows = append(rows, row{
				kind: "finding", id: item.ID, targetID: item.TargetID, environment: item.Environment,
				source: item.Source, evidenceRef: item.EvidenceRef, evidenceHash: item.EvidenceHash, trust: item.Trust,
				summary: item.Body, observedAt: item.ObservedAt, expiresAt: item.ExpiresAt,
			})
		}
	}
	for _, item := range st.Cautions {
		if item.Status == state.MemoryStatusCandidate {
			rows = append(rows, row{
				kind: "caution", id: item.ID, targetID: item.TargetID, environment: item.Environment,
				source: item.Source, evidenceRef: item.EvidenceRef, evidenceHash: item.EvidenceHash, trust: item.Trust,
				summary: item.Body, observedAt: item.ObservedAt, expiresAt: item.ExpiresAt,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].observedAt.After(rows[j].observedAt) })
	if len(rows) == 0 {
		return "No memory candidates are waiting for review.", nil
	}
	var b strings.Builder
	b.WriteString("Memory review inbox:\n")
	for _, item := range rows {
		fmt.Fprintf(&b, "- %s %s\n  target=%s environment=%s status=candidate trust=%s\n  source=%s evidence_ref=%s evidence_hash=%s\n  observed=%s expires=%s sensitivity=%s\n  summary=%s\n",
			item.kind,
			item.id,
			item.targetID,
			firstNonEmpty(item.environment, state.EnvironmentUnknown),
			firstNonEmpty(item.trust, state.MemoryTrustUntrusted),
			firstNonEmpty(item.source, "unknown"),
			firstNonEmpty(item.evidenceRef, "none"),
			shortEvidenceHash(item.evidenceHash),
			formatTime(item.observedAt),
			formatTime(item.expiresAt),
			sensitivityLabel(item.summary),
			clampRenderedText(redactSensitiveText(item.summary), 2, 240),
		)
		if item.kind == "playbook" {
			fmt.Fprintf(&b, "  success_checks=%s\n", firstNonEmpty(clampRenderedText(redactSensitiveText(item.successChecks), 2, 240), "none"))
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func shortEvidenceHash(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return firstNonEmpty(value, "none")
	}
	return value[:12]
}

func sensitivityLabel(value string) string {
	if containsSensitiveText(value) {
		return "review-required"
	}
	return "no-known-marker"
}

// PromoteMemory marks one reviewed candidate active for a bounded period.
func (m *Manager) PromoteMemory(ctx context.Context, kind, id string) error {
	return m.transitionMemory(ctx, kind, id, state.MemoryStatusActive)
}

// RejectMemory marks one candidate rejected so it cannot be retrieved.
func (m *Manager) RejectMemory(ctx context.Context, kind, id string) error {
	return m.transitionMemory(ctx, kind, id, state.MemoryStatusRejected)
}

// RevokeMemory immediately removes an active item from retrieval.
func (m *Manager) RevokeMemory(ctx context.Context, kind, id string) error {
	return m.transitionMemory(ctx, kind, id, state.MemoryStatusRevoked)
}

func (m *Manager) transitionMemory(ctx context.Context, kind, id, next string) error {
	if err := m.EnsureFiles(); err != nil {
		return err
	}
	st, err := m.loadState(ctx)
	if err != nil {
		return err
	}
	now := m.now()
	found := false
	prepare := func(current, targetID, environment string, expiresAt *time.Time, trust *string) error {
		if err := validateMemoryTransition(current, next); err != nil {
			return err
		}
		if next != state.MemoryStatusActive {
			return nil
		}
		targetEnv := targetEnvironment(st, targetID)
		if environment == "" || environment == state.EnvironmentUnknown || environment != targetEnv {
			return fmt.Errorf("cannot promote memory for an unknown or mismatched environment")
		}
		*trust = state.MemoryTrustOperator
		*expiresAt = now.Add(activeTTL)
		return nil
	}

	switch kind {
	case "fact":
		parts := strings.SplitN(id, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("fact id must be target_id:key")
		}
		for targetIdx := range st.Targets {
			for factIdx := range st.Targets[targetIdx].Facts {
				item := &st.Targets[targetIdx].Facts[factIdx]
				if item.HostID != parts[0] || item.Key != parts[1] {
					continue
				}
				if err := prepare(item.Status, item.HostID, item.Environment, &item.ExpiresAt, &item.Trust); err != nil {
					return err
				}
				item.Status = next
				item.UpdatedAt = now
				item.EvidenceHash = factIntegrity(*item)
				found = true
			}
		}
	case "playbook":
		for idx := range st.Playbooks {
			item := &st.Playbooks[idx]
			if item.ID != id {
				continue
			}
			if next == state.MemoryStatusActive && len(item.SuccessChecks) == 0 {
				return fmt.Errorf("cannot promote playbook %q without an explicit success check", id)
			}
			if err := prepare(item.Status, item.TargetID, item.Environment, &item.ExpiresAt, &item.Trust); err != nil {
				return err
			}
			item.Status = next
			item.UpdatedAt = now
			item.EvidenceHash = playbookIntegrity(*item)
			found = true
		}
	case "finding":
		for idx := range st.Findings {
			item := &st.Findings[idx]
			if item.ID != id {
				continue
			}
			if err := prepare(item.Status, item.TargetID, item.Environment, &item.ExpiresAt, &item.Trust); err != nil {
				return err
			}
			item.Status = next
			item.UpdatedAt = now
			item.EvidenceHash = findingIntegrity(*item)
			found = true
		}
	case "caution":
		for idx := range st.Cautions {
			item := &st.Cautions[idx]
			if item.ID != id {
				continue
			}
			if err := prepare(item.Status, item.TargetID, item.Environment, &item.ExpiresAt, &item.Trust); err != nil {
				return err
			}
			item.Status = next
			item.UpdatedAt = now
			item.EvidenceHash = cautionIntegrity(*item)
			found = true
		}
	default:
		return fmt.Errorf("memory kind must be one of: fact, playbook, finding, caution")
	}
	if !found {
		return fmt.Errorf("%s %q was not found", kind, id)
	}
	return m.writeAllState(ctx, st, "operator "+next+" memory")
}

func validateMemoryTransition(current, next string) error {
	switch next {
	case state.MemoryStatusActive, state.MemoryStatusRejected:
		if current != state.MemoryStatusCandidate {
			return fmt.Errorf("cannot transition %s memory from %q; only candidates can be promoted or rejected", next, current)
		}
	case state.MemoryStatusRevoked:
		if current != state.MemoryStatusActive {
			return fmt.Errorf("cannot revoke memory with status %q; only active memory can be revoked", current)
		}
	default:
		return fmt.Errorf("unsupported memory transition %q", next)
	}
	return nil
}

// DeleteMemory removes one item from canonical operational state. Generated
// view snapshots are retained as local audit history.
func (m *Manager) DeleteMemory(ctx context.Context, kind, id string) error {
	if err := m.EnsureFiles(); err != nil {
		return err
	}
	st, err := m.loadState(ctx)
	if err != nil {
		return err
	}
	found := false
	switch kind {
	case "playbook":
		st.Playbooks, found = deleteByID(st.Playbooks, id, func(item state.Playbook) string { return item.ID })
	case "finding":
		st.Findings, found = deleteByID(st.Findings, id, func(item state.Finding) string { return item.ID })
	case "caution":
		st.Cautions, found = deleteByID(st.Cautions, id, func(item state.Caution) string { return item.ID })
	case "fact":
		parts := strings.SplitN(id, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("fact id must be target_id:key")
		}
		for idx := range st.Targets {
			if st.Targets[idx].Target.ID != parts[0] {
				continue
			}
			st.Targets[idx].Facts, found = deleteByID(st.Targets[idx].Facts, parts[1], func(item state.HostFact) string { return item.Key })
		}
	default:
		return fmt.Errorf("memory kind must be one of: fact, playbook, finding, caution")
	}
	if !found {
		return fmt.Errorf("%s %q was not found", kind, id)
	}
	return m.writeAllState(ctx, st, "operator deleted memory")
}

func deleteByID[T any](items []T, id string, idOf func(T) string) ([]T, bool) {
	out := items[:0]
	found := false
	for _, item := range items {
		if idOf(item) == id {
			found = true
			continue
		}
		out = append(out, item)
	}
	return out, found
}

// SetTargetEnvironment binds a provisional target to an operator-supplied
// environment and remote identity. This does not approve any command.
func (m *Manager) SetTargetEnvironment(ctx context.Context, targetID, environment, remoteIdentity string) error {
	environment = strings.ToLower(strings.TrimSpace(environment))
	remoteIdentity = strings.TrimSpace(remoteIdentity)
	if environment == "" || environment == state.EnvironmentUnknown || remoteIdentity == "" {
		return fmt.Errorf("target environment and remote identity are required")
	}
	if err := m.EnsureFiles(); err != nil {
		return err
	}
	st, err := m.loadState(ctx)
	if err != nil {
		return err
	}
	now := m.now()
	found := false
	for idx := range st.Targets {
		target := &st.Targets[idx]
		if target.Target.ID != targetID {
			continue
		}
		target.Target.Environment = environment
		target.Target.RemoteIdentity = remoteIdentity
		target.Target.Status = state.MemoryStatusActive
		target.Target.VerifiedAt = now
		target.Target.ExpiresAt = now.Add(activeTTL)
		target.Target.LastSeenAt = now
		for factIdx := range target.Facts {
			target.Facts[factIdx].Environment = environment
			target.Facts[factIdx].EvidenceHash = factIntegrity(target.Facts[factIdx])
		}
		for idx := range st.Playbooks {
			if st.Playbooks[idx].TargetID == targetID {
				st.Playbooks[idx].Environment = environment
				st.Playbooks[idx].EvidenceHash = playbookIntegrity(st.Playbooks[idx])
			}
		}
		for idx := range st.Findings {
			if st.Findings[idx].TargetID == targetID {
				st.Findings[idx].Environment = environment
				st.Findings[idx].EvidenceHash = findingIntegrity(st.Findings[idx])
			}
		}
		for idx := range st.Cautions {
			if st.Cautions[idx].TargetID == targetID {
				st.Cautions[idx].Environment = environment
				st.Cautions[idx].EvidenceHash = cautionIntegrity(st.Cautions[idx])
			}
		}
		found = true
	}
	if !found {
		return fmt.Errorf("target %q was not found", targetID)
	}
	return m.writeAllState(ctx, st, "operator bound target environment")
}

// Export writes generated Markdown views from canonical SQLite state.
func (m *Manager) Export(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		dir = m.dir
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	st, err := m.loadState(ctx)
	if err != nil {
		return err
	}
	for name, content := range renderedViews(st) {
		if err := writeFileAtomic(filepath.Join(dir, name), content); err != nil {
			return err
		}
	}
	if guidance, err := os.ReadFile(m.managedPath(GuidanceFile)); err == nil {
		if err := writeFileAtomic(filepath.Join(dir, GuidanceFile), string(guidance)); err != nil {
			return err
		}
	}
	return nil
}

// Import validates Markdown views before atomically replacing canonical
// operational state.
func (m *Manager) Import(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		dir = m.dir
	}
	importer := NewManager(dir, m.store)
	importer.now = m.now
	importer.hostname = m.hostname
	st, err := importer.parseManagedFiles()
	if err != nil {
		return err
	}
	importer.ensureRuntimeBootstrap(&st)
	if err := prepareImportedState(&st, m.now()); err != nil {
		return err
	}
	if err := validateImportedState(st, m.now()); err != nil {
		return err
	}
	return m.writeAllState(ctx, st, "validated Markdown import")
}
