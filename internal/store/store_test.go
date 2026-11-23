package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeResult is the canned answer for one SQL statement.
type fakeResult struct {
	cols []string
	rows [][]driver.Value
	err  error
}

// fakeHandler maps a statement and its arguments to a result.
type fakeHandler func(query string, args []driver.Value) fakeResult

type fakeConnector struct{ h fakeHandler }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) { return fakeConn(c), nil }
func (c fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type fakeConn struct{ h fakeHandler }

func (c fakeConn) Prepare(query string) (driver.Stmt, error) { return fakeStmt{c.h, query}, nil }
func (c fakeConn) Close() error                              { return nil }
func (c fakeConn) Begin() (driver.Tx, error)                 { return nil, errors.New("read-only") }

type fakeStmt struct {
	h     fakeHandler
	query string
}

func (s fakeStmt) Close() error  { return nil }
func (s fakeStmt) NumInput() int { return -1 }
func (s fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("store must not execute writes")
}
func (s fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	r := s.h(s.query, args)
	if r.err != nil {
		return nil, r.err
	}
	return &fakeRows{cols: r.cols, rows: r.rows}, nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

func newFakeStore(t *testing.T, h fakeHandler) *MySQLStore {
	t.Helper()
	db := sql.OpenDB(fakeConnector{h})
	t.Cleanup(func() { db.Close() })
	return NewMySQLStore(db)
}

var workflowCols = []string{"id", "name", "input_script", "output_script", "version", "production_version", "archived"}

func workflowRow(id int64, name string, version, prodVersion int64, archived bool) [][]driver.Value {
	return [][]driver.Value{{id, name, "IN", "OUT", version, prodVersion, archived}}
}

func TestGetWorkflowByName_NotFound(t *testing.T) {
	s := newFakeStore(t, func(string, []driver.Value) fakeResult {
		return fakeResult{cols: workflowCols}
	})
	wf, err := s.GetWorkflowByName(context.Background(), "missing")
	if err != nil || wf != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", wf, err)
	}
}

func TestGetWorkflowByName_ArchivedOrUnpublished(t *testing.T) {
	for _, tc := range []struct {
		name     string
		version  int64
		archived bool
	}{
		{"archived", 3, true},
		{"never published", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeStore(t, func(q string, _ []driver.Value) fakeResult {
				if strings.Contains(q, "FROM workflows w WHERE w.name") {
					return fakeResult{cols: workflowCols, rows: workflowRow(1, "wf", tc.version, 0, tc.archived)}
				}
				t.Fatalf("unexpected query: %s", q)
				return fakeResult{}
			})
			wf, err := s.GetWorkflowByName(context.Background(), "wf")
			if err != nil || wf != nil {
				t.Fatalf("got (%v, %v), want (nil, nil)", wf, err)
			}
		})
	}
}

func TestGetWorkflowByName_LatestFromWorkflowQueries(t *testing.T) {
	s := newFakeStore(t, func(q string, args []driver.Value) fakeResult {
		switch {
		case strings.Contains(q, "FROM workflows w WHERE w.name"):
			if args[0] != "wf" {
				t.Errorf("name arg = %v, want wf", args[0])
			}
			return fakeResult{cols: workflowCols, rows: workflowRow(7, "wf", 2, 0, false)}
		case strings.Contains(q, "FROM workflow_queries WHERE workflow_id"):
			if args[0] != int64(7) {
				t.Errorf("workflow_id arg = %v, want 7", args[0])
			}
			return fakeResult{
				cols: []string{"stage", "sort_order", "label", "sql_text"},
				rows: [][]driver.Value{
					{int64(0), int64(0), "a", "SELECT 1"},
					{int64(1), int64(0), "b", "SELECT 2"},
				},
			}
		}
		t.Fatalf("unexpected query: %s", q)
		return fakeResult{}
	})

	wf, err := s.GetWorkflowByName(context.Background(), "wf")
	if err != nil {
		t.Fatal(err)
	}
	if wf.ID != 7 || wf.Name != "wf" || wf.InputScript != "IN" || wf.OutputScript != "OUT" {
		t.Errorf("unexpected workflow: %+v", wf)
	}
	if len(wf.Queries) != 2 || wf.Queries[1] != (Query{Stage: 1, SortOrder: 0, Label: "b", SQLText: "SELECT 2"}) {
		t.Errorf("unexpected queries: %+v", wf.Queries)
	}
}

