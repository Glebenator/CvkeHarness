package core

import (
	"fmt"
	"sort"
	"strings"
)

// Phase identifies a routed stage in the harness lifecycle.
type Phase string

const (
	PhasePlanning  Phase = "planning"
	PhaseExecution Phase = "execution"
	PhaseCuration  Phase = "memory_curation"
)

// RoutingMode controls how model routing behaves.
type RoutingMode string

const (
	RoutingModeDisabled         RoutingMode = "disabled"
	RoutingModeAutoWithinPolicy RoutingMode = "auto_within_policy"
)

// TaskClass is a coarse task classification used for routing and retrieval.
type TaskClass string

const (
	TaskClassGeneral         TaskClass = "general"
	TaskClassInspection      TaskClass = "inspection"
	TaskClassDebugging       TaskClass = "debugging"
	TaskClassShellHeavy      TaskClass = "shell_heavy"
	TaskClassPolicySensitive TaskClass = "policy_sensitive"
	TaskClassLongHorizon     TaskClass = "long_horizon"
	TaskClassSummarization   TaskClass = "summarization"
)

// ModelRef normalizes a provider/model pair.
type ModelRef struct {
	Provider string
	Model    string
}

// NewModelRef creates a normalized model reference.
func NewModelRef(provider, model string) ModelRef {
	return ModelRef{
		Provider: strings.TrimSpace(provider),
		Model:    strings.TrimSpace(model),
	}
}

// ParseModelRef parses either provider/model or bare model identifiers.
func ParseModelRef(raw, defaultProvider string) ModelRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ModelRef{}
	}

	parts := strings.Split(raw, "/")
	if len(parts) >= 3 {
		return NewModelRef(parts[0], strings.Join(parts[1:], "/"))
	}
	if len(parts) == 2 {
		if parts[0] == "openrouter" || parts[0] == "lmstudio" {
			return NewModelRef(parts[0], parts[1])
		}
	}
	return NewModelRef(defaultProvider, raw)
}

// IsZero reports whether the model reference is empty.
func (m ModelRef) IsZero() bool {
	return m.Provider == "" || m.Model == ""
}

// String returns provider/model.
func (m ModelRef) String() string {
	if m.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s/%s", m.Provider, m.Model)
}

// Equal reports provider/model equality.
func (m ModelRef) Equal(other ModelRef) bool {
	return strings.EqualFold(m.Provider, other.Provider) && m.Model == other.Model
}

// ToolTrouble captures a failing tool pattern for refreshed retrieval.
type ToolTrouble struct {
	Tool        string
	DenialClass string
	Repeated    bool
}

// RetrievalContext carries the active run state into memory retrieval.
type RetrievalContext struct {
	Task        string
	TaskClass   TaskClass
	Phase       Phase
	ActiveModel ModelRef
	ActualModel ModelRef
	ToolNames   []string
	Trouble     *ToolTrouble
	MaxSnippets int
}

// RoutingConfig controls how per-phase routing is resolved.
type RoutingConfig struct {
	Enabled        bool
	Mode           RoutingMode
	DefaultModel   ModelRef
	PhaseModels    map[Phase]ModelRef
	ApprovedModels []ModelRef
	MinConfidence  float64
}

// ApprovedSet returns a normalized set of approved model identifiers.
func (c RoutingConfig) ApprovedSet() map[string]ModelRef {
	out := make(map[string]ModelRef, len(c.ApprovedModels))
	for _, ref := range c.ApprovedModels {
		if ref.IsZero() {
			continue
		}
		out[ref.String()] = ref
	}
	return out
}

// ToolsetKey returns a deterministic key for a tool set.
func ToolsetKey(names []string) string {
	if len(names) == 0 {
		return ""
	}
	items := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, name)
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

// RoutingSelection describes the chosen or recommended model for a phase.
type RoutingSelection struct {
	Phase                Phase
	Requested            ModelRef
	Confidence           float64
	Reason               string
	UsedDefault          bool
	NeedsApproval        bool
	Recommendation       *ModelRef
	RecommendationReason string
}
