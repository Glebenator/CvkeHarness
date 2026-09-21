package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

func TestRecoveryToolScopedApprovalAndRemoteRefusal(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "target")
	os.Mkdir(target, 0700)
	s := state.Open(filepath.Join(base, "state", "state.db"))
	defer s.Close()
	policy, err := securitypolicy.Resolve(&securitypolicy.Selection{Profile: securitypolicy.ProfileReasonable})
	if err != nil {
		t.Fatal(err)
	}
	e, err := recovery.New(s, recovery.Options{Roots: []string{target}, Policy: policy.Hash})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.ConfigureSecurityWithStore(policy, NewBlockingApprover(), nil, s)
	r.Register(NewRecoveryManageTool(e))
	ctx := WithExecutionTarget(context.Background(), ExecutionTarget{RuntimeID: "runtime", TargetID: "runtime", Kind: "runtime"})
	path := filepath.Join(target, "config")
	if err = os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	op, err := e.Prepare(ctx, recovery.Request{Changes: []recovery.Change{{Action: "replace", Path: path, Content: "new"}}})
	if err != nil {
		t.Fatal(err)
	}
	a := RecoveryArguments{Action: "apply", ID: op.ID, Digest: op.Digest}
	b, _ := json.Marshal(a)
	call := provider.ToolCall{ID: "recover-call"}
	call.Function.Name = "recovery_manage"
	call.Function.Arguments = string(b)
	if _, err = r.ExecuteTool(ctx, call); err == nil {
		t.Fatal("mutation bypassed approval")
	}
	grant, err := NewToolSecurityGrant(call.Function.Name, call.Function.Arguments, policy, time.Minute, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveSecurityActionGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	remote := WithExecutionTarget(context.Background(), ExecutionTarget{RuntimeID: "runtime", TargetID: "ssh-server", Kind: "ssh"})
	if _, err = r.ExecuteTool(remote, call); err == nil {
		t.Fatal("remote context mutated host")
	}
	// A rejected target must not consume the grant. The local exact action can
	// still consume it once and execute through the normal registry boundary.
	if _, err = r.ExecuteTool(ctx, call); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ExecuteTool(ctx, call); err == nil {
		t.Fatal("one-time grant was reused")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("unexpected target content: %q", got)
	}
}

func TestRecoveryServicePolicyCannotUseFileOnlyGrant(t *testing.T) {
	selection := securitypolicy.DefaultSelection()
	if err := selection.SetOverride(securitypolicy.SettingServiceChanges, string(securitypolicy.DecisionDeny)); err != nil {
		t.Fatal(err)
	}
	policy, err := securitypolicy.Resolve(selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"apply_service", "recover_service", "apply_ssh", "recover_ssh", "confirm_ssh"} {
		b, _ := json.Marshal(RecoveryArguments{Action: action, ID: "id", Digest: "digest"})
		if _, err := NewToolSecurityGrant("recovery_manage", string(b), policy, time.Minute, "test"); err == nil {
			t.Fatalf("%s bypassed denied service policy", action)
		}
	}
}

func TestSSHActionsCannotMixAdaptersOrRewriteAPlan(t *testing.T) {
	for _, raw := range []string{
		`{"action":"prepare","service":"nginx","ssh_service":"ssh"}`,
		`{"action":"apply_ssh","id":"id","ssh_service":"different"}`,
		`{"action":"confirm_ssh","id":"id","changes":[{"action":"delete","path":"/etc/passwd"}]}`,
		`{"action":"ssh_services","ssh_service":"ssh"}`,
	} {
		if _, err := decodeRecovery(json.RawMessage(raw)); err == nil {
			t.Fatalf("mixed SSH action accepted: %s", raw)
		}
	}
	for _, action := range []string{"apply_ssh", "recover_ssh", "confirm_ssh"} {
		found := false
		for _, effect := range classifyToolEffects("recovery_manage", json.RawMessage(`{"action":"`+action+`"}`)) {
			found = found || effect.Setting == securitypolicy.SettingServiceChanges
		}
		if !found {
			t.Fatalf("%s omitted guarded service effects", action)
		}
	}
}

func TestSnapshotActionsAreExplicitAndConservativelyScoped(t *testing.T) {
	for _, raw := range []string{`{"action":"snapshot_prepare","snapshot":"work","changes":[{"action":"delete","path":"/etc/passwd"}]}`, `{"action":"apply_snapshot","id":"id","snapshot":"other"}`, `{"action":"snapshot_restore_prepare","id":"id","service":"nginx"}`} {
		if _, err := decodeRecovery(json.RawMessage(raw)); err == nil {
			t.Fatalf("mixed action accepted: %s", raw)
		}
	}
	selection := securitypolicy.DefaultSelection()
	if err := selection.SetOverride(securitypolicy.SettingFileOverwrite, string(securitypolicy.DecisionDeny)); err != nil {
		t.Fatal(err)
	}
	policy, err := securitypolicy.Resolve(selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"apply_snapshot", "recover_snapshot"} {
		b, _ := json.Marshal(RecoveryArguments{Action: action, ID: "id", Digest: "digest"})
		if _, err := NewToolSecurityGrant("recovery_manage", string(b), policy, time.Minute, "test"); err == nil {
			t.Fatalf("snapshot bypassed denied overwrite: %s", action)
		}
	}
}
