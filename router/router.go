package router

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/state"
)

// ApprovalPrompter asks the user whether a recommended model may be used.
type ApprovalPrompter func(ctx context.Context, selection core.RoutingSelection) (bool, error)

// Router chooses models for planning, execution, and curation.
type Router struct {
	config   core.RoutingConfig
	store    *state.Store
	prompter ApprovalPrompter
}

// New creates a router.
func New(config core.RoutingConfig, store *state.Store, prompter ApprovalPrompter) *Router {
	return &Router{
		config:   config,
		store:    store,
		prompter: prompter,
	}
}

// Select chooses a model for a phase/task/tool profile.
func (r *Router) Select(ctx context.Context, phase core.Phase, task string, taskClass core.TaskClass, toolNames []string) (core.RoutingSelection, error) {
	defaultRef := r.phaseDefault(phase)
	selection := core.RoutingSelection{
		Phase:       phase,
		Requested:   defaultRef,
		UsedDefault: true,
		Reason:      "routing disabled; using the configured default model",
	}

	if !r.config.Enabled || r.config.Mode == core.RoutingModeDisabled {
		return selection, nil
	}

	toolset := core.ToolsetKey(toolNames)
	stats, err := r.listStats(ctx, phase, taskClass, toolset)
	if err != nil {
		selection.Reason = fmt.Sprintf("routing stats unavailable (%v); using the configured default model", err)
		return selection, nil
	}

	approved := r.config.ApprovedSet()
	type candidate struct {
		ref        core.ModelRef
		score      float64
		confidence float64
		reason     string
		approved   bool
	}
	var candidates []candidate

	for _, stat := range stats {
		ref := core.NewModelRef(stat.Provider, stat.Model)
		score, confidence, reason := scoreStat(stat, defaultRef)
		_, ok := approved[ref.String()]
		candidates = append(candidates, candidate{
			ref:        ref,
			score:      score,
			confidence: confidence,
			reason:     reason,
			approved:   ok,
		})
	}

	if len(candidates) == 0 {
		selection.Reason = "no routing history yet; using the configured default model"
		return selection, nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].confidence > candidates[j].confidence
		}
		return candidates[i].score > candidates[j].score
	})

	var persisted []state.RoutingCandidate
	for _, item := range candidates {
		status := "observed"
		if item.approved {
			status = "approved"
		}
		persisted = append(persisted, state.RoutingCandidate{
			Provider:   item.ref.Provider,
			Model:      item.ref.Model,
			Phase:      phase,
			TaskClass:  taskClass,
			Toolset:    toolset,
			Approved:   item.approved,
			Score:      item.score,
			Confidence: item.confidence,
			Reason:     item.reason,
			Status:     status,
			UpdatedAt:  time.Now().UTC(),
		})
	}
	if r.store != nil && r.store.Available() {
		_ = r.store.SaveRoutingCandidates(ctx, phase, taskClass, toolset, persisted)
	}

	top := candidates[0]
	if top.approved && top.confidence >= r.config.MinConfidence {
		selection.Requested = top.ref
		selection.Confidence = top.confidence
		selection.UsedDefault = top.ref.Equal(defaultRef)
		selection.Reason = fmt.Sprintf("selected %s from historical phase/task/tool stats: %s", top.ref.String(), top.reason)
		return selection, nil
	}

	if !top.approved && top.score > 0 && top.confidence >= r.config.MinConfidence {
		selection.NeedsApproval = true
		selection.Recommendation = &top.ref
		selection.RecommendationReason = fmt.Sprintf("recommended %s for %s/%s because %s", top.ref.String(), phase, taskClass, top.reason)
		if r.prompter != nil {
			approvedNow, err := r.prompter(ctx, selection)
			if err != nil {
				return selection, err
			}
			if approvedNow {
				selection.Requested = top.ref
				selection.Confidence = top.confidence
				selection.UsedDefault = false
				selection.NeedsApproval = false
				selection.Reason = fmt.Sprintf("user approved recommendation: %s", top.reason)
				if r.store != nil && r.store.Available() {
					_ = r.store.SaveModelApproval(ctx, state.ModelApproval{
						Provider:   top.ref.Provider,
						Model:      top.ref.Model,
						Status:     "approved_once",
						Source:     "prompt",
						Rationale:  top.reason,
						ApprovedAt: time.Now().UTC(),
					})
				}
				return selection, nil
			}
		}
		selection.Reason = fmt.Sprintf("top candidate %s is not approved; using the configured default model", top.ref.String())
		return selection, nil
	}

	selection.Reason = fmt.Sprintf("routing confidence %.2f is below threshold %.2f; using the configured default model", top.confidence, r.config.MinConfidence)
	return selection, nil
}

func (r *Router) phaseDefault(phase core.Phase) core.ModelRef {
	if ref, ok := r.config.PhaseModels[phase]; ok && !ref.IsZero() {
		return ref
	}
	return r.config.DefaultModel
}

func (r *Router) listStats(ctx context.Context, phase core.Phase, taskClass core.TaskClass, toolset string) ([]state.ModelStats, error) {
	if r.store == nil || !r.store.Available() {
		return nil, fmt.Errorf("state store unavailable")
	}
	return r.store.ListModelStats(ctx, phase, taskClass, toolset)
}

func scoreStat(stat state.ModelStats, defaultRef core.ModelRef) (float64, float64, string) {
	if stat.Runs == 0 {
		return 0, 0, "no successful samples"
	}

	successRate := float64(stat.Successes) / float64(stat.Runs)
	denialRate := float64(stat.PolicyDenials) / float64(stat.Runs)
	latencyPenalty := stat.AvgLatencyMs / 1000.0 / 100.0
	score := (successRate * 100.0) - (denialRate * 40.0) - latencyPenalty

	confidence := float64(stat.Runs) / 4.0
	if confidence > 1 {
		confidence = 1
	}

	var reasonParts []string
	reasonParts = append(reasonParts, fmt.Sprintf("%.0f%% success over %d run(s)", successRate*100, stat.Runs))
	if stat.PolicyDenials > 0 {
		reasonParts = append(reasonParts, fmt.Sprintf("%d policy denial(s)", stat.PolicyDenials))
	}
	if stat.AvgLatencyMs > 0 {
		reasonParts = append(reasonParts, fmt.Sprintf("avg latency %.0fms", stat.AvgLatencyMs))
	}
	if stat.Provider == defaultRef.Provider && stat.Model == defaultRef.Model {
		reasonParts = append(reasonParts, "this is also the default")
	}
	return score, confidence, strings.Join(reasonParts, ", ")
}
