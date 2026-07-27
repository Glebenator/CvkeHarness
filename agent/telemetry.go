package agent

import (
	"context"
	"encoding/json"

	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/telemetry"
	"github.com/coolcake/cvkeharness/provider"
)

func emitModelCallCompleted(ctx context.Context, phase core.Phase, iteration int, providerName, requestedModel string, resp *provider.ChatResponse, durationMs int64, callErr error) {
	actualModel := ""
	var promptTokens, completionTokens, totalTokens, cachedTokens int
	var cachedKnown bool
	if resp != nil {
		actualModel = resp.Model
		promptTokens = resp.Usage.PromptTokens
		completionTokens = resp.Usage.CompletionTokens
		totalTokens = resp.Usage.TotalTokens
		cachedTokens, cachedKnown = resp.Usage.CachedTokens()
	}
	var cacheHitRatio float64
	if cachedKnown && promptTokens > 0 {
		cacheHitRatio = float64(cachedTokens) / float64(promptTokens)
	}
	payload, _ := json.Marshal(map[string]any{
		"success":           callErr == nil,
		"duration_ms":       durationMs,
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
		"cached_tokens":     cachedTokens,
		"cached_known":      cachedKnown,
		"cache_hit_ratio":   cacheHitRatio,
		"error":             errString(callErr),
	})
	_ = telemetry.Record(telemetry.WithFields(ctx, telemetry.Fields{
		Phase:          string(phase),
		Iteration:      iteration,
		Provider:       providerName,
		RequestedModel: requestedModel,
		ActualModel:    actualModel,
	}), telemetry.Event{
		Type:           telemetry.EventModelCallCompleted,
		Phase:          string(phase),
		Iteration:      iteration,
		Provider:       providerName,
		RequestedModel: requestedModel,
		ActualModel:    actualModel,
		Payload:        payload,
	})
}

func emitPromptPlanned(ctx context.Context, phase core.Phase, iteration int, providerName, requestedModel string, plan promptPlan, messageCount int) {
	payload, _ := json.Marshal(map[string]any{
		"stable_prefix_hash": plan.PrefixHash,
		"prompt_hash":        plan.PromptHash,
		"message_count":      messageCount,
		"tool_names":         plan.ToolNames,
		"tool_count":         len(plan.ToolNames),
	})
	_ = telemetry.Record(telemetry.WithFields(ctx, telemetry.Fields{
		Phase:          string(phase),
		Iteration:      iteration,
		Provider:       providerName,
		RequestedModel: requestedModel,
	}), telemetry.Event{
		Type:           telemetry.EventPromptPlanned,
		Phase:          string(phase),
		Iteration:      iteration,
		Provider:       providerName,
		RequestedModel: requestedModel,
		Payload:        payload,
	})
}
