// Package log provides a thin structured logging wrapper around slog.
package log

import (
	"context"
	"io"
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
// level should be one of: off, debug, info, warn, error.
// "off" discards all log output (default for clean agent-only terminal output).
// format should be "json" or "text".
func Init(level, format string) {
	InitWithWriter(level, format, os.Stderr)
}

// InitWithWriter initializes the global logger, directing output to the
// provided writer.
func InitWithWriter(level, format string, out io.Writer) {
	if out == nil {
		out = os.Stderr
	}

	// "off" silences all structured logs so only agent output reaches the terminal.
	if strings.ToLower(level) == "off" {
		handler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})
		slog.SetDefault(slog.New(handler))
		return
	}

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
	if strings.ToLower(format) != "json" {
		opts.ReplaceAttr = func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		}
	}

	var handler slog.Handler
	if strings.ToLower(format) == "json" {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
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
