# Цели сервиса агента (владелец A). Запуск из services/agent или через корневой Makefile.
PYTHON ?= $(shell python3 -c "" >/dev/null 2>&1 && echo python3 || echo python)
COV_DIR ?= ../../coverage/agent

.PHONY: install test coverage lint train eval run dev
install: ## Установить зависимости для работы и разработки
	$(PYTHON) -m pip install -r requirements-dev.txt

test: ## Юнит- и интеграционные тесты (stub-LLM, моки MCP в процессе, без сети)
	$(PYTHON) -m pytest -q

coverage: ## Отчёт о покрытии (HTML + Cobertura + сводка), падает ниже 80%
	mkdir -p $(COV_DIR)
	$(PYTHON) -m pytest -q --cov --cov-report=term --cov-report=html:$(COV_DIR)/html \
	  --cov-report=xml:$(COV_DIR)/cobertura.xml --cov-fail-under=80 | tee $(COV_DIR)/summary.txt | tail -45

lint: ## ruff + OpenSpec scenario → test trace
	$(PYTHON) -m ruff check . && $(PYTHON) -m ruff format --check .
	$(PYTHON) tools/spec_trace.py

train: ## Переобучить классификатор намерений и записать eval/reports/intents.md
	$(PYTHON) -m training.train_intents

eval: ## Прогнать набор оценки (eval/cases) и записать eval/reports/latest.{md,json}
	$(PYTHON) ../../eval/run_eval.py

run: ## Запустить агента локально на :8090 (stub-LLM)
	AGENT_DEV_MODE=true AGENT_ALLOW_CLOCK_OVERRIDE=true $(PYTHON) -m agent.app

dev: run
