package chatexport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestWriteMarkdownCreatesPrivateRedactedExport(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "exports")
	now := time.Date(2026, 8, 14, 12, 30, 0, 0, time.UTC)
	detail := state.ChatSessionDetail{
		Session: state.ChatSessionSummary{
			ID:          42,
			StartedAt:   now.Add(-time.Minute),
			Provider:    "openai",
			PinnedModel: "gpt-test",
			TurnCount:   1,
		},
		Turns: []state.ChatTurn{{
			ID:                 7,
			TurnIndex:          1,
			UserInput:          "inspect api_key=super-secret-value",
			FinalOutput:        "done with sk-abcdefghijklmnopqrstuvwxyz123456",
			TaskClass:          core.TaskClassGeneral,
			TaskState:          state.TaskStateCompleted,
			Success:            true,
			RequestedModel:     "gpt-test",
			VerificationStatus: "verified",
			TotalTokens:        12,
		}},
		ToolsByTurnID: map[int64][]state.ToolOutcome{
			7: {{
				ToolName:   "shell_execute",
				Command:    "echo password=another-secret",
				Success:    true,
				DurationMs: 4,
			}},
		},
	}

	path, err := WriteMarkdown(dir, detail, now)
	if err != nil {
		t.Fatalf("WriteMarkdown returned error: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("export path %q is outside %q", path, dir)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat export: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("export mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat export dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("export directory mode = %o, want 700", got)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	text := string(body)
	for _, secret := range []string{"super-secret-value", "sk-abcdefghijklmnopqrstuvwxyz123456", "another-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("export contains unredacted secret %q:\n%s", secret, text)
		}
	}
	for _, want := range []string{"# CvkeHarness chat export", "## Turn 1", "shell_execute: SUCCEEDED", "[REDACTED]", "review it before sharing"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export missing %q:\n%s", want, text)
		}
	}
}

func TestWriteMarkdownAvoidsOverwritingExistingExport(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 8, 14, 12, 30, 0, 0, time.UTC)
	detail := state.ChatSessionDetail{Session: state.ChatSessionSummary{ID: 9}}
	first, err := WriteMarkdown(dir, detail, now)
	if err != nil {
		t.Fatalf("first WriteMarkdown returned error: %v", err)
	}
	second, err := WriteMarkdown(dir, detail, now)
	if err != nil {
		t.Fatalf("second WriteMarkdown returned error: %v", err)
	}
	if first == second {
		t.Fatalf("second export overwrote %q", first)
	}
}

func TestWriteMarkdownRejectsUnpersistedSession(t *testing.T) {
	t.Parallel()

	_, err := WriteMarkdown(t.TempDir(), state.ChatSessionDetail{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "not persisted") {
		t.Fatalf("expected unpersisted-session error, got %v", err)
	}
}

func TestDirectoryForStateDBUsesSiblingExportsDirectory(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "state.db")
	got, err := DirectoryForStateDB(statePath)
	if err != nil {
		t.Fatalf("DirectoryForStateDB returned error: %v", err)
	}
	if want := filepath.Join(filepath.Dir(statePath), "exports"); got != want {
		t.Fatalf("DirectoryForStateDB = %q, want %q", got, want)
	}
}
