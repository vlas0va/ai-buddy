---
name: ui-states
description: Frontend patterns for every new screen, request or stream in web/ — TanStack Query, SSE chat, Zod parsing, loading/error/empty states and the under-1-second rule. Use when building or changing any UI that talks to the API.
---
# UI states and data access (R63, R76–R79)

- API access only through `web/src/api/client.ts` (base URL from `VITE_API_BASE_URL`). Every response is parsed with a Zod
  schema in `web/src/api/schemas.ts`; a parse failure is shown as an error state, never swallowed.
- Server state = TanStack Query (query keys in `web/src/api/keys.ts`). UI state = Zustand. Do not duplicate server data in Zustand.
- Local actions (tick a task, open a card, switch tabs) are optimistic: update the cache immediately, roll back on error.
  Target < 1 s; e2e measures it for task toggle and plan load.
- Chat stream: one SSE connection per message. Map events from `contracts/agent-stream.md`:
  `status` → waiting text ("Ищу в базе знаний…", "Отправляю заявку…"); `token` → append; `sources` → chips + test badge;
  `action_proposal` → draft card with "Отправить"; `action_result` → result card + invalidate `plan` and `actions`;
  `error` → message + "Повторить" if retryable; `done` → unlock input. The input and the rest of the page stay usable while streaming.
- Every view has four states designed: loading (skeleton), empty, error (with retry), success.
- Role-dependent views come from `GET /api/me` role; never hide data only on the client — the API must not return it.
- Clickstream: `track(name, props)` batches events to `POST /api/events`; names follow `contracts/events.md`.
- Copy in Russian, neutral, no emojis. Badge text for KB answers: "Тестовая инструкция прототипа".
- Tests: Vitest + Testing Library for components and the SSE reducer; Playwright for scenarios.
