package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestEnsureFilesCreatesStructuredMemoryAndStableRuntimeHost(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := NewManager(dir, state.Open(""), 3)
	manager.hostname = func() string { return "builder.local" }

	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	for _, name := range []string{
		OperatorFile,
		SoulFile,
		TargetsFile,
		HostFile,
		PlaybooksFile,
		FindingsFile,
		CautionsFile,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}

	stateA, err := manager.parseManagedFiles()
	if err != nil {
		t.Fatalf("parseManagedFiles returned error: %v", err)
	}
	if stateA.RuntimeHostID == "" {
		t.Fatal("expected runtime host id to be created")
	}

	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("second EnsureFiles returned error: %v", err)
	}
	stateB, err := manager.parseManagedFiles()
	if err != nil {
		t.Fatalf("second parseManagedFiles returned error: %v", err)
	}
	if stateA.RuntimeHostID != stateB.RuntimeHostID {
		t.Fatalf("expected runtime host id to stay stable, got %q then %q", stateA.RuntimeHostID, stateB.RuntimeHostID)
	}
}

func TestResolveTargetCreatesProvisionalSSHRecordAndMergesHostname(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	manager.hostname = func() string { return "runtime.local" }

	ctx := context.Background()
	first, err := manager.ResolveTarget(ctx, TargetResolutionInput{Command: "ssh prod-app systemctl status nginx"})
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}
	if first.TargetKind != TargetKindSSH {
		t.Fatalf("expected ssh target kind, got %q", first.TargetKind)
	}

	if err := manager.CurateRunOutcome(ctx, RunOutcome{
		Task:   "inspect remote host identity",
		Target: first,
		ToolCalls: []ObservedToolCall{
			{
				ToolName: "shell_execute",
				Command:  "ssh prod-app hostname",
				Result:   "web-01.internal\n",
				Success:  true,
			},
		},
	}); err != nil {
		t.Fatalf("CurateRunOutcome returned error: %v", err)
	}

	second, err := manager.ResolveTarget(ctx, TargetResolutionInput{Command: "ssh web-01.internal systemctl status nginx"})
	if err != nil {
		t.Fatalf("second ResolveTarget returned error: %v", err)
	}
	if first.TargetID != second.TargetID {
		t.Fatalf("expected hostname enrichment to merge into one target id, got %q and %q", first.TargetID, second.TargetID)
	}
}

func TestCurateRunOutcomeCreatesDirectUsePlaybook(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	manager.hostname = func() string { return "runtime.local" }

	ctx := context.Background()
	target, err := manager.ResolveTarget(ctx, TargetResolutionInput{Command: "ssh prod-web systemctl status nginx"})
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}

	if err := manager.CurateRunOutcome(ctx, RunOutcome{
		Task:   "restart the nginx service on prod-web",
		Target: target,
		ToolCalls: []ObservedToolCall{
			{ToolName: "shell_execute", Command: "ssh prod-web systemctl status nginx --no-pager", Result: "active\n", Success: true},
			{ToolName: "shell_execute", Command: "ssh prod-web sudo systemctl restart nginx", Result: "", Success: true},
			{ToolName: "shell_execute", Command: "ssh prod-web systemctl is-active nginx", Result: "active\n", Success: true},
		},
	}); err != nil {
		t.Fatalf("CurateRunOutcome returned error: %v", err)
	}

	retrieved, err := manager.Retrieve(ctx, core.RetrievalContext{
		Task:          "restart the nginx service on prod-web",
		TaskClass:     core.TaskClassDebugging,
		Phase:         core.PhaseExecution,
		ActiveModel:   core.NewModelRef("openrouter", "model-a"),
		RuntimeHostID: target.RuntimeHostID,
		TargetID:      target.TargetID,
		TargetKind:    target.TargetKind,
		ToolNames:     []string{"shell_execute"},
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(retrieved.PlaybookBrief, "ssh prod-web sudo systemctl restart nginx") {
		t.Fatalf("expected playbook brief to include restart command, got %q", retrieved.PlaybookBrief)
	}
	if !strings.Contains(retrieved.PlaybookBrief, "direct-use allowed") {
		t.Fatalf("expected fresh successful playbook to be direct-use eligible, got %q", retrieved.PlaybookBrief)
	}
}

func TestStaleOrFailedPlaybookRendersVerifyFirst(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	manager.hostname = func() string { return "runtime.local" }

	ctx := context.Background()
	target, err := manager.ResolveTarget(ctx, TargetResolutionInput{Command: "ssh prod-web systemctl status nginx"})
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}

	if err := manager.CurateRunOutcome(ctx, RunOutcome{
		Task:   "restart nginx on prod-web",
		Target: target,
		ToolCalls: []ObservedToolCall{
			{ToolName: "shell_execute", Command: "ssh prod-web sudo systemctl restart nginx", Result: "", Success: true},
		},
	}); err != nil {
		t.Fatalf("CurateRunOutcome returned error: %v", err)
	}

	memState, err := manager.loadState(ctx)
	if err != nil {
		t.Fatalf("loadState returned error: %v", err)
	}
	if len(memState.Playbooks) != 1 {
		t.Fatalf("expected one playbook, got %d", len(memState.Playbooks))
	}
	memState.Playbooks[0].LastVerifiedAt = time.Now().UTC().Add(-45 * 24 * time.Hour)
	if err := manager.writeAllState(ctx, memState, "test stale playbook"); err != nil {
		t.Fatalf("writeAllState returned error: %v", err)
	}

	retrieved, err := manager.Retrieve(ctx, core.RetrievalContext{
		Task:          "restart nginx on prod-web",
		TaskClass:     core.TaskClassDebugging,
		Phase:         core.PhaseExecution,
		ActiveModel:   core.NewModelRef("openrouter", "model-a"),
		RuntimeHostID: target.RuntimeHostID,
		TargetID:      target.TargetID,
		TargetKind:    target.TargetKind,
		ToolNames:     []string{"shell_execute"},
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(retrieved.PlaybookBrief, "verify-first") {
		t.Fatalf("expected stale playbook to render verify-first, got %q", retrieved.PlaybookBrief)
	}

	if err := manager.CurateRunOutcome(ctx, RunOutcome{
		Task:   "restart nginx on prod-web",
		Target: target,
		ToolCalls: []ObservedToolCall{
			{
				ToolName: "shell_execute",
				Command:  "ssh prod-web sudo systemctl restart nginx",
				Result:   "permission denied",
				Success:  false,
			},
		},
		ExecutionError: "command failed",
	}); err != nil {
		t.Fatalf("CurateRunOutcome failure returned error: %v", err)
	}

	memState, err = manager.loadState(ctx)
	if err != nil {
		t.Fatalf("loadState after failure returned error: %v", err)
	}
	if memState.Playbooks[0].FailureCount == 0 {
		t.Fatal("expected playbook failure count to increase")
	}
	if len(memState.Cautions) == 0 {
		t.Fatal("expected a caution to be created after failure")
	}
}

func TestLegacyMemoryImportsIntoFindingsNeedsCuration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LegacyMemoryFile), []byte("# Memory\n\n- Old durable note\n"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager := NewManager(dir, state.Open(""), 3)
	manager.hostname = func() string { return "runtime.local" }

	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FindingsFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "origin: legacy_memory") {
		t.Fatalf("expected legacy import origin metadata, got %q", content)
	}
	if !strings.Contains(content, "status: needs_curation") {
		t.Fatalf("expected needs_curation status, got %q", content)
	}
}

