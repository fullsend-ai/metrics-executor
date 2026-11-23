# Migrating to metrics-executor

The service was renamed from `devlake-workflows` to `metrics-executor`. The
runtime contract — HTTP API, environment variables and database schema — is
unchanged, so a normal deployment needs at most a manifest update for the
certificate mount path.

## Unchanged (no action needed)

- HTTP endpoints: `/health/live`, `/health/ready`, `/metrics`,
  `GET|POST /workflows/{name}`.
- Environment variables, including `DASHBOARD_DB_*` and `DEVLAKE_DB_*`.
  `DB_*` names were never read by the current code; the README used to
  document them incorrectly.
- Defaults: port `7070`, refresh `60s`, script timeout `30s`, database names
  `dashboard` / `devlake`.
- Both database connections remain read-only.

## Changed (review your manifests and scripts)

| Change | Old | New | Action |
|---|---|---|---|
| Executable in the image | `/devlake-workflows` | `/metrics-executor` | Only relevant if you override `command`/`args`; the default entrypoint handles it. |
| Local build output | `bin/devlake-workflows` | `bin/metrics-executor` | Update scripts that call the binary by path. |
| CA certificate mount | `/etc/devlake-workflows/ca` | `/etc/metrics-executor/ca` | Update `DASHBOARD_DB_CA_PATH`, `DEVLAKE_DB_CA_PATH` and the volume `mountPath` together, or keep the old path in your own overlay. |
| Compose service name | `workflows` | `metrics-executor` | Only affects local `podman compose` commands; the build stage `workflows` is unchanged. |
| Go module path | the previous module path | `github.com/fullsend-ai/metrics-executor` | Only for code that imports these packages; `internal/` cannot be imported externally. |

## Behaviour changes to be aware of

These are new safeguards, not renames. Defaults preserve the previous
behaviour except where noted.

- **Error responses** for workflow failures are now generic
  (`{"error":"workflow execution failed","request_id":"..."}`). Clients that
  parsed driver or script error text must use logs and the request ID instead.
- **`413` for oversized bodies is stricter**: the whole body is capped at
  1 MiB (`MaxRequestBodyBytes`), including bodies that a streaming JSON
  decoder could previously have accepted after reading only a prefix.
- **Timeouts and saturation** are new:
  `REQUEST_TIMEOUT_SECONDS` (default `300`) returns `504` on expiry, and
  `MAX_CONCURRENT_EXECUTIONS` (default `32`) returns `503` with `Retry-After`
  when saturated. Raise the limit if you previously served more parallel
  workflows.
- **Client IP is no longer logged by default**, and `X-Forwarded-For` is
  never trusted. Set `LOG_CLIENT_IP=true` if you need the connection address.
- **CORS** remains `*` by default, but the allowlist is now configurable via
  `CORS_ALLOWED_ORIGINS` (comma-separated; `none` disables) and allowed
  headers are explicit rather than `*`.
- **Pod security context** is stricter: non-root, read-only root filesystem,
  all capabilities dropped, `RuntimeDefault` seccomp and no service-account
  token. With a read-only root filesystem, mount an `emptyDir` at `/tmp` if
  your manifest replaces the container spec.
- **Request IDs** supplied by clients are only echoed if they are 1–128
  characters of `[A-Za-z0-9._-]`; otherwise a new ID is generated.

## Verification after rollout

```sh
curl -fsS http://<host>:7070/health/live
curl -fsS http://<host>:7070/health/ready      # 200 only when both DBs answer
curl -fsS http://<host>:7070/metrics | head
curl -fsS "http://<host>:7070/workflows/<existing-workflow>?<params>"
```

Check the startup log line `database connections established` for both
endpoints, and confirm `REFRESH_INTERVAL_SECONDS` cache messages appear at the
expected cadence.

## Rollback

Redeploy the previous image tag. There are no schema migrations, no environment
variable renames and no data format changes, so rollback is a plain image
change. Keep the CA path consistent with the image you deploy.
