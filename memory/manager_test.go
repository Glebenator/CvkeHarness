package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestRetrieveUsesModelScopedMemory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: "Model A prefers tighter shell commands.", Provider: "openrouter", Model: "model-a", Phase: core.PhaseExecution, Confidence: 0.9},
		{Body: "Model B benefits from a short recap before acting.", Provider: "openrouter", Model: "model-b", Phase: core.PhaseExecution, Confidence: 0.9},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}

	gotA, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "debug the shell failure",
		TaskClass:   core.TaskClassDebugging,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-a"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(gotA.Learned, "Model A prefers tighter shell commands.") {
		t.Fatalf("expected model-a memory, got %q", gotA.Learned)
	}
	if strings.Contains(gotA.Learned, "Model B benefits") {
		t.Fatalf("unexpected model-b leak in learned context: %q", gotA.Learned)
	}

	gotC, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "debug the shell failure",
		TaskClass:   core.TaskClassDebugging,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-c"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if strings.Contains(gotC.Learned, "Model A prefers") || strings.Contains(gotC.Learned, "Model B benefits") {
		t.Fatalf("expected model-specific memory isolation, got %q", gotC.Learned)
	}
}

func TestEnsureFilesCreatesOperatorGuide(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := NewManager(dir, state.Open(""), 3)

	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, OperatorFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "## Dependency Handling") {
		t.Fatalf("expected operator guide to include dependency handling instructions, got %q", content)
	}
	if !strings.Contains(content, "perform the install yourself") {
		t.Fatalf("expected operator guide to prefer proactive install help, got %q", content)
	}
}

func TestRetrieveIncludesOperatorGuide(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := NewManager(dir, state.Open(""), 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	got, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "inspect runtime instructions",
		TaskClass:   core.TaskClassInspection,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-a"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(got.Operator, "## File Roles") {
		t.Fatalf("expected operator guide to be retrieved, got %q", got.Operator)
	}
	if !strings.Contains(got.Operator, filepath.Join(dir, FindingsFile)) {
		t.Fatalf("expected operator guide to expose the findings path, got %q", got.Operator)
	}
	if !strings.Contains(got.Operator, "memory_record_finding") {
		t.Fatalf("expected operator guide to mention the ad hoc finding tool, got %q", got.Operator)
	}
}

func TestRepeatedCrossModelLessonPromotesToGlobalMemory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	body := "One command per shell_execute call keeps recovery simpler after failures."
	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: body, Provider: "openrouter", Model: "model-a", ToolName: "shell_execute", Phase: core.PhaseExecution, Confidence: 0.8},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}
	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: body, Provider: "openrouter", Model: "model-b", ToolName: "shell_execute", Phase: core.PhaseExecution, Confidence: 0.8},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}

	got, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "run shell diagnostics",
		TaskClass:   core.TaskClassShellHeavy,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-c"),
		ToolNames:   []string{"shell_execute"},
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(got.Learned, body) {
		t.Fatalf("expected promoted global memory, got %q", got.Learned)
	}
}

func TestRollbackRestoresPriorMemorySnapshot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: "First lesson", Provider: "openrouter", Model: "model-a", Phase: core.PhaseExecution, Confidence: 0.7},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}
	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: "Second lesson", Provider: "openrouter", Model: "model-a", Phase: core.PhaseExecution, Confidence: 0.7},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}

	snapshots, err := store.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots returned error: %v", err)
	}
	if len(snapshots) == 0 {
		t.Fatal("expected snapshots to be recorded")
	}

	latestFindingsSnapshot := ""
	for _, snapshot := range snapshots {
		if snapshot.SourceFile == FindingsFile {
			latestFindingsSnapshot = snapshot.ID
			break
		}
	}
	if latestFindingsSnapshot == "" {
		t.Fatal("expected findings snapshot")
	}

	if err := manager.Rollback(context.Background(), latestFindingsSnapshot); err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FindingsFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "First lesson") {
		t.Fatalf("expected restored first lesson, got %q", content)
	}
	if strings.Contains(content, "Second lesson") {
		t.Fatalf("expected second lesson to be rolled back, got %q", content)
	}

	retrieved, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "inspect memory",
		TaskClass:   core.TaskClassInspection,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-a"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if strings.Contains(retrieved.Learned, "Second lesson") {
		t.Fatalf("expected rollback to deactivate stale DB memory, got %q", retrieved.Learned)
	}
}

