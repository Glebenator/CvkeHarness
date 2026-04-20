package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strings"

	"github.com/coolcake/cvkeharness/state"
)

// PersistLessons updates findings.md and promotes durable lessons into memory.md.
func (m *Manager) PersistLessons(ctx context.Context, lessons []Lesson) error {
	if len(lessons) == 0 {
		return nil
	}
	if err := m.EnsureFiles(); err != nil {
		return err
	}

	findingsEntries, err := m.parseFile(FindingsFile, m.managedPath(FindingsFile))
	if err != nil {
		return err
	}

	additions := make([]state.MemoryEntry, 0, len(lessons))
	for _, lesson := range lessons {
		entry, ok := m.lessonEntry(FindingsFile, lesson)
		if !ok {
			continue
		}
		additions = append(additions, entry)
		findingsEntries = append([]state.MemoryEntry{entry}, findingsEntries...)
	}
	if len(additions) == 0 {
		return nil
	}

	if err := m.writeManagedFile(ctx, FindingsFile, m.managedPath(FindingsFile), findingsEntries, "update findings"); err != nil {
		return err
	}
	if m.store != nil && m.store.Available() {
		_ = m.store.SaveMemoryEntries(ctx, additions)
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
		Body:       body,
		Normalized: normalizeLesson(body),
		CreatedAt:  now,
		UpdatedAt:  now,
		LastSeenAt: now,
	}, true
}

func (m *Manager) promoteRepeatedLessons(ctx context.Context, additions []state.MemoryEntry) error {
	allEntries, err := m.parseManagedFiles()
	if err != nil {
		return err
	}

	memoryEntries, err := m.parseFile(MemoryFile, m.managedPath(MemoryFile))
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
	if err := m.writeManagedFile(ctx, MemoryFile, m.managedPath(MemoryFile), memoryEntries, "promote durable lessons"); err != nil {
		return err
	}
	if m.store != nil && m.store.Available() {
		_ = m.store.SaveMemoryEntries(ctx, promotions)
	}
	return nil
}

func repeatedLessonStats(entries []state.MemoryEntry, normalized string) (int, map[string]bool) {
	matches := 0
	models := make(map[string]bool)
	for _, existing := range entries {
		if existing.Normalized != normalized {
			continue
		}
		matches++
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
