package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if m.store == nil || !m.store.Available() {
		return nil
	}
	entries, err := m.parseManagedFiles()
	if err != nil {
		return err
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
	if err := json.Unmarshal([]byte(metaStr), &meta); err != nil {
		return state.MemoryEntry{}, false
	}
	meta.SourceFile = sourceName
	meta.Body = body
	if meta.Normalized == "" {
		meta.Normalized = normalizeLesson(meta.Body)
	}
	return meta, true
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
	snapshotID, err := m.snapshotFile(ctx, sourceName, path, reason)
	if err != nil {
		return err
	}

	var b strings.Builder
	title := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
	b.WriteString("# ")
	b.WriteString(strings.Title(title))
	b.WriteString("\n\n")

	for _, entry := range entries {
		entry.SourceFile = sourceName
		entry.SnapshotID = snapshotID
		metaBytes, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		b.WriteString("<!-- cvkeharness: ")
		b.Write(metaBytes)
		b.WriteString(" -->\n")
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(entry.Body))
		b.WriteString("\n\n")
	}

	return os.WriteFile(path, []byte(b.String()), 0644)
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
