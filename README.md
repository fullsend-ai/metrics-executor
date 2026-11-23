# metrics-executor

Workflow execution service for the Org Pulse Leading Forward Metrics. Reads workflow
definitions from a metrics definitions database and executes their SQL queries
against a DevLake database, transforming inputs and outputs via JavaScript
(goja). No management API — this component only serves published workflow
endpoints.

## Quick start

```sh
make build   # compile to bin/metrics-executor
make run     # build and run locally on :7070
make test    # run tests (mock database, no DB required)
make image   # build container image
```

Coming from `devlake-workflows`? See
[docs/migrating-to-metrics-executor.md](docs/migrating-to-metrics-executor.md)
for the compatibility notes and rollout checks.

## Endpoints

| Path | Method | Description |
|------|--------|-------------|
| `/health/live` | GET | Liveness probe — returns `{"status":"ok"}` |
| `/health/ready` | GET | Readiness probe (database connectivity) — returns `{"status":"ok"}` or `503` `{"status":"not_ready"}` |
| `/metrics` | GET | Prometheus metrics |
| `/workflows/{name}` | GET | Execute a published workflow (query params as input) |
| `/workflows/{name}` | POST | Execute a published workflow (JSON body as input) |

## Workflow execution pipeline

```
HTTP request → Input Transform (JS) → SQL Queries (DevLake DB) → Output Transform (JS) → HTTP response
```

1. **Input Transform** — JavaScript function validates/reshapes request parameters
2. **SQL Queries** — parameterized queries run against the DevLake database;
   parallel within a stage, sequential between stages
3. **Output Transform** — JavaScript function combines query results into the response

See [docs/workflows.md](docs/workflows.md) for the full specification and
[docs/architecture.md](docs/architecture.md) for the service architecture.

## Configuration

All configuration is via environment variables. See `env.example` for the complete list of variables.

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `7070` | HTTP listen port |
| `LOG_LEVEL` | `INFO` | Log level: DEBUG, INFO, WARN, ERROR |
| `LOG_FORMAT` | `json` | Log format: json or text |
| `REFRESH_INTERVAL_SECONDS` | `60` | How often to re-read workflow definitions from the DB |
| `SCRIPT_TIMEOUT_SECONDS` | `30` | Maximum run time of a single input or output transform |
| `REQUEST_TIMEOUT_SECONDS` | `300` | Maximum run time of a whole workflow request |
| `MAX_CONCURRENT_EXECUTIONS` | `32` | In-flight workflow executions before new requests get `503` |
| `CORS_ALLOWED_ORIGINS` | `*` | Comma-separated CORS allowlist; `none` disables CORS |
| `LOG_CLIENT_IP` | `false` | Log the caller IP (personal data; uses the connection address, never `X-Forwarded-For`) |
| `DASHBOARD_DB_HOST` | `localhost` | Definitions (dashboard) database host |
| `DASHBOARD_DB_PORT` | `3306` | Definitions database port |
| `DASHBOARD_DB_USER` | | Definitions database user (required to enable workflows) |
| `DASHBOARD_DB_PASSWORD` | | Definitions database password |
| `DASHBOARD_DB_NAME` | `dashboard` | Definitions database name (required to enable workflows) |
| `DASHBOARD_DB_CA_PATH` | | CA bundle for TLS to the definitions database |
| `DEVLAKE_DB_HOST` | `localhost` | DevLake database host |
| `DEVLAKE_DB_PORT` | `3306` | DevLake database port |
| `DEVLAKE_DB_USER` | | DevLake database user |
| `DEVLAKE_DB_PASSWORD` | | DevLake database password |
| `DEVLAKE_DB_NAME` | `devlake` | DevLake database name |
| `DEVLAKE_DB_CA_PATH` | | CA bundle for TLS to the DevLake database |

The service starts without database configuration — `/health/live` and `/metrics`
work, but `/health/ready` and workflow endpoints return `503 Service Unavailable`.

Both database connections are read-only. The definitions database holds workflow
metadata (tables: `workflows`, `workflow_queries`, `workflow_versions`). The
DevLake database is the target for workflow SQL queries.

## Container

```sh
make image    # builds metrics-executor:latest
podman run -p 127.0.0.1:7070:7070 \
  -e DASHBOARD_DB_USER=metricsconfig -e DASHBOARD_DB_PASSWORD=changeme \
  -e DASHBOARD_DB_NAME=metricsconfig \
  -e DEVLAKE_DB_USER=metricsconfig -e DEVLAKE_DB_PASSWORD=changeme \
  -e DEVLAKE_DB_NAME=metricsconfig \
  metrics-executor:latest
```

## Security

See [SECURITY.md](SECURITY.md) for how to report vulnerabilities. Contributions
are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## Development

```sh
podman compose -f compose-dev.yaml up --build
```

The compose stack binds MySQL and the service to loopback only and uses
throwaway credentials; it is for local development, never for deployment.

## License

[Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
