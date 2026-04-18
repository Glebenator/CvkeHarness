// Package log provides a thin structured logging wrapper around slog.
package log

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type contextKey string

const (
	keyRequestID contextKey = "request_id"
	keyToolName  contextKey = "tool_name"
	keyIteration contextKey = "iteration"
)

// Init initializes the global slog logger.
// level should be one of: debug, info, warn, error.
// format should be "json" or "text".
func Init(level, format string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if strings.ToLower(format) == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	slog.SetDefault(slog.New(handler))
}

// WithRequestID adds a request ID to the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// WithToolName adds a tool name to the context.
func WithToolName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, keyToolName, name)
}

// WithIteration adds an iteration number to the context.
func WithIteration(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, keyIteration, n)
}

// FromContext returns a logger with contextual fields extracted from ctx.
func FromContext(ctx context.Context) *slog.Logger {
	logger := slog.Default()

	if id, ok := ctx.Value(keyRequestID).(string); ok && id != "" {
		logger = logger.With("request_id", id)
	}
	if name, ok := ctx.Value(keyToolName).(string); ok && name != "" {
		logger = logger.With("tool", name)
	}
	if n, ok := ctx.Value(keyIteration).(int); ok {
		logger = logger.With("iteration", n)
	}

	return logger
}
