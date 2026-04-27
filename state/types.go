package state

import (
	"time"

	"github.com/coolcake/cvkeharness/core"
)

// RunRecord stores the structured outcome of a harness run.
type RunRecord struct {
	ID                          int64
	StartedAt                   time.Time
	FinishedAt                  time.Time
	Provider                    string
	Task                        string
	TaskClass                   core.TaskClass
	Success                     bool
	ErrorMessage                string
	FinalOutput                 string
	VerificationStatus          string
	VerificationReason          string
	VerificationMissingActions  string
	VerificationRepairTriggered bool
	RoutingEnabled              bool
	Phases                      []PhaseRecord
	Tools                       []ToolOutcome
}

// PhaseRecord captures one routed phase invocation.
type PhaseRecord struct {
	Phase             core.Phase
	Provider          string
	RequestedModel    string
	ActualModel       string
	Success           bool
	LatencyMs         int64
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	CachedTokens      int
	CachedTokensKnown bool
	Confidence        float64
	Explanation       string
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

// RecentModelUsage summarizes recently used requested/actual model pairs.
type RecentModelUsage struct {
	Provider       string
	RequestedModel string
	ActualModel    string
	LastUsedAt     time.Time
	Uses           int
	Successes      int
}

// ModelAlias tracks cases where a requested provider model resolved to a
// different actual backend model identifier.
type ModelAlias struct {
	Provider       string
	RequestedModel string
	ActualModel    string
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
	SeenCount      int
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
	SeenCount  int
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

// Target stores one resolved runtime or remote host identity.
type Target struct {
	ID          string
	Kind        string
	PrimaryName string
	Transport   string
	Confidence  float64
	Status      string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// TargetAlias links alternate names back to a stable target identifier.
type TargetAlias struct {
	TargetID   string
	Alias      string
	AliasType  string
	Confidence float64
	LastSeenAt time.Time
}

// HostFact stores one verified fact about a runtime or target host.
type HostFact struct {
	HostID     string
	Key        string
	Value      string
	Confidence float64
	VerifiedAt time.Time
	UpdatedAt  time.Time
}

// Playbook stores a durable target-specific operational procedure.
type Playbook struct {
	ID             string
	TargetID       string
	Intent         string
	ToolName       string
	Status         string
	Title          string
	Confidence     float64
	SuccessCount   int
	FailureCount   int
	LastVerifiedAt time.Time
	LastUsedAt     time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	MatchTerms     []string
	Preconditions  []string
	VerifySteps    []string
	ActionSteps    []string
	SuccessChecks  []string
	Notes          string
}

// Finding stores a provisional reusable observation awaiting promotion.
type Finding struct {
	ID         string
	TargetID   string
	Intent     string
	ToolName   string
	Status     string
	Origin     string
	Body       string
	Confidence float64
	SeenCount  int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Caution stores negative memory for unreliable or denied approaches.
type Caution struct {
	ID           string
	TargetID     string
	Intent       string
	ToolName     string
	Status       string
	Body         string
	Confidence   float64
	FailureCount int
	LastSeenAt   time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// OperationalMemory is the structured target-aware memory index.
type OperationalMemory struct {
	Targets       []Target
	TargetAliases []TargetAlias
	HostFacts     []HostFact
	Playbooks     []Playbook
	Findings      []Finding
	Cautions      []Caution
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

// ScheduledJob stores a durable CvkeHarness-owned background task.
type ScheduledJob struct {
	ID               string
	Name             string
	ScheduleKind     string
	ScheduleSpec     string
	Prompt           string
	Enabled          bool
	NextRunAt        time.Time
	LastRunAt        time.Time
	LastRunStatus    string
	ConsecutiveFail  int
	ClaimedBy        string
	ClaimExpiresAt   time.Time
	ClaimHeartbeatAt time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ScheduledJobRun stores one scheduler execution attempt.
type ScheduledJobRun struct {
	ID         int64
	JobID      string
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string
	Output     string
	Error      string
	RunID      int64
}

// SystemCronAudit records user-crontab inspection and mutation attempts.
type SystemCronAudit struct {
	ID             int64
	Action         string
	Target         string
	OldSnippet     string
	NewSnippet     string
	Success        bool
	ErrorMessage   string
	InitiatingTool string
	CreatedAt      time.Time
}

// RunSummary is a compact persisted run view for inspection surfaces.
type RunSummary struct {
	ID                          int64
	StartedAt                   time.Time
	FinishedAt                  time.Time
	Provider                    string
	Task                        string
	TaskClass                   core.TaskClass
	Success                     bool
	ErrorMessage                string
	FinalOutput                 string
	VerificationStatus          string
	VerificationReason          string
	VerificationMissingActions  string
	VerificationRepairTriggered bool
	RoutingEnabled              bool
	Phases                      []PhaseRecord
	Tools                       []ToolOutcome
}

// ChatSessionSummary is a compact persisted chat session view.
type ChatSessionSummary struct {
	ID             int64
	StartedAt      time.Time
	FinishedAt     time.Time
	Provider       string
	PinnedModel    string
	RoutingEnabled bool
	ExitReason     string
	TurnCount      int
}

// ChatSessionDetail contains a session and its persisted turns/messages.
type ChatSessionDetail struct {
	Session  ChatSessionSummary
	Turns    []ChatTurn
	Messages []ChatMessage
}

// ChatSession stores one interactive chat lifecycle.
type ChatSession struct {
	ID             int64
	StartedAt      time.Time
	FinishedAt     time.Time
	Provider       string
	PinnedModel    string
	RoutingEnabled bool
	ExitReason     string
}

// ChatTurn stores one user-driven turn inside a chat session.
type ChatTurn struct {
	ID                          int64
	SessionID                   int64
	TurnIndex                   int
	UserInput                   string
	TaskClass                   core.TaskClass
	RequestedModel              string
	ActualModel                 string
	Success                     bool
	ErrorMessage                string
	LatencyMs                   int64
	PromptTokens                int
	CompletionTokens            int
	TotalTokens                 int
	FinalOutput                 string
	VerificationStatus          string
	VerificationReason          string
	VerificationMissingActions  string
	VerificationRepairTriggered bool
	CreatedAt                   time.Time
}

// ChatMessage stores one persisted transcript message in session order.
type ChatMessage struct {
	ID            int64
	SessionID     int64
	TurnID        int64
	MessageIndex  int
	Role          string
	Content       string
	ToolCallID    string
	ToolName      string
	ToolArguments string
	ToolCallsJSON string
	CreatedAt     time.Time
}

// Snapshot records a pre-write copy of a managed memory file.
type Snapshot struct {
	ID         string
	SourceFile string
	Path       string
	Reason     string
	CreatedAt  time.Time
}

const (
	ApprovalStatusApproved     = "approved"
	ApprovalStatusApprovedOnce = "approved_once"
)
