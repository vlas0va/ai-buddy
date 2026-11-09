# Backend (zone B) implementation brief

Single source for every agent working on zone B. Contracts in `contracts/` win over this file; this file wins over
`docs/context/architecture.md` where they differ (it is newer). Requirement IDs refer to `docs/context/requirements.md`.

## 1. Layout (one Go module `aibuddy` at the repo root)
```
go.mod, vendor/                      third-party code is vendored (no network needed to build)
contracts/embed.go                   package contracts: Schemas embed.FS (schemas/*.json)
migrations/embed.go                  package migrations: FS embed.FS (*.sql, lexical order)
services/cmd/core                    binary core (port 8080)
services/cmd/mcp-mocks               binary mcp-mocks (port 8100), hosts /mcp/<server> for six servers
services/internal/platform/...       shared libraries (agent P)
services/internal/mocks/<server>     absence, approvals, access (agent M1); directory, checklist, kb (agent M2)
services/internal/mocks/app          wiring of mcp-mocks (integration, orchestrator)
services/internal/core/<pkg>         chat, agentclient, actions, approvals, mcpclient (agent C1);
                                     authapi, plan, escalations, notify, analytics, flags, cron, ratelimit (agent C2)
services/internal/core/app           wiring of core (integration, orchestrator)
services/Dockerfile                  one image recipe, ARG CMD=core|mcp-mocks (agent I)
```
Agents edit ONLY their packages (plus tests). Never edit `app/` packages, `go.mod`, `vendor/`, `contracts/`, `migrations/`.
If you need a contract or schema change, stop and report it.

## 2. Rules for all Go code
- Go 1.24, standard layout, `gofmt`, `go vet` clean. Only dependencies already in go.mod:
  pgx/v5 (pgxpool), modelcontextprotocol/go-sdk (mcp), redis/go-redis/v9, alicebob/miniredis/v2 (tests only),
  twmb/franz-go (kgo), gopkg.in/yaml.v3. Everything else = standard library. Build offline: `go build -mod=vendor ./...`.
