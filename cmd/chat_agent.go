package cmd

import (
	"context"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/promptdump"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/router"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

// newChatAgent constructs the shared chat runtime used by both the line-oriented
// CLI and the Bubble Tea console. UI-specific input and rendering stay outside
// this boundary.
func newChatAgent(
	ctx context.Context,
	cfg *config.Config,
	store *state.Store,
	observer tools.EventObserver,
	blockManualApprovals bool,
	modelApproval router.ApprovalPrompter,
) (*agent.Agent, error) {
	p, err := resolveProvider(cfg, "")
	if err != nil {
		return nil, err
	}

	mem := memory.NewManager(cfg.MemoryDir, store)
	if err := mem.EnsureFiles(); err != nil {
		return nil, err
	}
	if err := mem.Reindex(ctx); err != nil {
		log.FromContext(ctx).Warn("failed to reindex memory metadata", "error", err)
	}

	promptDumper := promptdump.NewWithRetentionDays(cfg.DebugPromptDumps, cfg.PromptDumpDir, cfg.PromptDumpRetentionDays)
	registry, err := defaultRegistryFromConfig(cfg, store, mem, p, promptDumper, blockManualApprovals)
	if err != nil {
		return nil, err
	}
	routingCfg := routingConfigFromConfig(cfg, store)
	r := router.New(routingCfg, store, modelApproval)

	return agent.New(agent.Options{
		Provider:           p,
		ProviderName:       cfg.Provider,
		ProviderResolver:   providerResolver{cfg: cfg},
		ToolRegistry:       registry,
		EventObserver:      observer,
		DefaultModel:       cfg.PrimaryModel(),
		MaxIterations:      cfg.MaxIterations,
		MaxTokens:          cfg.MaxTokens,
		RoutingConfig:      routingCfg,
		Router:             r,
		MemoryRetriever:    mem,
		MemoryCurator:      mem,
		RunRecorder:        store,
		BlockedWorkStore:   store,
		PromptDumper:       promptDumper,
		TelemetryWriter:    telemetryWriterFromConfig(cfg, store),
		SafetyMode:         cfg.SafetyMode,
		SafetyModel:        cfg.SafetyModel,
		ClassifierProvider: p,
	}), nil
}
