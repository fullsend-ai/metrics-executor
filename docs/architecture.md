# Architecture

`metrics-executor` is a stateless, read-only workflow execution runtime. This
document describes the component boundary, the database access model, the
health probe design, and the logging and observability architecture. The
workflow definition format and execution pipeline are specified in
[workflows.md](workflows.md).

## Component boundary

`metrics-executor` serves published workflow endpoints
(`GET/POST /workflows/{name}`) and operational endpoints (`/health/live`,
`/health/ready`, `/metrics`). It contains no management functionality:

- Workflow authoring, drafts, version publishing and admin authentication
  belong to a separate management component.
- Stored procedures invoked by workflows (`CALL procedure_name(:param)`) are
  deployed to the DevLake database beforehand by the management component or
  migration tooling — never by this service.

## Database access model

The service uses two databases:

1. **Definitions database** (`DASHBOARD_DB_*`) — stores workflow definitions
   (`workflows`, `workflow_versions`, `workflow_queries`). The service reads
   published definitions from it into an in-memory cache, refreshed every
   `REFRESH_INTERVAL_SECONDS`.
2. **DevLake database** (`DEVLAKE_DB_*`) — the analytical data lake holding
   ingested software engineering metrics. Workflow queries run against it
   (`SELECT` statements and `CALL` of pre-deployed stored procedures).

Principles:

- **Least privilege.** Both connections use read-only credentials: `SELECT`
  for the definitions database, `SELECT` and `EXECUTE` for the DevLake
  database. Read-write credentials and admin API keys must never be provided
  to this service. Note that a MySQL stored procedure created with
  `SQL SECURITY DEFINER` runs with its definer's privileges, so `CALL` targets
  must be reviewed and trusted even when the service account is read-only.
- **Independent configuration.** The two `*_DB_*` variable sets do not fall
  back to each other; the databases may live on separate servers. Credentials
  (user/password) and connection details (host, port, TLS CA path) are
  supplied entirely through environment variables — nothing is hardcoded.
- **DSN safety.** DSNs are built with `mysql.Config.FormatDSN`, so credentials
  containing special characters round-trip correctly and cannot inject
  connection options.

## Health and readiness probes

Health checking is split into two endpoints with distinct semantics:

- **`GET /health/live`** — liveness. Purely checks process vitality and HTTP
  listener responsiveness; never touches external dependencies. Returns
  `200 {"status":"ok"}`.
- **`GET /health/ready`** — readiness. Verifies the service can serve traffic
  by pinging every active database pool via `PingContext` with a strict
  1–2 second timeout. Returns `200 {"status":"ok"}` when healthy, or
  `503 {"status":"not_ready"}` when any database is unavailable.

Design rules:

- Liveness is decoupled from database health so transient database outages
  remove pods from load balancer rotation (readiness failure) without
  triggering restart cascades (liveness failure).
- Readiness failures never terminate the process.
- Health responses carry no diagnostics — no credentials, hostnames, SQL
  errors or stack traces. The underlying error is logged server-side instead.
- Successful (`200`) requests to `/health/live`, `/health/ready` and
  `/metrics` are suppressed from access logs to avoid log pollution;
  failures on those endpoints are logged.

## Logging and observability

The service uses Go's standard `log/slog` — no third-party logging
dependencies:

- **Configuration.** `LOG_LEVEL` (`DEBUG`/`INFO`/`WARN`/`ERROR`, default
  `INFO`) and `LOG_FORMAT` (`json` or `text`, default `json`) are read from
  the environment.
- **Request correlation.** A `X-Request-ID` (or `X-Correlation-ID`) value is
  taken from the incoming request when it is 1–128 characters of
  `[A-Za-z0-9._-]`; otherwise a fresh ID is generated. The ID is injected
  into the request context, echoed back in the response header, and attached
  to every log line for the request.
- **Pipeline visibility.** At `DEBUG`, each execution logs cache/version
  resolution, input transform timing, per-stage SQL progression (labels,
  durations, row counts) and output transform timing. A single `INFO`
  summary log per execution records workflow, version, duration, stage count,
  query count and total rows.
- **Script console.** `console.log/debug/info/warn/error` inside workflow
  JavaScript transforms are bridged into `slog` (routed to the matching log
  level) instead of being discarded.
- **Data protection.** Database DSNs are never logged with passwords. SQL
  parameter values, full result rows and response payloads are never logged —
  only parameter counts, row counts and byte counts. Sensitive HTTP headers
  are scrubbed from debug output.

<!-- Test comment added to verify fullsend functionality (issue #3).
     Commands exercised by this test: triage, code, review, fix, prioritize. -->
