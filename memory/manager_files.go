package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/state"
	"gopkg.in/yaml.v3"
)

type markdownRecord struct {
	Title string
	Meta  string
	Body  string
}

// EnsureFiles creates the managed memory files when missing and imports
// legacy memory.md content into findings.md on first structured bootstrap.
func (m *Manager) EnsureFiles() error {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "snapshots"), 0755); err != nil {
		return err
	}

	seedText := map[string]string{
		m.managedPath(OperatorFile): defaultOperatorMarkdown(),
		m.managedPath(SoulFile):     "# Soul\n\n",
	}
	for path, content := range seedText {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
	}

	needsStructuredWrite := false
	for _, name := range structuredManagedFiles() {
		if _, err := os.Stat(m.managedPath(name)); os.IsNotExist(err) {
			needsStructuredWrite = true
			break
		}
	}

	state, err := m.parseManagedFiles()
	if err != nil {
		return err
	}
	changed := m.ensureRuntimeBootstrap(&state)
	if needsStructuredWrite {
		changed = true
		if imported, err := m.importLegacyMemory(&state); err != nil {
			return err
		} else if imported {
			changed = true
		}
	}
	if !changed && !needsStructuredWrite {
		return nil
	}
	return m.writeAllState(context.Background(), state, "bootstrap structured memory")
}

// Show returns the managed memory files as a readable string.
func (m *Manager) Show(ctx context.Context) (string, error) {
	if err := m.EnsureFiles(); err != nil {
		return "", err
	}

	var sections []string
	for _, name := range allManagedFiles() {
		data, err := os.ReadFile(m.managedPath(name))
		if err != nil {
			return "", err
		}
		sections = append(sections, fmt.Sprintf("## %s\n%s", name, strings.TrimSpace(string(data))))
	}

	if m.store != nil && m.store.Available() {
		snapshots, err := m.store.ListSnapshots(ctx)
		if err == nil {
			sections = append(sections, fmt.Sprintf("## snapshots\n%d snapshot(s) recorded", len(snapshots)))
		}
	}

	return strings.Join(sections, "\n\n"), nil
}

// Rollback restores a managed memory file from a snapshot.
func (m *Manager) Rollback(ctx context.Context, snapshotID string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	if m.store == nil || !m.store.Available() {
		return fmt.Errorf("state database unavailable; rollback metadata is not accessible")
	}

	snapshot, err := m.store.GetSnapshot(ctx, snapshotID)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(snapshot.Path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.managedPath(snapshot.SourceFile), data, 0644); err != nil {
		return err
	}
	return m.Reindex(ctx)
}

// Reindex rebuilds structured memory metadata from the managed markdown files.
func (m *Manager) Reindex(ctx context.Context) error {
	state, err := m.parseManagedFiles()
	if err != nil {
		return err
	}
	m.ensureRuntimeBootstrap(&state)
	return m.writeAllState(ctx, state, "normalize structured memory")
}

func (m *Manager) ensureRuntimeBootstrap(st *fileState) bool {
	now := m.now()
	runtimeHostID := st.RuntimeHostID
	if runtimeHostID == "" {
		runtimeHostID = defaultRuntimeHostID(m.hostname())
		st.RuntimeHostID = runtimeHostID
	}

	targetIdx := -1
	for i, target := range st.Targets {
		if target.Target.ID == runtimeHostID {
			targetIdx = i
			break
		}
	}

	changed := false
	if targetIdx < 0 {
		st.Targets = append(st.Targets, targetRecord{
			Target: state.Target{
				ID:          runtimeHostID,
				Kind:        TargetKindRuntime,
				PrimaryName: m.hostname(),
				Transport:   "local",
				Confidence:  1,
				Status:      "active",
				FirstSeenAt: now,
				LastSeenAt:  now,
			},
			Aliases: []string{m.hostname()},
			Facts: []state.HostFact{
				{
					HostID:     runtimeHostID,
					Key:        "hostname",
					Value:      m.hostname(),
					Confidence: 1,
					VerifiedAt: now,
					UpdatedAt:  now,
				},
			},
		})
		changed = true
	} else {
		target := st.Targets[targetIdx]
		if target.Target.Kind == "" {
			target.Target.Kind = TargetKindRuntime
			changed = true
		}
		if target.Target.PrimaryName == "" {
			target.Target.PrimaryName = m.hostname()
			changed = true
		}
		if target.Target.Transport == "" {
			target.Target.Transport = "local"
			changed = true
		}
		if target.Target.Status == "" {
			target.Target.Status = "active"
			changed = true
		}
		if target.Target.Confidence == 0 {
			target.Target.Confidence = 1
			changed = true
		}
		if target.Target.FirstSeenAt.IsZero() {
			target.Target.FirstSeenAt = now
			changed = true
		}
		if target.Target.LastSeenAt.IsZero() {
			target.Target.LastSeenAt = now
			changed = true
		}
		if !containsString(target.Aliases, m.hostname()) {
			target.Aliases = append(target.Aliases, m.hostname())
			changed = true
		}
		target.Facts = upsertFact(target.Facts, state.HostFact{
			HostID:     runtimeHostID,
			Key:        "hostname",
			Value:      m.hostname(),
			Confidence: 1,
			VerifiedAt: now,
			UpdatedAt:  now,
		})
		st.Targets[targetIdx] = target
	}

	if len(st.RuntimeHostFacts) == 0 {
		for _, target := range st.Targets {
			if target.Target.ID == runtimeHostID {
				st.RuntimeHostFacts = append([]state.HostFact(nil), target.Facts...)
				changed = true
				break
			}
		}
	}
	st.RuntimeHostFacts = dedupeFacts(st.RuntimeHostFacts)
	return changed
}

