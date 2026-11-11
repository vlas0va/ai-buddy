---
doc_id: eng-branches-pr
title: "Ветки, коммиты и pull request"
source_type: test
acl: {roles: [intern, mentor, lead, alumni], teams: ["*"]}
checklist_item_id: CL-07
checklist: [CL-07]
tags: [ветка, dev-ветка, branch, коммит, pull request, пулл реквест, pr, merge]
updated: 2026-11-10
---
# Ветки, коммиты и pull request

## Ветки
- Основная ветка — `main`, в неё напрямую не пушим.
- Новая ветка под каждую задачу: `git checkout -b feature/DLV-123-short-name` (номер задачи из Jira в названии обязателен).
- Исправления — `fix/DLV-124-...`. Ветку держим короткой: 1–3 дня работы.

## Коммиты
- Сообщение начинается с номера задачи: `DLV-123: добавить расчёт срока доставки`.
- Один коммит — одно логическое изменение; перед пушем запускай тесты локально.

## Pull request
1. Запушь ветку: `git push -u origin feature/DLV-123-short-name`.
2. Открой pull request в `main` в Bitbucket, заполни шаблон: что сделано, как проверить, ссылка на задачу.
3. Дождись зелёного CI и назначь ревьюеров (кого — см. инструкцию по код-ревью своей команды).
4. После одобрения ревьюеров нажми Merge; ветку удали.
