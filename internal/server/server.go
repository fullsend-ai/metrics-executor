package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/fullsend-ai/metrics-executor/internal/logging"
	"github.com/fullsend-ai/metrics-executor/internal/store"
)

type Pinger interface {
	PingContext(ctx context.Context) error
}

type WorkflowStore interface {
	GetWorkflowByName(ctx context.Context, name string) (*store.Workflow, error)
}

type WorkflowEngine interface {
	Execute(ctx context.Context, wf *store.Workflow, input map[string]any) (any, error)
}

type Server struct {
	store   WorkflowStore
	engine  WorkflowEngine
	pingers []Pinger
	mux     *http.ServeMux
	opts    Options
	sem     chan struct{}
}

// Options controls runtime safeguards. The zero value preserves the historical
// behaviour with a bounded request timeout and concurrency limit.
type Options struct {
	RequestTimeout          time.Duration
	MaxConcurrentExecutions int
	// AllowedOrigins is the CORS allowlist. A nil slice keeps the historical
	// wildcard; an empty non-nil slice disables CORS entirely.
	AllowedOrigins []string
	LogClientIP    bool
}

const (
	// DefaultRequestTimeout bounds a workflow request end to end.
	DefaultRequestTimeout = 5 * time.Minute
	// DefaultMaxConcurrentExecutions caps in-flight workflow executions.
	DefaultMaxConcurrentExecutions = 32
)

func New(s WorkflowStore, e WorkflowEngine, pingers ...Pinger) *Server {
	return NewWithOptions(Options{}, s, e, pingers...)
}

func NewWithOptions(opts Options, s WorkflowStore, e WorkflowEngine, pingers ...Pinger) *Server {
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = DefaultRequestTimeout
	}
	if opts.MaxConcurrentExecutions <= 0 {
		opts.MaxConcurrentExecutions = DefaultMaxConcurrentExecutions
	}
	if opts.AllowedOrigins == nil {
		opts.AllowedOrigins = []string{"*"}
	}
	srv := &Server{
		store:   s,
		engine:  e,
		pingers: pingers,
		mux:     http.NewServeMux(),
		opts:    opts,
		sem:     make(chan struct{}, opts.MaxConcurrentExecutions),
	}
	srv.routes()
	return srv
}

func (s *Server) Handler() http.Handler {
	return withRequestID(withSecurityHeaders(withLoggingConfig(s.opts, withCORSConfig(s.opts.AllowedOrigins, s.mux))))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health/live", s.handleLiveness)
	s.mux.HandleFunc("GET /health/ready", s.handleReadiness)
	s.mux.Handle("/metrics", promhttp.Handler())
	s.mux.HandleFunc("GET /workflows/{name}", s.handleWorkflow)
	s.mux.HandleFunc("POST /workflows/{name}", s.handleWorkflow)
}

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	if s.store == nil || len(s.pingers) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	for i, pinger := range s.pingers {
		if err := pinger.PingContext(ctx); err != nil {
			logger.ErrorContext(ctx, "readiness probe failed", "pinger_index", i+1, "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWorkflow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)
	w.Header().Set("Cache-Control", "no-store")

	name := r.PathValue("name")
	requestID := logging.RequestIDFromContext(ctx)

	if s.store == nil || s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
		return
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":      "too many concurrent workflow executions",
			"request_id": requestID,
		})
		return
	}

	if s.opts.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.opts.RequestTimeout)
		defer cancel()
	}

	wf, err := s.store.GetWorkflowByName(ctx, name)
	if err != nil {
		logger.ErrorContext(ctx, "error loading workflow", "workflow", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":      "internal error",
			"request_id": requestID,
		})
		return
	}
	if wf == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workflow not found"})
		return
	}

	input, err := parseInput(r)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	// Log workflow request at DEBUG level
	paramKeys := make([]string, 0, len(r.URL.Query()))
	for k := range r.URL.Query() {
		paramKeys = append(paramKeys, k)
	}
	attrs := []any{"workflow", name, "method", r.Method, "param_keys", paramKeys}
	if s.opts.LogClientIP {
		attrs = append(attrs, "client_ip", r.RemoteAddr)
	}
	logger.DebugContext(ctx, "workflow request received", attrs...)

	output, err := s.engine.Execute(ctx, wf, input)
	if err != nil {
		logger.ErrorContext(ctx, "error executing workflow", "workflow", name, "error", err)
		if errors.Is(err, context.DeadlineExceeded) {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{
				"error":      "workflow execution timed out",
				"request_id": requestID,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":      "workflow execution failed",
			"request_id": requestID,
		})
		return
	}

	writeJSON(w, http.StatusOK, output)
}

// MaxRequestBodyBytes caps the size of a POST /workflows/{name} JSON body.
const MaxRequestBodyBytes = 1 << 20

