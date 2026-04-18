package tools

import (
	"context"
	"encoding/json"
	"fmt"

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
	var defs []provider.ToolDef
	for _, t := range r.tools {
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

// ExecuteTool attempts to find and run a tool based on the model's requested call.
func (r *Registry) ExecuteTool(ctx context.Context, call provider.ToolCall) (string, error) {
	if call.Function.Name == "" {
		return "", fmt.Errorf("tool call missing function name")
	}

	t, ok := r.Get(call.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", call.Function.Name)
	}

	return t.Execute(ctx, json.RawMessage(call.Function.Arguments))
}
