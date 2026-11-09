# Аккаунт A — AI и агент

Зона: `services/agent/`, `openspec/`, `data/kb/`, `eval/`, контракт `contracts/agent-stream.md`.
Требования в зоне ответственности: R02, R03, R04, R05, R06, R10, R15, R21, R22, R24, R25, R37, R44–R46, R53, R57, R60, R61, R69, R71, R72, R75, R77–R79.
Бюджет по фазам (доля недельного лимита): Ф0 10% · Ф1 25% · Ф2 30% · Ф3 10% · Ф4 5% · резерв 20%.

## Что сдаёт по фазам
| Фаза | Результат |
|---|---|
| Ф0 | ADR-001 (язык агента), `contracts/agent-stream.md`, `agent.yaml` (черновик), `openspec init`, контекст в конфиге OpenSpec |
| Ф1 | Агент на stub: intent router, S3 (объяснение → черновик → proposal → вызов MCP после подтверждения), SSE-события |
| Ф2 | S2: поиск по базе знаний с ACL, ответы с источниками, эскалация; S4 логика; провайдер `openai_compat`; синтетическая база знаний 25–40 документов по 2 командам |
| Ф3 | Eval-набор (60+ кейсов: ответы, отказы, prompt-injection, «к кому идти»), отчёт eval, покрытие > 80% |
| Ф4 | Раздел README «LLM и ключи», `.env.example`, слайды «AI и права», «Метрики и пилот» |

## Стартовые промпты для Claude Code
Сессия 1 (Opus через `/model opusplan`, Plan Mode):
```
Read CLAUDE.md, docs/context/architecture.md and the S2/S3 sections of docs/context/product.md.
Goal: write ADR-001 (agent service language: Python+Flask vs Go) and contracts/agent-stream.md.
Constraints: R59 (Avito stack compatibility), R71 (rights), R77-R79 (non-blocking, errors).
Produce a plan first; do not write code yet.
```
Сессия 2 (Sonnet):
```
/opsx:propose agent-s3-absence-flow
Scope: stub-mode intent router and the S3 day-off flow per docs/context/product.md S3 steps 1-10.
Specs must cover: no write tool call without confirmed proposal (R71), idempotency (R79), result shown and saved (R66).
Every scenario gets an ID used in test names.
```
Сессия 3 (Sonnet): `/opsx:apply`, затем «Use the test-writer subagent for services/agent/agent/flows/absence».

## Системный промпт агента — обязательные правила
- Отвечать только по документам, которые вернул `kb.search`; без источника — честно сказать и предложить эскалацию.
- Каждый ответ из базы помечается как тестовая инструкция прототипа; не называть их регламентами Авито.
- Не выполнять запись без подтверждённого proposal; не действовать от имени другого человека.
- Отказ (вежливо, с адресатом) на: чужие данные, зарплату, компенсации, медицину, найм, увольнения.
- 1:1, ревью компетенций, обратная связь, экватор — только напомнить и помочь подготовиться.
- Тон нейтральный, на «ты» или «вы» — решить в Ф0 и держать везде; без эмодзи.

## Синтетическая база знаний (`data/kb/*.md`, frontmatter)
```
---
doc_id: delivery-code-review
title: Как у нас проходит код-ревью (команда Доставка)
source_type: test
acl: {roles: [intern, mentor, lead], teams: [delivery]}
updated: 2026-11-01
---
```
Темы из чек-листа (обязательны): кто есть кто в команде (CL-03), каналы и чаты команды (CL-04), пространство кластера/команды
и доступы (CL-05), документы и мануалы команды (CL-07), основы защиты данных, раскрытие информации, работа с КИ — тестовые
версии со ссылкой на официальные документы (CL-08), как закрывать практику и писать учебные работы (CL-09), подготовка к
экватору и структура презентации (CL-10, CL-11), выпускной и опрос (CL-14–CL-16), что делает куратор стажировки (CL-02, CL-06).
Темы вне чек-листа: отгул и отсутствия (S3), ревью кода, стендапы, инструменты команды, бенефиты (только общая справка). Вдохновляться Playbook и статьями Авито, но не копировать их и
не выдавать за регламенты.

## Проект в claude.ai
Загрузить: `case-brief.md`, `requirements.md`, `product.md`, `architecture.md`, этот файл.
Использовать для: формулировок промпта, генерации синтетических документов базы, eval-кейсов, текста слайдов.
