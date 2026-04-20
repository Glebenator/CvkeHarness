package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/state"
)

type providerResolver struct {
	cfg *config.Config
}

func (r providerResolver) Resolve(providerName string) (provider.Provider, error) {
	return resolveProvider(r.cfg, providerName)
}

func resolveProvider(cfg *config.Config, providerName string) (provider.Provider, error) {
	switch strings.TrimSpace(providerName) {
	case "", cfg.Provider:
		switch cfg.Provider {
		case "openrouter":
			return provider.NewOpenRouter(cfg.GetAPIKey("openrouter")), nil
		case "lmstudio":
			return provider.NewLMStudio(cfg.BaseURL), nil
		default:
			return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
		}
	case "openrouter":
		return provider.NewOpenRouter(cfg.GetAPIKey("openrouter")), nil
	case "lmstudio":
		return provider.NewLMStudio(cfg.BaseURL), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", providerName)
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
