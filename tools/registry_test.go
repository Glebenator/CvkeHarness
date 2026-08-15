package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/securitypolicy"
	"github.com/coolcake/cvkeharness/state"
)

func TestDefinitionsForTaskExcludesIrrelevantTools(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.Register(fakeRegistryTool{name: "shell_execute"})
	registry.Register(fakeRegistryTool{name: "memory_record_finding"})
	registry.Register(fakeRegistryTool{name: "schedule_manage"})
	registry.Register(fakeRegistryTool{name: "system_cron_manage"})
	registry.Register(fakeRegistryTool{name: "web_search"})

	got := definitionNames(registry.DefinitionsForTask(core.TaskClassSummarization, "summarize the last run"))
	if len(got) != 0 {
		t.Fatalf("expected summarization turn to exclude irrelevant tools, got %#v", got)
	}

	got = definitionNames(registry.DefinitionsForTask(core.TaskClassInspection, "check service status"))
	if len(got) != 1 || got[0] != "shell_execute" {
		t.Fatalf("expected inspection turn to keep only shell_execute, got %#v", got)
	}

	got = definitionNames(registry.DefinitionsForTask(core.TaskClassGeneral, "edit the user crontab"))
	if len(got) != 1 || got[0] != "system_cron_manage" {
		t.Fatalf("expected explicit crontab turn to keep only system_cron_manage, got %#v", got)
	}
}

func TestRegistryConsumesExactToolGrantOnce(t *testing.T) {
	t.Parallel()
	policy, err := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}
	store := state.Open(filepath.Join(t.TempDir(), "state.db"))
	defer store.Close()
	arguments := `{"action":"add"}`
	grant, err := NewToolSecurityGrant("schedule_manage", arguments, policy, time.Minute, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSecurityActionGrant(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.ConfigureSecurityWithStore(policy, NewBlockingApprover(), nil, store)
	registry.Register(fakeRegistryTool{name: "schedule_manage"})
	call := provider.ToolCall{Function: provider.ToolFunction{Name: "schedule_manage", Arguments: arguments}}
	if _, err := registry.ExecuteTool(context.Background(), call); err != nil {
		t.Fatalf("exact tool grant did not authorize: %v", err)
	}
	if _, err := registry.ExecuteTool(context.Background(), call); err == nil {
		t.Fatal("spent tool grant authorized a second call")
	} else if _, ok := IsApprovalRequired(err); !ok {
		t.Fatalf("second tool call should request approval, got %v", err)
	}
}

func TestRegistryAdvertisementIsNotAuthorization(t *testing.T) {
	t.Parallel()
	policy, err := securitypolicy.Resolve(securitypolicy.DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.ConfigureSecurity(policy, NewBlockingApprover(), nil)
	registry.Register(fakeRegistryTool{name: "schedule_manage"})
	if len(registry.Definitions()) != 1 {
		t.Fatal("expected schedule tool to remain advertised")
	}
	_, err = registry.ExecuteTool(context.Background(), provider.ToolCall{Function: provider.ToolFunction{
		Name: "schedule_manage", Arguments: `{"action":"add"}`,
	}})
	if _, ok := IsApprovalRequired(err); !ok {
		t.Fatalf("reasonable scheduled mutation should require approval, got %v", err)
	}
	if _, err := registry.ExecuteTool(context.Background(), provider.ToolCall{Function: provider.ToolFunction{
		Name: "schedule_manage", Arguments: `{"action":"list"}`,
	}}); err != nil {
		t.Fatalf("schedule list should remain allowed: %v", err)
	}
}

func TestRegistryAppliesProfilesToNonShellTools(t *testing.T) {
	t.Parallel()
	cases := []struct {
		profile securitypolicy.Profile
		wantErr bool
	}{
		{securitypolicy.ProfileExtraStrict, true},
		{securitypolicy.ProfileReasonable, true},
		{securitypolicy.ProfileLessStrict, false},
		{securitypolicy.ProfileMinimal, false},
		{securitypolicy.ProfileYOLO, false},
	}
	for _, tc := range cases {
		policy, err := securitypolicy.Resolve(&securitypolicy.Selection{Version: securitypolicy.SchemaVersion, Profile: tc.profile})
		if err != nil {
			t.Fatal(err)
		}
		registry := NewRegistry()
		registry.ConfigureSecurity(policy, NewBlockingApprover(), nil)
		registry.Register(fakeRegistryTool{name: "schedule_manage"})
		_, err = registry.ExecuteTool(context.Background(), provider.ToolCall{Function: provider.ToolFunction{
			Name: "schedule_manage", Arguments: `{"action":"add"}`,
		}})
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s schedule add error=%v, wantErr=%v", tc.profile, err, tc.wantErr)
		}
	}
}

type fakeRegistryTool struct {
	name string
}

func (t fakeRegistryTool) Name() string                { return t.name }
func (t fakeRegistryTool) Description() string         { return t.name }
func (t fakeRegistryTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t fakeRegistryTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "", nil
}

func definitionNames(defs []provider.ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Function.Name)
	}
	return out
}
