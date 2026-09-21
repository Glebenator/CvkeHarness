package state

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
)

func TestStatePlatformDatabaseURI(t *testing.T) {
	for _, tc := range []struct{ input, path string }{
		{"C:/Users/test/state space #%/state.db", "/C:/Users/test/state space #%/state.db"},
		{"/tmp/state space ?#%/state.db", "/tmp/state space ?#%/state.db"},
		{"//server/share/state.db", "//server/share/state.db"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			u, err := url.Parse(stateDatabaseDSN(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if u.Scheme != "file" || u.Host != "" || u.Path != tc.path || u.Fragment != "" {
				t.Fatalf("database path changed meaning in URI: %s", u)
			}
			if u.Query().Get("_pragma") != "busy_timeout(5000)" || u.Query().Get("_txlock") != "immediate" {
				t.Fatalf("connection settings lost: %s", u)
			}
		})
	}
}

func TestStatePlatformOpenRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state space #%", "state.db")
	first := Open(path)
	if !first.Available() {
		t.Fatal(first.Err())
	}
	t.Cleanup(func() { first.Close() })
	want := json.RawMessage(`{"sequence":1}`)
	if err := first.PutRecoveryGuard(context.Background(), "test-target", "test-guard", want); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := Open(path)
	t.Cleanup(func() { second.Close() })
	if !second.Available() {
		t.Fatal(second.Err())
	}
	got, _, err := second.RecoveryGuard(context.Background(), "test-target", "test-guard")
	if err != nil || string(got) != string(want) {
		t.Fatalf("state did not survive reopen: %s / %v", got, err)
	}
}
