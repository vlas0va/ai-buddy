---
doc_id: delivery-tools
title: "Инструменты команды Доставка"
source_type: test
acl: {roles: [intern, mentor, lead, alumni], teams: [delivery]}
checklist_item_id: CL-07
checklist: [CL-07]
tags: [инструменты, стек, Go, Kafka, Grafana]
updated: 2026-11-01
---
# Инструменты команды «Доставка»

- Язык — Go, базы — PostgreSQL и Redis, обмен событиями — Kafka.
- Мониторинг — Grafana, алерты разбирают дежурные в закрытом канале #delivery-oncall.
- CI/CD — внутренняя платформа, пайплайн запускается на каждый MR.
- Задачи — Jira, код — GitLab, документы — Confluence, «Пространство кластера «Доставка»».
- Локальный запуск сервиса — docker compose по инструкции «Онбординг разработчика».
