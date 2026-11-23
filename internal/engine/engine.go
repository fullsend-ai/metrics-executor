package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/fullsend-ai/metrics-executor/internal/logging"
	"github.com/fullsend-ai/metrics-executor/internal/store"
)

// QueryRunner executes SQL against the DevLake database.
type QueryRunner interface {
	RunQuery(ctx context.Context, sqlText string, args []any) ([]map[string]any, error)
}

// DefaultScriptTimeout bounds the wall-clock time of a single input or output
// transform so a runaway script cannot pin a CPU core indefinitely.
const DefaultScriptTimeout = 30 * time.Second

type Engine struct {
	runner        QueryRunner
	scriptTimeout time.Duration
}

type Option func(*Engine)

// WithScriptTimeout overrides DefaultScriptTimeout. A value <= 0 disables the
// timeout; scripts are then only aborted when the request context is cancelled.
func WithScriptTimeout(d time.Duration) Option {
	return func(e *Engine) { e.scriptTimeout = d }
}

func New(runner QueryRunner, opts ...Option) *Engine {
	e := &Engine{runner: runner, scriptTimeout: DefaultScriptTimeout}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *Engine) Execute(ctx context.Context, wf *store.Workflow, input map[string]any) (any, error) {
	// Input transform
	inputStart := time.Now()
	params, err := runInputTransform(ctx, wf.InputScript, input, wf.Name, e.scriptTimeout)
	if err != nil {
		return nil, fmt.Errorf("input transform: %w", err)
	}
	inputDuration := time.Since(inputStart)

	logger := logging.FromContext(ctx)
	logger.DebugContext(ctx, "input transform completed", "workflow", wf.Name, "duration_ms", inputDuration.Milliseconds(), "param_keys", getKeys(params))

	// Execute queries
	results, err := e.executeQueries(ctx, wf.Queries, params, logger, wf.Name)
	if err != nil {
		return nil, err
	}

	// Output transform
	outputStart := time.Now()
	output, err := runOutputTransform(ctx, wf.OutputScript, input, results, wf.Name, e.scriptTimeout)
	if err != nil {
		return nil, fmt.Errorf("output transform: %w", err)
	}
	outputDuration := time.Since(outputStart)

	logger.DebugContext(ctx, "output transform completed", "workflow", wf.Name, "duration_ms", outputDuration.Milliseconds())

	return output, nil
}

func runInputTransform(ctx context.Context, script string, input map[string]any, wfName string, timeout time.Duration) (map[string]any, error) {
	if script == "" {
		return input, nil
	}

	logger := logging.FromContext(ctx)

	vm := goja.New()
	if err := setupConsole(vm, ctx, logger, wfName, "input_transform"); err != nil {
		return nil, err
	}
	if err := vm.Set("input", input); err != nil {
		return nil, err
	}

	val, err := runScript(ctx, vm, script, timeout)
	if err != nil {
		return nil, err
	}

	exported := val.Export()
	m, ok := exported.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("input script must return an object, got %T", exported)
	}
	return m, nil
}

func runOutputTransform(ctx context.Context, script string, input map[string]any, results map[string]any, wfName string, timeout time.Duration) (any, error) {
	if script == "" {
		return results, nil
	}

	logger := logging.FromContext(ctx)

	vm := goja.New()
	if err := setupConsole(vm, ctx, logger, wfName, "output_transform"); err != nil {
		return nil, err
	}
	if err := vm.Set("input", input); err != nil {
		return nil, err
	}
	if err := vm.Set("results", results); err != nil {
		return nil, err
	}

	val, err := runScript(ctx, vm, script, timeout)
	if err != nil {
		return nil, err
	}
	return val.Export(), nil
}

// runScript executes script on vm and interrupts it when timeout elapses or ctx
// is cancelled. goja can only interrupt JavaScript code, not native built-ins
// such as a single long-running regular expression match.
func runScript(ctx context.Context, vm *goja.Runtime, script string, timeout time.Duration) (goja.Value, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()

	val, err := vm.RunString(script)
	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) && ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && timeout > 0 {
				return nil, fmt.Errorf("script exceeded time limit of %s: %w", timeout, ctx.Err())
			}
			return nil, fmt.Errorf("script aborted: %w", ctx.Err())
		}
		return nil, err
	}
	return val, nil
}

