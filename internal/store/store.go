package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type Workflow struct {
	ID           int
	Name         string
	InputScript  string
	OutputScript string
	Queries      []Query
}

type Query struct {
	Stage     int
	SortOrder int
	Label     string
	SQLText   string
}

type WorkflowSummary struct {
	Name              string
	Version           int
	ProductionVersion int
	QueryCount        int
}

type Store interface {
	GetWorkflowByName(ctx context.Context, name string) (*Workflow, error)
	ListPublishedWorkflows(ctx context.Context) ([]WorkflowSummary, error)
}

// MySQLStore reads workflow definitions from the metrics definitions database.
type MySQLStore struct {
	db *sql.DB
}

func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db}
}

func (s *MySQLStore) GetWorkflowByName(ctx context.Context, name string) (*Workflow, error) {
	var (
		id                int
		wfName            string
		inputScript       sql.NullString
		outputScript      sql.NullString
		version           int
		productionVersion int
		archived          bool
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT w.id, w.name, w.input_script, w.output_script, w.version,
		        COALESCE((SELECT v.version FROM workflow_versions v WHERE v.workflow_id = w.id AND v.production = TRUE LIMIT 1), 0),
		        w.archived
		 FROM workflows w WHERE w.name = ?`, name,
	).Scan(&id, &wfName, &inputScript, &outputScript, &version, &productionVersion, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query workflow: %w", err)
	}

	if archived || version == 0 {
		return nil, nil
	}

	if productionVersion > 0 {
		return s.loadVersion(ctx, id, productionVersion)
	}

	queries, err := s.loadQueries(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Workflow{
		ID:           id,
		Name:         wfName,
		InputScript:  inputScript.String,
		OutputScript: outputScript.String,
		Queries:      queries,
	}, nil
}

func (s *MySQLStore) ListPublishedWorkflows(ctx context.Context) ([]WorkflowSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.name, w.version,
		        COALESCE((SELECT v.version FROM workflow_versions v WHERE v.workflow_id = w.id AND v.production = TRUE LIMIT 1), 0),
		        (SELECT COUNT(*) FROM workflow_queries q WHERE q.workflow_id = w.id)
		 FROM workflows w
		 WHERE w.archived = false AND w.version > 0
		 ORDER BY w.name`)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	var list []WorkflowSummary
	for rows.Next() {
		var ws WorkflowSummary
		if err := rows.Scan(&ws.Name, &ws.Version, &ws.ProductionVersion, &ws.QueryCount); err != nil {
			return nil, fmt.Errorf("scan workflow summary: %w", err)
		}
		list = append(list, ws)
	}
	return list, rows.Err()
}

func (s *MySQLStore) loadQueries(ctx context.Context, workflowID int) ([]Query, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT stage, sort_order, label, sql_text
		 FROM workflow_queries WHERE workflow_id = ? ORDER BY stage, sort_order`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("query workflow_queries: %w", err)
	}
	defer rows.Close()

	var queries []Query
	for rows.Next() {
		var q Query
		if err := rows.Scan(&q.Stage, &q.SortOrder, &q.Label, &q.SQLText); err != nil {
			return nil, fmt.Errorf("scan query row: %w", err)
		}
		queries = append(queries, q)
	}
	return queries, rows.Err()
}

func (s *MySQLStore) loadVersion(ctx context.Context, workflowID, version int) (*Workflow, error) {
	var (
		name         string
		inputScript  sql.NullString
		outputScript sql.NullString
		queriesJSON  sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT name, input_script, output_script, queries
		 FROM workflow_versions
		 WHERE workflow_id = ? AND version = ?`,
		workflowID, version,
	).Scan(&name, &inputScript, &outputScript, &queriesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query workflow_versions: %w", err)
	}

	wf := &Workflow{
		ID:           workflowID,
		Name:         name,
		InputScript:  inputScript.String,
		OutputScript: outputScript.String,
	}

	if queriesJSON.Valid && queriesJSON.String != "" {
		var vqs []versionQuery
		if err := json.Unmarshal([]byte(queriesJSON.String), &vqs); err != nil {
			return nil, fmt.Errorf("parse version queries JSON: %w", err)
		}
		for _, q := range vqs {
			wf.Queries = append(wf.Queries, Query(q))
		}
	}

	return wf, nil
}

type versionQuery struct {
	Stage     int    `json:"stage"`
	SortOrder int    `json:"sort_order"`
	Label     string `json:"label"`
	SQLText   string `json:"sql_text"`
}
