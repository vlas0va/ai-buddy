.PHONY: help up up-full down reset build logs ps test test-demo coverage lint contracts seed vendor test-db-up test-db-down e2e smoke test-agent lint-agent eval train agent-dev
.DEFAULT_GOAL := help

# Тонкая обёртка: логика живёт в scripts/, поэтому этот файл меняется редко.
PYTHON ?= $(shell python3 -c "" >/dev/null 2>&1 && echo python3 || echo python)
COMPOSE := docker compose -f deploy/docker-compose.yml

help: ## Список целей
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

up: ## Запустить профиль core (web, core, agent, mcp-mocks, postgres, redis)
	$(COMPOSE) --profile core up -d --build

up-full: ## Запустить профиль full (+Kafka, Grafana)
	KAFKA_BROKERS=kafka:9092 $(COMPOSE) --profile full up -d --build

down: ## Остановить всё (том базы сохраняется)
	$(COMPOSE) --profile full down

reset: ## Удалить том базы (скачанная модель A-Vibe сохраняется) и снова запустить профиль core
	$(COMPOSE) --profile full down
	docker volume rm -f aibuddy_pgdata
	$(COMPOSE) --profile core up -d --build

build: ## Собрать все образы
	$(COMPOSE) --profile full build

logs: ## Следить за логами всех сервисов
	$(COMPOSE) --profile full logs -f --tail=100

ps: ## Статус сервисов
	$(COMPOSE) --profile full ps

test: ## Юнит- и интеграционные тесты Go (поднимает одноразовый Postgres, если TEST_DATABASE_URL не задан) + тесты агента
	bash scripts/test.sh
	@$(MAKE) --no-print-directory test-agent

test-demo: ## Тесты Go с тегом сборки demo
	bash scripts/test.sh -tags demo

coverage: ## Покрытие по сервисам в coverage/, падает ниже 80%
	bash scripts/coverage.sh

lint: ## gofmt, go vet, контракты, openspec validate + линтер агента
	bash scripts/lint.sh
	@$(MAKE) --no-print-directory lint-agent

contracts: ## Проверить схемы инструментов MCP и openapi.yaml
	$(PYTHON) scripts/check_contracts.py

seed: ## Пересобрать migrations/0003_seed_demo.sql из data/checklist
	$(PYTHON) scripts/gen_seed.py

vendor: ## Разрешить зависимости Go и положить в vendor (нужна сеть)
	bash scripts/vendor.sh

test-db-up: ## Поднять одноразовый тестовый Postgres на localhost:55432
	bash scripts/test-db.sh up

test-db-down: ## Остановить одноразовый тестовый Postgres
	bash scripts/test-db.sh down

e2e: ## Playwright e2e на запущенном стеке (владелец C)
	bash scripts/e2e.sh

smoke: ## S3 + S4 через API ядра на запущенном стеке (make up)
	bash scripts/smoke.sh

# ---- Агент (владелец A, services/agent; зависимости Python: pip install -r services/agent/requirements-dev.txt)
test-agent: ## Только тесты агента (stub-LLM, моки MCP в процессе, без сети)
	@echo "== services/agent"; cd services/agent && $(PYTHON) -m pytest -q

lint-agent: ## ruff + трассировка сценариев OpenSpec в тесты агента
	cd services/agent && $(PYTHON) -m ruff check . && $(PYTHON) -m ruff format --check .
	$(PYTHON) services/agent/tools/spec_trace.py

eval: ## Набор оценки агента (eval/cases) → eval/reports/latest.md; падает, если любая метрика ограничений < 100%
	$(PYTHON) eval/run_eval.py

train: ## Переобучить классификатор намерений агента → eval/reports/intents.md
	cd services/agent && $(PYTHON) -m training.train_intents

agent-dev: ## Агент отдельно на :8090 с консолью разработки по http://localhost:8090/dev
	cd services/agent && AGENT_DEV_MODE=true AGENT_ALLOW_CLOCK_OVERRIDE=true $(PYTHON) -m agent.app