- `context.Context` is the first parameter; wrap errors with `fmt.Errorf("doing x: %w", err)`; log with `log/slog`.
- User-facing messages (error `message`, notification titles, summaries) are in Russian, neutral, no emojis.
- SQL by hand with pgx, parameters always bound (`$1`), never string-concatenated. One transaction per write use case.
- Identity ALWAYS comes from verified JWT claims (`sub`, `role`, `team_id`), never from request bodies or tool args (R71).
- Errors returned to clients never reveal whether a foreign resource exists (`forbidden`, not `not_found`, for other users' data).
- Tests: table-driven, names carry IDs (`TestCreateRequest_S3_Idempotent_R79`). DB tests use `pgtest` (below) and
  real migrations, never mocks of SQL. Each agent's packages ≥ 80 % statement coverage. No `t.Skip` in your own packages
  except the pgtest skip when `TEST_DATABASE_URL` is unset.
- Keep tool output short when you run commands (`-count=1 ./... 2>&1 | tail -40`).

## 3. Test database
`TEST_DATABASE_URL` points to a Postgres 16 superuser connection (default for local runs:
`postgres://buddy:buddy@127.0.0.1:55432/postgres?sslmode=disable`, started by `scripts/test-db.sh up`).
`pgtest.New(t)` creates a fresh database `t_<random>`, applies `migrations.FS` in order (schema + demo seed),
returns a `*pgxpool.Pool`, drops the database on cleanup. Tests may rely on the demo seed (people, plans, resources)
from `scripts/gen_seed.py`; insert anything else they need.

## 4. Platform packages (agent P) — `services/internal/platform/`
| Package | Responsibility |
|---|---|
| `canonjson` | Canonical JSON of any value: object keys sorted, no insignificant whitespace, UTF-8, numbers preserved as given; `SHA256Hex(v)` |
| `authn` | HS256 JWT (stdlib only): `Issue(secret, Claims, ttl)`, `Verify(secret, token, now)`; Claims{Sub, Role, TeamID, CohortStart, Exp, Iat}; strict header (`alg` must be HS256, `typ` JWT), constant-time compare, exp/iat checks; `Middleware` for net/http putting Claims into ctx; `FromContext` |
| `confirm` | Confirmation tokens exactly as `contracts/mcp-tools.md`: `Issue(secret, sub, key, argsHash, ttl)`, `Verify(secret, token, sub, key, args, now)`; `ArgsHash(args)` = canonjson SHA-256 of args without `idempotency_key`/`confirmation_token` |
| `apierr` | Error type {Code, Message(ru), Retryable, HTTPStatus}; codes from openapi `Error.code` and mcp-tools errors; `WriteJSON(w, err)` |
| `httpx` | JSON read/write helpers with size limits, request-id + recover + access-log middleware, CORS (allow-list), SSE writer (`event:`/`id:`/`data:` lines, flush, `: ping` keep-alive every 15 s, ctx cancel) and SSE reader (parse a stream into events) |
| `period` | `Current(cohortStart, today)` → week_1 (<7 days), week_2 (<14), month_1 (<30), month_2 (<90), after_3_months; `DueOffsetDays(period)` 7/14/30/60/100; labels «1 неделя»… |
| `pg` | `Open(ctx, url)` pool with sane limits and retry on startup (Postgres may still be starting) |
| `pgtest` | see §3 |
| `mcpx` | Server side: build a go-sdk `mcp.Server` whose tools come from `contracts.Schemas` (name, description, inputSchema, outputSchema) and whose handlers are plain Go funcs `func(ctx, Caller, json.RawMessage) (any, error)`; `Caller` = verified JWT claims of the HTTP request; role allow-list from the schema's `roles` enforced before the handler; `apierr` errors become MCP tool results with `isError: true` and structured content `{code, message, retryable}`; write tools: confirmation token verified by mcpx before the handler (sub, key = `idempotency_key`, args hash). Streamable HTTP handler in stateless JSON mode mounted at `/mcp/<server>`. Client side: `Client.Call(ctx, userToken, server, tool, args) (json.RawMessage, *apierr.Error, error)` used by core (approvals, kb.add_faq) and by tests |

## 5. Mock MCP servers (agents M1, M2) — behaviour per tool (schemas in `contracts/schemas/`)
Each server package exposes `Register(srv *mcpx.Server, pool *pgxpool.Pool, opts...)` (exact shape set by agent P) and owns its schema.
- **absence** (M1). `get_policy`: KB document `absence-policy-<team_id>` else `absence-policy` from `kb.documents` (read-only use of kb schema allowed). `list_my_requests`, `get_request` (own only → else `forbidden`). `validate_request`: start ≥ tomorrow, end ≥ start, weekdays only, ≤ 3 consecutive days, no overlap with own `pending_approval`/`approved` requests; returns problems in Russian + approver (employee's `lead_id`). `create_request`: re-validates; inserts `absence.requests` (status `pending_approval`, approver = lead) and `approvals.items` (kind `absence_request`) in one tx; idempotent by key (`replayed: true`).
- **approvals** (M1). `list_pending`: items where approver = caller and status pending. `decide`: only own items (else `forbidden`), only pending (else `conflict`); updates the item and the underlying `absence.requests.status` (approved/rejected) or `access.requests.status` (granted/rejected); idempotent by key.
- **access** (M1). `list_required`: resources of kind tool/space with `team_id IS NULL OR = caller team`, each with the caller's status (not_requested/pending_approval/granted/rejected). `list_my_access`: resources the caller requested or holds. `list_team_channels`: channel resources of the team plus `team_id IS NULL`, names/purpose/closed only (closed = elevated). `request_access`: resource must be visible to the caller's team (else `forbidden`); non-elevated → `granted` immediately; elevated → `pending_approval` + `approvals.items` for the caller's lead; result carries `checklist_item_id`; idempotent.
- **directory** (M2). `whoami` (current_period via `period`), `get_my_contacts`: own mentor, lead, HR BP, curator with `channel` and `ask_about` (fixed Russian lists per role, e.g. lead → «отгул и отсутствия», «цели стажировки»; curator → «методичка наставника», «экватор и выпускной», «документы для вуза»; hrbp → «кадровые вопросы»).
- **checklist** (M2). `get_plan_template(role)`: periods + items where `role IN (<role>, 'both')`, ordered by position.
- **kb** (M2). Loader: on start read `KB_DIR/*.md` (YAML frontmatter: doc_id, title, source_type: test, acl {roles, teams}, checklist_item_id?, url?) and upsert into `kb.documents`; refuse docs whose source_type is not `test`. `search`: `websearch_to_tsquery('russian', q)`, ACL filter (`acl_roles @> [role]` and (`'*' = ANY(acl_teams)` or team in acl_teams)), private notes only for `owner_id = caller`, `ts_headline` snippet, rank, k ≤ 10. `add_faq` (mentor): doc_id `faq-<team>-<short id>`, kind faq, acl roles {intern, mentor, lead}, teams {caller team}, idempotent.
  Ship sample KB docs needed by S3/S4 so the stack works before agent A adds content: `data/kb/absence-policy.md`, `data/kb/absence-policy-delivery.md`, `data/kb/access-guide.md`, `data/kb/team-channels-delivery.md` (all `source_type: test`, Russian, clearly synthetic).

## 6. Core (agents C1, C2) — behaviour per endpoint (shapes in `contracts/openapi.yaml`)
Common: all `/api/*` except login require JWT (`authn.Middleware`); CORS from `CORS_ORIGINS`; errors via `apierr`.
**C1 (chat & actions)**
- `agentclient`: `Turn(ctx, userToken, TurnRequest) (<-chan Event, error)` — POST `AGENT_URL/v1/turn`, SSE parsed with `httpx`; timeout `LLM_TIMEOUT_S`+10 s; connection error → typed error.
- `mcpclient`: thin adapter over `mcpx.Client` with base URL `MCP_BASE_URL` (default `http://mcp-mocks:8100/mcp`).
- `chat`: POST sessions; GET session (own only); POST messages → save user message, build `user_context` (role, team, current_period, open tasks of own plan) and last 10 messages as history, call agent, relay events unchanged to the client SSE (`httpx` writer), persist: `action_proposal` → `core.proposals` (id = proposal_id, draft, draft_hash = canonjson hash of `draft.args`, expires 30 min); `token`s → assistant message text; `sources` → message sources; finally emit `done{message_id}`. Agent unreachable → `error{upstream_unavailable, retryable:true, "Ассистент временно недоступен, попробуйте ещё раз"}` then close. Rate limit: `ratelimit` (C2) per user `CHAT_RATE_PER_MIN`.
- Confirm: proposal must belong to caller+session; `executed` → replay stored `action_result` + `done` (no agent call); `cancelled`/`expired` → 409 JSON before streaming; `pending`/`failed` → status `confirmed`, issue `confirm` token (sub = caller, key = proposal_id, args hash = draft_hash, ttl 5 min), call agent with `turn.kind=confirm`; on internal `tool_result` (never relayed): success → `actions.Save` then emit `action_result`; tool error → proposal `failed`, emit `error` (retryable per tool). Cancel → `cancelled`, 204.
- `actions`: `Save` in one tx: insert `core.actions` (idempotency_key = proposal id), link a plan task: access → top-level task of the caller's intern plan with `checklist_item_id` from the result (status `waiting_approval` or `done` when granted); absence → new task `source='action'` in the current period, title «Отгул <даты>», status `waiting_approval`; set `linked_action_id`; emit analytics `action.created`. `List` own actions newest first. `ApplyDecision(external_id, status)` updates action + linked task (approved/granted → done; rejected → not_started) + notification `status_change` for the owner.
- `approvals`: GET → `approvals.list_pending` via mcpclient with the lead's token; POST decision → requires `Idempotency-Key` (uuid), issues confirm token for args `{approval_id, decision[, comment]}`, calls `approvals.decide`, then `actions.ApplyDecision`, analytics `action.status_changed`.
**C2 (rest of the API)**
- `authapi`: login via `SELECT … FROM core.users WHERE id=$1 AND can_login AND password_hash = crypt($2, password_hash)`; JWT ttl `JWT_TTL` (12 h); generic 401 message; `GET /api/me` with flags for the team.
- `plan`: GET plan per openapi rules (intern own; mentor own mentor plan, `intern_id` selects; `user_id` = intern of the caller's team for mentor/lead; else 403); periods with tasks and nested subtasks; status `overdue` reported when due_at < today and not done; `current_period` via `period`; `next_step`: first non-done top-level task in current or earlier periods, priority overdue > in_progress > not_started, human_only last; action by item: CL-05/CL-01 → request_access, CL-04 → draft_message, CL-07/CL-08/CL-09/CL-10 → ask_buddy (+ Russian prompt), source=action → skipped, human_only → mark_done. PATCH task: own plan only, status in {not_started,in_progress,done}; all subtasks done → parent done; analytics `plan.task_done` with on_time. PUT goals: mentor/lead of the intern's team, replace goals, analytics `plan.goals_set`. GET team/interns: mentor → interns of own mentor plans; lead → interns of own team; counts done/total/overdue + pending access requests (count of the intern's actions of kind access_request with status pending_approval).
- `escalations`: POST (intern) → target: mentor = owner of the mentor plan for the sender; lead = lead of the sender's team; hrbp/curator = first user with that role; GET (own sent + addressed to caller); answer (addressee only) → notification `escalation_answer` to sender; `add_to_kb` true and flag `escalation_to_kb` on → `kb.add_faq` via mcpclient with a core-issued confirm token (key = escalation id).
- `notify`: list own (newest first, `read` flag), mark read; `Create(ctx, tx, n)` with optional dedupe_key (ON CONFLICT DO NOTHING).
- `analytics`: `Emit(ctx, q, Event)` inserts `core.analytics_events` and, when Kafka is enabled, `core.outbox` in the same tx; POST `/api/events` (clickstream, names `ui.*`, ≤ 100) and `/api/feedback`; `Publisher` goroutine (kgo) drains outbox to topics when `KAFKA_BROKERS` is set; props whitelist — never message text.
- `flags`: runtime flags from `core.feature_flags` with Redis cache (30 s, fail-open to DB); per-team targeting; PUT only when `buildflags.Demo` and role lead; analytics `flag.changed`.
- `cron`: every `CRON_INTERVAL` (default 24 h) and via POST `/api/cron/run` (demo build only): for every plan mark overdue tasks (`status='overdue'`), create notifications with dedupe keys `overdue:<task_id>` and `period:<checklist_item_id>:<period>` when an item's period becomes current (both roles), plus survey reminders CL-15/CL-16; texts are Russian templates (no LLM needed).
- `ratelimit`: Redis fixed window per user and route; fail-open when Redis is down.

## 7. Environment
| Variable | Service | Default |
|---|---|---|
| `CORE_PORT` / `MCP_MOCKS_PORT` | core / mocks | 8080 / 8100 |
| `POSTGRES_URL` | both | `postgres://buddy:buddy@postgres:5432/buddy?sslmode=disable` |
| `REDIS_URL` | core | `redis://redis:6379/0` |
| `JWT_SECRET`, `CONFIRMATION_HMAC_SECRET` | both | dev-only defaults in compose; required, service refuses to start if empty |
| `AGENT_URL` | core | `http://agent:8090` |
| `MCP_BASE_URL` | core | `http://mcp-mocks:8100/mcp` |
| `KAFKA_BROKERS` | core | empty = Kafka off |
| `CORS_ORIGINS` | core | `http://localhost:5173` |
| `LLM_TIMEOUT_S`, `CRON_INTERVAL`, `CHAT_RATE_PER_MIN`, `JWT_TTL` | core | 30, 24h, 20, 12h |
| `KB_DIR` | mocks | `/app/data/kb` |

## 8. Definition of done for an agent
`gofmt -l services` empty, `go vet -mod=vendor ./services/...` clean, `go test -mod=vendor -count=1` green for your packages with
`TEST_DATABASE_URL` set, your packages ≥ 80 % coverage, every public func has a doc comment, report back in ≤ 15 lines
(files, coverage per package, deviations from this brief, open questions). Do not paste code into the report.
