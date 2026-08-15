package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
)

func TestSummarizeToolOutputMasksTruncatesAndDigests(t *testing.T) {
	raw := "api_key=abcdefghijklmnop " + strings.Repeat("x", 5000)
	inline, original, stored, truncated, digest := SummarizeToolOutput(raw)
	if strings.Contains(inline, "abcdefghijklmnop") || !strings.Contains(inline, "[REDACTED]") {
		t.Fatalf("expected masked inline output, got %q", inline[:min(80, len(inline))])
	}
	if original != int64(len(raw)) || stored != int64(len(inline)) || !truncated || len(digest) != 64 {
		t.Fatalf("unexpected summary metadata original=%d stored=%d truncated=%v digest=%q", original, stored, truncated, digest)
	}
}

func TestToolOutputPersistsWithPrivateStateStorage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private", "state.db")
	store := Open(path)
	if store.Err() != nil {
		t.Fatal(store.Err())
	}
	defer store.Close()
	inline, original, stored, truncated, digest := SummarizeToolOutput("password=supersecretvalue")
	now := time.Now().UTC()
	err := store.RecordRun(context.Background(), RunRecord{
		StartedAt: now, FinishedAt: now, Provider: "test", Task: "tool", TaskClass: core.TaskClassGeneral,
		TaskState: TaskStateCompleted, Success: true, Tools: []ToolOutcome{{
			Phase: core.PhaseExecution, Provider: "test", Model: "model", ToolName: "shell_execute", Success: true,
			OutputInline: inline, OutputOriginalBytes: original, OutputStoredBytes: stored, OutputTruncated: truncated, OutputDigest: digest,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolOutcomes, err := store.listRunTools(context.Background(), 1)
	if err != nil || len(toolOutcomes) != 1 {
		t.Fatalf("expected stored tool outcome, tools=%#v err=%v", toolOutcomes, err)
	}
	if strings.Contains(toolOutcomes[0].OutputInline, "supersecretvalue") || toolOutcomes[0].OutputDigest != digest {
		t.Fatalf("expected redacted durable output metadata, got %#v", toolOutcomes[0])
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("expected private state file, info=%v err=%v", info, err)
	}
}
