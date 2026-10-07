package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/glebenator/cvkeharness/config"
	"github.com/glebenator/cvkeharness/core"
	"github.com/glebenator/cvkeharness/internal/modelruntime"
	"github.com/glebenator/cvkeharness/internal/promptdump"
	"github.com/glebenator/cvkeharness/internal/telemetry"
	"github.com/glebenator/cvkeharness/memory"
	"github.com/glebenator/cvkeharness/provider"
	"github.com/glebenator/cvkeharness/recovery"
	"github.com/glebenator/cvkeharness/securitypolicy"
	"github.com/glebenator/cvkeharness/state"
	"github.com/glebenator/cvkeharness/tools"
)

type providerResolver struct {
	cfg *config.Config
}

func (r providerResolver) Resolve(providerName string) (provider.Provider, error) {
	return resolveProvider(r.cfg, providerName)
}

func (r providerResolver) ResolveModel(ref core.ModelRef) (provider.Provider, error) {
	return modelruntime.ResolveModel(r.cfg, ref)
}

func resolveProvider(cfg *config.Config, providerName string) (provider.Provider, error) {
	name := strings.TrimSpace(providerName)
	if name == "" {
		client, _, err := modelruntime.ResolveRole(cfg, config.RolePrimary)
		return client, err
	}
	connection, err := cfg.ConnectionByID(name)
	if err != nil {
		return nil, err
	}
	return modelruntime.NewClient(connection)
}

type runtimeModelClients struct {
	Primary, Judge, Classifier                provider.Provider
	PrimaryModel, JudgeModel, ClassifierModel config.ResolvedModel
	Verifier                                  core.ModelRef
}

func resolveRuntimeModels(cfg *config.Config) (runtimeModelClients, error) {
	var result runtimeModelClients
	var err error
	if err = cfg.ValidateModelRoles(true); err != nil {
		return result, err
	}
	if result.Primary, result.PrimaryModel, err = modelruntime.ResolveRole(cfg, config.RolePrimary); err != nil {
		return result, err
	}
	if result.Judge, result.JudgeModel, err = modelruntime.ResolveRole(cfg, config.RoleSafetyJudge); err != nil {
		return result, err
	}
	if result.Classifier, result.ClassifierModel, err = modelruntime.ResolveRole(cfg, config.RoleClassifier); err != nil {
		return result, err
	}
	if !cfg.VerifierInheritsExecution() {
		resolved, resolveErr := cfg.ResolveRole(config.RoleVerifier)
		if resolveErr != nil {
			return result, resolveErr
		}
		result.Verifier = modelruntime.Ref(resolved)
	}
	return result, nil
}

func routingConfigFromConfig(cfg *config.Config, store *state.Store) core.RoutingConfig {
	primary, _ := cfg.ResolveRole(config.RolePrimary)
	defaultRef := modelruntime.Ref(primary)
	approved := make([]core.ModelRef, 0, len(cfg.ApprovedModels))
	for _, raw := range cfg.ApprovedModels {
		normalized, err := normalizeModelArg(cfg, raw)
		if err != nil {
			continue
		}
		ref := core.ParseModelRef(normalized, primary.Connection.Provider)
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
	for role, phase := range map[config.ModelRole]core.Phase{config.RolePlanning: core.PhasePlanning, config.RoleExecution: core.PhaseExecution, config.RoleCuration: core.PhaseCuration} {
		if resolved, err := cfg.ResolveRole(role); err == nil {
			phaseModels[phase] = modelruntime.Ref(resolved)
		}
	}
	phaseModels[core.PhaseChat] = phaseModels[core.PhaseExecution]

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

func defaultRegistryFromConfig(cfg *config.Config, store *state.Store, mem *memory.Manager, judge provider.Provider, promptDumper *promptdump.Dumper, blockManualApprovals bool) (*tools.Registry, error) {
	securityPolicy, err := cfg.EffectiveSecurity()
	if err != nil {
		return nil, fmt.Errorf("resolve security policy: %w", err)
	}
	judgeModel, err := cfg.ResolveRole(config.RoleSafetyJudge)
	if err != nil {
		return nil, err
	}
	var advisor provider.Provider
	var advisorModel config.ResolvedModel
	if cfg.SafetyMode == tools.SafetyModeLLMAdvisor || securityPolicy.Profile == securitypolicy.ProfileLLMAdvisor {
		advisor, advisorModel, err = modelruntime.ResolveRole(cfg, config.RoleSafetyAdvisor)
		if err != nil {
			return nil, fmt.Errorf("resolve safety advisor: %w", err)
		}
	}
	return tools.NewDefaultRegistryFromOptions(tools.DefaultRegistryOptions{
		AllowedCommands:      cfg.AllowedCommands,
		Store:                store,
		Memory:               mem,
		Judge:                judge,
		Advisor:              advisor,
		AdvisorModel:         advisorModel.Model,
		SafetyMode:           cfg.SafetyMode,
		SafetyModel:          judgeModel.Model,
		PrimaryModel:         cfg.PrimaryModel(),
		PromptDumper:         promptDumper,
		BlockManualApprovals: blockManualApprovals,
		SecurityPolicy:       &securityPolicy,
		Recovery:             recoveryOptions(cfg),
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

func recoveryOptions(cfg *config.Config) recovery.Options {
	l := recovery.DefaultLimits()
	if cfg.Recovery.MaxRepairAttempts != 0 {
		l.MaxRepairAttempts = cfg.Recovery.MaxRepairAttempts
	}
	if cfg.Recovery.MaxFiles != 0 {
		l.MaxFiles = cfg.Recovery.MaxFiles
	}
	if cfg.Recovery.MaxBytes != 0 {
		l.MaxBytes = cfg.Recovery.MaxBytes
	}
	if cfg.Recovery.MaxFileBytes != 0 {
		l.MaxFileBytes = cfg.Recovery.MaxFileBytes
	}
	if cfg.Recovery.MaxAgeSeconds != 0 {
		l.MaxAgeSeconds = cfg.Recovery.MaxAgeSeconds
	}
	if cfg.Recovery.MinFreeBytes != 0 {
		l.MinFreeBytes = cfg.Recovery.MinFreeBytes
	}
	return recovery.Options{Roots: append([]string(nil), cfg.Recovery.Roots...), Limits: l, Services: append([]recovery.NGINXService(nil), cfg.Recovery.Services...), Snapshots: append([]recovery.SnapshotTarget(nil), cfg.Recovery.Snapshots...), SSHServices: append([]recovery.SSHService(nil), cfg.Recovery.SSHServices...), Fleet: cfg.Recovery.Fleet}
}

func telemetryWriterFromConfig(cfg *config.Config, store *state.Store) *telemetry.Writer {
	if cfg == nil {
		return nil
	}
	return telemetry.NewWriter(filepath.Join(filepath.Dir(cfg.StateDBPath), "telemetry"), telemetry.StreamLive, store)
}
