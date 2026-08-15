package tools

import (
	"context"
	"time"

	"github.com/coolcake/cvkeharness/internal/telemetry"
)

// EventType identifies a structured runtime event that may be rendered to the
// operator while a run is in progress.
type EventType string

const (
	EventToolCallStarted      EventType = "tool_call_started"
	EventToolCallFinished     EventType = "tool_call_finished"
	EventShellCommandStarted  EventType = "shell_command_started"
	EventShellApproval        EventType = "shell_approval"
	EventApprovalRequired     EventType = "approval_required"
	EventShellOutput          EventType = "shell_output"
	EventShellCommandFinished EventType = "shell_command_finished"
	EventMemoryInjected       EventType = "memory_injected"
	EventVerificationActivity EventType = "verification_activity"
)

// VerificationPhase names the operator-visible phase of completion
// verification. These phases expose workflow state, never model reasoning.
type VerificationPhase string

const (
	VerificationPhaseChecking  VerificationPhase = "checking"
	VerificationPhaseRepairing VerificationPhase = "repairing"
	VerificationPhaseCompleted VerificationPhase = "completed"
	VerificationPhaseStopped   VerificationPhase = "stopped"
)

// VerificationStopReason is a fixed, UI-safe explanation for why completion
// repair stopped before the verifier was satisfied.
type VerificationStopReason string

const (
	VerificationStopNone                  VerificationStopReason = ""
	VerificationStopNoProgress            VerificationStopReason = "no_progress"
	VerificationStopCapabilityUnavailable VerificationStopReason = "capability_unavailable"
	VerificationStopRepairLimit           VerificationStopReason = "repair_limit"
	VerificationStopIterationLimit        VerificationStopReason = "iteration_limit"
	VerificationStopVerifierUnavailable   VerificationStopReason = "verifier_unavailable"
)

// VerificationActivity is a compact, redacted progress summary for operator
// interfaces. It deliberately excludes prompts, candidate answers, repair
// instructions, tool names, and unrestricted tool details.
type VerificationActivity struct {
	Phase                 VerificationPhase
	Status                string
	RepairAttempt         int
	RepairLimit           int
	Reason                string
	MissingActions        []string
	CapabilitiesEvaluated bool
	CapabilitiesChanged   bool
	StopReason            VerificationStopReason
	Final                 bool
}

// MemorySource is a bounded, UI-safe description of one memory section used
// for a model call. Preview is intentionally compact and may still contain
// private operational context, so observers must not persist it implicitly.
type MemorySource struct {
	Name    string
	Origin  string
	Chars   int
	Preview string
}

// Event captures a single execution update.
type Event struct {
	Type            EventType
	Timestamp       time.Time
	ToolName        string
	ToolCallID      string
	Command         string
	Output          string
	ApprovalMode    string
	BlockedWorkID   string
	ApprovalReason  string
	ApprovalEffects []ShellEffect
	Success         bool
	ExitCode        int
	ExitCodeKnown   bool
	Duration        time.Duration
	ErrorMessage    string
	MemorySources   []MemorySource
	Verification    VerificationActivity
	SessionID       string
	TurnID          string
}

// EventObserver receives runtime events.
type EventObserver interface {
	Observe(Event)
}

type eventObserverKey struct{}

type toolCallContextKey struct{}

// ToolCallContext stores the active tool call metadata in context.
type ToolCallContext struct {
	ID   string
	Name string
}

// WithEventObserver attaches an observer to the context.
func WithEventObserver(ctx context.Context, observer EventObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, eventObserverKey{}, observer)
}

// EventObserverFromContext returns the observer attached to the context, if any.
func EventObserverFromContext(ctx context.Context) EventObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(eventObserverKey{}).(EventObserver)
	return observer
}

// WithToolCallContext adds tool-call metadata to the context.
func WithToolCallContext(ctx context.Context, id, name string) context.Context {
	return context.WithValue(ctx, toolCallContextKey{}, ToolCallContext{
		ID:   id,
		Name: name,
	})
}

// ToolCallContextFromContext returns tool-call metadata from the context.
func ToolCallContextFromContext(ctx context.Context) ToolCallContext {
	if ctx == nil {
		return ToolCallContext{}
	}
	meta, _ := ctx.Value(toolCallContextKey{}).(ToolCallContext)
	return meta
}

// EmitEvent sends an event to the active observer when one is configured.
func EmitEvent(ctx context.Context, event Event) {
	observer := EventObserverFromContext(ctx)
	if observer == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	meta := ToolCallContextFromContext(ctx)
	if event.ToolCallID == "" {
		event.ToolCallID = meta.ID
	}
	if event.ToolName == "" {
		event.ToolName = meta.Name
	}
	fields := telemetry.FieldsFromContext(ctx)
	if event.SessionID == "" {
		event.SessionID = fields.SessionID
	}
	if event.TurnID == "" {
		event.TurnID = fields.TurnID
	}
	observer.Observe(event)
}
