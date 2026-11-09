# AI-buddy — project memory for Claude Code

Prototype of an AI mentor for interns (Avito Start case, Hack&Change 2026).
Kept in English on purpose: this file is loaded every turn and English costs fewer tokens.
Full case text: `docs/context/case-brief.md` (Russian). Scope source of truth: `docs/context/requirements.md` (R01–R85).
Read those only when the task needs them — do not load them "just in case".

## Non-negotiables (from the case; violating any of these costs points or admission)
1. The prototype MUST start from README with one command and the main scenario (S3) MUST pass with NO external LLM key (`LLM_PROVIDER=stub` is the default).
2. Declare only scenarios that work end-to-end and have a green e2e test.
3. Respect user rights: data and operations the user may not access are never shown to the LLM, never returned, never executed (R71). Write actions require explicit user confirmation.
4. No sensitive personal data or financial operations (salary, compensation amounts, medical data). Refuse and redirect to HR BP (R72).
5. All data is synthetic. Every knowledge-base document has `source_type: test`; the UI shows the "Тестовая инструкция прототипа" badge. Never present test instructions as Avito regulations (R69).
6. An action counts as done only if the (mock) service returns a result, the assistant SHOWS it (card with id + status) and SAVES it (`actions` table, linked to a plan task, visible after reload) (R66).
7. Line coverage > 80% per service for team-written code (exclude deps, generated code, tests). CI fails below 80% (R73).
8. UI: simple local operations < 1 s; LLM/external calls never block the UI — stream via SSE, show waiting status, handle failures with retry and a clear message (R76–R79).
9. AI features are developed spec-first with OpenSpec; tests reference spec scenario IDs (R60, R75).
10. No secrets in git. `.env.example` documents every variable.

## Scenarios
- S1 Onboarding plan built from `data/checklist/checklist.yaml` (Avito checklist, items CL-01…CL-16, periods week_1…after_3_months): two plans — intern and mentor; task statuses (not_started, in_progress, waiting_approval, done, overdue); next step; mentor/lead enter goals; AI-cron reminders per period for both roles; `human_only` items are reminded, never done by buddy.
- S2 Ask buddy: answers about team and tools using user context (team, role, stage, plan tasks), cites sources, escalates to mentor with a draft message.
- S3 Day off (main scenario): explain procedure → collect dates → validate → show draft → confirm → create request via absence MCP → show id/status → save → manager approves → status updates.
- S4 Accesses and channels (core, right after S3; grounded in CL-01, CL-04, CL-05): required accesses (Confluence, Jira, team/cluster space), request via access MCP, elevated rights need manager approval, channel list + draft request to mentor, mentor sees what the intern requested.

## Repo map and owners
| Path | Owner | What |
|---|---|---|
| `services/cmd/core`, `services/internal/core/` | B | Go core API: REST + SSE proxy, plan, actions log, JWT check, feature flags, AI-cron, Kafka events |
| `services/cmd/mcp-mocks`, `services/internal/mocks/` | B | One binary, six MCP servers over mock corporate services at /mcp/<server> |
| `services/internal/platform/` | B | Shared Go libs: JWT, confirmation tokens, MCP helpers, SSE, pgtest |
| `services/agent/` | A | Agent service (Python+Flask or Go — see ADR-001): agent.yaml, prompts, RAG with ACL filter, LLM providers |
| `web/` | C | React + TS, TanStack Query/Router, Zustand, Zod, typed API client |
| `e2e/` | C | Playwright tests, one per declared scenario |
| `contracts/` | owner per file | openapi.yaml (B), agent-stream.md (A), mcp-tools.md (B), events.md (B) |
| `openspec/` | A (all write) | specs + changes + archive |
| `data/` | B, A | seed data, synthetic KB, `data/checklist/` (Avito checklist export) |
| `deploy/`, `.github/` | B | compose files, Dockerfiles, CI |
| `docs/` | C (all write) | context, product, architecture, ADRs |

Work inside your own area. Touch another owner's files only through a PR they review. Change `contracts/` only via PR.

## Commands
- `make up` / `make up-full` — compose profiles `core` (minimal) / `full` (+Kafka, Grafana)
- `make test` — unit + integration for all services
- `make coverage` — HTML + Cobertura + summary into `coverage/`; fails under 80%
- `make e2e` — Playwright against a running stack on stub LLM
- `make lint` — golangci-lint, ruff, eslint, `openspec validate --all`
- `make seed` — regenerate the demo seed from the checklist; `make reset` — recreate the database
- `make vendor` — re-vendor Go deps after changing go.mod (needs network)

## How to work (token budget matters: 3 Pro accounts for the whole hackathon)
- One task per session; `/clear` between tasks. Plan Mode before touching more than 2–3 files.
- Default model Sonnet; `/model opusplan` for planned multi-file work; Haiku for mechanical edits.
- Do not read: `node_modules`, `vendor`, `dist`, `build`, `coverage`, lock files, generated code. Use Grep/Glob before Read; read file ranges, not whole large files.
- Run tests with short output (`-q`, `| tail -50`, grep failures). Never paste full logs back.
- Feature flow: `/opsx:propose <change>` → human reviews spec → `/opsx:apply` → `test-writer` subagent → `reviewer` subagent → PR → `/opsx:archive`.
- Subagents run sequentially, max 2 at once. Use project skills in `.claude/skills/` instead of re-deriving procedures.
- Name tests with requirement/scenario IDs, e.g. `TestAbsence_S3_CreateRequest_R66`.
- Commit small; message starts with the requirement ID: `R66: save action result and link to plan task`.

## Conventions
- Go: standard layout `cmd/`, `internal/`; errors wrapped with context; `context.Context` first arg; table-driven tests; generated code (sqlc/oapi-codegen) under `internal/gen/` and excluded from coverage.
- Python (if agent is Python): 3.12, Flask, pydantic, pytest + pytest-cov, ruff.
- TS: strict mode; every API response parsed with a Zod schema in `web/src/api/`; server state in TanStack Query, UI state in Zustand.
- All user-facing text in Russian. Code, identifiers, commit messages in English.
- Russian UI copy: neutral tone, no emojis.
