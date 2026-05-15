package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

type providerResolver struct {
	cfg *config.Config
}

func (r providerResolver) Resolve(providerName string) (provider.Provider, error) {
	return resolveProvider(r.cfg, providerName)
}

func resolveProvider(cfg *config.Config, providerName string) (provider.Provider, error) {
	name := strings.TrimSpace(providerName)
	if name == "" {
		name = strings.TrimSpace(cfg.Provider)
	}

	switch name {
	case "codex":
		return provider.NewCodexFromCLIAuth(), nil
	case "openrouter":
		return provider.NewOpenRouter(cfg.GetAPIKey("openrouter")), nil
	case "openai":
		return provider.NewOpenAI(cfg.GetAPIKey("openai")), nil
	case "lmstudio":
		return provider.NewLMStudio(cfg.BaseURL), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", name)
	}
}

func routingConfigFromConfig(cfg *config.Config, store *state.Store) core.RoutingConfig {
	approved := make([]core.ModelRef, 0, len(cfg.ApprovedModels))
	for _, raw := range cfg.ApprovedModels {
		ref := core.ParseModelRef(raw, cfg.Provider)
		if ref.IsZero() {
			continue
		}
		approved = append(approved, ref)
	}

	if store != nil && store.Available() {
		if approvals, err := store.ListApprovedModelApprovals(context.Background()); err == nil {
			for _, approval := range approvals {
				ref := core.NewModelRef(approval.Provider, approval.Model)
				if ref.IsZero() {
					continue
				}
				approved = append(approved, ref)
			}
		}
	}

	defaultRef := core.NewModelRef(cfg.Provider, cfg.PrimaryModel())
	seenDefault := false
	for _, ref := range approved {
		if ref.Equal(defaultRef) {
			seenDefault = true
			break
		}
	}
	if !seenDefault && !defaultRef.IsZero() {
		approved = append(approved, defaultRef)
	}

	phaseModels := map[core.Phase]core.ModelRef{}
	if cfg.PlanningModel != "" {
		phaseModels[core.PhasePlanning] = core.ParseModelRef(cfg.PlanningModel, cfg.Provider)
	}
	if cfg.ExecutionModel != "" {
		phaseModels[core.PhaseExecution] = core.ParseModelRef(cfg.ExecutionModel, cfg.Provider)
	}
	if cfg.CurationModel != "" {
		phaseModels[core.PhaseCuration] = core.ParseModelRef(cfg.CurationModel, cfg.Provider)
	}

	mode := core.RoutingMode(cfg.RoutingMode)
	if !cfg.RoutingEnabled {
		mode = core.RoutingModeDisabled
	}

	return core.RoutingConfig{
		Enabled:        cfg.RoutingEnabled,
		Mode:           mode,
		DefaultModel:   defaultRef,
		PhaseModels:    phaseModels,
		ApprovedModels: approved,
		MinConfidence:  cfg.RoutingMinConfidence,
	}
}

func defaultRegistryFromConfig(cfg *config.Config, store *state.Store, mem *memory.Manager, judge provider.Provider, promptDumper *promptdump.Dumper) (*tools.Registry, error) {
	return tools.NewDefaultRegistryFromOptions(tools.DefaultRegistryOptions{
		AllowedCommands: cfg.AllowedCommands,
		Store:           store,
		Memory:          mem,
		Judge:           judge,
		SafetyMode:      cfg.SafetyMode,
		SafetyModel:     cfg.SafetyModel,
		PrimaryModel:    cfg.PrimaryModel(),
		PromptDumper:    promptDumper,
		WebSearch: tools.WebSearchOptions{
			Enabled:         cfg.WebSearch.Enabled,
			Provider:        cfg.WebSearch.Provider,
			APIKey:          cfg.TavilyAPIKey(),
			MaxResults:      cfg.WebSearch.MaxResults,
			SearchDepth:     cfg.WebSearch.SearchDepth,
			MaxFetchedChars: cfg.WebSearch.MaxFetchedChars,
			AllowedDomains:  cfg.WebSearch.AllowedDomains,
			BlockedDomains:  cfg.WebSearch.BlockedDomains,
		},
	})
}
