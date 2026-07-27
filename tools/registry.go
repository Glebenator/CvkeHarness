package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
)

// Tool represents an executable action that an LLM can request.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry manages the set of available tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get finds a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns the provider format for sharing available tools with an LLM.
func (r *Registry) Definitions() []provider.ToolDef {
	return r.definitionsForNames(r.Names())
}

// DefinitionsForTask returns only the tools relevant to one task turn.
func (r *Registry) DefinitionsForTask(taskClass core.TaskClass, task string) []provider.ToolDef {
	var names []string
	lower := strings.ToLower(task)
	for _, name := range r.Names() {
		if toolRelevantForTask(name, taskClass, lower) {
			names = append(names, name)
		}
	}
	return r.definitionsForNames(names)
}

func (r *Registry) definitionsForNames(names []string) []provider.ToolDef {
	var defs []provider.ToolDef
	for _, name := range names {
		t, ok := r.tools[name]
		if !ok {
			continue
		}
		defs = append(defs, provider.ToolDef{
			Type: "function",
			Function: provider.ToolFuncDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return defs
}

// Names returns the registered tool names in stable order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ExecuteTool attempts to find and run a tool based on the model's requested call.
func (r *Registry) ExecuteTool(ctx context.Context, call provider.ToolCall) (string, error) {
	if call.Function.Name == "" {
		return "", fmt.Errorf("tool call missing function name")
	}

	t, ok := r.Get(call.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", call.Function.Name)
	}

	return t.Execute(WithToolCallContext(ctx, call.ID, call.Function.Name), json.RawMessage(call.Function.Arguments))
}

func toolRelevantForTask(name string, taskClass core.TaskClass, lower string) bool {
	switch name {
	case "shell_execute":
		if taskClass == core.TaskClassSummarization {
			return false
		}
		return taskClass != core.TaskClassGeneral || containsAny(lower,
			"shell", "bash", "command", "run ", "execute", "inspect", "status", "check ",
			"debug", "fix", "restart", "deploy", "install", "docker", "service", "ssh ",
			"log", "disk", "cpu", "memory", "file", "process", "port", "container",
		)
	case "memory_record_finding":
		return containsAny(lower, "remember", "record finding", "note this", "save this", "memory")
	case "schedule_manage":
		return containsAny(lower, "schedule", "remind", "recurring", "every ", "daily", "weekly", "job", "health check")
	case "system_cron_manage":
		return containsAny(lower, "system cron", "user cron", "os cron", "crontab")
	case "web_search", "web_fetch":
		return containsAny(lower, "web", "url", "http", "https", "docs", "documentation", "release note", "latest", "current ", "search online", "website", "github issue")
	default:
		return true
	}
}

func containsAny(s string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}
