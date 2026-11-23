# Workflows

Workflows are named data pipelines that expose HTTP endpoints. Each workflow follows a three-stage execution model:

1. **Input Transform** — a JavaScript function that validates and reshapes incoming parameters.
2. **SQL Queries** — one or more parameterized SQL statements executed in parallel against the data lake.
3. **Output Transform** — a JavaScript function that combines query results into the final response.

Published workflows are available at `GET/POST /workflows/<name>` without authentication, making them suitable for integration with external tools, dashboards, and automated systems.

## Data Model

### Workflow (API response)

| Field | Type | Storage | Description |
|-------|------|---------|-------------|
| `id` | int | column | Auto-generated primary key |
| `name` | string | column | URL-safe endpoint name (unique) |
| `description` | string | column | Human-readable description |
| `input_script` | string | column | JavaScript — input transformation |
| `output_script` | string | column | JavaScript — output transformation |
| `test_input` | string | column | JSON — default test parameters |
| `tests` | array | column (JSON) | Test cases (name, input JSON, assert script, optional schema_id) |
| `version` | int | column | Publication version (0 = unpublished draft) |
| `has_draft` | bool | computed | `TRUE` when the `draft` column is not NULL |
| `production_version` | int | computed | Version currently serving live traffic (0 = none). **Not a column on the `workflows` table.** Derived via subquery: `SELECT version FROM workflow_versions WHERE workflow_id = w.id AND production = TRUE` |
| `archived` | bool | column | Whether the workflow is archived (disabled) |
| `queries` | array | joined | Ordered list of SQL query definitions (from `workflow_queries` table) |

> **Important:** `production_version` and `has_draft` are not stored as columns on the `workflows` table. If you query the database directly, compute them from the `workflow_versions` table and the `draft` column respectively. See the Database Schema section below.

### Database Schema

The `workflows` table columns (after all migrations):

```sql
id, name, description, input_script, output_script, test_input, tests,
archived, draft, version, created_at, updated_at
```

The `workflow_versions` table columns:

```sql
id, workflow_id, version, name, description, input_script, output_script,
test_input, queries (JSON), tests, publish_note, production (BOOLEAN),
published_at
```

The production version is tracked by the `production` boolean on `workflow_versions`:

```sql
SELECT version FROM workflow_versions
WHERE workflow_id = ? AND production = TRUE
```

To load a production workflow version with its full definition:

```sql
SELECT id, workflow_id, version, name, description, input_script,
       output_script, test_input, queries, tests, publish_note,
       production, published_at
FROM workflow_versions
WHERE workflow_id = ? AND production = TRUE
```

> **Note:** The workflow definition fields (`input_script`, `output_script`, `queries`, etc.) are stored directly as columns on `workflow_versions` — there is no `data` column. The `queries` column is JSON containing the array of query definitions.

### Workflow Query

| Field | Type | Description |
|-------|------|-------------|
| `stage` | int | Execution step (0-based). Queries in the same step run in parallel; steps execute sequentially. |
| `sort_order` | int | Display order within a step |
| `label` | string | Key used to reference results in the output script |
| `sql_text` | string | SQL statement (use `:variable_name` for named parameter placeholders) |

## Execution Pipeline

```
                      ┌─────────────────────┐
   HTTP request  ───▶ │  Input Transform    │ ───▶ map of parameters
   (JSON body         │  (JavaScript/goja)  │
    or query params)  └─────────────────────┘
                               │
                      ┌────────┴────────┐
                      │    Step 1       │
               ┌──────┼──────┐         │
               ▼      ▼      ▼         │  (parallel within step)
            Query A Query B Query C    │
               │      │      │         │
               └──────┼──────┘         │
                      ├────────────────┘
                      │    Step 2
               ┌──────┼──────┐            (sequential between steps)
               ▼      ▼      ▼
            Query D Query E Query F
               │      │      │
               └──────┼──────┘
                      ▼
                      ┌─────────────────────┐
                      │  Output Transform   │ ───▶ HTTP response
                      │  (JavaScript/goja)  │
                      └─────────────────────┘
```

### Input Transform

The input script receives a global `input` object containing the request parameters. It must return an object whose keys are used to resolve query parameters.

```javascript
// Extract and validate team_id from request params
var tid = input.team_id || '';
if (!tid) throw new Error('team_id is required');
({team_id: tid})
```

### SQL Queries

Queries are organized into **steps**. All queries within the same step execute in **parallel**. Steps execute **sequentially** — step 1 completes before step 2 begins. This allows later steps to depend on data produced by earlier ones.

Each query defines a `stage` (step number), a `label`, and a `sql_text` with named parameter placeholders. Named parameters use the `:variable_name` syntax and are resolved from the input transform output and prior query results.

```sql
-- stage: 0, label: team_info
SELECT id, name FROM teams WHERE id = :team_id
```

The same variable can appear multiple times in a query:

```sql
SELECT * FROM events WHERE start_date >= :from AND end_date <= :from
```

If all queries are independent, a single step (stage 0) is sufficient and they all run in parallel. Use multiple steps when a query needs data produced by a prior step.

### Named Parameter Resolution