func (m *Manager) importLegacyMemory(st *fileState) (bool, error) {
	legacyPath := m.managedPath(LegacyMemoryFile)
	if _, err := os.Stat(legacyPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return false, err
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		return false, nil
	}

	if len(st.Findings) > 0 || len(st.Playbooks) > 0 || len(st.Cautions) > 0 {
		return false, nil
	}

	now := m.now()
	var findings []state.Finding
	records, err := parseMarkdownRecords(content)
	if err == nil && len(records) > 0 {
		for _, record := range records {
			body := strings.TrimSpace(record.Body)
			if body == "" {
				body = strings.TrimSpace(record.Title)
			}
			if body == "" {
				continue
			}
			findings = append(findings, state.Finding{
				ID:         findingID("legacy", body),
				TargetID:   "unknown",
				Intent:     IntentGeneral,
				ToolName:   "",
				Status:     "needs_curation",
				Origin:     "legacy_memory",
				Body:       body,
				Confidence: 0.5,
				SeenCount:  1,
				CreatedAt:  now,
				UpdatedAt:  now,
			})
		}
	}
	if len(findings) == 0 {
		for _, body := range parseLegacyListItems(content) {
			findings = append(findings, state.Finding{
				ID:         findingID("legacy", body),
				TargetID:   "unknown",
				Intent:     IntentGeneral,
				ToolName:   "",
				Status:     "needs_curation",
				Origin:     "legacy_memory",
				Body:       body,
				Confidence: 0.5,
				SeenCount:  1,
				CreatedAt:  now,
				UpdatedAt:  now,
			})
		}
	}
	if len(findings) == 0 {
		return false, nil
	}
	st.Findings = append(st.Findings, findings...)
	return true, nil
}

func parseLegacyListItems(content string) []string {
	var out []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "- "))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func (m *Manager) parseManagedFiles() (fileState, error) {
	var out fileState

	targets, runtimeHostID, runtimeFacts, err := m.parseTargetsAndHostFiles()
	if err != nil {
		return fileState{}, err
	}
	out.Targets = targets
	out.RuntimeHostID = runtimeHostID
	out.RuntimeHostFacts = runtimeFacts

	playbooks, err := m.parsePlaybooksFile()
	if err != nil {
		return fileState{}, err
	}
	out.Playbooks = playbooks

	findings, err := m.parseFindingsFile()
	if err != nil {
		return fileState{}, err
	}
	out.Findings = findings

	cautions, err := m.parseCautionsFile()
	if err != nil {
		return fileState{}, err
	}
	out.Cautions = cautions

	return out, nil
}

func (m *Manager) parseTargetsAndHostFiles() ([]targetRecord, string, []state.HostFact, error) {
	var targets []targetRecord
	byID := make(map[string]int)

	targetContent, err := readOptionalFile(m.managedPath(TargetsFile))
	if err != nil {
		return nil, "", nil, err
	}
	records, err := parseMarkdownRecords(targetContent)
	if err != nil {
		return nil, "", nil, err
	}
	for _, record := range records {
		item, err := parseTargetRecord(record)
		if err != nil {
			return nil, "", nil, err
		}
		byID[item.Target.ID] = len(targets)
		targets = append(targets, item)
	}

	hostContent, err := readOptionalFile(m.managedPath(HostFile))
	if err != nil {
		return nil, "", nil, err
	}
	hostRecords, err := parseMarkdownRecords(hostContent)
	if err != nil {
		return nil, "", nil, err
	}

	runtimeHostID := ""
	var runtimeFacts []state.HostFact
	for _, record := range hostRecords {
		hostID, facts, err := parseHostRecord(record)
		if err != nil {
			return nil, "", nil, err
		}
		if runtimeHostID == "" {
			runtimeHostID = hostID
			runtimeFacts = facts
		}
		if idx, ok := byID[hostID]; ok {
			targets[idx].Facts = mergeFactLists(targets[idx].Facts, facts)
		}
	}

	return targets, runtimeHostID, runtimeFacts, nil
}

