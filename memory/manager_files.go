package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

// EnsureFiles creates the user-facing memory files when missing.
func (m *Manager) EnsureFiles() error {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}

	seed := map[string]string{
		m.managedPath(OperatorFile): defaultOperatorMarkdown(),
		m.managedPath(SoulFile):     "# Soul\n\n",
		m.managedPath(MemoryFile):   "# Memory\n\n",
		m.managedPath(FindingsFile): "# Findings\n\n",
	}

	for path, content := range seed {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
	}

	return os.MkdirAll(filepath.Join(m.dir, "snapshots"), 0755)
}

// Show returns the user-facing memory files as a readable string.
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

// Reindex rebuilds memory metadata from the managed markdown files.
func (m *Manager) Reindex(ctx context.Context) error {
	entriesBySource := make(map[string][]state.MemoryEntry, len(managedSourceFiles()))
	for _, name := range managedSourceFiles() {
		path := m.managedPath(name)
		items, err := m.parseFile(name, path)
		if err != nil {
			return err
		}
		entriesBySource[name] = items
		if err := m.writeManagedFile(ctx, name, path, items, "normalize memory file format"); err != nil {
			return err
		}
	}

	if m.store == nil || !m.store.Available() {
		return nil
	}

	var entries []state.MemoryEntry
	for _, name := range managedSourceFiles() {
		entries = append(entries, entriesBySource[name]...)
	}
	return m.store.SyncMemoryEntries(ctx, managedSourceFiles(), entries)
}

