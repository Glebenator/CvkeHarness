package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var (
	mu sync.Mutex
	// Matches common API key patterns (sk-..., or generic sk-or-v1- for OpenRouter)
	secretRegex = regexp.MustCompile(`(sk-[a-zA-Z0-9]{20,})|(sk-or-v1-[a-zA-Z0-9]{40,})`)
)

type contextKey struct {
	name string
}

var modelContextKey = &contextKey{"actual_model"}

// WithModel returns a new context with the actual model identifier attached.
func WithModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, modelContextKey, model)
}

// ModelFromContext extracts the actual model identifier if present.
func ModelFromContext(ctx context.Context) string {
	if val, ok := ctx.Value(modelContextKey).(string); ok {
		return val
	}
	return ""
}

// TelemetryEvent represents a single execution event for analytics.
type TelemetryEvent struct {
	Timestamp       time.Time `json:"timestamp"`
	Model           string    `json:"model"`
	ToolName        string    `json:"tool_name,omitempty"`
	BaseCommand     string    `json:"base_command,omitempty"`
	FullCommand     string    `json:"full_command,omitempty"`
	ApprovedByJudge bool      `json:"approved_by_judge,omitempty"`
	ApprovedByUser  bool      `json:"approved_by_user,omitempty"`
	ApprovalMode    string    `json:"approval_mode,omitempty"`
	Success         bool      `json:"success"`
	DurationMs      int64     `json:"duration_ms"`
	ErrorMessage    string    `json:"error_message,omitempty"`
}

// RecordEvent appends a structured telemetry event to a JSONL file.
func RecordEvent(event TelemetryEvent) error {
	mu.Lock()
	defer mu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ".cvkeharness")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	path := filepath.Join(dir, "telemetry.jsonl")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Mask potential secrets in fields that might contain them
	event.FullCommand = maskSecrets(event.FullCommand)
	event.ErrorMessage = maskSecrets(event.ErrorMessage)

	b, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = f.Write(append(b, '\n'))
	return err
}

func maskSecrets(s string) string {
	if s == "" {
		return s
	}
	return secretRegex.ReplaceAllString(s, "[REDACTED]")
}