func TestFileFallbackWorksWithoutSQLite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manager := NewManager(dir, state.Open(""), 3)
	manager.hostname = func() string { return "runtime.local" }

	ctx := context.Background()
	target, err := manager.ResolveTarget(ctx, TargetResolutionInput{Command: "ssh prod-db hostname"})
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}

	if err := manager.CurateRunOutcome(ctx, RunOutcome{
		Task:   "check prod-db hostname",
		Target: target,
		ToolCalls: []ObservedToolCall{
			{ToolName: "shell_execute", Command: "ssh prod-db hostname", Result: "db-01\n", Success: true},
		},
	}); err != nil {
		t.Fatalf("CurateRunOutcome returned error: %v", err)
	}

	retrieved, err := manager.Retrieve(ctx, core.RetrievalContext{
		Task:          "check prod-db hostname",
		TaskClass:     core.TaskClassInspection,
		Phase:         core.PhaseExecution,
		ActiveModel:   core.NewModelRef("openrouter", "model-a"),
		RuntimeHostID: target.RuntimeHostID,
		TargetID:      target.TargetID,
		TargetKind:    target.TargetKind,
		ToolNames:     []string{"shell_execute"},
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(retrieved.TargetSummary, "db-01") {
		t.Fatalf("expected file-backed target facts in retrieval, got %q", retrieved.TargetSummary)
	}
}

func TestSeedRuntimeHostNotesRoundTripsThroughStoreAndRetrieval(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	manager.hostname = func() string { return "builder.local" }

	ctx := context.Background()
	if err := manager.EnsureFiles(); err != nil {
		t.Fatalf("EnsureFiles returned error: %v", err)
	}

	wrote, err := manager.SeedRuntimeHostNotes(ctx, []string{
		"Docker requires sudo",
		"Homebrew lives in /opt/homebrew",
	})
	if err != nil {
		t.Fatalf("SeedRuntimeHostNotes returned error: %v", err)
	}
	if !wrote {
		t.Fatal("expected SeedRuntimeHostNotes to write the initial notes")
	}

	memState, err := manager.loadState(ctx)
	if err != nil {
		t.Fatalf("loadState returned error: %v", err)
	}
	if len(memState.RuntimeHostNotes) != 2 {
		t.Fatalf("expected two runtime host notes after store round-trip, got %d", len(memState.RuntimeHostNotes))
	}

	retrieved, err := manager.Retrieve(ctx, core.RetrievalContext{
		Task:        "list docker containers on this machine",
		TaskClass:   core.TaskClassInspection,
		Phase:       core.PhaseExecution,
		ActiveModel: core.NewModelRef("openrouter", "model-a"),
	})
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if !strings.Contains(retrieved.RuntimeHostSummary, "quirks:") {
		t.Fatalf("expected runtime host summary to include machine quirks, got %q", retrieved.RuntimeHostSummary)
	}
	if !strings.Contains(retrieved.RuntimeHostSummary, "Docker requires sudo") {
		t.Fatalf("expected runtime host summary to include Docker note, got %q", retrieved.RuntimeHostSummary)
	}
	if !strings.Contains(retrieved.RuntimeHostSummary, "Homebrew lives in /opt/homebrew") {
		t.Fatalf("expected runtime host summary to include Homebrew note, got %q", retrieved.RuntimeHostSummary)
	}
}

func TestClassifyIntentTreatsSpeedTestAsNetworkDebug(t *testing.T) {
	t.Parallel()

	if got := classifyIntent("run a speed test and record ping"); got != IntentNetworkDebug {
		t.Fatalf("expected speed test to classify as network debug, got %q", got)
	}
}

func TestSelectCautionSkipsUnrelatedHighFailureCaution(t *testing.T) {
	t.Parallel()

	mem := fileState{
		Cautions: []state.Caution{
			{
				ID:           "docker",
				TargetID:     "runtime",
				Intent:       IntentDockerRecovery,
				ToolName:     "shell_execute",
				Status:       "active",
				Body:         "Docker command was denied.",
				Confidence:   0.95,
				FailureCount: 99,
			},
			{
				ID:           "network",
				TargetID:     "runtime",
				Intent:       IntentNetworkDebug,
				ToolName:     "shell_execute",
				Status:       "active",
				Body:         "Avoid blocked one-off speedtest download pipes.",
				Confidence:   0.8,
				FailureCount: 1,
			},
		},
	}

	got := selectCaution(mem, "runtime", IntentNetworkDebug, "schedule_manage")
	if got == nil || got.ID != "network" {
		t.Fatalf("expected matching network caution, got %#v", got)
	}
}

func TestSelectPlaybookDoesNotMatchGenericShellToolOnly(t *testing.T) {
	t.Parallel()

	mem := fileState{
		Playbooks: []state.Playbook{
			{
				ID:           "health",
				TargetID:     "runtime",
				Intent:       IntentGeneral,
				ToolName:     "shell_execute",
				Status:       "active",
				Title:        "General health check",
				Confidence:   0.95,
				SuccessCount: 10,
			},
		},
	}

	got, strength := selectPlaybook(mem, "runtime", IntentNetworkDebug, "shell_execute")
	if got != nil || strength != 0 {
		t.Fatalf("expected no shell-only playbook match, got playbook=%#v strength=%d", got, strength)
	}
}

func TestSelectFindingSkipsNoisyRunOutcome(t *testing.T) {
	t.Parallel()

	mem := fileState{
		Findings: []state.Finding{
			{
				ID:         "noise",
				TargetID:   "runtime",
				Intent:     IntentNetworkDebug,
				Status:     "active",
				Origin:     "run_outcome",
				Body:       "I’ll do that next.",
				Confidence: 0.65,
				SeenCount:  20,
			},
			{
				ID:         "useful",
				TargetID:   "runtime",
				Intent:     IntentNetworkDebug,
				ToolName:   "shell_execute",
				Status:     "active",
				Origin:     "ad_hoc",
				Body:       "Use the installed speedtest CLI instead of downloading one-off scripts.",
				Confidence: 0.9,
				SeenCount:  1,
			},
		},
	}

	got := selectFinding(mem, "runtime", IntentNetworkDebug, "shell_execute")
	if got == nil || got.ID != "useful" {
		t.Fatalf("expected useful ad hoc finding, got %#v", got)
	}
}

func TestRollbackRestoresFindingsAndReindexes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()

	manager := NewManager(dir, store, 3)
	manager.hostname = func() string { return "runtime.local" }

	ctx := context.Background()
	if err := manager.PersistLessons(ctx, []Lesson{{Body: "First note", Confidence: 0.7}}); err != nil {
		t.Fatalf("PersistLessons first returned error: %v", err)
	}
	if err := manager.PersistLessons(ctx, []Lesson{{Body: "Second note", Confidence: 0.7}}); err != nil {
		t.Fatalf("PersistLessons second returned error: %v", err)
	}

	snapshots, err := store.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots returned error: %v", err)
	}
	var findingsSnapshot string
	for _, snapshot := range snapshots {
		if snapshot.SourceFile == FindingsFile {
			findingsSnapshot = snapshot.ID
			break
		}
	}
	if findingsSnapshot == "" {
		t.Fatal("expected at least one findings snapshot")
	}

	if err := manager.Rollback(ctx, findingsSnapshot); err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FindingsFile))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "First note") {
		t.Fatalf("expected rollback to restore first note, got %q", content)
	}
	if strings.Contains(content, "Second note") {
		t.Fatalf("expected rollback to remove second note, got %q", content)
	}
}
