package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

func TestApproveBlockedWorkCreatesOneScopedGrantAndResolvesWait(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	command := "docker system df"
	expected, assessment, err := shellSecurityGrantBinding(command, policy)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Decision != securitypolicy.DecisionAsk {
		t.Fatalf("test command should require approval, got %s: %s", assessment.Decision, assessment.Reason)
	}
	pending, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(ShellArgs{Command: command})
	if err != nil {
		t.Fatal(err)
	}
	continuation, err := json.Marshal(map[string]any{
		"tool_call": provider.ToolCall{
			ID:   "call-1",
			Type: "function",
			Function: provider.ToolFunction{
				Name:      "shell_execute",
				Arguments: string(arguments),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveBlockedWork(ctx, state.BlockedWork{
		ID:                     "blocked-1",
		Task:                   "inspect docker storage",
		TaskClass:              core.TaskClassPolicySensitive,
		TaskState:              state.TaskStateBlockedWaitingUser,
		BlockedReason:          assessment.Reason,
		PendingApprovalType:    "security_action",
		PendingApprovalPayload: string(pending),
		ContinuationData:       string(continuation),
	}); err != nil {
		t.Fatal(err)
	}

	grant, err := ApproveBlockedWork(ctx, store, policy, "blocked-1", 15*time.Minute, "test-tui")
	if err != nil {
		t.Fatal(err)
	}
	if grant.Source != "test-tui" || grant.RemainingUses != 1 || grant.Digest != expected.Digest {
		t.Fatalf("unexpected scoped grant: %#v", grant)
	}
	work, err := store.GetBlockedWork(ctx, "blocked-1")
	if err != nil {
		t.Fatal(err)
	}
	if work.TaskState != state.TaskStateRunning {
		t.Fatalf("blocked work was not resolved for retry: %#v", work)
	}
	if _, err := ApproveBlockedWork(ctx, store, policy, "blocked-1", 15*time.Minute, "test-repeat"); err == nil {
		t.Fatal("resolved work must not mint a second approval grant")
	}
	consumed, err := store.ConsumeSecurityActionGrant(ctx, expected, time.Now().UTC())
	if err != nil || !consumed {
		t.Fatalf("exact approved action did not consume the grant: consumed=%t err=%v", consumed, err)
	}
	consumed, err = store.ConsumeSecurityActionGrant(ctx, expected, time.Now().UTC())
	if err != nil || consumed {
		t.Fatalf("one-use grant authorized a second execution: consumed=%t err=%v", consumed, err)
	}
}

func TestScopedShellGrantExecutesOnceThenReblocks(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	store := state.Open(dbPath)
	defer store.Close()
	policy := resolvedProfile(t, securitypolicy.ProfileReasonable)
	command := "rm -f " + filepath.Join(dir, "absent")
	grant, err := NewShellSecurityGrant(command, policy, 15*time.Minute, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	tool := NewShellToolWithApprovals(nil, nil, NewBlockingApprover(), "", store)
	tool.applySecurityPolicy(policy, NewBlockingApprover(), nil)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":`+jsonQuote(command)+`}`)); err != nil {
		t.Fatalf("one-time grant did not execute: %v", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":`+jsonQuote(command)+`}`)); err == nil {
		t.Fatal("spent grant authorized a second execution")
	} else if _, ok := IsApprovalRequired(err); !ok {
		t.Fatalf("second execution should request approval, got %v", err)
	}
}

func TestShellGrantRefusesDeniedActionAndDoesNotPersistRawSecret(t *testing.T) {
	if _, err := NewShellSecurityGrant("rm -rf ./artifact", resolvedProfile(t, securitypolicy.ProfileExtraStrict), time.Minute, "test"); err == nil {
		t.Fatal("deny decision must not be grantable")
	}
	policy := resolvedProfile(t, securitypolicy.ProfileMinimal)
	secret := "sk-abcdefghijklmnopqrstuvwxyz012345"
	grant, err := NewShellSecurityGrant("opaque-tool --token "+secret, policy, time.Minute, "test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(grant.MaskedSummary, secret) {
		t.Fatalf("grant summary retained raw secret: %q", grant.MaskedSummary)
	}
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store := state.Open(dbPath)
	if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("raw command secret was persisted in the grant database")
	}
}

func TestSessionGrantRebindsTargetEffects(t *testing.T) {
	dir := t.TempDir()
	ordinary := filepath.Join(dir, "ordinary")
	link := filepath.Join(dir, "target")
	if err := os.WriteFile(ordinary, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ordinary, link); err != nil {
		t.Fatal(err)
	}
	selection := securitypolicy.DefaultSelection()
	if err := selection.SetOverride(securitypolicy.SettingRememberApprovals, "true"); err != nil {
		t.Fatal(err)
	}
	policy, err := securitypolicy.Resolve(selection)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewShellToolWithApprover(nil, nil, "")
	tool.applySecurityPolicy(policy, staticApprover{decision: ShellApprovalDecision{Approved: true, Remember: true, Mode: "test"}}, nil)
	command := "printf x > " + link
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":`+jsonQuote(command)+`}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/cvkeharness-session-grant-test", link); err != nil {
		t.Fatal(err)
	}
	tool.humanApprover = NewBlockingApprover()
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":`+jsonQuote(command)+`}`)); err == nil {
		t.Fatal("changed symlink target reused a stale session grant")
	} else if _, ok := IsApprovalRequired(err); !ok {
		t.Fatalf("changed target should request approval, got %v", err)
	}
}

func jsonQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
