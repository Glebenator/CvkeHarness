package state

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testSecurityGrant(now time.Time) SecurityActionGrant {
	return SecurityActionGrant{
		Digest:           "action-digest",
		ActionKind:       "shell_execute",
		MaskedSummary:    "rm ./artifact",
		EffectDigest:     "effect-digest",
		PolicyHash:       "policy-hash",
		Host:             "host-a",
		Principal:        "user-a",
		WorkingDirectory: "/work/a",
		Source:           "test",
		ExpiresAt:        now.Add(time.Minute),
		RemainingUses:    1,
		CreatedAt:        now,
	}
}

func TestStateDatabaseIsPrivate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "private", "state.db")
	store := Open(path)
	defer store.Close()
	if !store.Available() {
		t.Fatal(store.Err())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state database permissions=%#o, want 0600", got)
	}
}

func TestSecurityActionGrantIsExactExpiringAndSingleUse(t *testing.T) {
	t.Parallel()
	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	now := time.Now().UTC()
	grant := testSecurityGrant(now)
	if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	mismatch := grant
	mismatch.PolicyHash = "different-policy"
	if consumed, err := store.ConsumeSecurityActionGrant(context.Background(), mismatch, now); err != nil || consumed {
		t.Fatalf("mismatched policy consumed=%v err=%v", consumed, err)
	}
	if consumed, err := store.ConsumeSecurityActionGrant(context.Background(), grant, now); err != nil || !consumed {
		t.Fatalf("exact grant consumed=%v err=%v", consumed, err)
	}
	if consumed, err := store.ConsumeSecurityActionGrant(context.Background(), grant, now); err != nil || consumed {
		t.Fatalf("second use consumed=%v err=%v", consumed, err)
	}

	expired := testSecurityGrant(now.Add(-2 * time.Minute))
	expired.Digest = "expired-action"
	if err := store.SaveSecurityActionGrant(context.Background(), expired); err != nil {
		t.Fatal(err)
	}
	if consumed, err := store.ConsumeSecurityActionGrant(context.Background(), expired, now); err != nil || consumed {
		t.Fatalf("expired grant consumed=%v err=%v", consumed, err)
	}
}

func TestSecurityActionGrantConcurrentConsumeAllowsOneWinner(t *testing.T) {
	t.Parallel()
	store := Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	now := time.Now().UTC()
	grant := testSecurityGrant(now)
	if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consumed, err := store.ConsumeSecurityActionGrant(context.Background(), grant, now)
			if err != nil {
				t.Errorf("consume: %v", err)
				return
			}
			if consumed {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("concurrent winners=%d, want 1", got)
	}
}

func TestSecurityActionGrantRejectsEveryScopeMismatch(t *testing.T) {
	t.Parallel()
	fields := []struct {
		name   string
		mutate func(*SecurityActionGrant)
	}{
		{"digest", func(g *SecurityActionGrant) { g.Digest = "other" }},
		{"action", func(g *SecurityActionGrant) { g.ActionKind = "other" }},
		{"effect", func(g *SecurityActionGrant) { g.EffectDigest = "other" }},
		{"policy", func(g *SecurityActionGrant) { g.PolicyHash = "other" }},
		{"host", func(g *SecurityActionGrant) { g.Host = "other" }},
		{"principal", func(g *SecurityActionGrant) { g.Principal = "other" }},
		{"directory", func(g *SecurityActionGrant) { g.WorkingDirectory = "other" }},
	}
	for _, tc := range fields {
		t.Run(tc.name, func(t *testing.T) {
			store := Open(filepath.Join(t.TempDir(), "state.db"))
			defer store.Close()
			now := time.Now().UTC()
			grant := testSecurityGrant(now)
			if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			candidate := grant
			tc.mutate(&candidate)
			consumed, err := store.ConsumeSecurityActionGrant(context.Background(), candidate, now)
			if err != nil || consumed {
				t.Fatalf("mismatch consumed=%v err=%v", consumed, err)
			}
		})
	}
}