func TestGetWorkflowByName_ProductionVersion(t *testing.T) {
	s := newFakeStore(t, func(q string, args []driver.Value) fakeResult {
		switch {
		case strings.Contains(q, "FROM workflows w WHERE w.name"):
			return fakeResult{cols: workflowCols, rows: workflowRow(7, "wf", 5, 3, false)}
		case strings.Contains(q, "FROM workflow_versions") && strings.Contains(q, "version = ?"):
			if args[0] != int64(7) || args[1] != int64(3) {
				t.Errorf("args = %v, want [7 3]", args)
			}
			return fakeResult{
				cols: []string{"name", "input_script", "output_script", "queries"},
				rows: [][]driver.Value{{"wf-v3", nil, "OUT3",
					`[{"stage":1,"sort_order":2,"label":"q","sql_text":"SELECT :x"}]`}},
			}
		}
		t.Fatalf("unexpected query: %s", q)
		return fakeResult{}
	})

	wf, err := s.GetWorkflowByName(context.Background(), "wf")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Name != "wf-v3" || wf.InputScript != "" || wf.OutputScript != "OUT3" {
		t.Errorf("unexpected workflow: %+v", wf)
	}
	want := Query{Stage: 1, SortOrder: 2, Label: "q", SQLText: "SELECT :x"}
	if len(wf.Queries) != 1 || wf.Queries[0] != want {
		t.Errorf("queries = %+v, want [%+v]", wf.Queries, want)
	}
}

func TestGetWorkflowByName_ProductionVersionMissingRow(t *testing.T) {
	s := newFakeStore(t, func(q string, _ []driver.Value) fakeResult {
		if strings.Contains(q, "FROM workflows w WHERE w.name") {
			return fakeResult{cols: workflowCols, rows: workflowRow(7, "wf", 5, 3, false)}
		}
		return fakeResult{cols: []string{"name", "input_script", "output_script", "queries"}}
	})
	wf, err := s.GetWorkflowByName(context.Background(), "wf")
	if err != nil || wf != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", wf, err)
	}
}

func TestGetWorkflowByName_InvalidVersionJSON(t *testing.T) {
	s := newFakeStore(t, func(q string, _ []driver.Value) fakeResult {
		if strings.Contains(q, "FROM workflows w WHERE w.name") {
			return fakeResult{cols: workflowCols, rows: workflowRow(7, "wf", 5, 3, false)}
		}
		return fakeResult{
			cols: []string{"name", "input_script", "output_script", "queries"},
			rows: [][]driver.Value{{"wf", "", "", "{not json"}},
		}
	})
	if _, err := s.GetWorkflowByName(context.Background(), "wf"); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestGetWorkflowByName_DBError(t *testing.T) {
	s := newFakeStore(t, func(string, []driver.Value) fakeResult {
		return fakeResult{err: errors.New("db down")}
	})
	if _, err := s.GetWorkflowByName(context.Background(), "wf"); err == nil {
		t.Fatal("expected error")
	}
}

func TestListPublishedWorkflows(t *testing.T) {
	s := newFakeStore(t, func(q string, _ []driver.Value) fakeResult {
		if !strings.Contains(q, "w.archived = false AND w.version > 0") {
			t.Errorf("query does not filter archived/unpublished workflows: %s", q)
		}
		return fakeResult{
			cols: []string{"name", "version", "production_version", "query_count"},
			rows: [][]driver.Value{
				{"a", int64(1), int64(0), int64(2)},
				{"b", int64(4), int64(3), int64(1)},
			},
		}
	})

	list, err := s.ListPublishedWorkflows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []WorkflowSummary{
		{Name: "a", Version: 1, ProductionVersion: 0, QueryCount: 2},
		{Name: "b", Version: 4, ProductionVersion: 3, QueryCount: 1},
	}
	if len(list) != len(want) {
		t.Fatalf("got %d summaries, want %d", len(list), len(want))
	}
	for i := range want {
		if list[i] != want[i] {
			t.Errorf("list[%d] = %+v, want %+v", i, list[i], want[i])
		}
	}
}

func TestListPublishedWorkflows_DBError(t *testing.T) {
	s := newFakeStore(t, func(string, []driver.Value) fakeResult {
		return fakeResult{err: errors.New("db down")}
	})
	if _, err := s.ListPublishedWorkflows(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
