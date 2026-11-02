SHELL 		:= /bin/bash
BUILD_DIR   := build
COMPOSE     := docker compose
RESULTS_DIR := $(BUILD_DIR)/test-results

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: up
up: ## Start everything
	$(COMPOSE) up -d --wait

.PHONY: down
down: ## Stop and remove
	$(COMPOSE) down --remove-orphans

.PHONY: lint
lint: ## Run linters
	golangci-lint run ./...
	go vet ./...

.PHONY: fmt
fmt: ## Run formatters
	golangci-lint fmt ./...

.PHONY: generate
generate: ## Regenerate protobuf code
	buf lint proto
	buf generate proto

.PHONY: clean
clean: ## Remove volumes and build artefacts
	$(COMPOSE) down -v --remove-orphans
	rm -rf $(BUILD_DIR)
	go clean -testcache


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
		-- -race -timeout 10m ./test/integration/...

$(RESULTS_DIR):
	@mkdir -p $(RESULTS_DIR)