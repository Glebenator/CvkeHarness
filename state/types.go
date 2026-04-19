package state

import (
	"time"

	"github.com/coolcake/cvkeharness/core"
)

// RunRecord stores the structured outcome of a harness run.
type RunRecord struct {
	ID             int64
	StartedAt      time.Time
	FinishedAt     time.Time
	Provider       string
	Task           string
	TaskClass      core.TaskClass
	Success        bool
	ErrorMessage   string
	RoutingEnabled bool
	Phases         []PhaseRecord
	Tools          []ToolOutcome
}

// PhaseRecord captures one routed phase invocation.
type PhaseRecord struct {
	Phase            core.Phase
	Provider         string
	RequestedModel   string
	ActualModel      string
	Success          bool
	LatencyMs        int64
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Confidence       float64
	Explanation      string
}

// ToolOutcome records an individual tool result.
type ToolOutcome struct {
	Phase        core.Phase
	Provider     string
	Model        string
	ToolName     string
	Toolset      string
	Success      bool
	PolicyDenied bool
	DenialClass  string
	ErrorMessage string
	DurationMs   int64
}

// ModelStats is the normalized aggregate used by routing.
type ModelStats struct {
	Provider      string
	Model         string
	Phase         core.Phase
	TaskClass     core.TaskClass
	Toolset       string
	Runs          int
	Successes     int
	PolicyDenials int
	AvgLatencyMs  float64
	LastSeenAt    time.Time
}

// MemoryEntry stores machine-managed metadata for a memory snippet.
type MemoryEntry struct {
	ID         string
	SourceFile string
	Scope      string
	Provider   string
	Model      string
	ToolName   string
	TaskClass  core.TaskClass
	Phase      core.Phase
	Status     string
	Confidence float64
	Body       string
	Normalized string
	SnapshotID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastSeenAt time.Time
}

// MemoryFilter narrows memory entry retrieval.
type MemoryFilter struct {
	SourceFiles []string
	Phase       core.Phase
	TaskClass   core.TaskClass
	Provider    string
	Model       string
	ToolName    string
	OnlyActive  bool
}

// RoutingCandidate stores the last learned routing score for a model/profile.
type RoutingCandidate struct {
	Provider   string
	Model      string
	Phase      core.Phase
	TaskClass  core.TaskClass
	Toolset    string
	Approved   bool
	Score      float64
	Confidence float64
	Reason     string
	Status     string
	UpdatedAt  time.Time
}

// ModelApproval tracks approved and suggested models.
type ModelApproval struct {
	Provider   string
	Model      string
	Status     string
	Source     string
	Rationale  string
	ApprovedAt time.Time
}

// CommandApproval tracks approved shell commands that may be reused later.
type CommandApproval struct {
	Command    string
	Status     string
	Source     string
	Rationale  string
	ApprovedAt time.Time
}

// Snapshot records a pre-write copy of a managed memory file.
type Snapshot struct {
	ID         string
	SourceFile string
	Path       string
	Reason     string
	CreatedAt  time.Time
}
