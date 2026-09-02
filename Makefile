SHELL 		:= /bin/bash
BUILD_DIR   := build
COMPOSE     := docker compose

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

.PHONY: clean
clean: ## Remove volumes and build artefacts
	$(COMPOSE) down -v --remove-orphans
	rm -rf $(BUILD_DIR)
	go clean -testcache