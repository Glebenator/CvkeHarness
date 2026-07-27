package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
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