func (m *Manager) parseManagedFiles() ([]state.MemoryEntry, error) {
	var out []state.MemoryEntry
	for _, name := range managedSourceFiles() {
		items, err := m.parseFile(name, m.managedPath(name))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func (m *Manager) parseFile(sourceName, path string) ([]state.MemoryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	content := string(data)
	if strings.Contains(content, "<!-- cvkeharness:") {
		return parseLegacyManagedEntries(sourceName, content)
	}
	return parseReadableManagedEntries(sourceName, content, fileTimestamp(path))
}

func parseLegacyManagedEntries(sourceName, content string) ([]state.MemoryEntry, error) {
	parts := strings.Split(content, "<!-- cvkeharness:")
	var entries []state.MemoryEntry
	for _, part := range parts[1:] {
		entry, ok := parseManagedEntry(sourceName, part)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func parseManagedEntry(sourceName, raw string) (state.MemoryEntry, bool) {
	metaEnd := strings.Index(raw, "-->")
	if metaEnd < 0 {
		return state.MemoryEntry{}, false
	}

	metaStr := strings.TrimSpace(raw[:metaEnd])
	bodySection := strings.TrimSpace(raw[metaEnd+3:])
	if bodyEnd := strings.Index(bodySection, "<!-- cvkeharness:"); bodyEnd >= 0 {
		bodySection = strings.TrimSpace(bodySection[:bodyEnd])
	}

	body := firstManagedBodyLine(bodySection)
	if body == "" {
		return state.MemoryEntry{}, false
	}

	var meta state.MemoryEntry
	if err := parseLegacyMeta(metaStr, &meta); err != nil {
		return state.MemoryEntry{}, false
	}
	meta.SourceFile = sourceName
	meta.Body = body
	if meta.Normalized == "" {
		meta.Normalized = normalizeLesson(meta.Body)
	}
	if meta.SeenCount <= 0 {
		meta.SeenCount = 1
	}
	return meta, true
}

func parseLegacyMeta(raw string, entry *state.MemoryEntry) error {
	type legacyMeta struct {
		ID         string
		SourceFile string
		Scope      string
		Provider   string
		Model      string
		ToolName   string
		TaskClass  string
		Phase      string
		Status     string
		Confidence float64
		Body       string
		Normalized string
		SnapshotID string
		CreatedAt  string
		UpdatedAt  string
		LastSeenAt string
		SeenCount  int
	}

	var meta legacyMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return err
	}

	entry.ID = meta.ID
	entry.SourceFile = meta.SourceFile
	entry.Scope = meta.Scope
	entry.Provider = meta.Provider
	entry.Model = meta.Model
	entry.ToolName = meta.ToolName
	entry.TaskClass = core.TaskClass(meta.TaskClass)
	entry.Phase = core.Phase(meta.Phase)
	entry.Status = meta.Status
	entry.Confidence = meta.Confidence
	entry.Body = meta.Body
	entry.Normalized = meta.Normalized
	entry.SnapshotID = meta.SnapshotID
	entry.SeenCount = meta.SeenCount
	entry.CreatedAt = parseTime(meta.CreatedAt)
	entry.UpdatedAt = parseTime(meta.UpdatedAt)
	entry.LastSeenAt = parseTime(meta.LastSeenAt)
	return nil
}

func firstManagedBodyLine(bodySection string) string {
	for _, line := range strings.Split(bodySection, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func (m *Manager) writeManagedFile(ctx context.Context, sourceName, path string, entries []state.MemoryEntry, reason string) error {
	rendered := renderManagedFile(sourceName, entries)
	current, err := os.ReadFile(path)
	if err == nil && string(current) == rendered {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	snapshotID, err := m.snapshotFile(ctx, sourceName, path, reason)
	if err != nil {
		return err
	}
	_ = snapshotID
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

func renderManagedFile(sourceName string, entries []state.MemoryEntry) string {
	title := managedFileTitle(sourceName)
	entries = dedupeEntries(entries)
	if len(entries) == 0 {
		return "# " + title + "\n\n"
	}

	sections := make(map[string][]state.MemoryEntry)
	for _, entry := range entries {
		key := sectionKey(entry)
		sections[key] = append(sections[key], entry)
	}

	keys := make([]string, 0, len(sections))
	for key := range sections {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		return sectionSortRank(keys[i]) < sectionSortRank(keys[j])
	})

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")

	for i, key := range keys {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## ")
		b.WriteString(sectionHeading(key))
		b.WriteString("\n")

		items := sections[key]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
				return items[i].Body < items[j].Body
			}
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
		for _, entry := range items {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(entry.Body))
			b.WriteString("\n")
		}
	}

	return b.String()
}

func dedupeEntries(entries []state.MemoryEntry) []state.MemoryEntry {
	byID := make(map[string]state.MemoryEntry, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			entry.ID = entryID(entry.SourceFile, Lesson{
				Body:      entry.Body,
				Scope:     entry.Scope,
				Provider:  entry.Provider,
				Model:     entry.Model,
				ToolName:  entry.ToolName,
				TaskClass: entry.TaskClass,
				Phase:     entry.Phase,
			})
		}
		prev, exists := byID[entry.ID]
		if !exists || entry.UpdatedAt.After(prev.UpdatedAt) {
			byID[entry.ID] = entry
		}
	}

	out := make([]state.MemoryEntry, 0, len(byID))
	for _, entry := range byID {
		out = append(out, entry)
	}
	return out
}

func managedFileTitle(sourceName string) string {
	switch sourceName {
	case MemoryFile:
		return "Memory"
	case FindingsFile:
		return "Findings"
	default:
		title := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
		return strings.ToUpper(title[:1]) + title[1:]
	}
}

func parseReadableManagedEntries(sourceName, content string, fallbackTime time.Time) ([]state.MemoryEntry, error) {
	var entries []state.MemoryEntry
	section := managedSection{Scope: "global"}

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case line == "" || strings.HasPrefix(line, "# "):
			continue
		case strings.HasPrefix(line, "## "):
			section = parseManagedSection(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		case strings.HasPrefix(line, "- "):
			body := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if body == "" {
				continue
			}
			entry := state.MemoryEntry{
				ID:         entryID(sourceName, Lesson{Body: body, Scope: section.Scope, Provider: section.Provider, Model: section.Model, ToolName: section.ToolName, TaskClass: section.TaskClass, Phase: core.PhaseExecution}),
				SourceFile: sourceName,
				Scope:      section.Scope,
				Provider:   section.Provider,
				Model:      section.Model,
				ToolName:   section.ToolName,
				TaskClass:  section.TaskClass,
				Phase:      core.PhaseExecution,
				Status:     "active",
				Confidence: defaultConfidenceForSource(sourceName),
				SeenCount:  1,
				Body:       body,
				Normalized: normalizeLesson(body),
				CreatedAt:  fallbackTime,
				UpdatedAt:  fallbackTime,
				LastSeenAt: fallbackTime,
			}
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

type managedSection struct {
	Scope     string
	Provider  string
	Model     string
	ToolName  string
	TaskClass core.TaskClass
}

func parseManagedSection(heading string) managedSection {
	switch {
	case strings.EqualFold(heading, "Global"):
		return managedSection{Scope: "global"}
	case strings.HasPrefix(heading, "Tool: "):
		return managedSection{Scope: "tool", ToolName: strings.TrimSpace(strings.TrimPrefix(heading, "Tool: "))}
	case strings.HasPrefix(heading, "Model: "):
		ref := core.ParseModelRef(strings.TrimSpace(strings.TrimPrefix(heading, "Model: ")), "")
		return managedSection{Scope: "model", Provider: ref.Provider, Model: ref.Model}
	case strings.HasPrefix(heading, "Model Tool: "):
		rest := strings.TrimSpace(strings.TrimPrefix(heading, "Model Tool: "))
		parts := strings.SplitN(rest, "|", 2)
		ref := core.ParseModelRef(strings.TrimSpace(parts[0]), "")
		toolName := ""
		if len(parts) == 2 {
			toolName = strings.TrimSpace(parts[1])
		}
		return managedSection{Scope: "model_tool", Provider: ref.Provider, Model: ref.Model, ToolName: toolName}
	case strings.HasPrefix(heading, "Task Class: "):
		return managedSection{Scope: "task_class", TaskClass: core.TaskClass(strings.TrimSpace(strings.TrimPrefix(heading, "Task Class: ")))}
	default:
		return managedSection{Scope: "global"}
	}
}

func sectionKey(entry state.MemoryEntry) string {
	scope := entry.Scope
	if scope == "" {
		scope = defaultScope(Lesson{
			Provider:  entry.Provider,
			Model:     entry.Model,
			ToolName:  entry.ToolName,
			TaskClass: entry.TaskClass,
		})
	}

	switch scope {
	case "tool":
		return "tool|" + entry.ToolName
	case "model":
		return "model|" + core.NewModelRef(entry.Provider, entry.Model).String()
	case "model_tool":
		return "model_tool|" + core.NewModelRef(entry.Provider, entry.Model).String() + "|" + entry.ToolName
	case "task_class":
		return "task_class|" + string(entry.TaskClass)
	default:
		return "global"
	}
}

func sectionHeading(key string) string {
	switch {
	case key == "global":
		return "Global"
	case strings.HasPrefix(key, "tool|"):
		return "Tool: " + strings.TrimPrefix(key, "tool|")
	case strings.HasPrefix(key, "model|"):
		return "Model: " + strings.TrimPrefix(key, "model|")
	case strings.HasPrefix(key, "model_tool|"):
		rest := strings.TrimPrefix(key, "model_tool|")
		parts := strings.SplitN(rest, "|", 2)
		if len(parts) == 2 {
			return "Model Tool: " + parts[0] + " | " + parts[1]
		}
		return "Model Tool: " + rest
	case strings.HasPrefix(key, "task_class|"):
		return "Task Class: " + strings.TrimPrefix(key, "task_class|")
	default:
		return "Global"
	}
}

func sectionSortRank(key string) int {
	switch {
	case key == "global":
		return 0
	case strings.HasPrefix(key, "task_class|"):
		return 1
	case strings.HasPrefix(key, "tool|"):
		return 2
	case strings.HasPrefix(key, "model|"):
		return 3
	case strings.HasPrefix(key, "model_tool|"):
		return 4
	default:
		return 5
	}
}

func fileTimestamp(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Now().UTC()
	}
	return info.ModTime().UTC()
}

func defaultConfidenceForSource(sourceName string) float64 {
	if sourceName == MemoryFile {
		return 0.8
	}
	return 0.65
}

func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
