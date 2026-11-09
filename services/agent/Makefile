# Agent service targets (owner A). Run from services/agent or via the root Makefile.
PYTHON ?= $(shell python3 -c "" >/dev/null 2>&1 && echo python3 || echo python)
COV_DIR ?= ../../coverage/agent

.PHONY: install test coverage lint train eval run dev
install: ## Install runtime + dev dependencies
	$(PYTHON) -m pip install -r requirements-dev.txt

test: ## Unit + integration tests (stub LLM, in-process MCP mocks, no network)
	$(PYTHON) -m pytest -q

coverage: ## Coverage report (HTML + Cobertura + summary), fails under 80%
	mkdir -p $(COV_DIR)
	$(PYTHON) -m pytest -q --cov --cov-report=term --cov-report=html:$(COV_DIR)/html \
	  --cov-report=xml:$(COV_DIR)/cobertura.xml --cov-fail-under=80 | tee $(COV_DIR)/summary.txt | tail -45

lint: ## ruff + OpenSpec scenario → test trace
	$(PYTHON) -m ruff check . && $(PYTHON) -m ruff format --check .
	$(PYTHON) tools/spec_trace.py

train: ## Retrain the intent classifier and write eval/reports/intents.md
	$(PYTHON) -m training.train_intents

eval: ## Run the eval set (eval/cases) and write eval/reports/latest.{md,json}
	$(PYTHON) ../../eval/run_eval.py

run: ## Run the agent locally on :8090 (stub LLM)
	AGENT_DEV_MODE=true AGENT_ALLOW_CLOCK_OVERRIDE=true $(PYTHON) -m agent.app

dev: run