func (m *Manager) parsePlaybooksFile() ([]state.Playbook, error) {
	content, err := readOptionalFile(m.managedPath(PlaybooksFile))
	if err != nil {
		return nil, err
	}
	records, err := parseMarkdownRecords(content)
	if err != nil {
		return nil, err
	}
	out := make([]state.Playbook, 0, len(records))
	for _, record := range records {
		item, err := parsePlaybookRecord(record)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (m *Manager) parseFindingsFile() ([]state.Finding, error) {
	content, err := readOptionalFile(m.managedPath(FindingsFile))
	if err != nil {
		return nil, err
	}
	records, err := parseMarkdownRecords(content)
	if err != nil {
		return nil, err
	}
	out := make([]state.Finding, 0, len(records))
	for _, record := range records {
		item, err := parseFindingRecord(record)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (m *Manager) parseCautionsFile() ([]state.Caution, error) {
	content, err := readOptionalFile(m.managedPath(CautionsFile))
	if err != nil {
		return nil, err
	}
	records, err := parseMarkdownRecords(content)
	if err != nil {
		return nil, err
	}
	out := make([]state.Caution, 0, len(records))
	for _, record := range records {
		item, err := parseCautionRecord(record)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func readOptionalFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func parseMarkdownRecords(content string) ([]markdownRecord, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}

	rest := content
	if idx := strings.Index(rest, "\n## "); idx >= 0 {
		rest = rest[idx+1:]
	}
	if strings.HasPrefix(rest, "# ") {
		if idx := strings.Index(rest, "\n"); idx >= 0 {
			rest = rest[idx+1:]
		} else {
			return nil, nil
		}
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil, nil
	}

	parts := strings.Split(rest, "\n## ")
	records := make([]markdownRecord, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimPrefix(part, "## "))
		if part == "" {
			continue
		}
		lines := strings.Split(part, "\n")
		title := strings.TrimSpace(lines[0])
		body := ""
		if len(lines) > 1 {
			body = strings.Join(lines[1:], "\n")
		}
		meta, body := splitLeadingYAML(body)
		records = append(records, markdownRecord{
			Title: title,
			Meta:  meta,
			Body:  body,
		})
	}
	return records, nil
}

func splitLeadingYAML(body string) (string, string) {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, "```yaml\n") {
		return "", strings.TrimSpace(body)
	}
	rest := strings.TrimPrefix(trimmed, "```yaml\n")
	end := strings.Index(rest, "\n```")
	if end < 0 {
		return "", strings.TrimSpace(body)
	}
	meta := strings.TrimSpace(rest[:end])
	bodyRest := strings.TrimSpace(rest[end+4:])
	return meta, bodyRest
}

func parseTargetRecord(record markdownRecord) (targetRecord, error) {
	type targetMeta struct {
		TargetID    string   `yaml:"target_id"`
		Kind        string   `yaml:"kind"`
		PrimaryName string   `yaml:"primary_name"`
		Aliases     []string `yaml:"aliases"`
		Hostnames   []string `yaml:"hostnames"`
		IPs         []string `yaml:"ips"`
		Transport   string   `yaml:"transport"`
		FirstSeenAt string   `yaml:"first_seen_at"`
		LastSeenAt  string   `yaml:"last_seen_at"`
		Confidence  float64  `yaml:"confidence"`
		Status      string   `yaml:"status"`
	}
	var meta targetMeta
	if err := yaml.Unmarshal([]byte(record.Meta), &meta); err != nil {
		return targetRecord{}, err
	}
	facts := parseFactsSection(record.Body, meta.TargetID)
	return targetRecord{
		Target: state.Target{
			ID:          strings.TrimSpace(meta.TargetID),
			Kind:        strings.TrimSpace(meta.Kind),
			PrimaryName: firstNonEmpty(meta.PrimaryName, record.Title),
			Transport:   strings.TrimSpace(meta.Transport),
			Confidence:  meta.Confidence,
			Status:      strings.TrimSpace(meta.Status),
			FirstSeenAt: parseTime(meta.FirstSeenAt),
			LastSeenAt:  parseTime(meta.LastSeenAt),
		},
		Aliases:   dedupeStrings(meta.Aliases),
		Hostnames: dedupeStrings(meta.Hostnames),
		IPs:       dedupeStrings(meta.IPs),
		Facts:     facts,
	}, nil
}

func parseHostRecord(record markdownRecord) (string, []state.HostFact, error) {
	type hostMeta struct {
		RuntimeHostID string  `yaml:"runtime_host_id"`
		PrimaryName   string  `yaml:"primary_name"`
		VerifiedAt    string  `yaml:"verified_at"`
		Confidence    float64 `yaml:"confidence"`
		Status        string  `yaml:"status"`
	}
	var meta hostMeta
	if err := yaml.Unmarshal([]byte(record.Meta), &meta); err != nil {
		return "", nil, err
	}
	facts := parseFactsSection(record.Body, meta.RuntimeHostID)
	verifiedAt := parseTime(meta.VerifiedAt)
	if verifiedAt.IsZero() {
		verifiedAt = time.Now().UTC()
	}
	facts = upsertFact(facts, state.HostFact{
		HostID:     meta.RuntimeHostID,
		Key:        "primary_name",
		Value:      firstNonEmpty(meta.PrimaryName, record.Title),
		Confidence: maxFloat(meta.Confidence, 1),
		VerifiedAt: verifiedAt,
		UpdatedAt:  verifiedAt,
	})
	return strings.TrimSpace(meta.RuntimeHostID), facts, nil
}

func parsePlaybookRecord(record markdownRecord) (state.Playbook, error) {
	type playbookMeta struct {
		PlaybookID     string   `yaml:"playbook_id"`
		TargetID       string   `yaml:"target_id"`
		Intent         string   `yaml:"intent"`
		ToolName       string   `yaml:"tool_name"`
		Confidence     float64  `yaml:"confidence"`
		SuccessCount   int      `yaml:"success_count"`
		FailureCount   int      `yaml:"failure_count"`
		LastVerifiedAt string   `yaml:"last_verified_at"`
		LastUsedAt     string   `yaml:"last_used_at"`
		MatchTerms     []string `yaml:"match_terms"`
		Preconditions  []string `yaml:"preconditions"`
		Status         string   `yaml:"status"`
	}
	var meta playbookMeta
	if err := yaml.Unmarshal([]byte(record.Meta), &meta); err != nil {
		return state.Playbook{}, err
	}
	sections := splitSections(record.Body)
	return state.Playbook{
		ID:             strings.TrimSpace(meta.PlaybookID),
		TargetID:       strings.TrimSpace(meta.TargetID),
		Intent:         firstNonEmpty(strings.TrimSpace(meta.Intent), IntentGeneral),
		ToolName:       strings.TrimSpace(meta.ToolName),
		Status:         strings.TrimSpace(meta.Status),
		Title:          firstNonEmpty(record.Title, strings.TrimSpace(meta.Intent)),
		Confidence:     meta.Confidence,
		SuccessCount:   meta.SuccessCount,
		FailureCount:   meta.FailureCount,
		LastVerifiedAt: parseTime(meta.LastVerifiedAt),
		LastUsedAt:     parseTime(meta.LastUsedAt),
		MatchTerms:     dedupeStrings(meta.MatchTerms),
		Preconditions:  dedupeStrings(meta.Preconditions),
		VerifySteps:    parseListSection(sections["Verify"]),
		ActionSteps:    parseListSection(sections["Action"]),
		SuccessChecks:  parseListSection(sections["Success Checks"]),
		Notes:          strings.TrimSpace(sections["Notes"]),
	}, nil
}

func parseFindingRecord(record markdownRecord) (state.Finding, error) {
	type findingMeta struct {
		FindingID  string  `yaml:"finding_id"`
		TargetID   string  `yaml:"target_id"`
		Intent     string  `yaml:"intent"`
		ToolName   string  `yaml:"tool_name"`
		Confidence float64 `yaml:"confidence"`
		SeenCount  int     `yaml:"seen_count"`
		Origin     string  `yaml:"origin"`
		CreatedAt  string  `yaml:"created_at"`
		UpdatedAt  string  `yaml:"updated_at"`
		Status     string  `yaml:"status"`
	}
	var meta findingMeta
	if err := yaml.Unmarshal([]byte(record.Meta), &meta); err != nil {
		return state.Finding{}, err
	}
	return state.Finding{
		ID:         strings.TrimSpace(meta.FindingID),
		TargetID:   strings.TrimSpace(meta.TargetID),
		Intent:     firstNonEmpty(strings.TrimSpace(meta.Intent), IntentGeneral),
		ToolName:   strings.TrimSpace(meta.ToolName),
		Status:     strings.TrimSpace(meta.Status),
		Origin:     strings.TrimSpace(meta.Origin),
		Body:       strings.TrimSpace(record.Body),
		Confidence: meta.Confidence,
		SeenCount:  meta.SeenCount,
		CreatedAt:  parseTime(meta.CreatedAt),
		UpdatedAt:  parseTime(meta.UpdatedAt),
	}, nil
}

func parseCautionRecord(record markdownRecord) (state.Caution, error) {
	type cautionMeta struct {
		CautionID    string  `yaml:"caution_id"`
		TargetID     string  `yaml:"target_id"`
		Intent       string  `yaml:"intent"`
		ToolName     string  `yaml:"tool_name"`
		Confidence   float64 `yaml:"confidence"`
		FailureCount int     `yaml:"failure_count"`
		LastSeenAt   string  `yaml:"last_seen_at"`
		Status       string  `yaml:"status"`
		CreatedAt    string  `yaml:"created_at"`
		UpdatedAt    string  `yaml:"updated_at"`
	}
	var meta cautionMeta
	if err := yaml.Unmarshal([]byte(record.Meta), &meta); err != nil {
		return state.Caution{}, err
	}
	return state.Caution{
		ID:           strings.TrimSpace(meta.CautionID),
		TargetID:     strings.TrimSpace(meta.TargetID),
		Intent:       firstNonEmpty(strings.TrimSpace(meta.Intent), IntentGeneral),
		ToolName:     strings.TrimSpace(meta.ToolName),
		Status:       strings.TrimSpace(meta.Status),
		Body:         strings.TrimSpace(record.Body),
		Confidence:   meta.Confidence,
		FailureCount: meta.FailureCount,
		LastSeenAt:   parseTime(meta.LastSeenAt),
		CreatedAt:    parseTime(meta.CreatedAt),
		UpdatedAt:    parseTime(meta.UpdatedAt),
	}, nil
}

func parseFactsSection(body, hostID string) []state.HostFact {
	sections := splitSections(body)
	return parseFactLines(hostID, sections["Facts"])
}

func parseFactLines(hostID, body string) []state.HostFact {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	now := time.Now().UTC()
	var facts []state.HostFact
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "- "))
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		facts = append(facts, state.HostFact{
			HostID:     hostID,
			Key:        slugify(parts[0]),
			Value:      strings.TrimSpace(parts[1]),
			Confidence: 1,
			VerifiedAt: now,
			UpdatedAt:  now,
		})
	}
	return dedupeFacts(facts)
}

