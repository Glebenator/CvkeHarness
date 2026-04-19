package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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

const (
	// File names exposed to the user.
	SoulFile     = "soul.md"
	MemoryFile   = "memory.md"
	FindingsFile = "findings.md"
)

// RetrievalResult contains the system-facing context injected into a run.
type RetrievalResult struct {
	BuiltInRules string
	Soul         string
	Learned      string
	Snippets     []state.MemoryEntry
}

// Lesson is a curated memory candidate.
type Lesson struct {
	Body       string
	Scope      string
	Provider   string
	Model      string
	ToolName   string
	TaskClass  core.TaskClass
	Phase      core.Phase
	Confidence float64
}

// Manager handles readable memory files plus machine metadata.
type Manager struct {
	dir         string
	store       *state.Store
	maxSnippets int
	now         func() time.Time
}

// NewManager creates a new memory manager.
func NewManager(dir string, store *state.Store, maxSnippets int) *Manager {
	if maxSnippets <= 0 {
		maxSnippets = 3
	}
	return &Manager{
		dir:         dir,
		store:       store,
		maxSnippets: maxSnippets,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

// Dir returns the memory directory.
func (m *Manager) Dir() string {
	return m.dir
}

// EnsureFiles creates the user-facing memory files when missing.
func (m *Manager) EnsureFiles() error {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}

	seed := map[string]string{
		filepath.Join(m.dir, SoulFile):     "# Soul\n\n",
		filepath.Join(m.dir, MemoryFile):   "# Memory\n\n",
		filepath.Join(m.dir, FindingsFile): "# Findings\n\n",
	}

	for path, content := range seed {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Join(m.dir, "snapshots"), 0755); err != nil {
		return err
	}
	return nil
}

// Retrieve loads the current soul + learned context for a phase/model/task.
func (m *Manager) Retrieve(ctx context.Context, input core.RetrievalContext) (RetrievalResult, error) {
	if err := m.EnsureFiles(); err != nil {
		return RetrievalResult{}, err
	}

	soulPath := filepath.Join(m.dir, SoulFile)
	soulBytes, err := os.ReadFile(soulPath)
	if err != nil {
		return RetrievalResult{}, err
	}

	entries, err := m.lookupEntries(ctx, input)
	if err != nil {
		return RetrievalResult{}, err
	}

	limit := input.MaxSnippets
	if limit <= 0 {
		limit = m.maxSnippets
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}

	learned := formatLearnedContext(input, entries)
	return RetrievalResult{
		BuiltInRules: builtInRules(),
		Soul:         strings.TrimSpace(string(soulBytes)),
		Learned:      learned,
		Snippets:     entries,
	}, nil
}

func (m *Manager) lookupEntries(ctx context.Context, input core.RetrievalContext) ([]state.MemoryEntry, error) {
	entries := []state.MemoryEntry{}

	if m.store != nil && m.store.Available() {
		var toolName string
		if input.Trouble != nil && input.Trouble.Tool != "" {
			toolName = input.Trouble.Tool
		} else if len(input.ToolNames) > 0 {
			toolName = input.ToolNames[0]
		}

		fromDB, err := m.store.ListMemoryEntries(ctx, state.MemoryFilter{
			SourceFiles: []string{MemoryFile, FindingsFile},
			Phase:       input.Phase,
			TaskClass:   input.TaskClass,
			Provider:    input.ActiveModel.Provider,
			ToolName:    toolName,
			OnlyActive:  true,
		})
		if err == nil && len(fromDB) > 0 {
			entries = append(entries, scoreEntries(input, fromDB)...)
		}
	}

	if len(entries) == 0 {
		fileEntries, err := m.parseManagedFiles()
		if err != nil {
			return nil, err
		}
		entries = scoreEntries(input, fileEntries)
	}

	return entries, nil
}

func scoreEntries(input core.RetrievalContext, entries []state.MemoryEntry) []state.MemoryEntry {
	type scored struct {
		entry state.MemoryEntry
		score float64
	}

	var scoredEntries []scored
	activeModel := strings.TrimSpace(input.ActiveModel.Model)
	actualModel := strings.TrimSpace(input.ActualModel.Model)
	activeProvider := strings.TrimSpace(input.ActiveModel.Provider)
	actualProvider := strings.TrimSpace(input.ActualModel.Provider)

	for _, entry := range entries {
		if entry.Status != "" && entry.Status != "active" {
			continue
		}
		if entry.Provider != "" && !matchesAny(entry.Provider, activeProvider, actualProvider) {
			continue
		}
		if entry.Model != "" && !matchesAny(entry.Model, activeModel, actualModel) {
			continue
		}

		score := entry.Confidence
		if entry.SourceFile == FindingsFile {
			score += 0.2
		}
		if entry.Phase == input.Phase {
			score += 1.2
		}
		if entry.TaskClass != "" && entry.TaskClass == input.TaskClass {
			score += 1.0
		}
		if entry.Provider != "" && entry.Provider == activeProvider {
			score += 1.0
		}
		if entry.Provider != "" && actualProvider != "" && entry.Provider == actualProvider && entry.Provider != activeProvider {
			score += 1.0
		}
		if entry.Model != "" && entry.Model == activeModel {
			score += 1.4
		}
		if entry.Model != "" && actualModel != "" && entry.Model == actualModel && entry.Model != activeModel {
			score += 1.6
		}
		for _, tool := range input.ToolNames {
			if entry.ToolName != "" && entry.ToolName == tool {
				score += 0.8
			}
		}
		if input.Trouble != nil {
			if entry.ToolName != "" && entry.ToolName == input.Trouble.Tool {
				score += 1.0
			}
			if input.Trouble.DenialClass != "" && strings.Contains(strings.ToLower(entry.Body), strings.ToLower(input.Trouble.DenialClass)) {
				score += 0.6
			}
			if input.Trouble.Repeated {
				score += 0.3
			}
		}

		if entry.Scope == "global" {
			score += 0.3
		}
		scoredEntries = append(scoredEntries, scored{entry: entry, score: score})
	}

	sort.SliceStable(scoredEntries, func(i, j int) bool {
		if scoredEntries[i].score == scoredEntries[j].score {
			return scoredEntries[i].entry.UpdatedAt.After(scoredEntries[j].entry.UpdatedAt)
		}
		return scoredEntries[i].score > scoredEntries[j].score
	})

	out := make([]state.MemoryEntry, 0, len(scoredEntries))
	for _, item := range scoredEntries {
		out = append(out, item.entry)
	}
	return out
}

func formatLearnedContext(input core.RetrievalContext, entries []state.MemoryEntry) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("You are currently running as %s.", input.ActiveModel.String()))
	parts = append(parts, fmt.Sprintf("Active phase: %s. Task class: %s.", input.Phase, input.TaskClass))

	if len(entries) == 0 {
		parts = append(parts, "No learned context matched strongly enough to inject for this run.")
		return strings.Join(parts, "\n")
	}

	parts = append(parts, "Learned context:")
	for _, entry := range entries {
		scope := entry.Scope
		if scope == "" {
			scope = "global"
		}
		parts = append(parts, fmt.Sprintf("- [%s] %s", scope, strings.TrimSpace(entry.Body)))
	}
	return strings.Join(parts, "\n")
}

