package engine

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fullsend-ai/metrics-executor/internal/logging"
	"github.com/fullsend-ai/metrics-executor/internal/store"
)

type mockRunner struct {
	results map[string][]map[string]any
	err     error
}

func (m *mockRunner) RunQuery(_ context.Context, sqlText string, _ []any) ([]map[string]any, error) {
	if m.err != nil {
		return nil, m.err
	}
	if rows, ok := m.results[sqlText]; ok {
		return rows, nil
	}
	return []map[string]any{}, nil
}

func TestExecute_SimpleWorkflow(t *testing.T) {
	runner := &mockRunner{
		results: map[string][]map[string]any{
			"SELECT id, name FROM teams WHERE id = ?": {
				{"id": "t1", "name": "Team One"},
			},
		},
	}
	eng := New(runner)

	wf := &store.Workflow{
		InputScript:  `({team_id: input.team_id})`,
		OutputScript: `({team: results.team_info[0]})`,
		Queries: []store.Query{
			{Stage: 0, Label: "team_info", SQLText: "SELECT id, name FROM teams WHERE id = :team_id"},
		},
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{"team_id": "t1"})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	team := m["team"].(map[string]any)
	if team["name"] != "Team One" {
		t.Errorf("got name %v, want Team One", team["name"])
	}
}

func TestExecute_MultiStageQueries(t *testing.T) {
	runner := &mockRunner{
		results: map[string][]map[string]any{
			"SELECT id FROM teams WHERE name = ?":          {{"id": "t1"}},
			"SELECT source FROM sources WHERE team_id = ?": {{"source": "github"}},
		},
	}
	eng := New(runner)

	wf := &store.Workflow{
		InputScript:  `({team_name: input.name, team_id: input.name})`,
		OutputScript: `({sources: results.sources})`,
		Queries: []store.Query{
			{Stage: 0, Label: "team", SQLText: "SELECT id FROM teams WHERE name = :team_name"},
			{Stage: 1, Label: "sources", SQLText: "SELECT source FROM sources WHERE team_id = :team_id"},
		},
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{"name": "team1"})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	sources := m["sources"].([]map[string]any)
	if len(sources) != 1 {
		t.Errorf("got %d sources, want 1", len(sources))
	}
}