// errBodyTooLarge lets the handler map an oversized body to 413.
var errBodyTooLarge = errors.New("request body too large")

func parseInput(r *http.Request) (map[string]any, error) {
	if r.Method == http.MethodPost {
		// Deliberately not http.MaxBytesReader: GO-2026-6859 is a header race
		// triggered when its limit is exceeded. A bounded read enforces the
		// same limit without entering that code path.
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBodyBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		if len(body) > MaxRequestBodyBytes {
			return nil, fmt.Errorf("request body exceeds %d bytes: %w", MaxRequestBodyBytes, errBodyTooLarge)
		}

		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var input map[string]any
		if err := dec.Decode(&input); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		if _, err := convertNumbers(input); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		return input, nil
	}
	input := make(map[string]any)
	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			input[key] = values[0]
		} else {
			arr := make([]any, len(values))
			for i, v := range values {
				arr[i] = v
			}
			input[key] = arr
		}
	}
	return input, nil
}

// convertNumbers replaces json.Number values with int64 when the literal is an
// integer that fits, and float64 otherwise. Decoding straight to float64 would
// silently corrupt integers above 2^53 (e.g. large IDs).
func convertNumbers(v any) (any, error) {
	switch val := v.(type) {
	case json.Number:
		if i, err := strconv.ParseInt(string(val), 10, 64); err == nil {
			return i, nil
		}
		f, err := val.Float64()
		if err != nil {
			return nil, fmt.Errorf("number %s out of range", val)
		}
		return f, nil
	case map[string]any:
		for k, item := range val {
			c, err := convertNumbers(item)
			if err != nil {
				return nil, err
			}
			val[k] = c
		}
		return val, nil
	case []any:
		for i, item := range val {
			c, err := convertNumbers(item)
			if err != nil {
				return nil, err
			}
			val[i] = c
		}
		return val, nil
	default:
		return v, nil
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so an encoding error cannot be reported to the client.
	_ = json.NewEncoder(w).Encode(v)
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract or generate request ID
		reqID := validRequestID(r.Header.Get("X-Request-ID"))
		if reqID == "" {
			reqID = validRequestID(r.Header.Get("X-Correlation-ID"))
		}
		if reqID == "" {
			// Generate 16-byte (32 hex char) random ID per ADR 0003
			buf := make([]byte, 16)
			if _, err := rand.Read(buf); err == nil {
				reqID = hex.EncodeToString(buf)
			} else {
				// Fallback to UUID truncated (not ideal but safe)
				reqID = uuid.New().String()[:16]
			}
		}

		// Inject request ID into context
		ctx := logging.WithRequestID(r.Context(), reqID)

		// Create logger with request ID
		logger := logging.FromContext(ctx).With("request_id", reqID)
		ctx = logging.WithLogger(ctx, logger)

		// Set response header
		w.Header().Set("X-Request-ID", reqID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// maxRequestIDLength bounds correlation IDs echoed into headers and logs.
const maxRequestIDLength = 128

// validRequestID accepts only characters that are safe in HTTP headers and log
// lines. Anything else is discarded so client input cannot inject fields.
func validRequestID(id string) string {
	if id == "" || len(id) > maxRequestIDLength {
		return ""
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '-':
		default:
			return ""
		}
	}
	return id
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func withCORSConfig(origins []string, next http.Handler) http.Handler {
	wildcard := false
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if o == "*" {
			wildcard = true
		}
		allowed[o] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(origins) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		if wildcard {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Add("Vary", "Origin")
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID, X-Correlation-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return withLoggingConfig(Options{}, next)
}

func withLoggingConfig(opts Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		// Get logger from context or create a new one
		ctx := r.Context()
		logger := logging.FromContext(ctx)

		next.ServeHTTP(sw, r)

		// Skip access logs for 200 OK on health probes and metrics
		if sw.status == http.StatusOK && (r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" || r.URL.Path == "/metrics") {
			return
		}

		duration := time.Since(start).Round(time.Millisecond)
		requestID := logging.RequestIDFromContext(ctx)
		attrs := []any{"request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration_ms", duration.Milliseconds(), "bytes_written", sw.bytesWritten}
		if opts.LogClientIP {
			attrs = append(attrs, "client_ip", r.RemoteAddr)
		}
		logger.InfoContext(ctx, "request completed", attrs...)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status       int
	wroteHeader  bool
	bytesWritten int
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.wroteHeader {
		sw.status = code
		sw.wroteHeader = true
		sw.ResponseWriter.WriteHeader(code)
	}
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wroteHeader {
		sw.WriteHeader(http.StatusOK)
	}
	n, err := sw.ResponseWriter.Write(b)
	sw.bytesWritten += n
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (sw *statusWriter) Unwrap() http.ResponseWriter {
	return sw.ResponseWriter
}

// Flush preserves streaming semantics for handlers that flush explicitly.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