func splitSections(body string) map[string]string {
	body = strings.TrimSpace(body)
	if body == "" {
		return map[string]string{}
	}
	lines := strings.Split(body, "\n")
	out := make(map[string]string)
	current := ""
	var b strings.Builder
	flush := func() {
		if current == "" {
			return
		}
		out[current] = strings.TrimSpace(b.String())
		b.Reset()
	}
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "### ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			continue
		}
		if current == "" {
			if strings.TrimSpace(line) == "" {
				continue
			}
			current = "Body"
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	flush()
	return out
}

func parseListSection(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	var out []string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "- "))
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return dedupeStrings(out)
}

func (m *Manager) writeAllState(ctx context.Context, st fileState, reason string) error {
	st = normalizeState(st)
	rendered := map[string]string{
		TargetsFile:   renderTargetsFile(st),
		HostFile:      renderHostFile(st),
		PlaybooksFile: renderPlaybooksFile(st.Playbooks),
		FindingsFile:  renderFindingsFile(st.Findings),
		CautionsFile:  renderCautionsFile(st.Cautions),
	}
	for name, content := range rendered {
		if err := m.writeManagedFile(ctx, name, m.managedPath(name), content, reason); err != nil {
			return err
		}
	}
	if m.store != nil && m.store.Available() {
		return m.store.ReplaceOperationalMemory(ctx, operationalMemoryFromState(st))
	}
	return nil
}