func TestExecute_ParallelQueriesInStage(t *testing.T) {
	runner := &mockRunner{
		results: map[string][]map[string]any{
			"SELECT count(*) as c FROM orgs":     {{"c": int64(5)}},
			"SELECT count(*) as c FROM teams":    {{"c": int64(3)}},
			"SELECT count(*) as c FROM products": {{"c": int64(7)}},
		},
	}
	eng := New(runner)

	wf := &store.Workflow{
		InputScript:  `({})`,
		OutputScript: `({orgs: results.org_count, teams: results.team_count, products: results.product_count})`,
		Queries: []store.Query{
			{Stage: 0, Label: "org_count", SQLText: "SELECT count(*) as c FROM orgs"},
			{Stage: 0, Label: "team_count", SQLText: "SELECT count(*) as c FROM teams"},
			{Stage: 0, Label: "product_count", SQLText: "SELECT count(*) as c FROM products"},
		},
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	for _, key := range []string{"orgs", "teams", "products"} {
		if m[key] == nil {
			t.Errorf("missing key %q in output", key)
		}
	}
}

func TestExecute_EmptyInputScript(t *testing.T) {
	runner := &mockRunner{}
	eng := New(runner)

	wf := &store.Workflow{
		InputScript:  "",
		OutputScript: `({got: input.key})`,
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{"key": "value"})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	if m["got"] != "value" {
		t.Errorf("got %v, want value", m["got"])
	}
}

func TestExecute_EmptyOutputScript(t *testing.T) {
	runner := &mockRunner{
		results: map[string][]map[string]any{
			"SELECT 1 as n": {{"n": int64(1)}},
		},
	}
	eng := New(runner)

	wf := &store.Workflow{
		InputScript:  `({})`,
		OutputScript: "",
		Queries: []store.Query{
			{Stage: 0, Label: "data", SQLText: "SELECT 1 as n"},
		},
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	data := m["data"].([]map[string]any)
	if len(data) != 1 {
		t.Errorf("got %d rows, want 1", len(data))
	}
}

func TestExecute_NoQueries(t *testing.T) {
	eng := New(&mockRunner{})

	wf := &store.Workflow{
		InputScript:  `({x: 1})`,
		OutputScript: `({empty: true})`,
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	if m["empty"] != true {
		t.Errorf("got %v, want true", m["empty"])
	}
}

func TestExecute_InputTransformError(t *testing.T) {
	eng := New(&mockRunner{})

	wf := &store.Workflow{
		InputScript:  `throw new Error("bad input")`,
		OutputScript: `({})`,
	}

	_, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != `input transform: Error: bad input at <eval>:1:7(3)` {
		if len(got) < 15 || got[:15] != "input transform" {
			t.Errorf("expected input transform error, got: %s", got)
		}
	}
}

func TestExecute_OutputTransformError(t *testing.T) {
	eng := New(&mockRunner{})

	wf := &store.Workflow{
		InputScript:  `({})`,
		OutputScript: `throw new Error("bad output")`,
	}

	_, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); len(got) < 16 || got[:16] != "output transform" {
		t.Errorf("expected output transform error, got: %s", got)
	}
}

func TestExecute_QueryError(t *testing.T) {
	runner := &mockRunner{err: errors.New("connection refused")}
	eng := New(runner)

	wf := &store.Workflow{
		InputScript:  `({})`,
		OutputScript: `({})`,
		Queries: []store.Query{
			{Stage: 0, Label: "data", SQLText: "SELECT 1"},
		},
	}

	_, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecute_ConsoleNoOp(t *testing.T) {
	eng := New(&mockRunner{})

	wf := &store.Workflow{
		InputScript:  `console.log("hello"); console.warn("w"); ({ok: true})`,
		OutputScript: `console.error("e"); ({done: true})`,
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	if m["done"] != true {
		t.Errorf("got %v, want true", m["done"])
	}
}

func TestExecute_ConsoleCapturesToLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	ctx := logging.WithLogger(context.Background(), logger)
	ctx = logging.WithRequestID(ctx, "req-12345")

	eng := New(&mockRunner{})
	wf := &store.Workflow{
		Name:         "test-console-wf",
		InputScript:  `console.log("input message"); ({ok: true})`,
		OutputScript: `console.info("output message"); ({done: true})`,
	}

	_, err := eng.Execute(ctx, wf, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "input message") {
		t.Errorf("expected 'input message' in log output, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "output message") {
		t.Errorf("expected 'output message' in log output, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"source":"script"`) {
		t.Errorf("expected source:script in log output, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"workflow":"test-console-wf"`) {
		t.Errorf("expected workflow:test-console-wf in log output, got: %s", logOutput)
	}
}

func TestExecute_InputTransformValidation(t *testing.T) {
	eng := New(&mockRunner{})

	wf := &store.Workflow{
		InputScript: `
			var tid = input.team_id || '';
			if (!tid) throw new Error('team_id is required');
			({team_id: tid})
		`,
		OutputScript: `({})`,
	}

	_, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err == nil {
		t.Fatal("expected validation error")
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{"team_id": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	_ = result
}

func TestResolveNamedParams_RepeatedVariable(t *testing.T) {
	scope := map[string]any{"from": "2024-01-01"}
	sql, args := resolveNamedParams("WHERE created >= :from AND closed <= :from", scope)

	want := "WHERE created >= ? AND closed <= ?"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Fatalf("got %d args, want 2", len(args))
	}
	for i, a := range args {
		if a != "2024-01-01" {
			t.Errorf("args[%d] = %v, want 2024-01-01", i, a)
		}
	}
}

func TestResolveNamedParams_SliceExpansion(t *testing.T) {
	scope := map[string]any{"repos": []any{"repo-a", "repo-b", "repo-c"}}
	sql, args := resolveNamedParams("WHERE r.name IN (:repos)", scope)

	want := "WHERE r.name IN (?, ?, ?)"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Fatalf("got %d args, want 3", len(args))
	}
	expected := []any{"repo-a", "repo-b", "repo-c"}
	for i, a := range args {
		if a != expected[i] {
			t.Errorf("args[%d] = %v, want %v", i, a, expected[i])
		}
	}
}

func TestResolveNamedParams_EmptySlice(t *testing.T) {
	scope := map[string]any{"ids": []any{}}
	sql, args := resolveNamedParams("WHERE id IN (:ids)", scope)

	want := "WHERE id IN (NULL)"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("got %d args, want 0", len(args))
	}
}

func TestResolveNamedParams_LineComment(t *testing.T) {
	scope := map[string]any{"status": "active", "from": "2024-01-01"}
	sql, args := resolveNamedParams("WHERE status = :status\n  -- AND author IN (:user_whitelist)\n  AND created >= :from", scope)

	want := "WHERE status = ?\n  -- AND author IN (:user_whitelist)\n  AND created >= ?"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Fatalf("got %d args, want 2", len(args))
	}
	if args[0] != "active" || args[1] != "2024-01-01" {
		t.Errorf("args = %v, want [active 2024-01-01]", args)
	}
}

func TestResolveNamedParams_BlockComment(t *testing.T) {
	scope := map[string]any{"id": 42}
	sql, args := resolveNamedParams("SELECT /* :ignored */ * FROM t WHERE id = :id", scope)

	want := "SELECT /* :ignored */ * FROM t WHERE id = ?"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 1 || args[0] != 42 {
		t.Errorf("args = %v, want [42]", args)
	}
}

func TestResolveNamedParams_StringLiteral(t *testing.T) {
	scope := map[string]any{"id": 1}
	sql, args := resolveNamedParams("SELECT * FROM t WHERE name = ':not_a_param' AND id = :id", scope)

	want := "SELECT * FROM t WHERE name = ':not_a_param' AND id = ?"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 1 || args[0] != 1 {
		t.Errorf("args = %v, want [1]", args)
	}
}

func TestResolveNamedParams_BareNumberNotMatched(t *testing.T) {
	scope := map[string]any{}
	sql, args := resolveNamedParams("SELECT * FROM t WHERE id = :123", scope)

	want := "SELECT * FROM t WHERE id = :123"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("got %d args, want 0", len(args))
	}
}

func TestResolveNamedParams_UnderscoreStart(t *testing.T) {
	scope := map[string]any{"_internal": "val"}
	sql, args := resolveNamedParams("WHERE x = :_internal", scope)

	want := "WHERE x = ?"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 1 || args[0] != "val" {
		t.Errorf("args = %v, want [val]", args)
	}
}

func TestExecute_ScopeMerging(t *testing.T) {
	runner := &mockRunner{
		results: map[string][]map[string]any{
			"SELECT id FROM teams WHERE name = ?":          {{"id": "t1"}},
			"SELECT source FROM sources WHERE team_id = ?": {{"source": "github"}},
		},
	}
	eng := New(runner)

	wf := &store.Workflow{
		InputScript:  `({team_name: input.name, team_id: "t1"})`,
		OutputScript: `({sources: results.sources})`,
		Queries: []store.Query{
			{Stage: 0, Label: "team", SQLText: "SELECT id FROM teams WHERE name = :team_name"},
			{Stage: 1, Label: "sources", SQLText: "SELECT source FROM sources WHERE team_id = :team_id"},
		},
	}

	result, err := eng.Execute(context.Background(), wf, map[string]any{"name": "team1"})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	sources := m["sources"].([]map[string]any)
	if len(sources) != 1 || sources[0]["source"] != "github" {
		t.Errorf("got sources %v, want [{source: github}]", sources)
	}
}

func TestResolveNamedParams_TypedSlice(t *testing.T) {
	scope := map[string]any{"ids": []string{"a", "b"}}
	sql, args := resolveNamedParams("WHERE id IN (:ids)", scope)

	want := "WHERE id IN (?, ?)"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Fatalf("got %d args, want 2", len(args))
	}
	if args[0] != "a" || args[1] != "b" {
		t.Errorf("args = %v, want [a b]", args)
	}
}

func TestExecute_InputScriptTimeout(t *testing.T) {
	eng := New(&mockRunner{}, WithScriptTimeout(50*time.Millisecond))
	wf := &store.Workflow{InputScript: `while (true) {}`}

	start := time.Now()
	_, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "input transform: script exceeded time limit") {
		t.Errorf("unexpected error message: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("script was not interrupted promptly (took %s)", elapsed)
	}
}

func TestExecute_OutputScriptTimeout(t *testing.T) {
	eng := New(&mockRunner{}, WithScriptTimeout(50*time.Millisecond))
	wf := &store.Workflow{OutputScript: `for (;;) {}`}

	_, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "output transform:") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExecute_ScriptAbortedOnContextCancel(t *testing.T) {
	eng := New(&mockRunner{}, WithScriptTimeout(0))
	wf := &store.Workflow{InputScript: `while (true) {}`}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	_, err := eng.Execute(ctx, wf, map[string]any{})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestExecute_DefaultScriptTimeout(t *testing.T) {
	if got := New(&mockRunner{}).scriptTimeout; got != DefaultScriptTimeout {
		t.Errorf("default script timeout = %s, want %s", got, DefaultScriptTimeout)
	}
}

func TestExecute_FastScriptUnaffectedByTimeout(t *testing.T) {
	eng := New(&mockRunner{}, WithScriptTimeout(time.Second))
	wf := &store.Workflow{
		InputScript:  `var s = 0; for (var i = 0; i < 1000; i++) { s += i }; ({sum: s})`,
		OutputScript: `({sum: input.n})`,
	}
	result, err := eng.Execute(context.Background(), wf, map[string]any{"n": int64(7)})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(map[string]any)["sum"]; got != int64(7) {
		t.Errorf("got %v (%T), want 7", got, got)
	}
}

func TestExecute_ScriptSyntaxErrorNotReportedAsTimeout(t *testing.T) {
	eng := New(&mockRunner{}, WithScriptTimeout(time.Second))
	wf := &store.Workflow{InputScript: `({`}
	_, err := eng.Execute(context.Background(), wf, map[string]any{})
	if err == nil {
		t.Fatal("expected syntax error")
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "time limit") {
		t.Errorf("syntax error misreported as timeout: %v", err)
	}
}

func TestResolveNamedParams_QuotedAndCommentedNotMatched(t *testing.T) {
	scope := map[string]any{"id": 1}
	tests := []struct {
		name, sql, want string
	}{
		{"double quoted string", `SELECT ":x" FROM t WHERE id = :id`, `SELECT ":x" FROM t WHERE id = ?`},
		{"backtick identifier", "SELECT `a:x` FROM t WHERE id = :id", "SELECT `a:x` FROM t WHERE id = ?"},
		{"backslash escaped quote", `SELECT 'it\'s :x' FROM t WHERE id = :id`, `SELECT 'it\'s :x' FROM t WHERE id = ?`},
		{"escaped backslash before close", `SELECT 'a\\' AS p, :id`, `SELECT 'a\\' AS p, ?`},
		{"doubled single quote", `SELECT 'it''s :x' FROM t WHERE id = :id`, `SELECT 'it''s :x' FROM t WHERE id = ?`},
		{"doubled double quote", `SELECT "a"":x" FROM t WHERE id = :id`, `SELECT "a"":x" FROM t WHERE id = ?`},
		{"doubled backtick", "SELECT `a``:x` FROM t WHERE id = :id", "SELECT `a``:x` FROM t WHERE id = ?"},
		{"hash comment", "SELECT * FROM t # :x\nWHERE id = :id", "SELECT * FROM t # :x\nWHERE id = ?"},
		{"json path in string", `SELECT data->'$.a:x' FROM t WHERE id = :id`, `SELECT data->'$.a:x' FROM t WHERE id = ?`},
		{"assignment operator", `SELECT @v := :id`, `SELECT @v := ?`},
		{"unterminated string", `SELECT ':x`, `SELECT ':x`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, args := resolveNamedParams(tt.sql, scope)
			if got != tt.want {
				t.Errorf("sql = %q, want %q", got, tt.want)
			}
			wantArgs := strings.Count(tt.want, "?")
			if len(args) != wantArgs {
				t.Errorf("got %d args, want %d", len(args), wantArgs)
			}
		})
	}
}

func TestExecute_Int64AndFloat64InputsBehaveIdenticallyInJS(t *testing.T) {
	eng := New(&mockRunner{})
	wf := &store.Workflow{
		OutputScript: `({half: input.x / 2, sum: input.x + 1, eq: input.x === 5, t: typeof input.x,
			s: JSON.stringify(input), str: "" + input.x, int: Number.isInteger(input.x)})`,
	}
	run := func(x any) map[string]any {
		out, err := eng.Execute(context.Background(), wf, map[string]any{"x": x})
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)
	}
	fromInt, fromFloat := run(int64(5)), run(float64(5))
	for k := range fromFloat {
		a, b := fromInt[k], fromFloat[k]
		// goja may export integral numbers as int64; compare numerically.
		if af, ok := toFloat(a); ok {
			if bf, ok := toFloat(b); ok && af == bf {
				continue
			}
		}
		if a != b {
			t.Errorf("%s: int64 input gave %v (%T), float64 input gave %v (%T)", k, a, a, b, b)
		}
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
