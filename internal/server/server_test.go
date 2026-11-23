package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fullsend-ai/metrics-executor/internal/logging"
	"github.com/fullsend-ai/metrics-executor/internal/store"
)

type mockStore struct {
	workflows map[string]*store.Workflow
	err       error
}

func (m *mockStore) GetWorkflowByName(_ context.Context, name string) (*store.Workflow, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.workflows[name], nil
}

type mockEngine struct {
	output any
	err    error
}

func (m *mockEngine) Execute(_ context.Context, _ *store.Workflow, _ map[string]any) (any, error) {
	return m.output, m.err
}

type fastPinger struct {
	err error
}

func (m *fastPinger) PingContext(ctx context.Context) error {
	return m.err
}

type slowPinger struct {
	duration time.Duration
}

func (m *slowPinger) PingContext(ctx context.Context) error {
	select {
	case <-time.After(m.duration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestHealthLive(t *testing.T) {
	srv := New(nil, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health/live", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("got status %q, want ok", body["status"])
	}
}

func TestHealthReady_OK(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"test": {ID: 1, Name: "test"},
	}}
	pinger := &fastPinger{err: nil}
	srv := New(st, nil, pinger)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("got status %q, want ok", body["status"])
	}
}

func TestHealthReady_FailedPinger(t *testing.T) {
	pinger := &fastPinger{err: errors.New("connection refused")}
	srv := New(nil, nil, pinger)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Errorf("got status %q, want not_ready", body["status"])
	}
}

func TestHealthReady_Timeout(t *testing.T) {
	pinger := &slowPinger{duration: 5 * time.Second}
	srv := New(nil, nil, pinger)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Errorf("got status %q, want not_ready", body["status"])
	}
}

func TestHealthReady_NoStore(t *testing.T) {
	pinger := &fastPinger{err: nil}
	srv := New(nil, nil, pinger)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Errorf("got status %q, want not_ready", body["status"])
	}
}

func TestHealthReady_NoPingers(t *testing.T) {
	srv := New(nil, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Errorf("got status %q, want not_ready", body["status"])
	}
}

func TestHealthLegacyRemoved(t *testing.T) {
	srv := New(nil, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", rec.Code)
	}
}

func TestHealthReady_MultiplePingersOneFails(t *testing.T) {
	pinger1 := &fastPinger{err: nil}
	pinger2 := &fastPinger{err: errors.New("db2 connection failed")}
	srv := New(nil, nil, pinger1, pinger2)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Errorf("got status %q, want not_ready", body["status"])
	}
}

func TestWithLogging_SuppressHealthLive200(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	handler := withLogging(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestWithLogging_SuppressHealthReady200(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	handler := withLogging(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/ready", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestWithLogging_SuppressMetrics200(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`# metrics`))
	}))
	handler := withLogging(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestWithLogging_LogNon200Health(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
	})
	handler := withLogging(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", rec.Code)
	}
}

func TestWithLogging_LogWorkflowRequest(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"test-wf": {ID: 1, Name: "test-wf"},
	}}
	eng := &mockEngine{output: map[string]any{"result": "hello"}}
	srv := New(st, eng)

	handler := withLogging(srv.mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/workflows/test-wf", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestHealth(t *testing.T) {
	srv := New(nil, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", rec.Code)
	}
}

func TestWorkflow_GET(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"test-wf": {ID: 1, Name: "test-wf"},
	}}
	eng := &mockEngine{output: map[string]any{"result": "hello"}}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/test-wf?key=val", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["result"] != "hello" {
		t.Errorf("got %v, want hello", body["result"])
	}
}

func TestWorkflow_POST(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"test-wf": {ID: 1, Name: "test-wf"},
	}}
	eng := &mockEngine{output: map[string]any{"ok": true}}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/workflows/test-wf", strings.NewReader(`{"team_id":"t1"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestWorkflow_NotFound(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{}}
	eng := &mockEngine{}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/nonexistent", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", rec.Code)
	}
}

func TestWorkflow_StoreError(t *testing.T) {
	st := &mockStore{err: errors.New("db down")}
	eng := &mockEngine{}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/any", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", rec.Code)
	}
}

func TestWorkflow_ExecutionError(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"bad": {ID: 1, Name: "bad"},
	}}
	eng := &mockEngine{err: errors.New("script failed")}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/bad", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "workflow execution failed" {
		t.Errorf("got error %q, want workflow execution failed", body["error"])
	}
	if body["request_id"] == "" {
		t.Error("expected request_id in error body")
	}
}

func TestWorkflow_NoDB(t *testing.T) {
	srv := New(nil, nil)

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/any", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", rec.Code)
	}
}

func TestWorkflow_InvalidJSON(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"wf": {ID: 1, Name: "wf"},
	}}
	eng := &mockEngine{}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/workflows/wf", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", rec.Code)
	}
}

