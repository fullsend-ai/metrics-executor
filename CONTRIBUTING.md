# Contributing to metrics-executor

Thanks for your interest in contributing! This document explains how to get
started.

By participating in this project you agree to abide by our
[Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting bugs and requesting features

- Search existing issues before opening a new one.
- For bugs, include the version or commit, steps to reproduce, the expected and
  actual behavior, and relevant log output (with credentials removed).
- **Security vulnerabilities must not be reported in public issues.** Follow
  [SECURITY.md](SECURITY.md) instead.

## Development setup

Requirements:

- Go (version from the `go` directive in [go.mod](go.mod))
- `make`
- Optional: [golangci-lint](https://golangci-lint.run/) v2 for `make lint`
- Optional: `podman` (or Docker) for container builds and the local dev stack

Common tasks:

```sh
make build      # compile to bin/metrics-executor
make test       # run all tests with the race detector (no database needed)
make vet        # go vet
make lint       # golangci-lint (config in .golangci.yml)
make fmt-check  # verify gofmt formatting
make image      # build the container image
```

Configuration is done via environment variables; see [README.md](README.md)
and [env.example](env.example). The workflow format is documented in
[docs/workflows.md](docs/workflows.md), and the service architecture in
[docs/architecture.md](docs/architecture.md).

## Submitting changes

1. Fork the repository and create a topic branch from `main`.
2. Make your change. Keep pull requests focused on a single concern.
3. Add or update tests. HTTP handlers are tested with `net/http/httptest`;
   database access is tested through interfaces or fakes, so tests never need
   a real database.
4. Make sure `make fmt-check vet lint test` passes.
5. Update documentation (`README.md`, `docs/`, `env.example`) when behavior or
   configuration changes.
6. Open a pull request describing what changed and why.

### Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`.

### Code conventions

- Standard library first; add third-party dependencies only when clearly
  justified.
- Format with `gofmt`.
- Only add comments when the "why" is not obvious from the code.

## License

By contributing, you agree that your contributions will be licensed under the
project's [license](LICENSE).