func (m *Manager) writeManagedFile(ctx context.Context, sourceName, path, rendered, reason string) error {
	current, err := os.ReadFile(path)
	if err == nil && string(current) == rendered {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if _, err := m.snapshotFile(ctx, sourceName, path, reason); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(rendered), 0644)
}

func (m *Manager) snapshotFile(ctx context.Context, sourceName, path, reason string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	snapshotID := fmt.Sprintf("%d-%s", m.now().UnixNano(), strings.TrimSuffix(sourceName, filepath.Ext(sourceName)))
	snapshotPath := filepath.Join(m.dir, "snapshots", snapshotID+"-"+sourceName)
	if err := os.WriteFile(snapshotPath, data, 0644); err != nil {
		return "", err
	}
	if m.store != nil && m.store.Available() {
		_ = m.store.SaveSnapshot(ctx, state.Snapshot{
			ID:         snapshotID,
			SourceFile: sourceName,
			Path:       snapshotPath,
			Reason:     reason,
			CreatedAt:  m.now(),
		})
	}
	return snapshotID, nil
}

func normalizeState(st fileState) fileState {
	sort.SliceStable(st.Targets, func(i, j int) bool {
		return st.Targets[i].Target.PrimaryName < st.Targets[j].Target.PrimaryName
	})
	sort.SliceStable(st.Playbooks, func(i, j int) bool {
		if st.Playbooks[i].TargetID == st.Playbooks[j].TargetID {
			return st.Playbooks[i].Title < st.Playbooks[j].Title
		}
		return st.Playbooks[i].TargetID < st.Playbooks[j].TargetID
	})
	sort.SliceStable(st.Findings, func(i, j int) bool {
		return st.Findings[i].UpdatedAt.After(st.Findings[j].UpdatedAt)
	})
	sort.SliceStable(st.Cautions, func(i, j int) bool {
		return st.Cautions[i].UpdatedAt.After(st.Cautions[j].UpdatedAt)
	})
	for i := range st.Targets {
		st.Targets[i].Aliases = dedupeStrings(st.Targets[i].Aliases)
		st.Targets[i].Hostnames = dedupeStrings(st.Targets[i].Hostnames)
		st.Targets[i].IPs = dedupeStrings(st.Targets[i].IPs)
		st.Targets[i].Facts = dedupeFacts(st.Targets[i].Facts)
	}
	st.RuntimeHostFacts = dedupeFacts(st.RuntimeHostFacts)
	return st
}

