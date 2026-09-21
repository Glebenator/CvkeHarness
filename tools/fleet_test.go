package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/recovery"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

func TestFleetPolicyIncludesRemoteMutationAndCredentialUse(t *testing.T) {
	for _, setting := range []string{securitypolicy.SettingRemoteMutation, securitypolicy.SettingCredentialAccess, securitypolicy.SettingServiceChanges, securitypolicy.SettingNetworkAccess} {
		selection := securitypolicy.DefaultSelection()
		if err := selection.SetOverride(securitypolicy.SettingCredentialAccess, "ask"); err != nil {
			t.Fatal(err)
		}
		if err := selection.SetOverride(setting, "deny"); err != nil {
			t.Fatal(err)
		}
		policy, err := securitypolicy.Resolve(selection)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"apply", "recover"} {
			raw, _ := json.Marshal(FleetArguments{Action: action, ID: "id", Digest: "digest"})
			if _, err := NewToolSecurityGrant("recovery_fleet", string(raw), policy, time.Minute, "test"); err == nil {
				t.Fatalf("%s bypassed %s denial", action, setting)
			}
		}
	}
}
func TestFleetArgumentsCannotSupplyTransportOrWidenBatch(t *testing.T) {
	for _, raw := range []string{`{"action":"apply","id":"id","digest":"digest","operations":[{"host":"host","id":"id","digest":"digest"}]}`, `{"action":"prepare","operations":[{"host":"host","id":"id","digest":"digest","address":"127.0.0.1"}]}`, `{"action":"hosts","digest":"digest"}`, `{"action":"apply","id":"id","digest":"digest","max_hosts":100}`, `{"action":"inspect","id":"id","digest":"digest"}`} {
		if _, err := decodeFleet(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestFleetRequiresPolicyAndTrustedControllerContext(t *testing.T) {
	s := state.Open(filepath.Join(t.TempDir(), "state", "state.db"))
	defer s.Close()
	e, err := recovery.New(s, recovery.Options{})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewRecoveryFleetTool(e)
	for _, ctx := range []context.Context{context.Background(), WithExecutionTarget(context.Background(), ExecutionTarget{RuntimeID: "local", TargetID: "server", Kind: "ssh"})} {
		if _, err = tool.Execute(ctx, json.RawMessage(`{"action":"hosts"}`)); err == nil {
			t.Fatal("missing/remote controller context allowed")
		}
	}
	r := NewRegistry()
	r.Register(tool)
	call := provider.ToolCall{ID: "fleet"}
	call.Function.Name = "recovery_fleet"
	call.Function.Arguments = `{"action":"hosts"}`
	if _, err = r.ExecuteTool(WithExecutionTarget(context.Background(), ExecutionTarget{RuntimeID: "local", TargetID: "local", Kind: "runtime"}), call); err == nil {
		t.Fatal("policy-free registry used fleet")
	}
}
