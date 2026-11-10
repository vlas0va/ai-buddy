---
doc_id: frontend-onboarding
title: "Онбординг фронтенд-разработчика"
source_type: test
acl: {roles: [intern, mentor, lead, alumni], teams: ["*"]}
checklist_item_id: CL-07
checklist: [CL-07]
tags: [фронтенд, frontend, react, typescript, интерфейс, дизайн-система]
updated: 2026-11-10
---
# Онбординг фронтенд-разработчика

Тестовая инструкция для стажёров-фронтендеров.

- **Стек:** React и TypeScript, состояние — TanStack Query и Zustand, схемы — Zod.
- **Дизайн-система:** готовые компоненты и токены; новые компоненты согласуй с дизайнером.
- **Локальный запуск:** `npm ci`, затем `npm run dev`; моки API включаются режимом mock.
- **Тесты:** Vitest для компонентов, Playwright для сценариев; порог покрытия — 80%.
- **Первая задача:** небольшая доработка экрана с тестами и проверкой на dev-стенде.
