# Metrics Executor Implementation Plan

> **For agentic workers:** Use subagent-driven-development to implement and review each task.

**Goal:** Prepare metrics-executor on internal GitLab for staging before a later public migration.
**Architecture:** Rename Go module and executable while preserving existing image repositories, CI configuration, HTTP routes, and database environment variables. Add tested runtime safeguards and produce a private-release-readiness audit without rewriting history.
**Tech Stack:** Go 1.26.10, MySQL, goja, standard net/http, UBI9.
**Spec:** User-approved design and corrections in this conversation (GitLab-first).

## Global Constraints
- Module github.com/fullsend-ai/metrics-executor; executable cmd/metrics-executor and bin/metrics-executor, container executable /metrics-executor.
- Preserve .tekton/* and .gitlab-ci.yml byte-for-byte and all existing image repository references. No image builds, registry operations, push, remote creation, history rewrite, or deployment.
- Retain UBI9 digest-pinned base images and compose build target compatibility.
- Keep /workflows/{name}, DASHBOARD_DB_* and DEVLAKE_DB_* contracts. Keep Kubernetes resource identities/selectors operationally stable for staging; rename executable and internal CA path consistently.
- Apache-2.0 with Red Hat Inc. attribution; preserve existing copyright attribution, do not imply permission to erase third-party notices.
- Publish/review AGENTS.md and CLAUDE.md; defer Code of Conduct and security reporting contacts.
- Preserve CORS wildcard default for compatibility. TLS termination remains an ingress responsibility (document rather than introduce an unnecessary listener).
- Keep changes uncommitted for user review; no push/MR without permission.

## Review Focus
- Special-character database credentials round-trip through mysql.ParseDSN without option injection.
- Malformed client correlation IDs are replaced; no untrusted proxy IP logging by default.
- Request cancellation and saturation release concurrency slots without leaking goroutines.
- Existing GitLab CI and Tekton still invoke valid Containerfile paths/targets and image refs.
- Release audit distinguishes confirmed facts from scanner limitations, default sample credentials, and policy decisions.

## Task 1: Rename, licensing, and operational documentation
Files: go.mod, Go imports, cmd/metrics-executor/main.go, Makefile, Containerfile, compose-dev.yaml, deploy/workflows.template.yaml, LICENSE, NOTICE, AGENTS.md, CLAUDE.md, README.md, CONTRIBUTING.md, SECURITY.md name only, docs/adr/*, .dockerignore.
- [x] Rename module/imports and entrypoint; use mv for directory, targeted replacements elsewhere. Keep CI and Tekton unchanged, existing image refs and container stage target workflows unchanged. Keep K8s resource names/selectors for staging; consistently update CA mount paths.
- [x] Replace MIT LICENSE with canonical Apache-2.0 text and add NOTICE preserving Red Hat attribution. Update contributing license description if needed.
- [x] Review agent docs: replace obsolete tool-specific commands with generic instructions; naming/config examples accurate and no private operational details newly exposed.
- [x] Fix existing README/compose stale DB_* config to actual DASHBOARD_DB_*/DEVLAKE_DB_* variables. Clearly label dev-only sample credentials, loopback-bound compose ports. Add .dockerignore excluding Git, env credentials, build outputs but not required source/modules.
- [x] Run make fmt-check vet test build; inspect intentional legacy name allowlist. Write report under docs/superpowers/reports/task-1.md.

