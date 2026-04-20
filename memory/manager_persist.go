package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/coolcake/cvkeharness/state"
)

// PersistLessons updates findings.md and promotes durable lessons into memory.md.
func (m *Manager) PersistLessons(ctx context.Context, lessons []Lesson) error {
	lessons = filterPersistableLessons(lessons)
	if len(lessons) == 0 {
		return nil
	}
	if err := m.EnsureFiles(); err != nil {
		return err
	}

	additions := make([]state.MemoryEntry, 0, len(lessons))
	for _, lesson := range lessons {
		entry, ok := m.lessonEntry(FindingsFile, lesson)
		if !ok {
			continue
		}
		additions = append(additions, entry)
	}
	if len(additions) == 0 {
		return nil
	}

	if m.store != nil && m.store.Available() {
		_ = m.store.SaveMemoryEntries(ctx, additions)
	}
	findingsEntries, err := m.loadEntriesForSource(ctx, FindingsFile)
	if err != nil {
		return err
	}
	if m.store == nil || !m.store.Available() {
		findingsEntries = mergeEntries(findingsEntries, additions)
	}
	if err := m.writeManagedFile(ctx, FindingsFile, m.managedPath(FindingsFile), findingsEntries, "update findings"); err != nil {
		return err
	}

	return m.promoteRepeatedLessons(ctx, additions)
}

func (m *Manager) lessonEntry(source string, lesson Lesson) (state.MemoryEntry, bool) {
	body := strings.TrimSpace(lesson.Body)
	if body == "" {
		return state.MemoryEntry{}, false
	}

	now := m.now()
	return state.MemoryEntry{
		ID:         entryID(source, lesson),
		SourceFile: source,
		Scope:      defaultScope(lesson),
		Provider:   lesson.Provider,
		Model:      lesson.Model,
		ToolName:   lesson.ToolName,
		TaskClass:  lesson.TaskClass,
		Phase:      lesson.Phase,
		Status:     "active",
		Confidence: lesson.Confidence,
		SeenCount:  1,
		Body:       body,
		Normalized: normalizeLesson(body),
		CreatedAt:  now,
		UpdatedAt:  now,
		LastSeenAt: now,
	}, true
}

func (m *Manager) promoteRepeatedLessons(ctx context.Context, additions []state.MemoryEntry) error {
	allEntries, err := m.loadEntriesForPromotion(ctx)
	if err != nil {
		return err
	}

	memoryEntries, err := m.loadEntriesForSource(ctx, MemoryFile)
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

		matches, models := repeatedLessonStats(allEntries, add.Normalized)
		if matches < 2 {
			continue
		}

		scope, provider, model := promotedScope(add, models)
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
			SeenCount:  1,
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

	if m.store != nil && m.store.Available() {
		_ = m.store.SaveMemoryEntries(ctx, promotions)
	}
	memoryEntries, err = m.loadEntriesForSource(ctx, MemoryFile)
	if err != nil {
		return err
	}
	if m.store == nil || !m.store.Available() {
		memoryEntries = mergeEntries(memoryEntries, promotions)
	}
	return m.writeManagedFile(ctx, MemoryFile, m.managedPath(MemoryFile), memoryEntries, "promote durable lessons")
}

func repeatedLessonStats(entries []state.MemoryEntry, normalized string) (int, map[string]bool) {
	matches := 0
	models := make(map[string]bool)
	for _, existing := range entries {
		if existing.Normalized != normalized {
			continue
		}
		count := existing.SeenCount
		if count <= 0 {
			count = 1
		}
		matches += count
		if existing.Model != "" {
			models[existing.Provider+"/"+existing.Model] = true
		}
	}
	return matches, models
}

func promotedScope(entry state.MemoryEntry, models map[string]bool) (scope, provider, model string) {
	scope = entry.Scope
	provider = entry.Provider
	model = entry.Model
	if len(models) > 1 {
		return "global", "", ""
	}
	return scope, provider, model
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

func filterPersistableLessons(lessons []Lesson) []Lesson {
	var out []Lesson
	seen := make(map[string]bool, len(lessons))
	for _, lesson := range lessons {
		body := strings.TrimSpace(lesson.Body)
		if body == "" {
			continue
		}
		if lesson.Confidence > 0 && lesson.Confidence < 0.65 {
			continue
		}
		if looksGenericLesson(body) {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(lesson.Scope + "|" + lesson.Provider + "|" + lesson.Model + "|" + lesson.ToolName + "|" + string(lesson.TaskClass) + "|" + body))
		if seen[key] {
			continue
		}
		seen[key] = true
		lesson.Body = body
		out = append(out, lesson)
	}
	return out
}

func looksGenericLesson(body string) bool {
	lower := strings.ToLower(strings.TrimSpace(body))
	genericPrefixes := []string{
		"if ",
		"when ",
		"use ",
		"prefer ",
		"favor ",
		"remember ",
	}
	if strings.Contains(lower, "fails repeatedly") || strings.Contains(lower, "refresh context") || strings.Contains(lower, "simplify the next tool request") {
		return true
	}
	for _, prefix := range genericPrefixes {
		if strings.HasPrefix(lower, prefix) && !strings.Contains(lower, "`") && !strings.Contains(lower, "/") {
			return true
		}
	}
	return false
}

func (m *Manager) loadEntriesForSource(ctx context.Context, sourceName string) ([]state.MemoryEntry, error) {
	if m.store != nil && m.store.Available() {
		return m.store.ListMemoryEntries(ctx, state.MemoryFilter{
			SourceFiles: []string{sourceName},
			OnlyActive:  true,
		})
	}
	return m.parseFile(sourceName, m.managedPath(sourceName))
}

func (m *Manager) loadEntriesForPromotion(ctx context.Context) ([]state.MemoryEntry, error) {
	if m.store != nil && m.store.Available() {
		return m.store.ListMemoryEntries(ctx, state.MemoryFilter{
			SourceFiles: managedSourceFiles(),
			OnlyActive:  true,
		})
	}
	return m.parseManagedFiles()
}

func mergeEntries(existing, additions []state.MemoryEntry) []state.MemoryEntry {
	merged := append([]state.MemoryEntry{}, existing...)
	indexByID := make(map[string]int, len(existing))
	for i, entry := range merged {
		indexByID[entry.ID] = i
	}
	for _, add := range additions {
		if idx, ok := indexByID[add.ID]; ok {
			prev := merged[idx]
			add.CreatedAt = prev.CreatedAt
			add.SeenCount = prev.SeenCount + 1
			merged[idx] = add
			continue
		}
		merged = append(merged, add)
		indexByID[add.ID] = len(merged) - 1
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].UpdatedAt.Equal(merged[j].UpdatedAt) {
			return merged[i].Body < merged[j].Body
		}
		return merged[i].UpdatedAt.After(merged[j].UpdatedAt)
	})
	return merged
}
