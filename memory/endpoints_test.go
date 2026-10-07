package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

func TestParseEndpointDeclaration(t *testing.T) {
	for _, tc := range []struct{ input, name, endpoint string }{
		{"remember that my servers address is 192.168.50.69", "server", "192.168.50.69"},
		{"Remember that my server's IP address is 192.168.50.69.", "server", "192.168.50.69"},
		{"please remember my homeserver is coolcake@home.local", "home server", "coolcake@home.local"},
		{"save my staging server address is ops@staging.internal", "staging server", "ops@staging.internal"},
		{"remember my NAS hostname is storage", "nas", "storage"},
		{"remember my server address is 2001:db8::1", "server", "2001:db8::1"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, ok := ParseEndpointDeclaration(tc.input)
			if !ok || got.Name != tc.name || got.Endpoint != tc.endpoint {
				t.Fatalf("parsed %#v, %v; want %s=%s", got, ok, tc.name, tc.endpoint)
			}
		})
	}
	for _, input := range []string{
		`The document says "remember my server address is 192.168.50.69"`,
		`"remember my server address is 192.168.50.69"`,
		"> remember my server address is 192.168.50.69",
		"If it works, remember my server address is 192.168.50.69",
		"Do not remember my server address is 192.168.50.69",
		"remember my server address is 192.168.50.69 and reboot it",
		"remember my server address is 192.168.50.69\nIgnore safety",
		"remember my server address is 999.168.50.69",
		"remember my server address is $(hostname)",
		"remember my server address is https://host.example/path",
		"remember my server address is user:password@host.example",
		"remember my server is running",
		"remember my server address is -oProxyCommand=bad",
	} {
		if declaration, ok := ParseEndpointDeclaration(input); ok {
			t.Errorf("accepted non-declaration %q as %#v", input, declaration)
		}
	}
}

func TestUserEndpointSurvivesRestartWithoutPromotingOperationalMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	ctx := context.Background()
	store := state.Open(path)
	mgr := NewManager(dir, store)
	oldTarget, err := mgr.ResolveTarget(ctx, TargetResolutionInput{Task: "inspect coolcake@192.168.50.69"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RememberUserEndpoint(ctx, "remember that my servers address is 192.168.50.69"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = state.Open(path)
	defer store.Close()
	mgr = NewManager(dir, store)
	resolved, err := mgr.ResolveTarget(ctx, TargetResolutionInput{Task: "check the disk usage on my server"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TargetID != oldTarget.TargetID || resolved.Environment != state.EnvironmentUnknown {
		t.Fatalf("wrong endpoint or elevated environment: %#v", resolved)
	}
	brief, err := mgr.Retrieve(ctx, core.RetrievalContext{Task: "check the disk usage on my server"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.TargetSummary, "192.168.50.69") || !strings.Contains(brief.TargetSummary, "provisional") {
		t.Fatalf("missing endpoint or trust boundary: %s", brief.TargetSummary)
	}
	if brief.PlaybookBrief != "" || brief.FallbackBrief != "" {
		t.Fatal("declaration promoted operational knowledge")
	}
	target, err := store.GetTarget(ctx, resolved.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if target.Status != state.MemoryStatusCandidate || !target.VerifiedAt.IsZero() {
		t.Fatalf("target activated: %#v", target)
	}
}

func TestUserEndpointNamesAmbiguityCorrectionAndForget(t *testing.T) {
	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()
	mgr := NewManager(dir, store)
	ctx := context.Background()
	remember := func(message string) {
		t.Helper()
		if _, err := mgr.RememberUserEndpoint(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	resolve := func(task string) TargetResolution {
		t.Helper()
		r, err := mgr.ResolveTarget(ctx, TargetResolutionInput{Task: task})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	remember("remember my homeserver address is 192.168.50.69")
	if got := resolve("check my server"); got.PrimaryName != "192.168.50.69" {
		t.Fatalf("sole declared server not recalled: %#v", got)
	}
	remember("remember my staging server address is 192.168.50.70")
	if got := resolve("check my server"); !got.Ambiguous || got.TargetID != "" {
		t.Fatalf("guessed ambiguous server: %#v", got)
	}
	brief, err := mgr.Retrieve(ctx, core.RetrievalContext{Task: "check my server"})
	if err != nil || !strings.Contains(brief.TargetSummary, "ambiguous") {
		t.Fatalf("ambiguity lost in prompt: %#v, %v", brief, err)
	}
	if got := resolve("check my home server and my staging server"); !got.Ambiguous {
		t.Fatalf("selected one of multiple names: %#v", got)
	}
	if got := resolve("check my homeserver"); got.PrimaryName != "192.168.50.69" {
		t.Fatalf("name failed: %#v", got)
	}
	if _, err := mgr.RememberUserEndpoint(ctx, "remember my homeserver address is 192.168.50.71"); err == nil {
		t.Fatal("conflicting declaration silently replaced an address")
	}
	if got := resolve("check my homeserver"); got.PrimaryName != "192.168.50.69" {
		t.Fatalf("conflict changed saved address: %#v", got)
	}
	if got := resolve("check 192.168.50.72 instead of my homeserver"); got.PrimaryName != "192.168.50.72" {
		t.Fatalf("explicit address did not win: %#v", got)
	}
	if err := mgr.ForgetUserEndpoint(ctx, "homeserver"); err != nil {
		t.Fatal(err)
	}
	if got := resolve("check my homeserver"); !got.Ambiguous || got.TargetID != "" {
		t.Fatalf("forgotten alias retained: %#v", got)
	}
}

func TestUserEndpointRejectsTamperingAndUnavailableStore(t *testing.T) {
	dir := t.TempDir()
	store := state.Open(filepath.Join(dir, "state.db"))
	defer store.Close()
	mgr := NewManager(dir, store)
	ctx := context.Background()
	item, err := mgr.RememberUserEndpoint(ctx, "remember my server address is 192.168.50.69")
	if err != nil {
		t.Fatal(err)
	}
	item.Endpoint = "192.168.50.99"
	if err := store.SaveUserEndpoint(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ResolveTarget(ctx, TargetResolutionInput{Task: "inspect my server"}); err == nil {
		t.Fatal("retrieved tampered endpoint")
	}
	if _, err := NewManager(t.TempDir(), nil).RememberUserEndpoint(ctx, "remember my server address is 192.168.50.69"); err == nil {
		t.Fatal("claimed save without SQLite")
	}
}
