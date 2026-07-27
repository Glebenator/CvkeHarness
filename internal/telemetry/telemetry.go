package telemetry

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coolcake/cvkeharness/internal/secrets"
)

// Stream separates live runtime data from test and synthetic telemetry.
type Stream string

const (
	StreamLive      Stream = "live"
	StreamTest      Stream = "test"
	StreamSynthetic Stream = "synthetic"
)

// EventType names one canonical runtime event.
type EventType string

const (
	EventPromptPlanned         EventType = "prompt_planned"
	EventMemoryRetrieved       EventType = "memory_retrieved"
	EventModelCallCompleted    EventType = "model_call_completed"
	EventApprovalRequested     EventType = "approval_requested"
	EventApprovalResolved      EventType = "approval_resolved"
	EventToolStarted           EventType = "tool_started"
	EventToolFinished          EventType = "tool_finished"
	EventVerificationCompleted EventType = "verification_completed"
	EventTaskBlocked           EventType = "task_blocked"
	EventTaskResumed           EventType = "task_resumed"
	EventTaskCompleted         EventType = "task_completed"
	EventSchedulerClaimed      EventType = "scheduler_claimed"
	EventSchedulerHeartbeat    EventType = "scheduler_heartbeat"
	EventSchedulerStarted      EventType = "scheduler_started"
	EventSchedulerFinished     EventType = "scheduler_finished"
	EventSchedulerOverdue      EventType = "scheduler_overdue"
)