func TestWorkflow_QueryParamArrays(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"wf": {ID: 1, Name: "wf"},
	}}

	var capturedInput map[string]any
	eng := &captureEngine{output: map[string]any{}, capture: func(input map[string]any) {
		capturedInput = input
	}}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/workflows/wf?tag=a&tag=b&id=1", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	if capturedInput["id"] != "1" {
		t.Errorf("got id %v, want 1", capturedInput["id"])
	}
	tags, ok := capturedInput["tag"].([]any)
	if !ok {
		t.Fatalf("expected []any for tag, got %T", capturedInput["tag"])
	}
	if len(tags) != 2 {
		t.Errorf("got %d tags, want 2", len(tags))
	}
}

type captureEngine struct {
	output  any
	capture func(map[string]any)
}

func (e *captureEngine) Execute(_ context.Context, _ *store.Workflow, input map[string]any) (any, error) {
	if e.capture != nil {
		e.capture(input)
	}
	return e.output, nil
}

func TestServer_ConcurrentRequests(t *testing.T) {
	srv := New(nil, nil)
	handler := srv.Handler()

	done := make(chan bool, 100)

	for i := 0; i < 100; i++ {
		go func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/health/live", nil)
			handler.ServeHTTP(rec, req)
			done <- true
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestWithRequestID_ExtractHeader(t *testing.T) {
	srv := New(nil, nil)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("X-Request-ID", "test-req-id-12345")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "test-req-id-12345" {
		t.Errorf("expected X-Request-ID header to be 'test-req-id-12345', got '%s'", rec.Header().Get("X-Request-ID"))
	}
}

func TestWithRequestID_GenerateID(t *testing.T) {
	srv := New(nil, nil)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	handler.ServeHTTP(rec, req)

	reqID := rec.Header().Get("X-Request-ID")
	if reqID == "" {
		t.Error("expected X-Request-ID header to be set")
	}
	// Verify it's a 32-character hex string (16 bytes per ADR 0003)
	if len(reqID) != 32 {
		t.Errorf("expected X-Request-ID to be 32 chars (16 bytes hex), got %d", len(reqID))
	}
	// Verify it's valid hex
	for i := range reqID {
		if !((reqID[i] >= '0' && reqID[i] <= '9') || (reqID[i] >= 'a' && reqID[i] <= 'f')) {
			t.Errorf("expected X-Request-ID to be hex, got char '%c' at position %d", reqID[i], i)
		}
	}
}

func TestRequestIDInContext(t *testing.T) {
	var capturedRequestID string

	// Create a test handler that captures the context
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		capturedRequestID = logging.RequestIDFromContext(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	handler := withRequestID(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "ctx-test-id-1234")
	handler.ServeHTTP(rec, req)

	if capturedRequestID != "ctx-test-id-1234" {
		t.Errorf("expected request ID 'ctx-test-id-1234', got '%s'", capturedRequestID)
	}
}

func TestWithRequestID_XCorrelationID(t *testing.T) {
	srv := New(nil, nil)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("X-Correlation-ID", "correlation-id-xyz")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "correlation-id-xyz" {
		t.Errorf("expected X-Request-ID header to be 'correlation-id-xyz', got '%s'", rec.Header().Get("X-Request-ID"))
	}
}

func TestWithRequestID_RequestIDInWorkflowResponse(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"test-wf": {ID: 1, Name: "test-wf"},
	}}
	eng := &mockEngine{output: map[string]any{"result": "hello"}}
	srv := New(st, eng)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/workflows/test-wf", nil)
	req.Header.Set("X-Request-ID", "test-request-id-123")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "test-request-id-123" {
		t.Errorf("expected X-Request-ID header to be 'test-request-id-123', got '%s'", rec.Header().Get("X-Request-ID"))
	}
}

func TestWithRequestID_GeneratedIDIs32Chars(t *testing.T) {
	srv := New(nil, nil)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	handler.ServeHTTP(rec, req)

	reqID := rec.Header().Get("X-Request-ID")
	if len(reqID) != 32 {
		t.Errorf("expected X-Request-ID to be 32 chars (16 bytes hex), got %d chars: '%s'", len(reqID), reqID)
	}
	// Verify it's valid hex
	for i := range reqID {
		if !((reqID[i] >= '0' && reqID[i] <= '9') || (reqID[i] >= 'a' && reqID[i] <= 'f')) {
			t.Errorf("expected X-Request-ID to be hex, got char '%c' at position %d", reqID[i], i)
		}
	}
}

func TestWithCORS_ExposesRequestIDHeader(t *testing.T) {
	srv := New(nil, nil)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/health/live", nil)
	handler.ServeHTTP(rec, req)

	// CORS preflight should include Access-Control-Expose-Headers
	exposeHeaders := rec.Header().Get("Access-Control-Expose-Headers")
	if exposeHeaders == "" {
		t.Error("expected Access-Control-Expose-Headers to be set for CORS preflight")
	}
	if !strings.Contains(exposeHeaders, "X-Request-ID") {
		t.Errorf("expected Access-Control-Expose-Headers to include 'X-Request-ID', got '%s'", exposeHeaders)
	}
}

func TestWithRequestID_RequestIDInErrorResponse(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{}}
	eng := &mockEngine{}
	srv := New(st, eng)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/workflows/nonexistent", nil)
	req.Header.Set("X-Request-ID", "error-test-id-456")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "error-test-id-456" {
		t.Errorf("expected X-Request-ID header in error response to be 'error-test-id-456', got '%s'", rec.Header().Get("X-Request-ID"))
	}
}

