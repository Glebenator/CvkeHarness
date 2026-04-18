package tools

// NewDefaultRegistry creates the standard tool registry used by the CLI.
func NewDefaultRegistry(allowedCommands []string) *Registry {
	registry := NewRegistry()
	registry.Register(NewDockerListTool())
	registry.Register(NewDockerInspectTool())
	registry.Register(NewDockerRestartTool())
	registry.Register(NewHTTPHealthcheckTool())
	registry.Register(NewTCPHealthcheckTool())
	registry.Register(NewShellTool(allowedCommands))
	return registry
}

