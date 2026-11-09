# web — owner C

React + TypeScript strict, Vite, TanStack Router + Query, Zustand, Zod. Follow the `ui-states` skill for any UI that talks to the API.
- API client in `src/api/` only; base URL `VITE_API_BASE_URL` (R63); every response parsed by Zod.
- SSE chat reducer in `src/chat/stream.ts`, unit-tested event by event.
- Role views from `/api/me`; never rely on hiding data client-side.
- KB answers always show the "Тестовая инструкция прототипа" badge (R69).
- Tests: `npx vitest run --coverage` (> 80% lines), e2e in `../e2e` with Playwright on stub LLM.
- Do not read `node_modules`, `dist`, `coverage`.
