# Бэкенд (зона B): запуск, тесты, эксплуатация

Подробное устройство сервисов: [IMPLEMENTATION.md](IMPLEMENTATION.md). Контракты: `contracts/`.
Интеграция с агентом (A): [../agent/handoff.md](../agent/handoff.md) и `contracts/agent-stream.md` (v1).
Все данные синтетические; тестовые учётки: `intern.anna`, `intern.ivan`, `mentor.oleg`, `lead.maria`, `hrbp.elena`, `alumni.petr`, пароль у всех `demo`.

## Что где работает

| Сервис | Образ / сборка | Порт на хосте | Профили | Назначение |
|---|---|---|---|---|
| `postgres` | postgres:16-alpine | не публикуется | core, full | БД `buddy`, миграции из `migrations/` применяются при первом старте |
| `redis` | redis:7-alpine | не публикуется | core, full | кэш флагов, rate limit |
| `mcp-mocks` | `services/Dockerfile`, `CMD=mcp-mocks` | 127.0.0.1:8100 | core, full | шесть MCP-серверов `/mcp/<server>` над моками корпоративных систем |
| `core` | `services/Dockerfile`, `CMD=core` | 8080 (`CORE_HOST_PORT`) | core, full | REST + SSE, план, действия, согласования, аналитика, крон |
| `agent` (владелец A) | `services/agent/Dockerfile` | 127.0.0.1:8090 | core, full | Flask + SSE, stub LLM без ключа; ходит в `mcp-mocks` (`MCP_BASE_URL`) |
| `web` (владелец C) | `web/Dockerfile` (nginx) | 5173 (`WEB_HOST_PORT`) | core, full | SPA; браузер обращается к core на `VITE_API_BASE_URL` |
| `ollama` | ollama/ollama:0.5.7 | 127.0.0.1:11434 | ollama | необязательная локальная LLM (см. `docs/llm-and-keys.md`) |
| `kafka` | apache/kafka:3.9.0 (KRaft) | не публикуется | full | приёмник событий для Clickstream (R62) |
| `grafana` | grafana/grafana:11.3.0 | 3000 | full | дашборд «AI-buddy: метрики пользы», анонимный просмотр |

Профиль `core` — минимум для сценариев S1–S4. Профиль `full` добавляет Kafka и Grafana.

## Быстрый старт

Эксперту, одна команда (нужен только Docker):

```
docker compose -f deploy/docker-compose.yml --profile core up -d --build
```

Для разработчиков: `make up` (то же самое), `make up-full`, `make down`, `make reset` (сброс БД), `make logs`, `make ps`.
Проверка: `docker compose -f deploy/docker-compose.yml --profile core ps` — у postgres, redis, mcp-mocks, core, agent и web
статус `healthy`; затем `make smoke` проходит S3 и S4 через API core так же, как web (вход `intern.anna`, отгул →
подтверждение → ABS-… → согласование `lead.maria` → статус «согласовано»; доступ в Jira). Открыть: http://localhost:5173.

PowerShell без `make`:

```
docker compose -f deploy/docker-compose.yml --profile core up -d --build
docker compose -f deploy/docker-compose.yml --profile full down -v   # полный сброс вместе с БД
```

### Профиль full и Kafka

По умолчанию `KAFKA_BROKERS` пуст: core пишет события только в `core.analytics_events` (Grafana читает их в обоих профилях).
`make up-full` сам задаёт `KAFKA_BROKERS=kafka:9092`, и core дополнительно отправляет события в Kafka (топики
`buddy.domain`, `buddy.clickstream`).

PowerShell: `$env:KAFKA_BROKERS="kafka:9092"; docker compose -f deploy/docker-compose.yml --profile full up -d --build`.
Значение из shell перекрывает `.env`; без него Kafka выключена даже при запущенном контейнере `kafka`.
Grafana: http://localhost:3000 (дашборд открывается сразу, вход не нужен).

## Переменные окружения

Секреты по умолчанию годятся только для локальной разработки. Реальные значения кладите в `.env` (не коммитить), шаблон: `.env.example`.

