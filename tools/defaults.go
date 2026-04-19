package tools

import "github.com/coolcake/cvkeharness/provider"

// NewDefaultRegistry creates the standard tool registry used by the CLI.
func NewDefaultRegistry(allowedCommands []string, judge provider.Provider, safetyModel, primaryModel string) *Registry {
	registry := NewRegistry()
	registry.Register(NewShellTool(allowedCommands, judge, safetyModel, primaryModel))
	return registry
}
