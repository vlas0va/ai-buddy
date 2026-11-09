.PHONY: help up up-full down build test coverage lint contracts e2e seed reset
.DEFAULT_GOAL := help

PYTHON ?= $(shell python3 -c "" >/dev/null 2>&1 && echo python3 || echo python)
COMPOSE := docker compose -f deploy/docker-compose.yml
GO_MODULES := $(patsubst %/go.mod,%,$(wildcard services/*/go.mod))

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

up: ## Start profile core
	$(COMPOSE) --profile core up -d --build

up-full: ## Start profile full (+Kafka, Grafana)
	$(COMPOSE) --profile full up -d --build

down: ## Stop everything and remove volumes
	$(COMPOSE) --profile full down -v

build: ## Build images
	$(COMPOSE) --profile full build

test: ## Unit tests for all Go modules
	@set -e; for m in $(GO_MODULES); do echo "== $$m"; (cd $$m && go test ./...); done

coverage: ## Coverage report, fails under 80%
	bash scripts/coverage.sh

lint: ## gofmt check, go vet, openspec validate
	@set -e; bad=$$(gofmt -l services); if [ -n "$$bad" ]; then echo "gofmt needed:"; echo "$$bad"; exit 1; fi
	@set -e; for m in $(GO_MODULES); do (cd $$m && go vet ./...); done
	@if [ -d openspec ] && command -v openspec >/dev/null 2>&1; then openspec validate --all; else echo "openspec: skipped"; fi

contracts: ## Validate MCP tool schemas and openapi.yaml (needs python3 + pyyaml + jsonschema)
	$(PYTHON) scripts/check_contracts.py

e2e: ## Playwright e2e (owner C)
	@echo "e2e: owner C, not implemented yet"

seed: ## Regenerate migrations/0003_seed_demo.sql from data/checklist (needs python3 + pyyaml)
	$(PYTHON) scripts/gen_seed.py

reset: ## Recreate the database from migrations (drops the volume)
	$(COMPOSE) --profile full down -v
	$(COMPOSE) --profile core up -d --build
