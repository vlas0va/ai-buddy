# Контекст-пак AI-buddy: что куда класть

Пак распаковывается в корень нового репозитория. Часть файлов Claude Code читает сам, часть загружается в проекты claude.ai.

## Состав
| Файл | Для кого | Когда читается |
|---|---|---|
| `CLAUDE.md` | Claude Code, все | Каждый ход — поэтому коротко и на английском (кириллица дороже по токенам) |
| `services/agent/CLAUDE.md`, `services/core/CLAUDE.md`, `web/CLAUDE.md` | Claude Code | Когда Claude работает с файлами этой папки |
| `docs/context/case-brief.md` | все | Вместо PDF ТЗ — по запросу |
| `docs/context/requirements.md` | все | Матрица R01–R85, скилл `req-trace` |
| `docs/context/product.md` | A, C | Сценарии, учётки, метрики, сценарий видео |
| `docs/context/architecture.md` | A, B | Компоненты, контракты, права, переменные окружения |
| `docs/context/openspec-seed.md` | A | Инициализация OpenSpec и пример спеки |
| `docs/context/roles/*.md` | каждый свой | Зона, результаты по фазам, стартовые промпты |
| `.claude/agents/*.md` | Claude Code | Субагенты: test-writer, reviewer, security-auditor, req-auditor |
| `.claude/skills/*/SKILL.md` | Claude Code | Скиллы: add-mock-service, add-agent-tool, coverage-gate, req-trace, ui-states, clean-run |
| `.claude/settings.json`, `.claude/hooks/format.sh` | Claude Code | Запрет чтения тяжёлых папок, автоформат после правки |
| `.env.example` | все | Пример конфигурации без секретов (R46) |
| `data/checklist/avito-intern-checklist.csv` | все | Исходная выгрузка чек-листа Авито |
| `data/checklist/checklist.yaml` | A, B, C | Тот же чек-лист по ролям, периодам и сценариям; из него строятся планы S1 и сиды mcp-checklist |

## Первые шаги
1. Создать приватный репозиторий, распаковать пак в корень, `chmod +x .claude/hooks/format.sh`, закоммитить.
2. Пригласить аккаунт жюри `hack-and-change-2026` сразу.
3. Чек-лист Авито уже в `data/checklist/`. Если в исходной таблице есть отдельный лист с материалами для Backend-стажёров
   (ТЗ его упоминает), выгрузить и его — в CSV попадает только один лист.
4. Каждый: `claude` в корне репозитория → `/model` выбрать Sonnet по умолчанию → `/status` проверить, что вход по подписке Pro, а не по API-ключу.
5. Каждый создаёт свой проект в claude.ai и загружает в него `case-brief.md`, `requirements.md`, свой role-файл и
   `product.md` (A, C) или `architecture.md` (A, B). Больше файлов не грузить: проектные знания тоже расходуют лимит.
6. Стартовые промпты — в своём role-файле. Порядок фаз и бюджеты — в общем документе плана.
