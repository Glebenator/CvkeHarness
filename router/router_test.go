package router

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestSelectUsesBestApprovedModelPerPhase(t *testing.T) {
	t.Parallel()

	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	for i := 0; i < 4; i++ {
		recordRun(t, store, core.PhasePlanning, core.TaskClassInspection, "openrouter", "planner-pro", true, false)
		recordRun(t, store, core.PhasePlanning, core.TaskClassInspection, "openrouter", "default", i%2 == 0, false)
	}

	r := New(core.RoutingConfig{
		Enabled:        true,
		Mode:           core.RoutingModeAutoWithinPolicy,
		DefaultModel:   core.NewModelRef("openrouter", "default"),
		ApprovedModels: []core.ModelRef{core.NewModelRef("openrouter", "default"), core.NewModelRef("openrouter", "planner-pro")},
		MinConfidence:  0.55,
	}, store, nil)

	selection, err := r.Select(context.Background(), core.PhasePlanning, "inspect the service", core.TaskClassInspection, nil)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got := selection.Requested.String(); got != "openrouter/planner-pro" {
		t.Fatalf("expected planner-pro, got %s", got)
	}
	if selection.UsedDefault {
		t.Fatal("expected routed selection, got default")
	}
}

func TestSelectRecommendsUnapprovedHighPerformer(t *testing.T) {
	t.Parallel()

	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	for i := 0; i < 4; i++ {
		recordRun(t, store, core.PhaseExecution, core.TaskClassDebugging, "openrouter", "gpt-best", true, false)
		recordRun(t, store, core.PhaseExecution, core.TaskClassDebugging, "openrouter", "default", i < 2, false)
	}

	r := New(core.RoutingConfig{
		Enabled:        true,
		Mode:           core.RoutingModeAutoWithinPolicy,
		DefaultModel:   core.NewModelRef("openrouter", "default"),
		ApprovedModels: []core.ModelRef{core.NewModelRef("openrouter", "default")},
		MinConfidence:  0.55,
	}, store, nil)

	selection, err := r.Select(context.Background(), core.PhaseExecution, "debug the failing deploy", core.TaskClassDebugging, []string{"shell_execute"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if !selection.NeedsApproval {
		t.Fatal("expected approval recommendation for unapproved model")
	}
	if selection.Recommendation == nil || selection.Recommendation.String() != "openrouter/gpt-best" {
		t.Fatalf("expected gpt-best recommendation, got %#v", selection.Recommendation)
	}
	if got := selection.Requested.String(); got != "openrouter/default" {
		t.Fatalf("expected default fallback before approval, got %s", got)
	}
}

func TestSelectFallsBackWhenStatsAreSparse(t *testing.T) {
	t.Parallel()

	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	recordRun(t, store, core.PhaseExecution, core.TaskClassInspection, "openrouter", "candidate", true, false)

	r := New(core.RoutingConfig{
		Enabled:        true,
		Mode:           core.RoutingModeAutoWithinPolicy,
		DefaultModel:   core.NewModelRef("openrouter", "default"),
		ApprovedModels: []core.ModelRef{core.NewModelRef("openrouter", "default"), core.NewModelRef("openrouter", "candidate")},
		MinConfidence:  0.6,
	}, store, nil)

	selection, err := r.Select(context.Background(), core.PhaseExecution, "inspect status", core.TaskClassInspection, []string{"shell_execute"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got := selection.Requested.String(); got != "openrouter/default" {
		t.Fatalf("expected default fallback, got %s", got)
	}
}

func TestPolicyDenialsLowerRoutingScore(t *testing.T) {
	t.Parallel()

	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()

	for i := 0; i < 4; i++ {
		recordRun(t, store, core.PhaseExecution, core.TaskClassPolicySensitive, "openrouter", "careful", true, false)
		recordRun(t, store, core.PhaseExecution, core.TaskClassPolicySensitive, "openrouter", "denied-often", true, true)
	}

	r := New(core.RoutingConfig{
		Enabled:      true,
		Mode:         core.RoutingModeAutoWithinPolicy,
		DefaultModel: core.NewModelRef("openrouter", "denied-often"),
		ApprovedModels: []core.ModelRef{
			core.NewModelRef("openrouter", "denied-often"),
			core.NewModelRef("openrouter", "careful"),
		},
		MinConfidence: 0.55,
	}, store, nil)

	selection, err := r.Select(context.Background(), core.PhaseExecution, "restart a service with approval policy", core.TaskClassPolicySensitive, []string{"shell_execute"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got := selection.Requested.String(); got != "openrouter/careful" {
		t.Fatalf("expected careful model, got %s", got)
	}
}

func recordRun(t *testing.T, store *state.Store, phase core.Phase, taskClass core.TaskClass, providerName, model string, success bool, denied bool) {
	t.Helper()

	now := time.Now().UTC()
	record := state.RunRecord{
		StartedAt:      now,
		FinishedAt:     now.Add(50 * time.Millisecond),
		Provider:       providerName,
		Task:           "test task",
		TaskClass:      taskClass,
		Success:        success,
		RoutingEnabled: true,
		Phases: []state.PhaseRecord{{
			Phase:          phase,
			Provider:       providerName,
			RequestedModel: model,
			ActualModel:    model,
			Success:        success,
			LatencyMs:      100,
		}},
	}
	if phase == core.PhaseExecution {
		record.Tools = []state.ToolOutcome{{
			Phase:        phase,
			Provider:     providerName,
			Model:        model,
			ToolName:     "shell_execute",
			Toolset:      core.ToolsetKey([]string{"shell_execute"}),
			Success:      !denied,
			PolicyDenied: denied,
			DenialClass:  "judge_denial",
			DurationMs:   10,
		}}
	}

	if err := store.RecordRun(context.Background(), record); err != nil {
		t.Fatalf("RecordRun returned error: %v", err)
	}
}
