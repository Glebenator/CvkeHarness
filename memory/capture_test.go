package memory

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/coolcake/cvkeharness/state"
)

func TestOrdinaryEndpointDeclarationGrammar(t *testing.T) {
	for _, name := range []string{"home server", "home lab backup server", "team's backup server", "nas"} {
		if got := RemoteEndpointName("check disk usage on my " + name); got != name {
			t.Errorf("unknown remote name fell through: %q -> %q", name, got)
		}
	}
	for _, text := range []string{"my home server is 192.168.50.69", "My server is 2001:db8::1.", "my NAS hostname is storage", "my server is ops@staging.local"} {
		if _, ok := ParseEndpointDeclaration(text); !ok {
			t.Errorf("rejected %q", text)
		}
	}
	for _, text := range []string{"my home server is 192.168.50.69?", "is my home server 192.168.50.69", "my home server is not 192.168.50.69", "my home server is maybe 192.168.50.69", "do not remember my home server is 192.168.50.69", `"my home server is 192.168.50.69"`, "‘my home server is 192.168.50.69’", "The guide says my home server is 192.168.50.69", "my home server is 192.168.50.69 for now", "my server is user:secret@host.local", "my server is 192.168.50.69; reboot it"} {
		if _, ok := ParseEndpointDeclaration(text); ok {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestCaptureConcurrentConflictDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()
	mgr := NewManager(dir, store)
	start := make(chan struct{})
	results := make(chan EndpointCapture, 2)
	for _, ip := range []string{"192.168.50.69", "192.168.50.70"} {
		go func() {
			<-start
			results <- mgr.CaptureUserEndpoint(context.Background(), "my home server is "+ip, "turn-"+ip)
		}()
	}
	close(start)
	counts := map[string]int{}
	var winner EndpointCapture
	for range 2 {
		r := <-results
		counts[r.Status]++
		if r.Status == "saved" {
			winner = r
		}
	}
	items, err := mgr.UserEndpoints(context.Background())
	if err != nil || counts["saved"] != 1 || counts["needs_clarification"] != 1 || len(items) != 1 || items[0].Endpoint != winner.Endpoint {
		t.Fatalf("conflicting capture overwrote winner: %v %#v %v", counts, items, err)
	}
}

func TestCaptureDoesNotClaimSuccessWhenCommittedReadbackFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	store := state.Open(path)
	defer store.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Fault injection after INSERT: the transaction commits, but canonical
	// integrity validation cannot verify what was saved.
	if _, err := db.Exec(`CREATE TRIGGER corrupt_capture AFTER INSERT ON user_endpoints BEGIN UPDATE user_endpoints SET evidence_hash='invalid' WHERE name=NEW.name; END`); err != nil {
		t.Fatal(err)
	}
	r := NewManager(dir, store).CaptureUserEndpoint(context.Background(), "my home server is 192.168.50.69", "turn-1")
	if r.Status != "failed" || r.Reason != "canonical readback could not verify the saved address" {
		t.Fatalf("claimed success or rollback after unverifiable commit: %#v", r)
	}
	items, err := store.ListUserEndpoints(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("fault did not exercise committed readback: %#v %v", items, err)
	}
}
func TestCaptureConflictDeduplicationAndForget(t *testing.T) {
	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()
	mgr := NewManager(dir, store)
	ctx := context.Background()
	text := "my home server is 192.168.50.69"
	for _, want := range []string{"saved", "unchanged"} {
		if r := mgr.CaptureUserEndpoint(ctx, text, "turn-1"); r.Status != want {
			t.Fatalf("%#v want %s", r, want)
		}
	}
	if r := mgr.CaptureUserEndpoint(ctx, "my home server is 192.168.50.70", "turn-2"); r.Status != "needs_clarification" {
		t.Fatalf("silently replaced: %#v", r)
	}
	if err := mgr.ForgetUserEndpoint(ctx, "home server"); err != nil {
		t.Fatal(err)
	}
	if r := mgr.CaptureUserEndpoint(ctx, text, "turn-1"); r.Status != "needs_clarification" {
		t.Fatalf("resurrected forgotten endpoint: %#v", r)
	}
	if r := mgr.CaptureUserEndpoint(ctx, text, "turn-3"); r.Status != "saved" {
		t.Fatalf("new declaration failed: %#v", r)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if r := mgr.CaptureUserEndpoint(ctx, text, "turn-4"); r.Status != "failed" {
		t.Fatalf("closed database saved: %#v", r)
	}
}
