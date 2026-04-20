package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/state"
)

func TestMemoryRecordFindingTool_WritesFinding(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := memory.NewManager(dir, store, 3)
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	tool := NewMemoryRecordFindingTool(manager)
	args, err := json.Marshal(map[string]any{
		"body":       "Homebrew may be required to install Docker on this machine.",
		"confidence": 0.8,
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result, filepath.Join(dir, memory.FindingsFile)) {
		t.Fatalf("expected result to mention findings path, got %q", result)
	}

	data, err := os.ReadFile(filepath.Join(dir, memory.FindingsFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "Homebrew may be required to install Docker on this machine.") {
		t.Fatalf("expected finding to be persisted, got %q", string(data))
	}
}

func TestMemoryRecordFindingTool_RequiresToolNameForToolScope(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := memory.NewManager(dir, state.Open(""), 3)
	tool := NewMemoryRecordFindingTool(manager)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"body":"Shell needs smaller steps.","scope":"tool"}`))
	if err == nil {
		t.Fatal("expected missing tool_name to be rejected")
	}
	if !strings.Contains(err.Error(), "tool_name is required") {
		t.Fatalf("expected tool_name validation error, got %v", err)
	}
}

func TestDefaultRegistryWithStoreAndMemory_RegistersMemoryTool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := memory.NewManager(dir, state.Open(""), 3)
	registry := NewDefaultRegistryWithStoreAndMemory([]string{"pwd"}, nil, manager, nil, SafetyModeUserConfirm, "", "primary")

	if _, ok := registry.Get("memory_record_finding"); !ok {
		t.Fatal("expected memory_record_finding tool to be registered")
	}
}