func TestFileFallbackWorksWhenStateDBUnavailable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := NewManager(dir, state.Open(""), 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}
	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: "Fallback file memory still works.", Provider: "openrouter", Model: "fallback", Phase: core.PhaseExecution, Confidence: 0.8},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}

	got, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "inspect system",
		TaskClass:   core.TaskClassInspection,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "fallback"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(got.Learned, "Fallback file memory still works.") {
		t.Fatalf("expected file-backed retrieval, got %q", got.Learned)
	}
}

func TestRetrieveMatchesActualServedModelWhenRequestedModelIsAlias(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: "Actual GPT best model likes compact refreshed context.", Provider: "openrouter", Model: "gpt-best", Phase: core.PhaseExecution, Confidence: 0.9},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}

	got, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "recover after tool denial",
		TaskClass:   core.TaskClassDebugging,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "auto"),
		ActualModel: core.NewModelRef("openrouter", "gpt-best"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(got.Learned, "Actual GPT best model likes compact refreshed context.") {
		t.Fatalf("expected actual-model lesson to be injected, got %q", got.Learned)
	}
}

func TestReindexMarksDeletedMemoryInactive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: "Lesson to delete", Provider: "openrouter", Model: "model-a", Phase: core.PhaseExecution, Confidence: 0.7},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, FindingsFile), []byte("# Findings\n\n"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := manager.Reindex(context.Background()); err != nil {
		t.Fatalf("Reindex returned error: %v", err)
	}

	got, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "inspect memory",
		TaskClass:   core.TaskClassInspection,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-a"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if strings.Contains(got.Learned, "Lesson to delete") {
		t.Fatalf("expected deleted memory to be inactive after reindex, got %q", got.Learned)
	}
}

func TestPersistLessonsKeepsReadableFindingsWithoutDuplicates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	lesson := Lesson{
		Body:       "Docker is not available in this environment.",
		Scope:      "global",
		Phase:      core.PhaseExecution,
		Confidence: 0.9,
	}
	if err := manager.PersistLessons(context.Background(), []Lesson{lesson}); err != nil {
		t.Fatalf("first PersistLessons returned error: %v", err)
	}
	if err := manager.PersistLessons(context.Background(), []Lesson{lesson}); err != nil {
		t.Fatalf("second PersistLessons returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FindingsFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "<!-- cvkeharness:") {
		t.Fatalf("expected readable findings format, got %q", content)
	}
	if strings.Count(content, "Docker is not available in this environment.") != 1 {
		t.Fatalf("expected duplicate finding to be collapsed, got %q", content)
	}

	entries, err := store.ListMemoryEntries(context.Background(), state.MemoryFilter{
		SourceFiles: []string{FindingsFile},
		OnlyActive:  true,
	})
	if err != nil {
		t.Fatalf("ListMemoryEntries returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 active finding entry, got %d", len(entries))
	}
	if entries[0].SeenCount != 2 {
		t.Fatalf("expected seen_count=2 after repeat, got %d", entries[0].SeenCount)
	}
}

func TestRetrieveOnlyInjectsRelevantGlobalFindings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	if err := manager.PersistLessons(context.Background(), []Lesson{
		{Body: "Docker is not available in this environment.", Scope: "global", Phase: core.PhaseExecution, Confidence: 0.9},
	}); err != nil {
		t.Fatalf("PersistLessons returned error: %v", err)
	}

	unrelated, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "explain the Go test failure",
		TaskClass:   core.TaskClassDebugging,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-a"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if strings.Contains(unrelated.Learned, "Docker is not available") {
		t.Fatalf("expected unrelated task to skip docker finding, got %q", unrelated.Learned)
	}

	related, err := manager.Retrieve(context.Background(), core.RetrievalContext{
		Task:        "debug why docker compose cannot start",
		TaskClass:   core.TaskClassDebugging,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-a"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(related.Learned, "Docker is not available in this environment.") {
		t.Fatalf("expected related task to inject docker finding, got %q", related.Learned)
	}
}
