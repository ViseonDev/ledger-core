
export PATH := $(shell go env GOPATH)/bin:$(PATH)

SHELL := /bin/bash
.DEFAULT_GOAL := help

BUILD_DIR   := build
RESULTS_DIR := $(BUILD_DIR)/test-results
COMPOSE     := docker compose
COMPOSE_DEV := docker compose -f docker-compose.yml -f docker-compose.dev.yml

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# Environment

.PHONY: up
up: .env ## Start everything and wait until healthy
	$(COMPOSE) up -d --build --wait

.PHONY: dev
dev: .env ## Start with hot reload and a debugger port
	$(COMPOSE_DEV) up --build

.PHONY: down
down: ## Stop and remove containers
	$(COMPOSE) down --remove-orphans

.PHONY: logs
logs: ## Tail service logs
	$(COMPOSE) logs -f --tail=100

.PHONY: ps
ps: ## Show service status
	$(COMPOSE) ps

# Create .env from template on first run so fresh clones work out of the box.
.env:
	@cp .env.example .env
	@echo "created .env from .env.example"

# Database

.PHONY: migrate
migrate: ## Run migrations
	$(COMPOSE) run --rm ledgerctl migrate up

.PHONY: migrate-version
migrate-version: ## Print the schema version
	$(COMPOSE) run --rm ledgerctl migrate version

.PHONY: seed
seed: ## Populate with demo data (idempotent)
	$(COMPOSE) run --rm ledgerctl seed

.PHONY: reconcile
reconcile: ## Check every cached balance against the journal
	$(COMPOSE) run --rm ledgerctl reconcile

.PHONY: psql
psql: ## Open a psql shell
	$(COMPOSE) exec postgres psql -U $${POSTGRES_USER:-ledger} -d $${POSTGRES_DB:-ledger}

# Tests

.PHONY: test
test: test-unit test-int ## Full suite

.PHONY: test-unit
test-unit: $(RESULTS_DIR) ## Fast, no I/O
	go tool gotestsum \
		--junitfile $(RESULTS_DIR)/unit.xml \
		--format testname \
		-- -race -short ./internal/... ./cmd/...

.PHONY: test-int
test-int: $(RESULTS_DIR) ## Integration, real dependencies
	go tool gotestsum \
		--junitfile $(RESULTS_DIR)/integration.xml \
		--format testname \
		-- -race -timeout 15m ./test/integration/...

.PHONY: cover
cover: $(RESULTS_DIR) ## Unit coverage report
	go test -short -coverprofile=$(RESULTS_DIR)/coverage.out ./internal/...
	go tool cover -html=$(RESULTS_DIR)/coverage.out -o $(RESULTS_DIR)/coverage.html
	@echo "coverage report: $(RESULTS_DIR)/coverage.html"

# Code quality

.PHONY: lint
lint: ## Run linters
	golangci-lint run ./...
	go vet ./...

.PHONY: fmt
fmt: ## Run formatters
	golangci-lint fmt ./...

BUF ?= $(shell which buf 2>/dev/null || echo $(shell go env GOPATH)/bin/buf)

.PHONY: generate
generate: ## Regenerate protobuf code
	$(BUF) lint proto
	$(BUF) generate proto

.PHONY: verify
verify: ## Check that generated code and go.mod are up to date
	$(MAKE) generate
	go mod tidy
	@if [[ -n "$$(git status --porcelain)" ]]; then \
		echo "working tree is dirty after generate + tidy:"; \
		git status --porcelain; \
		exit 1; \
	fi

# Cleanup

.PHONY: clean
clean: ## Remove volumes and build artefacts
	$(COMPOSE) down -v --remove-orphans
	rm -rf $(BUILD_DIR)
	go clean -testcache

$(RESULTS_DIR):
	@mkdir -p $(RESULTS_DIR)
