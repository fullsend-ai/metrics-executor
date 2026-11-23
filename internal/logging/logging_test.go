package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSetup_JSON(t *testing.T) {
	var buf bytes.Buffer
	logger := Setup(slog.LevelDebug, "json", &buf)

	// Verify default logger is set
	if slog.Default() != logger {
		t.Error("default logger not set")
	}

	// Test logging
	logger.Info("test message", "key", "value")

	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Errorf("expected 'test message' in output, got: %s", output)
	}
	if !strings.Contains(output, `"key":"value"`) {
		t.Errorf("expected structured output, got: %s", output)
	}
}

func TestSetup_Text(t *testing.T) {
	var buf bytes.Buffer
	logger := Setup(slog.LevelDebug, "text", &buf)

	logger.Info("test message", "key", "value")

	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Errorf("expected 'test message' in output, got: %s", output)
	}
}

func TestSetup_InvalidFormatDefaultsToJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := Setup(slog.LevelDebug, "invalid", &buf)

	logger.Info("test message", "key", "value")

	output := buf.String()
	if !strings.Contains(output, `"key":"value"`) {
		t.Errorf("expected JSON output for invalid format, got: %s", output)
	}
}

func TestWithLogger(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	ctxWithLogger := WithLogger(ctx, logger)

	if FromContext(ctxWithLogger) != logger {
		t.Error("logger not retrieved from context")
	}
}

func TestFromContext_Default(t *testing.T) {
	ctx := context.Background()

	logger := FromContext(ctx)

	if logger == nil {
		t.Error("expected default logger")
	}
}

func TestWithRequestID(t *testing.T) {
	ctx := context.Background()

	ctxWithID := WithRequestID(ctx, "abc123")

	if RequestIDFromContext(ctxWithID) != "abc123" {
		t.Errorf("expected 'abc123', got '%s'", RequestIDFromContext(ctxWithID))
	}
}

func TestRequestIDFromContext_Empty(t *testing.T) {
	ctx := context.Background()

	reqID := RequestIDFromContext(ctx)

	if reqID != "" {
		t.Errorf("expected empty string, got '%s'", reqID)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"warn", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"error", slog.LevelError},
		{"invalid", slog.LevelInfo}, // default
		{"", slog.LevelInfo},        // default
		{"  warn  ", slog.LevelWarn},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			level := ParseLevel(tt.input)
			if level != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, level)
			}
		})
	}
}