func renderTargetsFile(st fileState) string {
	var b strings.Builder
	b.WriteString("# Targets\n\n")
	for i, target := range st.Targets {
		if i > 0 {
			b.WriteString("\n")
		}
		meta := map[string]any{
			"target_id":     target.Target.ID,
			"kind":          target.Target.Kind,
			"primary_name":  target.Target.PrimaryName,
			"aliases":       dedupeStrings(target.Aliases),
			"hostnames":     dedupeStrings(target.Hostnames),
			"ips":           dedupeStrings(target.IPs),
			"transport":     target.Target.Transport,
			"first_seen_at": formatTime(target.Target.FirstSeenAt),
			"last_seen_at":  formatTime(target.Target.LastSeenAt),
			"confidence":    target.Target.Confidence,
			"status":        target.Target.Status,
		}
		b.WriteString("## ")
		b.WriteString(firstNonEmpty(target.Target.PrimaryName, target.Target.ID))
		b.WriteString("\n")
		b.WriteString(renderYAML(meta))
		if len(target.Facts) > 0 {
			b.WriteString("\n### Facts\n")
			for _, fact := range dedupeFacts(target.Facts) {
				if fact.Key == "primary_name" {
					continue
				}
				b.WriteString("- ")
				b.WriteString(fact.Key)
				b.WriteString(": ")
				b.WriteString(fact.Value)
				b.WriteString("\n")
			}
		}
	}
	if len(st.Targets) == 0 {
		return "# Targets\n\n"
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderHostFile(st fileState) string {
	var b strings.Builder
	b.WriteString("# Host\n\n")
	meta := map[string]any{
		"runtime_host_id": st.RuntimeHostID,
		"primary_name":    runtimePrimaryName(st),
		"verified_at":     formatTime(latestFactTime(st.RuntimeHostFacts)),
		"confidence":      1,
		"status":          "active",
	}
	b.WriteString("## ")
	b.WriteString(runtimePrimaryName(st))
	b.WriteString("\n")
	b.WriteString(renderYAML(meta))
	if len(st.RuntimeHostFacts) > 0 {
		b.WriteString("\n### Facts\n")
		for _, fact := range dedupeFacts(st.RuntimeHostFacts) {
			if fact.Key == "primary_name" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(fact.Key)
			b.WriteString(": ")
			b.WriteString(fact.Value)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderPlaybooksFile(playbooks []state.Playbook) string {
	var b strings.Builder
	b.WriteString("# Playbooks\n\n")
	for i, playbook := range playbooks {
		if i > 0 {
			b.WriteString("\n")
		}
		meta := map[string]any{
			"playbook_id":      playbook.ID,
			"target_id":        playbook.TargetID,
			"intent":           playbook.Intent,
			"tool_name":        playbook.ToolName,
			"confidence":       playbook.Confidence,
			"success_count":    playbook.SuccessCount,
			"failure_count":    playbook.FailureCount,
			"last_verified_at": formatTime(playbook.LastVerifiedAt),
			"last_used_at":     formatTime(playbook.LastUsedAt),
			"match_terms":      dedupeStrings(playbook.MatchTerms),
			"preconditions":    dedupeStrings(playbook.Preconditions),
			"status":           playbook.Status,
		}
		b.WriteString("## ")
		b.WriteString(playbook.Title)
		b.WriteString("\n")
		b.WriteString(renderYAML(meta))
		renderListSection(&b, "Verify", playbook.VerifySteps)
		renderListSection(&b, "Action", playbook.ActionSteps)
		renderListSection(&b, "Success Checks", playbook.SuccessChecks)
		if strings.TrimSpace(playbook.Notes) != "" {
			b.WriteString("\n### Notes\n")
			b.WriteString(strings.TrimSpace(playbook.Notes))
			b.WriteString("\n")
		}
	}
	if len(playbooks) == 0 {
		return "# Playbooks\n\n"
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderFindingsFile(findings []state.Finding) string {
	var b strings.Builder
	b.WriteString("# Findings\n\n")
	for i, finding := range findings {
		if i > 0 {
			b.WriteString("\n")
		}
		meta := map[string]any{
			"finding_id": finding.ID,
			"target_id":  finding.TargetID,
			"intent":     finding.Intent,
			"tool_name":  finding.ToolName,
			"confidence": finding.Confidence,
			"seen_count": finding.SeenCount,
			"origin":     finding.Origin,
			"created_at": formatTime(finding.CreatedAt),
			"updated_at": formatTime(finding.UpdatedAt),
			"status":     finding.Status,
		}
		b.WriteString("## ")
		b.WriteString(findingTitle(finding.Body))
		b.WriteString("\n")
		b.WriteString(renderYAML(meta))
		if strings.TrimSpace(finding.Body) != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(finding.Body))
			b.WriteString("\n")
		}
	}
	if len(findings) == 0 {
		return "# Findings\n\n"
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderCautionsFile(cautions []state.Caution) string {
	var b strings.Builder
	b.WriteString("# Cautions\n\n")
	for i, caution := range cautions {
		if i > 0 {
			b.WriteString("\n")
		}
		meta := map[string]any{
			"caution_id":    caution.ID,
			"target_id":     caution.TargetID,
			"intent":        caution.Intent,
			"tool_name":     caution.ToolName,
			"confidence":    caution.Confidence,
			"failure_count": caution.FailureCount,
			"last_seen_at":  formatTime(caution.LastSeenAt),
			"created_at":    formatTime(caution.CreatedAt),
			"updated_at":    formatTime(caution.UpdatedAt),
			"status":        caution.Status,
		}
		b.WriteString("## ")
		b.WriteString(findingTitle(caution.Body))
		b.WriteString("\n")
		b.WriteString(renderYAML(meta))
		if strings.TrimSpace(caution.Body) != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(caution.Body))
			b.WriteString("\n")
		}
	}
	if len(cautions) == 0 {
		return "# Cautions\n\n"
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderListSection(b *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n### ")
	b.WriteString(heading)
	b.WriteString("\n")
	for _, item := range dedupeStrings(items) {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
}

func renderYAML(v any) string {
	data, _ := yaml.Marshal(v)
	return "```yaml\n" + strings.TrimSpace(string(data)) + "\n```\n"
}

func operationalMemoryFromState(st fileState) state.OperationalMemory {
	mem := state.OperationalMemory{
		Playbooks: st.Playbooks,
		Findings:  st.Findings,
		Cautions:  st.Cautions,
		HostFacts: dedupeFacts(st.RuntimeHostFacts),
	}
	for _, target := range st.Targets {
		mem.Targets = append(mem.Targets, target.Target)
		mem.HostFacts = mergeFactLists(mem.HostFacts, target.Facts)
		for _, alias := range target.Aliases {
			mem.TargetAliases = append(mem.TargetAliases, state.TargetAlias{
				TargetID:   target.Target.ID,
				Alias:      alias,
				AliasType:  "alias",
				Confidence: target.Target.Confidence,
				LastSeenAt: target.Target.LastSeenAt,
			})
		}
		for _, alias := range target.Hostnames {
			mem.TargetAliases = append(mem.TargetAliases, state.TargetAlias{
				TargetID:   target.Target.ID,
				Alias:      alias,
				AliasType:  "hostname",
				Confidence: target.Target.Confidence,
				LastSeenAt: target.Target.LastSeenAt,
			})
		}
		for _, alias := range target.IPs {
			mem.TargetAliases = append(mem.TargetAliases, state.TargetAlias{
				TargetID:   target.Target.ID,
				Alias:      alias,
				AliasType:  "ip",
				Confidence: target.Target.Confidence,
				LastSeenAt: target.Target.LastSeenAt,
			})
		}
	}
	return mem
}

func stateFromOperationalMemory(mem state.OperationalMemory) fileState {
	st := fileState{
		Playbooks: mem.Playbooks,
		Findings:  mem.Findings,
		Cautions:  mem.Cautions,
	}
	targetByID := make(map[string]int, len(mem.Targets))
	for _, target := range mem.Targets {
		item := targetRecord{Target: target}
		st.Targets = append(st.Targets, item)
		targetByID[target.ID] = len(st.Targets) - 1
		if target.Kind == TargetKindRuntime && st.RuntimeHostID == "" {
			st.RuntimeHostID = target.ID
		}
	}
	for _, alias := range mem.TargetAliases {
		idx, ok := targetByID[alias.TargetID]
		if !ok {
			continue
		}
		switch alias.AliasType {
		case "hostname":
			st.Targets[idx].Hostnames = append(st.Targets[idx].Hostnames, alias.Alias)
		case "ip":
			st.Targets[idx].IPs = append(st.Targets[idx].IPs, alias.Alias)
		default:
			st.Targets[idx].Aliases = append(st.Targets[idx].Aliases, alias.Alias)
		}
	}
	for _, fact := range mem.HostFacts {
		if fact.HostID == st.RuntimeHostID {
			st.RuntimeHostFacts = append(st.RuntimeHostFacts, fact)
		}
		if idx, ok := targetByID[fact.HostID]; ok {
			st.Targets[idx].Facts = append(st.Targets[idx].Facts, fact)
		}
	}
	return normalizeState(st)
}

func runtimePrimaryName(st fileState) string {
	for _, target := range st.Targets {
		if target.Target.ID == st.RuntimeHostID {
			return firstNonEmpty(target.Target.PrimaryName, target.Target.ID)
		}
	}
	return st.RuntimeHostID
}

func latestFactTime(facts []state.HostFact) time.Time {
	var latest time.Time
	for _, fact := range facts {
		if fact.UpdatedAt.After(latest) {
			latest = fact.UpdatedAt
		}
	}
	if latest.IsZero() {
		latest = time.Now().UTC()
	}
	return latest
}

func defaultOperatorMarkdown() string {
	return `# Operator Guide

Stable runtime guidance for CvkeHarness memory, target identity, and safety boundaries.

## Prompt Stack

Every run receives instructions in this order:

1. Built-in runtime rules
2. operator.md
3. soul.md
4. runtime-host summary from host.md
5. a compact retrieved brief from targets.md, playbooks.md, cautions.md, and findings.md

## Managed Files

- operator.md: Stable harness rules and memory boundaries. User-editable.
- soul.md: Persona and tone only. User-editable and never auto-edited by the harness.
- targets.md: Target registry, aliases, and concise verified target facts.
- host.md: Concise verified profile of the runtime host.
- playbooks.md: Durable target-specific procedures with verify/action/success-check sections.
- findings.md: Provisional observations awaiting promotion.
- cautions.md: Target-specific negative memory for bad or unreliable approaches.

## Memory Boundaries

The runtime must never inject whole managed files into prompt context.
Only a small rendered brief may be loaded:

1. one runtime-host summary
2. at most one target summary
3. at most one primary playbook
4. at most one caution
5. at most one fallback finding when no strong playbook exists

## Memory Ownership

The model may suggest what is worth remembering, but the harness owns ids, timestamps, dedupe, freshness, file layout, and structured persistence.

The execution phase should not dump freeform notes into managed files.
If a concise verified ad hoc note is worth preserving mid-run, use the memory_record_finding tool.

## Dependency Handling

If a task is blocked because a required tool, package, binary, or service is missing:

1. Confirm what is missing from the actual error or a lightweight check.
2. Explain the install or system change you want to make.
3. Ask the user for permission before installing or otherwise mutating the system.
4. Once approved, perform the install yourself instead of only telling the user what to type.
5. Verify the dependency is available and continue the original task.
`
}

func defaultRuntimeHostID(hostname string) string {
	return "runtime-" + shortHash(strings.ToLower(strings.TrimSpace(hostname)))
}

func shortHash(input string) string {
	sum := sha1.Sum([]byte(input))
	return hex.EncodeToString(sum[:])[:12]
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}

func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	ts, _ := time.Parse(time.RFC3339, raw)
	return ts.UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func dedupeStrings(items []string) []string {
	var out []string
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func dedupeFacts(items []state.HostFact) []state.HostFact {
	byKey := make(map[string]state.HostFact, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.HostID + "|" + item.Key)
		if key == "|" {
			continue
		}
		prev, ok := byKey[key]
		if !ok || item.UpdatedAt.After(prev.UpdatedAt) {
			byKey[key] = item
		}
	}
	out := make([]state.HostFact, 0, len(byKey))
	for _, item := range byKey {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HostID == out[j].HostID {
			return out[i].Key < out[j].Key
		}
		return out[i].HostID < out[j].HostID
	})
	return out
}

func mergeFactLists(existing, additions []state.HostFact) []state.HostFact {
	out := append([]state.HostFact{}, existing...)
	for _, item := range additions {
		out = upsertFact(out, item)
	}
	return dedupeFacts(out)
}

func upsertFact(items []state.HostFact, item state.HostFact) []state.HostFact {
	for i, existing := range items {
		if existing.HostID == item.HostID && existing.Key == item.Key {
			if item.UpdatedAt.After(existing.UpdatedAt) || existing.Value == "" {
				items[i] = item
			}
			return items
		}
	}
	return append(items, item)
}

func findingTitle(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "Untitled"
	}
	if idx := strings.IndexAny(body, ".\n"); idx > 0 {
		body = body[:idx]
	}
	body = strings.TrimSpace(body)
	if len(body) > 72 {
		body = strings.TrimSpace(body[:72])
	}
	return body
}

func slugify(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('_')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "_")
}