func TestWithRequestID_RequestIDInHealthLive(t *testing.T) {
	srv := New(nil, nil)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/live", nil)
	req.Header.Set("X-Request-ID", "health-live-id-789")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "health-live-id-789" {
		t.Errorf("expected X-Request-ID header to be 'health-live-id-789', got '%s'", rec.Header().Get("X-Request-ID"))
	}
}

func TestWithRequestID_RequestIDInHealthReady(t *testing.T) {
	st := &mockStore{workflows: map[string]*store.Workflow{
		"test": {ID: 1, Name: "test"},
	}}
	pinger := &fastPinger{err: nil}
	srv := New(st, nil, pinger)
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/ready", nil)
	req.Header.Set("X-Request-ID", "health-ready-id-999")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "health-ready-id-999" {
		t.Errorf("expected X-Request-ID header to be 'health-ready-id-999', got '%s'", rec.Header().Get("X-Request-ID"))
	}
}

func postCapture(t *testing.T, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	st := &mockStore{workflows: map[string]*store.Workflow{"wf": {ID: 1, Name: "wf"}}}
	var captured map[string]any
	eng := &captureEngine{output: map[string]any{"ok": true}, capture: func(in map[string]any) { captured = in }}
	srv := New(st, eng)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/workflows/wf", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec, captured
}

func TestWorkflow_POST_BodyTooLarge(t *testing.T) {
	body := `{"x":"` + strings.Repeat("a", MaxRequestBodyBytes) + `"}`
	rec, captured := postCapture(t, body)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got status %d, want 413", rec.Code)
	}
	if captured != nil {
		t.Error("engine must not run for oversized body")
	}
}

func TestWorkflow_POST_BodyJustUnderLimit(t *testing.T) {
	prefix, suffix := `{"x":"`, `"}`
	body := prefix + strings.Repeat("a", MaxRequestBodyBytes-len(prefix)-len(suffix)) + suffix
	rec, captured := postCapture(t, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(captured["x"].(string)) != MaxRequestBodyBytes-len(prefix)-len(suffix) {
		t.Error("body was truncated")
	}
}

func TestWorkflow_POST_NumberTypes(t *testing.T) {
	rec, in := postCapture(t, `{"small": 42, "neg": -7, "big": 9007199254740993, "float": 1.5,
		"exp": 1e3, "huge": 18446744073709551616, "nested": {"n": 3, "arr": [1, 2.5, "s", true, null]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	checks := map[string]any{
		"small": int64(42),
		"neg":   int64(-7),
		"big":   int64(9007199254740993), // would be rounded to ...992 as float64
		"float": 1.5,
		"exp":   1000.0,
		"huge":  1.8446744073709552e19,
	}
	for k, want := range checks {
		if in[k] != want {
			t.Errorf("%s = %v (%T), want %v (%T)", k, in[k], in[k], want, want)
		}
	}
	nested := in["nested"].(map[string]any)
	if nested["n"] != int64(3) {
		t.Errorf("nested.n = %v (%T), want int64 3", nested["n"], nested["n"])
	}
	arr := nested["arr"].([]any)
	wantArr := []any{int64(1), 2.5, "s", true, nil}
	for i := range wantArr {
		if arr[i] != wantArr[i] {
			t.Errorf("arr[%d] = %v (%T), want %v (%T)", i, arr[i], arr[i], wantArr[i], wantArr[i])
		}
	}
}

func TestWorkflow_POST_NumberOutOfRange(t *testing.T) {
	rec, captured := postCapture(t, `{"x": 1e400}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", rec.Code)
	}
	if captured != nil {
		t.Error("engine must not run for invalid body")
	}
}

func TestWorkflow_POST_NullBody(t *testing.T) {
	rec, _ := postCapture(t, `null`)
	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestWorkflow_POST_NonObjectBody(t *testing.T) {
	rec, _ := postCapture(t, `[1,2]`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", rec.Code)
	}
}
