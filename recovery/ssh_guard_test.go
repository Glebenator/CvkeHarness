package recovery

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestArmedSSHBlocksCompetingRecoveryMutations(t *testing.T) {
	e, root := fixture(t)
	ctx := context.Background()
	path := filepath.Join(root, "config")
	put(t, path, "original")
	op := prepare(t, e, Change{Action: "replace", Path: path, Content: "changed"})
	if err := e.store.PutRecoveryGuard(ctx, e.target, "ssh", json.RawMessage(`{"heartbeat":1}`)); err != nil {
		t.Fatal(err)
	}
	armed := json.RawMessage(`{"id":"pending-ssh"}`)
	if err := e.store.ArmRecoveryGuard(ctx, e.target, "ssh", armed); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, op.ID, op.Digest); err == nil || !strings.Contains(err.Error(), "SSH rollback is armed") {
		t.Fatalf("competing file apply: %v", err)
	}
	if _, err := e.Prepare(ctx, Request{Changes: []Change{{Action: "replace", Path: path, Content: "other"}}}); err == nil {
		t.Fatal("backup preparation can delay an armed SSH deadline")
	}
	if _, err := e.PrepareSnapshot(ctx, "anything"); err == nil || !strings.Contains(err.Error(), "SSH rollback is armed") {
		t.Fatalf("competing snapshot preparation: %v", err)
	}
	content(t, path, "original")
	if _, err := e.Inspect(ctx, op.ID); err != nil {
		t.Fatalf("inspection stranded by armed guard: %v", err)
	}
	if err := e.store.ClearRecoveryGuard(ctx, e.target, "ssh", armed); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, op.ID, op.Digest); err != nil {
		t.Fatal(err)
	}
	content(t, path, "changed")
}