// PersistLessons updates findings.md and promotes durable lessons into memory.md.
func (m *Manager) PersistLessons(ctx context.Context, lessons []Lesson) error {
	if len(lessons) == 0 {
		return nil
	}
	if err := m.EnsureFiles(); err != nil {
		return err
	}

	findingsPath := filepath.Join(m.dir, FindingsFile)
	findingsEntries, err := m.parseFile(FindingsFile, findingsPath)
	if err != nil {
		return err
	}

	var additions []state.MemoryEntry
	for _, lesson := range lessons {
		body := strings.TrimSpace(lesson.Body)
		if body == "" {
			continue
		}

		entry := state.MemoryEntry{
			ID:         entryID(FindingsFile, lesson),
			SourceFile: FindingsFile,
			Scope:      defaultScope(lesson),
			Provider:   lesson.Provider,
			Model:      lesson.Model,
			ToolName:   lesson.ToolName,
			TaskClass:  lesson.TaskClass,
			Phase:      lesson.Phase,
			Status:     "active",
			Confidence: lesson.Confidence,
			Body:       body,
			Normalized: normalizeLesson(body),
			CreatedAt:  m.now(),
			UpdatedAt:  m.now(),
			LastSeenAt: m.now(),
		}
		additions = append(additions, entry)
		findingsEntries = append([]state.MemoryEntry{entry}, findingsEntries...)
	}

	if len(additions) == 0 {
		return nil
	}

	if err := m.writeManagedFile(ctx, FindingsFile, findingsPath, findingsEntries, "update findings"); err != nil {
		return err
	}
	if m.store != nil && m.store.Available() {
		_ = m.store.SaveMemoryEntries(ctx, additions)
	}

	return m.promoteRepeatedLessons(ctx, additions)
}

