# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
pull requests or discussions.**

Report vulnerabilities privately through one of these channels:

- **GitHub private vulnerability reporting** — use the "Report a vulnerability"
  button on the repository's **Security** tab.
- **Email** — Red Hat Product Security at
  [secalert@redhat.com](mailto:secalert@redhat.com). See
  <https://access.redhat.com/security/team/contact> for details and for the
  PGP key to encrypt sensitive reports.

Please include:

- a description of the issue and its impact,
- the affected version or commit,
- steps to reproduce or a proof of concept,
- any known mitigations.

We will acknowledge your report, keep you informed about the fix, and credit
you in the advisory unless you prefer to remain anonymous. Please give us a
reasonable amount of time to release a fix before disclosing the issue publicly.

## Supported versions

Security fixes are applied to the latest release and the `main` branch.

## Security model

Keep the following in mind when deploying `metrics-executor`:

- **Workflow endpoints are unauthenticated.** Every published workflow can be
  executed by anyone who can reach the service. Restrict network access (for
  example with a reverse proxy, an authenticating gateway or network policies)
  if workflow results are not meant to be public.
- **Workflow definitions are trusted code.** Input/output transforms
  (JavaScript) and SQL queries are loaded from the definitions database and
  executed as-is. Anyone with write access to that database can run arbitrary
  read-only SQL against the DevLake database through this service. Protect it
  accordingly.
- **Use read-only database credentials** for both databases, as described in
  the [architecture documentation](docs/architecture.md). Note that a
  MySQL stored procedure created with `SQL SECURITY DEFINER` runs with its
  definer's privileges, so `CALL` targets must be reviewed and trusted even
  when the service account itself is read-only.
- **Enable TLS for database connections** by setting `DASHBOARD_DB_CA_PATH` and
  `DEVLAKE_DB_CA_PATH`. Without a CA path, connections are not encrypted.
- **Terminate TLS in front of the service.** The HTTP listener serves plain
  HTTP and has no TLS mode; publish it only through an ingress or reverse proxy
  that terminates TLS.
- **Bound execution cost.** `REQUEST_TIMEOUT_SECONDS`, `SCRIPT_TIMEOUT_SECONDS`
  and `MAX_CONCURRENT_EXECUTIONS` limit how long and how many workflows run.
  Timeouts cannot interrupt every native JavaScript built-in (for example a
  single long-running regular expression) and do not cap memory use, so treat
  workflow definitions as trusted and monitor resource consumption.
- **Restrict CORS** with `CORS_ALLOWED_ORIGINS`; the default `*` allows any
  origin to read responses from a browser.
- **Client IP logging is off by default** (`LOG_CLIENT_IP`). When enabled it
  logs the connection address, never the client-supplied `X-Forwarded-For`
  header. IP addresses are personal data in some jurisdictions.
- `/metrics` is served on the same port as the API. Do not expose it publicly
  if Prometheus metrics are considered sensitive in your environment.
