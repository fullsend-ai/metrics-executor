BINARY  := bin/metrics-executor
IMAGE   := metrics-executor
TAG     ?= latest
GO      := go
GOFLAGS ?=
GOLANGCI_LINT ?= golangci-lint

PORT ?= 7070

.PHONY: build test vet fmt-check lint clean image query run dev dev-down help

build: ## Build the binary
	$(GO) build $(GOFLAGS) -buildvcs=false -o $(BINARY) ./cmd/metrics-executor/

test: ## Run all tests (mock DB, no real database needed)
	$(GO) test ./... -race -count=1

vet: ## Run go vet
	$(GO) vet ./...

fmt-check: ## Check that all files are gofmt'd
	@test -z "$$(gofmt -l .)" || { gofmt -l . ; echo "above files are not gofmt'd"; exit 1; }

lint: ## Run golangci-lint (must be built with a Go version >= go.mod)
	$(GOLANGCI_LINT) run

image: ## Build container image
	podman build -t $(IMAGE):$(TAG) .

query: ## Query the running service's endpoints
	@echo "=== /health/live ==="
	@curl -sf http://localhost:$(PORT)/health/live | jq .
	@echo "=== /health/ready ==="
	@curl -sf http://localhost:$(PORT)/health/ready | jq .
	@echo "=== /metrics (first 5 lines) ==="
	@curl -sf http://localhost:$(PORT)/metrics | head -5

run: build ## Build and run the service locally
	./$(BINARY)

dev: ## Start local dev environment (MySQL + metrics-executor)
	podman compose -f compose-dev.yaml up --build -d

dev-down: ## Stop local dev environment
	podman compose -f compose-dev.yaml down

clean: ## Remove build artifacts
	rm -rf bin/

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'