func (m *Manager) promoteRepeatedLessons(ctx context.Context, additions []state.MemoryEntry) error {
	allEntries, err := m.parseManagedFiles()
	if err != nil {
		return err
	}

	existingMemoryPath := filepath.Join(m.dir, MemoryFile)
	memoryEntries, err := m.parseFile(MemoryFile, existingMemoryPath)
	if err != nil {
		return err
	}

	existingByNormalized := make(map[string]state.MemoryEntry, len(memoryEntries))
	for _, entry := range memoryEntries {
		existingByNormalized[entry.Normalized] = entry
	}

	var promotions []state.MemoryEntry
	for _, add := range additions {
		if _, exists := existingByNormalized[add.Normalized]; exists {
			continue
		}
		matches := 0
		models := make(map[string]bool)
		for _, existing := range allEntries {
			if existing.Normalized != add.Normalized {
				continue
			}
			matches++
			if existing.Model != "" {
				models[existing.Provider+"/"+existing.Model] = true
			}
		}
		if matches < 2 {
			continue
		}

		scope := add.Scope
		provider := add.Provider
		model := add.Model
		if len(models) > 1 {
			scope = "global"
			provider = ""
			model = ""
		}

		promotions = append(promotions, state.MemoryEntry{
			ID:         entryID(MemoryFile, Lesson{Body: add.Body, Scope: scope, Provider: provider, Model: model, ToolName: add.ToolName, TaskClass: add.TaskClass, Phase: add.Phase, Confidence: maxFloat(add.Confidence, 0.75)}),
			SourceFile: MemoryFile,
			Scope:      scope,
			Provider:   provider,
			Model:      model,
			ToolName:   add.ToolName,
			TaskClass:  add.TaskClass,
			Phase:      add.Phase,
			Status:     "active",
			Confidence: maxFloat(add.Confidence, 0.75),
			Body:       add.Body,
			Normalized: add.Normalized,
			CreatedAt:  m.now(),
			UpdatedAt:  m.now(),
			LastSeenAt: m.now(),
		})
	}

	if len(promotions) == 0 {
		return nil
	}

	memoryEntries = append(promotions, memoryEntries...)
	if err := m.writeManagedFile(ctx, MemoryFile, existingMemoryPath, memoryEntries, "promote durable lessons"); err != nil {
		return err
	}
	if m.store != nil && m.store.Available() {
		_ = m.store.SaveMemoryEntries(ctx, promotions)
	}
	return nil
}

// Show returns the user-facing memory files as a readable string.
func (m *Manager) Show(ctx context.Context) (string, error) {
	if err := m.EnsureFiles(); err != nil {
		return "", err
	}

	var sections []string
	for _, name := range []string{SoulFile, MemoryFile, FindingsFile} {
		path := filepath.Join(m.dir, name)
		data, err := os.ReadFile(path)
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
	if err := os.WriteFile(filepath.Join(m.dir, snapshot.SourceFile), data, 0644); err != nil {
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
	return m.store.SyncMemoryEntries(ctx, []string{MemoryFile, FindingsFile}, entries)
}

func (m *Manager) parseManagedFiles() ([]state.MemoryEntry, error) {
	var out []state.MemoryEntry
	for _, name := range []string{MemoryFile, FindingsFile} {
		items, err := m.parseFile(name, filepath.Join(m.dir, name))
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
		metaEnd := strings.Index(part, "-->")
		if metaEnd < 0 {
			continue
		}

		metaStr := strings.TrimSpace(part[:metaEnd])
		bodySection := strings.TrimSpace(part[metaEnd+3:])
		bodyEnd := strings.Index(bodySection, "<!-- cvkeharness:")
		if bodyEnd >= 0 {
			bodySection = strings.TrimSpace(bodySection[:bodyEnd])
		}
		bodyLines := strings.Split(bodySection, "\n")
		var cleaned []string
		for _, line := range bodyLines {
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			cleaned = append(cleaned, line)
			break
		}
		if len(cleaned) == 0 {
			continue
		}

		var meta state.MemoryEntry
		if err := json.Unmarshal([]byte(metaStr), &meta); err != nil {
			continue
		}
		meta.SourceFile = sourceName
		meta.Body = strings.Join(cleaned, " ")
		if meta.Normalized == "" {
			meta.Normalized = normalizeLesson(meta.Body)
		}
		entries = append(entries, meta)
	}

	return entries, nil
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

func builtInRules() string {
	return `You are CvkeHarness.
Keep the runtime rules compact and invariant.
Use tools deliberately, read errors carefully, and adapt before repeating a failed action.`
}

func defaultScope(lesson Lesson) string {
	if lesson.Scope != "" {
		return lesson.Scope
	}
	if lesson.Provider != "" && lesson.Model != "" && lesson.ToolName != "" {
		return "model_tool"
	}
	if lesson.Provider != "" && lesson.Model != "" {
		return "model"
	}
	if lesson.ToolName != "" {
		return "tool"
	}
	if lesson.TaskClass != "" {
		return "task_class"
	}
	return "global"
}

func entryID(source string, lesson Lesson) string {
	sum := sha1.Sum([]byte(source + "|" + normalizeLesson(lesson.Body) + "|" + lesson.Provider + "|" + lesson.Model + "|" + lesson.ToolName + "|" + string(lesson.Phase) + "|" + string(lesson.TaskClass)))
	return hex.EncodeToString(sum[:])
}

func normalizeLesson(body string) string {
	body = strings.ToLower(strings.TrimSpace(body))
	replacer := strings.NewReplacer(",", "", ".", "", ":", "", ";", "", "`", "", "\"", "", "'", "")
	body = replacer.Replace(body)
	body = strings.Join(strings.Fields(body), " ")
	return body
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func matchesAny(value string, options ...string) bool {
	for _, option := range options {
		if option != "" && value == option {
			return true
		}
	}
	return false
}