| Переменная | Сервис | По умолчанию |
|---|---|---|
| `CORE_PORT` / `MCP_MOCKS_PORT` | core / mocks | 8080 / 8100 (внутри контейнера) |
| `CORE_HOST_PORT` | compose | 8080 (порт core на хосте) |
| `POSTGRES_URL` | core, mocks | `postgres://buddy:buddy@postgres:5432/buddy?sslmode=disable` |
| `REDIS_URL` | core | `redis://redis:6379/0` |
| `JWT_SECRET`, `CONFIRMATION_HMAC_SECRET` | core, mocks (и agent для JWT) | dev-значения в compose; пустые значения — сервис не стартует |
| `AGENT_URL` | core | `http://agent:8090` |
| `MCP_BASE_URL` | core, agent | `http://mcp-mocks:8100/mcp` |
| `KAFKA_BROKERS` | core | пусто = Kafka выключена |
| `CORS_ORIGINS` | core | `http://localhost:5173` |
| `LLM_TIMEOUT_S`, `CRON_INTERVAL`, `CHAT_RATE_PER_MIN`, `JWT_TTL` | core | 30, 24h, 20, 12h |
| `KB_DIR` | mocks | `/app/data/kb` (том `data/kb`, только чтение) |
| `LLM_PROVIDER`, `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL` | agent | `stub` без ключей |

## Версии зависимостей

| Компонент | Версия |
|---|---|
| Go | 1.24 (образ builder: golang:1.24-alpine, runtime: distroless static debian12 nonroot) |
| PostgreSQL | 16 |
| Redis | 7 |
| Kafka | 3.9.0 (KRaft, один узел) |
| Grafana | 11.3.0 |
| Go-модули | см. `go.mod` (pgx/v5, modelcontextprotocol/go-sdk, go-redis/v9, franz-go, miniredis — только тесты, yaml.v3); код в `vendor/` |

Образ собирается с тегом `demo` (`BUILD_TAGS=demo`): включает демо-эндпоинты (прогон крона, переключение флагов). Сборка без них: `--build-arg BUILD_TAGS=`.

## Тесты и покрытие

| Команда | Что делает |
|---|---|
| `make test` | все Go-тесты; если `TEST_DATABASE_URL` не задан — поднимает одноразовый Postgres на `127.0.0.1:55432` |
| `make test-demo` | то же с тегом `demo` |
| `make coverage` | покрытие по наборам `platform`, `mocks`, `core`; падает при < 80% (R73) |
| `make lint` | gofmt, go vet, `scripts/check_contracts.py`, `openspec validate` (если есть) |
| `make test-db-up` / `make test-db-down` | вручную запустить / остановить тестовую БД |
| `make smoke` | S3 + S4 через API core на запущенном стеке (`scripts/smoke.py`, только стандартная библиотека Python) |
| `make e2e` | Playwright (владелец C), нужен запущенный `make up` |

Отчёты покрытия: `coverage/<набор>/index.html` (HTML) и `cover.out`; итоговая таблица печатается в конце; агент — `coverage/agent`
(HTML + Cobertura). В CI они выкладываются артефактом `coverage`. Для `make lint` локально нужны `pyyaml` и `jsonschema`.

CI (`.github/workflows/ci.yml`): `go` (lint, test, coverage Go и агента), `agent` (ruff, трассировка спеки, eval, образ),
`web` (eslint, типы, vitest с порогом 80%, сборка), `contracts`, `migrations`, `compose`, `secrets` (gitleaks v8.21.2),
`openspec`, `stack` (`up --wait` без `.env` и ключа LLM, затем `scripts/smoke.py`).
Своя БД для тестов: `TEST_DATABASE_URL=postgres://user:pass@host:5432/postgres?sslmode=disable make test` (нужны права создавать базы).

## Windows

Запускайте `make` и `scripts/*.sh` из Git Bash или WSL, Docker Desktop с WSL 2. Подробности: [../dev-setup-windows.md](../dev-setup-windows.md).
Если `make` нет, используйте команды `docker compose` из раздела «Быстрый старт».

## Если что-то не работает

| Симптом | Что делать |
|---|---|
| `port is already allocated` на 8080 | в `.env` задать `CORE_HOST_PORT=8081` (и `VITE_API_BASE_URL`, `CORS_ORIGINS` в соответствии) |
| Порт 3000, 5173, 8090 или 8100 занят | остановить процесс-владельца или поменять левую часть `ports:` в `deploy/docker-compose.yml` |
| `Cannot connect to the Docker daemon` | запустить Docker Desktop, дождаться статуса Running, повторить |
| `make test` пишет «Docker не запущен» | запустить Docker или задать `TEST_DATABASE_URL` |
| core не стартует, ждёт `mcp-mocks` | `docker compose -f deploy/docker-compose.yml --profile core logs mcp-mocks`; чаще всего БД ещё применяет миграции или нет `data/kb` |
| Схема БД изменилась, а данные старые | `make reset` (удаляет том `pgdata`, заново применяет миграции и сид) |
| `inconsistent vendoring` | `make vendor` (нужна сеть), закоммитить `vendor/` |