## Task 2: Tested runtime and deployment safeguards
Files: internal/config/*, internal/server/*, cmd/metrics-executor/main.go, env.example, README.md, deploy/workflows.template.yaml, related docs.
- [x] Use test-driven development: write focused failing tests, observe failures, implement, run focused and race tests.
- [x] DSNs via mysql.Config.FormatDSN, preserving ParseTime and CA handling; test credential/host/DB escaping and reject malformed configuration where necessary.
- [x] Generic workflow internal failures, request ID available through existing response header; no raw SQL/goja errors in responses. Validate IDs (ASCII alphanumeric ._- max 128), generate for invalid supplied IDs.
- [x] Disable client-IP logging by default via LOG_CLIENT_IP=false; opt-in logs RemoteAddr only, never blindly trust XFF. Configurable CORS_ALLOWED_ORIGINS preserving * default, empty disables; accurate allowed methods/headers.
- [x] REQUEST_TIMEOUT_SECONDS default 300 and MAX_CONCURRENT_EXECUTIONS default 32. Context deadline for workflow work (not a false promise of forceful JS/native interruption). Nonblocking global execution admission returns 503 when saturated, release with defer on all exits; tests for error/cancellation/saturation. Existing Server.New callers continue to compile through options method or separate constructor.
- [x] Add nosniff and no-store API response headers. Correct statusWriter implicit WriteHeader and implement transparent Unwrap/Flusher compatibility.
- [x] Deployment securityContext: nonroot, drop all capabilities, no privilege escalation, seccomp RuntimeDefault, read-only rootfs plus writable tmp emptyDir, disable service account token. Preserve resource identities/image refs and CI.
- [x] Document TLS ingress, SQL DB read-only privilege dependence including SQL SECURITY DEFINER procedure caveat, goja native/memory limits and outstanding limits; no claims of complete sandboxing.
- [x] Run full checks; write report docs/superpowers/reports/task-2.md.

## Task 3: Private current-tree/history security audit and final verification
Files: docs/security/release-readiness.md (sanitized actionable report).
- [x] Read tracked files and locally reachable history/ref metadata, inspect deleted files and credential-like matches with redaction. Do not print discovered secrets or upload content to third-party scanning services.
- [x] Distinguish personal author names/emails and workstation hostname from infrastructure identifiers and public corporate contacts; author attribution is not automatically removed. No history rewrite.
- [x] Run compatible golangci-lint, govulncheck (record versions/date/network limitations), secret scanner if available, go test -race, vet, format, build, local binary HTTP smoke (no image build). No blindly update dependencies just for age.
- [x] Report severity, exact path/line or commit reference, evidence, fix/defer decision and remaining publication gates. Explicitly leave CI/registry/history/contacts migration for later.
- [x] Verify .tekton/.gitlab-ci.yml and image refs unchanged against base. Write report docs/superpowers/reports/task-3.md.

## Execution ledger
- Setup: feature branch chore/metrics-executor-release-prep from fetched origin/main, clean baseline.
- Baseline fmt/vet/race tests/build PASS; installed golangci-lint blocked by Go1.25 build vs required Go1.26.10.
- Preflight: Tasks 1/2 share README, config docs, deploy and main; sequential execution, Task 2 consumes renamed module/entrypoint. Task 3 read-only audits prior task output. No conflicting file ownership.
- Ruling: Keep K8s resource names/selectors stable for staging — operational continuity explicitly requested — cosmetic legacy names remain until migration.
- Ruling: Preserve wildcard CORS default and ingress TLS model — avoid unrequested breaking API/deployment changes — users must configure restricted origins and ingress explicitly.
- Ruling: No --version/release pipeline now — no agreed release version/source metadata and no image/release work requested — release tooling deferred to GitHub discussion.
- Ruling: Do not automatically commit — keep user-reviewable changes on feature branch — user may commit after inspection.

- Task 1: complete — rename, Apache-2.0 + NOTICE, docs/compose/.dockerignore; fmt/vet/race tests/build pass.
- Task 2: complete — DSN FormatDSN, error redaction, request-ID validation, CORS allowlist, opt-in IP logging, request timeout, concurrency cap, security headers, statusWriter fixes, pod hardening; new tests pass with -race; lint 0 issues.
- Task 3: complete — gitleaks (tree + branch history) no leaks; manual all-ref scan no credentials; report docs/security/release-readiness.md.
- Ruling: Upgrade golang.org/x/text v0.40.0 -> v0.41.0 for GO-2026-6629 — targeted, non-reachable but safe — costs a dependency bump in the MR.
- Ruling: Do not add a Go toolchain directive or bump the pinned UBI digest for GO-2026-6859 — Konflux hermetic prefetch would break and no image work is in scope — the finding stays open as a production blocker until the base image is updated with the go directive.
- Ruling: Use `mysql.Config.FormatDSN` while keeping plain-DSN test expectations — verified the driver round-trips special characters — costs nothing; if a future driver changes escaping, the round-trip test catches it.
- Task 1/2 verification: golangci-lint v2 latest 0 issues; govulncheck 1 stdlib finding; local smoke live=200 ready=503 metrics=200 workflow=503.
