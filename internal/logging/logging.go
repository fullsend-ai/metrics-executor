package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

const requestIDKey = "request_id"

// Setup initializes the global logger with the specified level, format, and output writer.
// It configures either JSON or text handler based on the format string.
func Setup(level slog.Level, format string, w io.Writer) *slog.Logger {
	var handler slog.Handler

	formatLower := strings.ToLower(strings.TrimSpace(format))
	switch formatLower {
	case "json":
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: level,
		})
	case "text":
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{
			Level: level,
		})
	default:
		// Default to JSON
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: level,
		})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// WithLogger returns a new context with the logger attached.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey("logger"), logger)
}

// FromContext returns the logger from the context, or the default logger if none is present.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(contextKey("logger")).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// WithRequestID returns a new context with the request ID attached.
func WithRequestID(ctx context.Context, reqID string) context.Context {
	return context.WithValue(ctx, contextKey(requestIDKey), reqID)
}

// RequestIDFromContext returns the request ID from the context, or an empty string if none is present.
func RequestIDFromContext(ctx context.Context) string {
	if reqID, ok := ctx.Value(contextKey(requestIDKey)).(string); ok {
		return reqID
	}
	return ""
}

// ParseLevel parses a string representation of a log level.
// Accepts: DEBUG, INFO, WARN, ERROR (case-insensitive, surrounding whitespace ignored).
// Unknown values fall back to slog.LevelInfo.
func ParseLevel(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type contextKey string
