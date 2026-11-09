# Аккаунт B — бэкенд и интеграции

Зона: `services/core/`, `services/mcp-*/`, `deploy/`, `.github/`, `Makefile`, контракты `openapi.yaml`, `mcp-tools.md`, `events.md`, миграции.
Требования в зоне ответственности: R02, R03, R04, R08, R11, R17, R19–R23, R27, R33, R34, R36, R39, R40, R47, R51, R52, R54–R56, R62, R66, R68, R70, R71, R73, R74, R79.
Бюджет по фазам: Ф0 10% · Ф1 30% · Ф2 30% · Ф3 10% · Ф4 5% · резерв 15%.

## Что сдаёт по фазам
| Фаза | Результат |
|---|---|
| Ф0 | Скелет монорепо, Makefile, compose-профили `core`/`full`, CI (линтеры, тесты, порог покрытия, `openspec validate --all`, gitleaks), `openapi.yaml`, `mcp-tools.md`, миграции v1 |
| Ф1 | mock-idp + тестовые учётки, core: plan/actions/confirm/SSE-прокси, mcp-absence, mcp-approvals, mcp-directory, seed |
| Ф2 | mcp-kb (FTS), mcp-checklist, mcp-access; feature flags runtime + build tag `demo`; ИИ-крон; outbox → Kafka → analytics_events → дашборд Grafana |
| Ф3 | Матрица тестов прав по каждому MCP-серверу, тесты сбоев и идемпотентности, `make coverage` со сводкой, покрытие > 80% |
| Ф4 | Таблица версий зависимостей для README, проверка чистого запуска, тег `submission` |

## Стартовые промпты для Claude Code
Сессия 1 (`/model opusplan`, Plan Mode):
```
Read CLAUDE.md and docs/context/architecture.md.
Goal: monorepo skeleton (services/core, services/mcp-*, web, e2e, deploy, contracts), Makefile targets from CLAUDE.md,
docker compose with profiles core/full and healthchecks, GitHub Actions CI with an 80% coverage gate per service.
Plan first, list every file you will create, then implement with empty services that pass health checks.
```
Сессия 2 (Sonnet): «Use the add-mock-service skill to create mcp-absence per docs/context/architecture.md MCP table.»
Сессия 3 (Sonnet): `/opsx:propose core-actions-confirmation` — подтверждение, токен, идемпотентность, сохранение (R66, R71, R79).

## Важные детали
- MCP-серверы на официальном Go SDK, транспорт streamable HTTP; JSON Schema инструментов кладём в `contracts/schemas/`.
- Каждый MCP-сервер сам проверяет JWT и права; тест «роль × инструмент × ресурс» обязателен.
- Генерированный код (sqlc, oapi-codegen) — в `internal/gen/`, исключён из покрытия и линтеров.
- Профиль `core` должен работать без Kafka: события пишутся сразу в `analytics_events`.
- Порты и требования к памяти — в README; цель профиля `core` < 4 ГБ RAM.

## Проект в claude.ai
Загрузить: `case-brief.md`, `requirements.md`, `architecture.md`, этот файл.
Использовать для: обсуждения схемы данных, текста ADR-002/003, слайда «Внедрение и интеграции».