func (e *Engine) executeQueries(ctx context.Context, queries []store.Query, params map[string]any, logger *slog.Logger, wfName string) (map[string]any, error) {
	stages := groupByStage(queries)
	results := make(map[string]any)
	var mu sync.Mutex

	for stageIdx, stage := range stages {
		scope := buildScope(results, params)

		labels := make([]string, len(stage))
		for i, q := range stage {
			labels[i] = q.Label
		}
		logger.DebugContext(ctx, "executing stage queries", "workflow", wfName, "stage", stageIdx, "query_count", len(stage), "query_labels", labels)

		var wg sync.WaitGroup
		errs := make([]error, len(stage))

		for i, q := range stage {
			wg.Add(1)
			go func(idx int, query store.Query) {
				defer wg.Done()
				stageStart := time.Now()
				resolvedSQL, args := resolveNamedParams(query.SQLText, scope)
				rows, err := e.runner.RunQuery(ctx, resolvedSQL, args)
				duration := time.Since(stageStart)

				if err != nil {
					errs[idx] = fmt.Errorf("query %q: %w", query.Label, err)
					logger.ErrorContext(ctx, "query execution failed", "workflow", wfName, "stage", stageIdx, "label", query.Label, "duration_ms", duration.Milliseconds(), "error", err)
					return
				}
				mu.Lock()
				results[query.Label] = rows
				mu.Unlock()
				logger.DebugContext(ctx, "query executed", "workflow", wfName, "stage", stageIdx, "label", query.Label, "sql", resolvedSQL, "args_count", len(args), "duration_ms", duration.Milliseconds(), "rows_returned", len(rows))
			}(i, q)
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
	}

	return results, nil
}

func buildScope(results map[string]any, params map[string]any) map[string]any {
	scope := make(map[string]any, len(results)+len(params))
	for k, v := range results {
		scope[k] = v
	}
	for k, v := range params {
		scope[k] = v
	}
	return scope
}

func resolveNamedParams(sqlText string, scope map[string]any) (string, []any) {
	var args []any
	var out strings.Builder
	i, n := 0, len(sqlText)

	for i < n {
		c := sqlText[i]

		if c == '-' && i+1 < n && sqlText[i+1] == '-' {
			start := i
			i += 2
			for i < n && sqlText[i] != '\n' {
				i++
			}
			out.WriteString(sqlText[start:i])
			continue
		}

		if c == '/' && i+1 < n && sqlText[i+1] == '*' {
			start := i
			i += 2
			for i+1 < n && !(sqlText[i] == '*' && sqlText[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
			out.WriteString(sqlText[start:i])
			continue
		}

		// MySQL treats '#' as a line comment, like '-- '.
		if c == '#' {
			start := i
			for i < n && sqlText[i] != '\n' {
				i++
			}
			out.WriteString(sqlText[start:i])
			continue
		}

		// Quoted strings ('...', "...") and identifiers (`...`) are copied
		// verbatim so a ':name' inside them is never treated as a parameter.
		// A doubled quote character escapes itself; inside strings a
		// backslash escapes the next character (MySQL default sql_mode).
		if c == '\'' || c == '"' || c == '`' {
			start := i
			i++
			for i < n {
				if sqlText[i] == '\\' && c != '`' && i+1 < n {
					i += 2
					continue
				}
				if sqlText[i] == c {
					i++
					if i < n && sqlText[i] == c {
						i++
						continue
					}
					break
				}
				i++
			}
			out.WriteString(sqlText[start:i])
			continue
		}

		if c == ':' && i+1 < n && isParamStart(sqlText[i+1]) {
			i++
			start := i
			for i < n && isWordChar(sqlText[i]) {
				i++
			}
			name := sqlText[start:i]
			val := scope[name]
			if slice, ok := toSlice(val); ok {
				if len(slice) == 0 {
					out.WriteString("NULL")
				} else {
					for j, v := range slice {
						if j > 0 {
							out.WriteString(", ")
						}
						out.WriteByte('?')
						args = append(args, v)
					}
				}
			} else {
				out.WriteByte('?')
				args = append(args, val)
			}
			continue
		}

		out.WriteByte(c)
		i++
	}

	return out.String(), args
}

func isParamStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isWordChar(c byte) bool {
	return isParamStart(c) || (c >= '0' && c <= '9')
}

func toSlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	if s, ok := v.([]any); ok {
		return s, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		result := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = rv.Index(i).Interface()
		}
		return result, true
	}
	return nil, false
}

func groupByStage(queries []store.Query) [][]store.Query {
	if len(queries) == 0 {
		return nil
	}
	stageMap := make(map[int][]store.Query)
	for _, q := range queries {
		stageMap[q.Stage] = append(stageMap[q.Stage], q)
	}
	nums := make([]int, 0, len(stageMap))
	for s := range stageMap {
		nums = append(nums, s)
	}
	sort.Ints(nums)
	stages := make([][]store.Query, len(nums))
	for i, s := range nums {
		stages[i] = stageMap[s]
	}
	return stages
}

func getKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func setupConsole(vm *goja.Runtime, ctx context.Context, logger *slog.Logger, wfName, stage string) error {
	console := vm.NewObject()

	// Helper to stringify values safely
	stringify := func(v any) string {
		switch val := v.(type) {
		case nil:
			return "null"
		case string:
			return val
		case error:
			return val.Error()
		case fmt.Stringer:
			return val.String()
		default:
			// Try JSON encoding for complex objects
			if b, err := json.Marshal(val); err == nil {
				return string(b)
			}
			return fmt.Sprintf("%v", val)
		}
	}

	// Combine multiple arguments into a single message
	joinArgs := func(args []goja.Value) string {
		strs := make([]string, len(args))
		for i, v := range args {
			strs[i] = stringify(v.Export())
		}
		return strings.Join(strs, " ")
	}

	for _, fn := range []string{"log", "info", "warn", "error", "debug"} {
		err := console.Set(fn, func(call goja.FunctionCall) goja.Value {
			msg := joinArgs(call.Arguments)
			// Truncate long messages (UTF-8 safe)
			runes := []rune(msg)
			if len(runes) > 2048 {
				msg = string(runes[:2048]) + " [truncated]"
			}

			// Map console functions to slog levels
			switch fn {
			case "log", "debug":
				logger.DebugContext(ctx, msg, "source", "script", "workflow", wfName, "stage", stage)
			case "info":
				logger.InfoContext(ctx, msg, "source", "script", "workflow", wfName, "stage", stage)
			case "warn":
				logger.WarnContext(ctx, msg, "source", "script", "workflow", wfName, "stage", stage)
			case "error":
				logger.ErrorContext(ctx, msg, "source", "script", "workflow", wfName, "stage", stage)
			}
			return goja.Undefined()
		})
		if err != nil {
			return err
		}
	}
	return vm.Set("console", console)
}
