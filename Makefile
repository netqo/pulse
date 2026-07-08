# Pulse - developer task runner.
# Run `make help` for the list of available targets.

SHELL := /usr/bin/env bash

# Build metadata injected into the version package at link time.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/netqo/pulse/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT) \
	-X $(VERSION_PKG).Date=$(DATE)

# Database URL used by the migration targets. Override on the command line.
DATABASE_URL ?= postgres://pulse:pulse@localhost:5432/pulse?sslmode=disable
MIGRATIONS_DIR := migrations

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## Tidy and verify Go module dependencies.
	go mod tidy

.PHONY: fmt
fmt: ## Format Go sources.
	gofmt -s -w .

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (requires golangci-lint on PATH).
	golangci-lint run

.PHONY: sqlc
sqlc: ## Regenerate the sqlc data-access code from the SQL queries.
	sqlc generate

.PHONY: test
test: ## Run unit tests.
	go test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector and coverage.
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: build
build: ## Build all binaries into ./bin.
	@mkdir -p bin
	@for pkg in $$(go list ./cmd/... 2>/dev/null); do \
		name=$$(basename $$pkg); \
		echo "building $$name"; \
		go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$$name $$pkg; \
	done

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations.
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back the most recent migration.
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1

.PHONY: migrate-reset
migrate-reset: ## Roll back all migrations.
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down -all

.PHONY: compose-up
compose-up: ## Start the local stack in the background.
	docker compose up -d

.PHONY: compose-down
compose-down: ## Stop the local stack and remove volumes.
	docker compose down -v

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin coverage.out
