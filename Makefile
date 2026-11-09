.PHONY: help up up-full down reset build logs ps test test-demo coverage lint contracts seed vendor test-db-up test-db-down e2e test-agent lint-agent eval train agent-dev
.DEFAULT_GOAL := help

# Thin wrapper: logic lives in scripts/ so this file rarely changes.
PYTHON ?= $(shell python3 -c "" >/dev/null 2>&1 && echo python3 || echo python)
COMPOSE := docker compose -f deploy/docker-compose.yml

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

up: ## Start profile core (web, core, agent, mcp-mocks, postgres, redis)
	$(COMPOSE) --profile core up -d --build

up-full: ## Start profile full (+Kafka, Grafana)
	$(COMPOSE) --profile full up -d --build

down: ## Stop everything (keeps the database volume)
	$(COMPOSE) --profile full down

reset: ## Drop the database volume and start profile core again
	$(COMPOSE) --profile full down -v
	$(COMPOSE) --profile core up -d --build

build: ## Build all images
	$(COMPOSE) --profile full build

logs: ## Follow logs of all services
	$(COMPOSE) --profile full logs -f --tail=100

ps: ## Service status
	$(COMPOSE) --profile full ps

test: ## Go unit + integration tests (starts a disposable Postgres if TEST_DATABASE_URL is unset) + agent tests
	bash scripts/test.sh
	@$(MAKE) --no-print-directory test-agent

test-demo: ## Go tests with the demo build tag
	bash scripts/test.sh -tags demo

coverage: ## Coverage per service into coverage/, fails under 80%
	bash scripts/coverage.sh

lint: ## gofmt, go vet, contracts, openspec validate + agent lint
	bash scripts/lint.sh
	@$(MAKE) --no-print-directory lint-agent

contracts: ## Validate MCP tool schemas and openapi.yaml
	$(PYTHON) scripts/check_contracts.py

seed: ## Regenerate migrations/0003_seed_demo.sql from data/checklist
	$(PYTHON) scripts/gen_seed.py

vendor: ## Resolve and vendor Go dependencies (needs network)
	bash scripts/vendor.sh

test-db-up: ## Start the disposable test Postgres on localhost:55432
	bash scripts/test-db.sh up

test-db-down: ## Stop the disposable test Postgres
	bash scripts/test-db.sh down

e2e: ## Playwright e2e against a running stack (owner C)
	bash scripts/e2e.sh

# ---- Agent (owner A, services/agent; Python deps: pip install -r services/agent/requirements-dev.txt)
test-agent: ## Agent tests only (stub LLM, in-process MCP mocks, no network)
	@echo "== services/agent"; cd services/agent && $(PYTHON) -m pytest -q

lint-agent: ## ruff + OpenSpec scenario → test trace for the agent
	cd services/agent && $(PYTHON) -m ruff check . && $(PYTHON) -m ruff format --check .
	$(PYTHON) services/agent/tools/spec_trace.py

eval: ## Agent eval set (eval/cases) → eval/reports/latest.md; fails if any guard metric < 100%
	$(PYTHON) eval/run_eval.py

train: ## Retrain the agent intent classifier → eval/reports/intents.md
	cd services/agent && $(PYTHON) -m training.train_intents

agent-dev: ## Agent alone on :8090 with the dev console at http://localhost:8090/dev
	cd services/agent && AGENT_DEV_MODE=true AGENT_ALLOW_CLOCK_OVERRIDE=true $(PYTHON) -m agent.app
