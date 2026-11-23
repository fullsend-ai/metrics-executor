package engine

import (
	"context"
	"database/sql"
)

// MySQLRunner executes queries against the DevLake database.
type MySQLRunner struct {
	db *sql.DB
}

func NewMySQLRunner(db *sql.DB) *MySQLRunner {
	return &MySQLRunner{db: db}
}

func (r *MySQLRunner) RunQuery(ctx context.Context, sqlText string, args []any) ([]map[string]any, error) {
	rows, err := r.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		result = append(result, row)
	}
	if result == nil {
		result = []map[string]any{}
	}
	return result, rows.Err()
}
