// Package modelruntime resolves shared model connections for every entrypoint.
package modelruntime

import (
	"fmt"
	"github.com/glebenator/cvkeharness/config"
	"github.com/glebenator/cvkeharness/core"
	"github.com/glebenator/cvkeharness/provider"
)

func NewClient(connection config.Connection) (provider.Provider, error) {
	if err := config.ValidateConnectionDefinition(connection); err != nil {
		return nil, err
	}
	switch connection.Provider {
	case "codex":
		if connection.AuthFile != "" {
			return provider.NewCodexWithAuthPath(connection.AuthFile), nil
		}
		return provider.NewCodexFromCLIAuth(), nil
	case "openrouter":
		return provider.NewOpenRouterWithBaseURL(connection.APIKey, connection.BaseURL), nil
	case "openai":
		if connection.BaseURL != "" {
			return provider.NewOpenAIWithBaseURL(connection.APIKey, connection.BaseURL), nil
		}
		return provider.NewOpenAI(connection.APIKey), nil
	case "lmstudio":
		return provider.NewLMStudioWithAPIKey(connection.BaseURL, connection.APIKey), nil
	}
	return nil, fmt.Errorf("unsupported provider %q", connection.Provider)
}

func ResolveRole(cfg *config.Config, role config.ModelRole) (provider.Provider, config.ResolvedModel, error) {
	resolved, err := cfg.ResolveRole(role)
	if err != nil {
		return nil, resolved, err
	}
	client, err := NewClient(resolved.Connection)
	return client, resolved, err
}

// Ref preserves old provider/model references only for lazy legacy configs.
// Every explicit connection has its own identity, including IDs such as
// "lmstudio" that happen to match the provider kind.
func Ref(resolved config.ResolvedModel) core.ModelRef {
	ref := core.NewModelRef(resolved.Connection.Provider, resolved.Model)
	if !resolved.LegacyConnection || resolved.ConnectionID != resolved.Connection.Provider {
		ref.Connection = resolved.ConnectionID
	}
	return ref
}

func ResolveModel(cfg *config.Config, ref core.ModelRef) (provider.Provider, error) {
	id := ref.Connection
	if id == "" {
		id = ref.Provider
	}
	connection, err := cfg.ConnectionByID(id)
	if err != nil {
		return nil, err
	}
	if connection.Provider != ref.Provider {
		return nil, fmt.Errorf("connection %s uses %s, not %s", id, connection.Provider, ref.Provider)
	}
	return NewClient(connection)
}