// Event is the append-only telemetry envelope used by both the JSONL source
// stream and SQLite projections.
type Event struct {
	EventID        string          `json:"event_id"`
	Timestamp      time.Time       `json:"timestamp"`
	Stream         Stream          `json:"stream"`
	Type           EventType       `json:"type"`
	SessionID      string          `json:"session_id,omitempty"`
	RunID          string          `json:"run_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	JobID          string          `json:"job_id,omitempty"`
	Phase          string          `json:"phase,omitempty"`
	Iteration      int             `json:"iteration,omitempty"`
	Provider       string          `json:"provider,omitempty"`
	RequestedModel string          `json:"requested_model,omitempty"`
	ActualModel    string          `json:"actual_model,omitempty"`
	TaskState      string          `json:"task_state,omitempty"`
	TargetID       string          `json:"target_id,omitempty"`
	ToolCallID     string          `json:"tool_call_id,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

// Projector receives canonical events after they have been appended to the
// source stream and materializes any query tables it needs.
type Projector interface {
	ProjectTelemetryEvent(ctx context.Context, event Event) error
}

// Writer appends canonical events and updates projections from those events.
type Writer struct {
	stream    Stream
	path      string
	projector Projector
	mu        sync.Mutex
	now       func() time.Time
	newID     func() (string, error)
}

// NewWriter creates a canonical event writer rooted at dir/<stream>/events.jsonl.
func NewWriter(dir string, stream Stream, projector Projector) *Writer {
	if stream == "" {
		stream = StreamLive
	}
	return &Writer{
		stream:    stream,
		path:      filepath.Join(dir, string(stream), "events.jsonl"),
		projector: projector,
		now:       func() time.Time { return time.Now().UTC() },
		newID:     newEventID,
	}
}

// ReadEvents loads canonical telemetry events from one append-only JSONL stream.
func ReadEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode telemetry event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// Path returns the append-only source stream path.
func (w *Writer) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// Record appends one event to the canonical stream, then projects it.
func (w *Writer) Record(ctx context.Context, event Event) (Event, error) {
	if w == nil {
		return Event{}, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if event.EventID == "" {
		id, err := w.newID()
		if err != nil {
			return Event{}, err
		}
		event.EventID = id
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = w.now()
	}
	if event.Stream == "" {
		event.Stream = w.stream
	}
	event = redactEvent(event)

	if err := os.MkdirAll(filepath.Dir(w.path), 0755); err != nil {
		return Event{}, err
	}
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return Event{}, err
	}
	data, err := json.Marshal(event)
	if err != nil {
		_ = f.Close()
		return Event{}, err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return Event{}, err
	}
	if err := f.Close(); err != nil {
		return Event{}, err
	}
	if w.projector != nil {
		if err := w.projector.ProjectTelemetryEvent(ctx, event); err != nil {
			return Event{}, err
		}
	}
	return event, nil
}

type contextKey struct {
	name string
}

var (
	writerContextKey = &contextKey{"writer"}
	fieldsContextKey = &contextKey{"fields"}
)

// Fields stores correlation metadata that is merged into later events.
type Fields struct {
	SessionID      string
	RunID          string
	TurnID         string
	JobID          string
	Phase          string
	Iteration      int
	Provider       string
	RequestedModel string
	ActualModel    string
	TaskState      string
	TargetID       string
	ToolCallID     string
}

// WithWriter attaches a writer to a runtime context.
func WithWriter(ctx context.Context, writer *Writer) context.Context {
	if writer == nil {
		return ctx
	}
	return context.WithValue(ctx, writerContextKey, writer)
}

// WriterFromContext extracts the active canonical writer, if any.
func WriterFromContext(ctx context.Context) *Writer {
	writer, _ := ctx.Value(writerContextKey).(*Writer)
	return writer
}

// WithFields merges correlation fields into a context.
func WithFields(ctx context.Context, fields Fields) context.Context {
	current := FieldsFromContext(ctx)
	return context.WithValue(ctx, fieldsContextKey, mergeFields(current, fields))
}

// FieldsFromContext returns the active correlation fields.
func FieldsFromContext(ctx context.Context) Fields {
	fields, _ := ctx.Value(fieldsContextKey).(Fields)
	return fields
}

// WithModel preserves the historical helper while updating actual-model context.
func WithModel(ctx context.Context, model string) context.Context {
	return WithFields(ctx, Fields{ActualModel: model})
}

// ModelFromContext extracts the actual model identifier if present.
func ModelFromContext(ctx context.Context) string {
	return FieldsFromContext(ctx).ActualModel
}

// Record emits one event through the writer attached to ctx.
func Record(ctx context.Context, event Event) error {
	writer := WriterFromContext(ctx)
	if writer == nil {
		return nil
	}
	event = applyFields(event, FieldsFromContext(ctx))
	_, err := writer.Record(ctx, event)
	return err
}

func applyFields(event Event, fields Fields) Event {
	if event.SessionID == "" {
		event.SessionID = fields.SessionID
	}
	if event.RunID == "" {
		event.RunID = fields.RunID
	}
	if event.TurnID == "" {
		event.TurnID = fields.TurnID
	}
	if event.JobID == "" {
		event.JobID = fields.JobID
	}
	if event.Phase == "" {
		event.Phase = fields.Phase
	}
	if event.Iteration == 0 {
		event.Iteration = fields.Iteration
	}
	if event.Provider == "" {
		event.Provider = fields.Provider
	}
	if event.RequestedModel == "" {
		event.RequestedModel = fields.RequestedModel
	}
	if event.ActualModel == "" {
		event.ActualModel = fields.ActualModel
	}
	if event.TaskState == "" {
		event.TaskState = fields.TaskState
	}
	if event.TargetID == "" {
		event.TargetID = fields.TargetID
	}
	if event.ToolCallID == "" {
		event.ToolCallID = fields.ToolCallID
	}
	return event
}

func mergeFields(base, next Fields) Fields {
	out := base
	if next.SessionID != "" {
		out.SessionID = next.SessionID
	}
	if next.RunID != "" {
		out.RunID = next.RunID
	}
	if next.TurnID != "" {
		out.TurnID = next.TurnID
	}
	if next.JobID != "" {
		out.JobID = next.JobID
	}
	if next.Phase != "" {
		out.Phase = next.Phase
	}
	if next.Iteration != 0 {
		out.Iteration = next.Iteration
	}
	if next.Provider != "" {
		out.Provider = next.Provider
	}
	if next.RequestedModel != "" {
		out.RequestedModel = next.RequestedModel
	}
	if next.ActualModel != "" {
		out.ActualModel = next.ActualModel
	}
	if next.TaskState != "" {
		out.TaskState = next.TaskState
	}
	if next.TargetID != "" {
		out.TargetID = next.TargetID
	}
	if next.ToolCallID != "" {
		out.ToolCallID = next.ToolCallID
	}
	return out
}

func redactEvent(event Event) Event {
	event.SessionID = secrets.Mask(event.SessionID)
	event.RunID = secrets.Mask(event.RunID)
	event.TurnID = secrets.Mask(event.TurnID)
	event.JobID = secrets.Mask(event.JobID)
	event.Provider = secrets.Mask(event.Provider)
	event.RequestedModel = secrets.Mask(event.RequestedModel)
	event.ActualModel = secrets.Mask(event.ActualModel)
	event.TargetID = secrets.Mask(event.TargetID)
	event.ToolCallID = secrets.Mask(event.ToolCallID)
	if len(event.Payload) > 0 {
		var raw any
		if err := json.Unmarshal(event.Payload, &raw); err == nil {
			raw = redactAny(raw)
			if data, marshalErr := json.Marshal(raw); marshalErr == nil {
				event.Payload = data
			}
		}
	}
	return event
}

func redactAny(v any) any {
	switch item := v.(type) {
	case string:
		return secrets.Mask(item)
	case []any:
		out := make([]any, len(item))
		for i, child := range item {
			out[i] = redactAny(child)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(item))
		for key, child := range item {
			out[key] = redactAny(child)
		}
		return out
	default:
		return item
	}
}

func newEventID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate telemetry event id: %w", err)
	}
	return "evt_" + strings.ToLower(hex.EncodeToString(raw[:])), nil
}
