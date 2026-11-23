# Agents

Base rules for AI assistants working in this repository. Claude-Code-specific tool/skill
hygiene lives in [CLAUDE.md](./CLAUDE.md); this file is the source of truth for project
conventions and structure.

## Golden rules

1. **One message = all related operations** — batch reads, writes, shell commands, and
   agent spawns together.
2. **No working files in repo root** — root holds only `README.md`, `AGENTS.md`,
   `CLAUDE.md`, `LICENSE`, `NOTICE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
   `SECURITY.md`, `.gitignore`, `.golangci.yml`, `env.example`, `go.mod`/`go.sum`,
   `Makefile`, `Containerfile`, `compose-dev.yaml`. Code goes
   in `cmd/`, `internal/`; docs go in `docs/`.
3. **Prefer editing over creating** — edit existing files rather than creating new ones.
4. **Prefer Grep/Glob over shell find/grep** when searching.

## Project overview

metrics-executor is the workflow execution service for the Org Pulse Leading Forward Metrics.
It reads workflow definitions from a metrics definitions database and executes their
SQL queries against a DevLake database, with JavaScript (goja) input/output transforms.
This component only serves published workflow endpoints — management is handled by a
separate component.

Serves `/health/live`, `/health/ready`, `/metrics`, and `GET/POST /workflows/{name}` on `:7070` from a
single Go binary. Both database connections are read-only.

## Repository structure

| Directory | Purpose |
|---|---|
| `cmd/metrics-executor/` | Entry point — config from env vars, database connections, wires server |
| `internal/config/` | Environment variable parsing |
| `internal/store/` | Workflow store — reads definitions from the metrics definitions database |
| `internal/engine/` | Execution pipeline — JS transforms (goja) + SQL queries against DevLake |
| `internal/server/` | HTTP server, handlers, request logging middleware |
| `docs/` | Workflow specification and architecture documentation |

## Build and test

```sh
make build      # compile to bin/metrics-executor
make test       # run tests with race detector (mock DB, no real DB needed)
make vet        # go vet
make lint       # golangci-lint (config in .golangci.yml)
make fmt-check  # verify gofmt formatting
make image      # build container image
```

## Configuration

All config via environment variables. Two databases:
- **Definitions DB** (`DB_*`) — workflow metadata (read-only)
- **DevLake DB** (`DEVLAKE_DB_*`) — target for workflow SQL queries (read-only)

See README.md for the full table.

## Key conventions

- Go 1.26, standard library plus vetted third-party packages (see `go.mod`) — avoid
  unnecessary new dependencies
- Tests use `net/http/httptest` for HTTP tests and mock interfaces for database access
- No comments unless the "why" is non-obvious

## Commit style

[Conventional Commits](https://www.conventionalcommits.org/):
`<type>(<scope>): <description>`. Types: `feat`, `fix`, `docs`, `style`, `refactor`,
`test`, `chore`.

When AI assists with a commit, include a trailer:

```
feat: add user authentication

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
```

**Never push to a remote or open an MR without explicit user approval.**

## Git workflow

- Work happens on a feature branch, never directly on `main`.
- Branch naming: `<type>/<short-description>` (matches commit types).
- Use `gh` for PR operations (GitHub repo), not `glab`.
