package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fullsend-ai/metrics-executor/internal/logging"
	"github.com/fullsend-ai/metrics-executor/internal/store"
)

type blockingEngine struct {
	started  chan struct{}
	release  chan struct{}
	startOne sync.Once
}

func newBlockingEngine() *blockingEngine {
	return &blockingEngine{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingEngine) Execute(_ context.Context, _ *store.Workflow, _ map[string]any) (any, error) {
	b.startOne.Do(func() { close(b.started) })
	<-b.release
	return map[string]any{"ok": true}, nil
}

type deadlineEngine struct{}

func (deadlineEngine) Execute(ctx context.Context, _ *store.Workflow, _ map[string]any) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func workflowServer(t *testing.T, opts Options, eng WorkflowEngine) *Server {
	t.Helper()
	st := &mockStore{workflows: map[string]*store.Workflow{
		"wf": {ID: 1, Name: "wf"},
	}}
	return NewWithOptions(opts, st, eng)
}

func TestWorkflowExecutionErrorDoesNotLeakInternals(t *testing.T) {
	eng := &mockEngine{err: errors.New(`query "q1": access denied for user 'dbuser' to table secret_table`)}
	srv := workflowServer(t, Options{}, eng)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/wf", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"dbuser", "secret_table", "access denied"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaked internal detail %q: %s", leak, body)
		}
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["error"] != "workflow execution failed" {
		t.Errorf("error = %q, want generic message", payload["error"])
	}
	if payload["request_id"] == "" {
		t.Error("expected request_id in error response")
	}
}

func TestWorkflowStoreErrorIncludesRequestIDOnly(t *testing.T) {
	st := &mockStore{err: errors.New("dial tcp 10.0.0.1:3306: connection refused")}
	srv := NewWithOptions(Options{}, st, &mockEngine{})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/wf", nil))

	body := rec.Body.String()
	if strings.Contains(body, "10.0.0.1") {
		t.Errorf("response leaked internal detail: %s", body)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["error"] != "internal error" || payload["request_id"] == "" {
		t.Errorf("unexpected payload: %v", payload)
	}
}

func TestRequestIDInvalidValueIsReplaced(t *testing.T) {
	srv := New(nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("X-Request-ID", "evil\ninjected id")

	srv.Handler().ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-ID")
	if got == "" || got == "evil\ninjected id" {
		t.Fatalf("invalid request ID was not replaced: %q", got)
	}
	if len(got) != 32 {
		t.Errorf("generated request ID length = %d, want 32 hex characters", len(got))
	}
	if strings.ContainsAny(got, "\n\r ") {
		t.Errorf("generated request ID contains invalid characters: %q", got)
	}
}

func TestRequestIDValidValueIsPreserved(t *testing.T) {
	srv := New(nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("X-Request-ID", "abc.DEF_123-xyz")

	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "abc.DEF_123-xyz" {
		t.Errorf("valid request ID = %q, want it preserved", got)
	}
}

func TestCORSDefaultIsWildcard(t *testing.T) {
	srv := New(nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("Origin", "https://anything.example")

	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

func TestCORSConfiguredOrigin(t *testing.T) {
	srv := NewWithOptions(Options{AllowedOrigins: []string{"https://ok.example"}}, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("Origin", "https://ok.example")
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://ok.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want configured origin", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("Origin", "https://evil.example")
	srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin received %q", got)
	}
}

func TestCORSDisabled(t *testing.T) {
	srv := NewWithOptions(Options{AllowedOrigins: []string{}}, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/workflows/wf", nil)
	req.Header.Set("Origin", "https://anything.example")
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS header present while disabled: %q", got)
	}
	if rec.Code == http.StatusNoContent {
		t.Errorf("OPTIONS was answered with 204 while CORS is disabled")
	}
}

func TestWorkflowConcurrencyLimit(t *testing.T) {
	eng := newBlockingEngine()
	srv := workflowServer(t, Options{MaxConcurrentExecutions: 1}, eng)

	firstDone := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/wf", nil))
		firstDone <- rec.Code
	}()

	select {
	case <-eng.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not start")
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/wf", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("saturated request status = %d, want 503", rec.Code)
	}

	close(eng.release)
	if code := <-firstDone; code != http.StatusOK {
		t.Errorf("first request status = %d, want 200", code)
	}
}

func TestWorkflowRequestTimeout(t *testing.T) {
	srv := workflowServer(t, Options{RequestTimeout: 20 * time.Millisecond}, deadlineEngine{})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/wf", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", rec.Code)
	}
}

func TestAPIResponseHeaders(t *testing.T) {
	srv := workflowServer(t, Options{}, &mockEngine{output: map[string]any{"ok": true}})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/wf", nil))

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestClientIPNotLoggedByDefault(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.Setup(slog.LevelDebug, "text", &buf)
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	srv := workflowServer(t, Options{}, &mockEngine{output: map[string]any{"ok": true}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/workflows/wf", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	srv.Handler().ServeHTTP(rec, req)

	if strings.Contains(buf.String(), "client_ip") || strings.Contains(buf.String(), "203.0.113.9") {
		t.Errorf("client IP logged by default:\n%s", buf.String())
	}
}

func TestClientIPLoggedWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.Setup(slog.LevelDebug, "text", &buf)
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	srv := workflowServer(t, Options{LogClientIP: true}, &mockEngine{output: map[string]any{"ok": true}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/workflows/wf", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	srv.Handler().ServeHTTP(rec, req)

	if !strings.Contains(buf.String(), "client_ip=203.0.113.9:1234") {
		t.Errorf("expected RemoteAddr client_ip in log, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "198.51.100.7") {
		t.Errorf("spoofable X-Forwarded-For was logged:\n%s", buf.String())
	}
}

func TestStatusWriterImplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}

	if _, err := sw.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if sw.status != http.StatusOK {
		t.Errorf("status = %d, want 200", sw.status)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("recorder status = %d, want 200", rec.Code)
	}
}

func TestStatusWriterUnwrapAndFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}

	if sw.Unwrap() != http.ResponseWriter(rec) {
		t.Error("Unwrap did not return the wrapped writer")
	}
	sw.Flush()
	if !rec.Flushed {
		t.Error("Flush did not reach the underlying writer")
	}
}