Named parameters (`:variable_name`) in SQL are resolved at execution time. The resolver builds a scope by merging **prior query results** (keyed by query label) with the **input transform output** (which takes precedence on key collisions). Each `:variable_name` is replaced with a `?` placeholder and the corresponding value is bound as a positional argument, keeping queries parameterized and safe from SQL injection.

**Variable names** must start with a letter or underscore, followed by word characters: `:team_id`, `:min_count`, `:_internal`. A bare `:123` is not matched.

**Repeated variables** — the same variable can appear multiple times. Each occurrence generates its own `?` and arg:

```sql
WHERE created >= :from AND closed <= :from
-- resolves to: WHERE created >= ? AND closed <= ?
-- args: [from_value, from_value]
```

**Slice expansion** — if a variable resolves to an array/slice, it expands into multiple comma-separated `?` placeholders. This is designed for `IN` clauses:

```sql
WHERE r.name IN (:repos)
-- if repos = ["repo-a", "repo-b", "repo-c"]
-- resolves to: WHERE r.name IN (?, ?, ?)
-- args: ["repo-a", "repo-b", "repo-c"]
```

Empty slices bind a single `NULL`.

**Comments and string literals are skipped** — the resolver does not match `:variable_name` inside:

- Line comments (`-- ...` and `# ...`)
- Block comments (`/* ... */`)
- Single- and double-quoted string literals (`'...'`, `"..."`), including
  doubled-quote (`'it''s'`) and backslash (`'it\'s'`) escapes
- Backtick-quoted identifiers (`` `...` ``)

Backslash escapes follow MySQL's default `sql_mode`. If the server runs with
`NO_BACKSLASH_ESCAPES`, avoid string literals that end in a backslash.

This means you can safely comment out parameterized clauses:

```sql
WHERE status = :status
  -- AND author IN (:user_whitelist)
  AND created >= :from
```

Only `:status` and `:from` are resolved; `:user_whitelist` inside the comment is left untouched.

### Output Transform

The output script receives two globals:

- `input` — the original request input (query parameters or JSON body), not the input transform output
- `results` — an object keyed by query label, each value an array of row objects

```javascript
var info = results['team_info'] || [];
var team = info.length > 0 ? info[0] : null;
({team: team})
```

### Script time limit

Each input and output transform is aborted after `SCRIPT_TIMEOUT_SECONDS`
(default 30 seconds), or as soon as the client disconnects. The request then
fails with `500` and an error message containing `script exceeded time limit`.
The limit applies to JavaScript code; a single long-running call into a native
built-in (for example one pathological regular-expression match) is not
interrupted until it returns.

### Console

All scripts have access to `console.log()`, `console.info()`, `console.warn()`, `console.error()`, and `console.debug()`. Output is captured during test execution but not included in live endpoint responses.

## Endpoints

Operational endpoints:
- `GET /health/live` — liveness probe (returns `{"status":"ok"}`)
- `GET /health/ready` — readiness probe (returns `{"status":"ok"}` or `503` `{"status":"not_ready"}`)
- `GET /metrics` — Prometheus metrics

Workflow execution endpoints:
- `GET /workflows/{name}` — execute workflow with query parameters
- `POST /workflows/{name}` — execute workflow with JSON body

No authentication required. Only published workflows (`version > 0`) are served.

POST bodies must be a JSON object of at most 1 MiB. JSON integers that fit in a
signed 64-bit integer are decoded exactly, so they reach SQL parameters without
rounding when no input transform is used (e.g. large IDs, `LIMIT :n`). Inside
JavaScript transforms all numbers are IEEE-754 doubles as usual.

Response `200`: the output transform result directly (not wrapped in stages).

Response `400`: the POST body is not a valid JSON object.

Response `404`: workflow not found or not published.

Response `413`: the POST body exceeds 1 MiB.

Response `500`: execution error with `{"error": "..."}`.

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `7070` | HTTP listen port |
| `REFRESH_INTERVAL_SECONDS` | `60` | How often to re-read workflow definitions from the database |
| `SCRIPT_TIMEOUT_SECONDS` | `30` | Maximum run time of a single input or output transform |
| `WORKFLOWS_BASE_URL` | `http://localhost:<PORT>` | Base URL shown in the editor for the live endpoint |
| `DASHBOARD_DB_HOST` | `localhost` | Dashboard database host (read-only) |
| `DASHBOARD_DB_PORT` | `3306` | Dashboard database port |
| `DASHBOARD_DB_USER` | - | Dashboard database user |
| `DASHBOARD_DB_PASSWORD` | - | Dashboard database password |
| `DASHBOARD_DB_NAME` | `dashboard` | Dashboard database name |
| `DASHBOARD_DB_CA_PATH` | - | Path to CA certificate for TLS |
| `DEVLAKE_DB_HOST` | `localhost` | DevLake database host (read-only) |
| `DEVLAKE_DB_PORT` | `3306` | DevLake database port |
| `DEVLAKE_DB_USER` | - | DevLake database user |
| `DEVLAKE_DB_PASSWORD` | - | DevLake database password |
| `DEVLAKE_DB_NAME` | `devlake` | DevLake database name |
| `DEVLAKE_DB_CA_PATH` | - | Path to CA certificate for TLS |
| `LOG_LEVEL` | `INFO` | Log level: `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `LOG_FORMAT` | `json` | Log format: `json` or `text` |
