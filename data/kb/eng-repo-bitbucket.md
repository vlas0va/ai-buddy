---
doc_id: eng-repo-bitbucket
title: "Как получить код: Bitbucket, SSH-ключ и клонирование репозитория"
source_type: test
acl: {roles: [intern, mentor, lead, alumni], teams: ["*"]}
checklist_item_id: CL-05
checklist: [CL-05, CL-07]
tags: [репозиторий, bitbucket, git, клонировать, стянуть, ssh]
updated: 2026-11-10
---
# Как получить код: Bitbucket и клонирование репозитория

Код всех команд хранится в Bitbucket. Доступ к нему запрашивается как обычный доступ — можно попросить AI-buddy: «запроси доступ к Bitbucket».

## Шаги
1. Сгенерируй SSH-ключ: `ssh-keygen -t ed25519 -C "имя@компания"`.
2. Добавь публичный ключ (`~/.ssh/id_ed25519.pub`) в профиль Bitbucket: Settings → SSH keys.
3. Найди репозиторий своей команды в проекте команды (Доставка — проект DLV, Поиск — проект SRCH) и скопируй SSH-ссылку.
4. Склонируй: `git clone ssh://git@bitbucket.example/dlv/delivery-service.git` (адрес тестовый).
5. Подними сервис локально по инструкции «Dev-стенд и локальное окружение».

## Если не получается
- `Permission denied (publickey)` — ключ не добавлен в профиль или доступ к Bitbucket ещё не выдан: проверь статус заявки в «Моих заявках».
- Репозитория не видно — попроси наставника добавить тебя в группу проекта команды.
