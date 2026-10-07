package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebenator/cvkeharness/core"
)

func TestNamedConnectionsDoNotContaminateLegacyRoutingStatistics(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	if !store.Available() {
		t.Fatal(store.Err())
	}
	ctx := context.Background()
	phase := PhaseRecord{Connection: "private-server", Phase: core.PhaseExecution, Provider: "lmstudio", RequestedModel: "same-model", ActualModel: "same-model", Success: true}
	if err := store.RecordRun(ctx, RunRecord{StartedAt: time.Now(), FinishedAt: time.Now(), Task: "test", TaskClass: core.TaskClassGeneral, Success: true, Phases: []PhaseRecord{phase}}); err != nil {
		t.Fatal(err)
	}
	phase.Phase = core.PhaseChat
	if err := store.RecordChatPhaseStats(ctx, core.TaskClassGeneral, phase, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := store.ListAllModelStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("named endpoint contaminated provider-only stats: %+v", stats)
	}
	phase.Connection = ""
	if err := store.RecordChatPhaseStats(ctx, core.TaskClassGeneral, phase, nil); err != nil {
		t.Fatal(err)
	}
	stats, err = store.ListAllModelStats(ctx)
	if err != nil || len(stats) != 1 || stats[0].Runs != 1 {
		t.Fatalf("legacy aggregate behavior changed: %+v %v", stats, err)
	}
}
