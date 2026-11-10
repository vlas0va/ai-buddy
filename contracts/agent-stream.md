# Agent stream contract

Owner: **A**. Status: **v1 — confirmed by A** (B's draft accepted as is; A added only optional fields, one optional event
`next_step`, the `approval_decision` proposal kind for leads, and auxiliary endpoints). Everything B and C already implement
against the draft keeps working: new fields are additive, unknown fields must be ignored by Zod (`.passthrough()` or default strip).

Two hops use the same events:
1. agent → core: `POST {AGENT_URL}/v1/turn` answers `text/event-stream` (`AGENT_URL=http://agent:8090` in compose).
2. core → web: `POST /api/chat/sessions/{id}/messages` and `.../proposals/{pid}/confirm` relay the events unchanged
   (core adds nothing to `data`, but persists messages, proposals and actions on the way).

## Wire format
Standard SSE. One JSON object per `data:` line. Every event has an incrementing `id:` so a client can detect gaps.
```
id: 3
event: status
data: {"stage":"searching_kb","text":"Ищу в базе знаний"}

```
Keep-alive: core sends a comment line `: ping` every 15 s. A stream always ends with `done` or `error` (then the connection
closes). The agent emits its first `status` within ~100 ms, so the UI never waits silently (R77, R78).

## Events
| event | data (required → optional) | Who emits | Web behaviour |
|---|---|---|---|
| `status` | `{"stage": "thinking" \| "searching_kb" \| "calling_service" \| "saving"}` → `text` (Russian label) | agent | Waiting text (R78) |
| `token` | `{"text": "..."}` | agent | Append to the current answer |
| `sources` | `{"items": [{"doc_id","title","source_type":"test","url"}]}` → `items[].badge` = «Тестовая инструкция прототипа» | agent | Source chips + badge (R69) |
| `action_proposal` | `{"proposal_id","kind","draft":{...},"summary"}` → `title`, `requires_approval`, `task_checklist_item_id`, `expires_at`, `args_hash` | agent; core persists to `core.proposals` | Draft card with «Отправить» / «Отмена» |
| `action_result` | `{"action_id","kind","external_id","status","summary","approver_display_name","task_id"}` | core (after the MCP write succeeds and `core.actions` is saved) | Result card; invalidate `plan` and `actions` queries (R66) |
| `escalation_draft` | `{"to_role":"mentor"\|"lead"\|"curator"\|"hrbp","text"}` → `to_user_id`, `to_name`, `topic` | agent | Editable draft + «Отправить» → `POST /api/escalations` |
| `next_step` *(new, optional)* | `{"checklist_item_id","title","period","status","human_only","cta":{"kind":"ask"\|"access"\|"absence"\|"escalate","label","message"}}` → `task_id` | agent | «Следующий шаг» card in chat; the button sends `cta.message` as a new message (S1) |
| `refusal` | `{"reason":"no_rights"\|"out_of_scope","redirect_role":"hrbp"\|"lead"\|"mentor"\|"curator"\|null}` → `detail`, `redirect_name`, `text` | agent | Polite refusal block (R71, R72) |
| `tool_result` | internal, see «Confirmation turn» | agent → core only | never relayed to web |
| `error` | `{"code","message","retryable"}` | agent or core | Message + «Повторить» if retryable (R79) |
| `done` | `{"message_id"}` → `intent`, `provider`, `latency_ms`, `tool_calls`, `escalated`, `confidence`, `prompt_version`, `request_id` | agent; core may enrich `message_id` | Unlock input; core writes `chat.answered` / `chat.escalated` / `chat.refused` from these fields |

`refusal.detail` (analytics and eval only): `no_rights`, `prompt_injection`, `sensitive_data`, `hr`, `trip`, `benefit`,
`other_team_resource`, `not_my_mentee`. `reason` stays within the two-value enum.

### Draft shape
`draft = {"tool": "<server>.<tool>", "args": {...}, "display": {...}}`.
- `args` are EXACTLY the write tool's arguments without `idempotency_key` and `confirmation_token`. Core stores
  `draft_hash = sha256(canonical_json(args))` (keys sorted, no spaces, UTF-8) and signs that hash; the MCP server recomputes it
  from the arguments it receives. Nothing outside `args` is covered by the signature, so `display` never reaches the tool.
- `proposal_id` is a UUID: it becomes the `idempotency_key` of the write (`format: uuid` in the tool schemas).
- `absence_request`: `{"tool":"absence.create_request","args":{"type":"day_off","start_date":"2026-11-13","end_date":"2026-11-13","comment":""},"display":{"approver_display_name":"Мария Лебедева","dates":"13.11.2026","period_label":"13 ноября (пт)","working_days":1}}`
- `access_request`: `{"tool":"access.request_access","args":{"resource_id":"space-delivery","reason":"..."},"display":{"resource_name":"Пространство кластера «Доставка»","elevated":false,"approver_display_name":""}}`
- `approval_decision` *(lead in chat, optional for core)*: `{"tool":"approvals.decide","args":{"approval_id":"APR-…","decision":"approved"|"rejected","comment":""},"display":{"ref_id":"ABS-000101","requester_display_name":"Анна Котова","decision_label":"Согласовать"}}`.
  Core handles it like the other kinds (sign `args`, resume the agent); if core does not support it yet, the lead decides with
  the buttons of `POST /api/approvals/{id}/decision`.

## core → agent request (`POST /v1/turn`)
```json
{
  "session_id": "uuid",
  "message_id": "uuid",
  "turn": {"kind": "message", "text": "хочу отгул в пятницу"},
  "user_context": {
    "user_id": "intern.anna", "role": "intern", "team_id": "delivery",
    "current_period": "week_2", "open_tasks": [{"checklist_item_id": "CL-07", "title": "...", "status": "in_progress", "due_at": "2026-11-16"}],
    "display_name": "Анна Котова",
    "mentees": [{"user_id": "intern.anna", "display_name": "Анна Котова", "accesses": [{"name": "Jira", "status": "not_requested"}], "open_tasks": []}]
  },
  "history": [{"author": "user", "text": "..."}, {"author": "assistant", "text": "..."}],
  "flags": {"scenario_s3": true}
}
```
Optional, all additive: `message_id`; `user_context.display_name`; `open_tasks[].status|due_at|period|human_only|task_id`;
`user_context.plan.tasks` (full plan, same item shape — preferred when available, gives «готово N из M»);
`user_context.location` (office city: «Москва», «Санкт-Петербург» or «Удалённо» — picks the matching KB section); `user_context.mentees` (mentor and lead: what core returns from `GET /api/team/interns`); `flags` (runtime feature flags of the
user's team, R55 — a disabled scenario answers «пилот выключен»); `today` (ISO date, honoured only with
`AGENT_ALLOW_CLOCK_OVERRIDE=true`, for demo and e2e).
Headers: `Authorization: Bearer <user JWT>` (the agent verifies it with `JWT_SECRET` and forwards it to MCP servers unchanged),
`X-Request-Id` (echoed back and put into `done.request_id`), optional `X-Service-Token` (`AGENT_SERVICE_TOKEN`).
The agent takes `user_id`, `role`, `team_id` from JWT claims; a `user_context` that disagrees → `403 context_mismatch`.

### Confirmation turn
`"turn": {"kind": "confirm", "proposal_id": "uuid", "draft": {...}, "confirmation_token": "..."}`.
The agent re-checks the token (signature, `exp`, `sub`, `key = proposal_id`, `args_hash`), checks that the tool is a write tool
of the user's role and that the proposal was issued by the agent to this user (proposal registry, 30 min), then calls
`draft.tool` with `draft.args` unchanged, `idempotency_key = proposal_id`, `confirmation_token`. Stream:
`status{calling_service}` → `status{saving}` → `tool_result` → `done`, or `error`.
```json
{"proposal_id": "uuid", "kind": "absence_request", "tool": "absence.create_request",
 "external_id": "ABS-000101", "status": "pending_approval", "status_label": "На согласовании",
 "approver_display_name": "Мария Лебедева", "summary": "Отгул 13 ноября (пт)", "task_checklist_item_id": null,
 "followup": "Заявка отправлена на согласование…", "replayed": false, "result": { /* raw MCP output */ }}
```
Core turns it into `action_result` after saving (it may show `followup` as a token after the card).

## Rules both sides rely on
- The agent never calls a write tool in a `message` turn; it emits `action_proposal` and stops (R71). The LLM is never offered
  write tools at all (ADR-003).
- In a `confirm` turn the agent calls `draft.tool` with `draft.args` unchanged, `idempotency_key = proposal_id` and the given
  `confirmation_token`; on success it returns the MCP result in the internal event `tool_result`. `tool_result` is never relayed to web.
- Confirming an already executed proposal does not call the agent: core replays the stored `action_result` and `done` (R79).
  If it does reach the agent, the agent answers from its result cache / the MCP idempotency with `replayed: true`. A cancelled or
  expired proposal answers 409 (core).
- `action_result.status` values: `pending_approval`, `approved`, `rejected`, `granted`, `created`, `failed` (same enum as
  `Action.status` in openapi.yaml).
- Every action is linked to a plan task: S4 actions to the checklist task of the resource (`task_checklist_item_id`, from
  `access.resources.checklist_item_id`), S3 actions to a task core creates in the current period with `source = "action"`.
- Timeout: core cancels the agent call after `LLM_TIMEOUT_S` + 10 s and emits `error{"code":"upstream_unavailable","retryable":true}`.
- The agent receives only ACL-filtered documents and only the tools allowed for the user's role; it re-checks the ACL of
  `kb.search` results against its own copy of `data/kb` before anything reaches the LLM (defense in depth).

## Auxiliary agent endpoints (all require the user JWT)
| Method | Path | Caller | Purpose |
|---|---|---|---|
| `POST` | `/v1/reminders` | core AI-cron | `{"user_context","items":[{"task_id","checklist_item_id","title","human_only","reason":"overdue"\|"period_start"}]}` → `{"reminders":[{"task_id","checklist_item_id","text","kind"}]}` (R54; human-only items: «remind and help prepare», R05) |
| `POST` | `/v1/summary/progress` | core | `{"user_context","actions":[...]}` → `{"text","bullets","sources"}` for the equator presentation (CL-11) |
| `POST` | `/v1/kb/faq-draft` | core, `POST /api/escalations/{id}/answer` with add-to-KB | `{"question","answer","team_id"}` (mentor/lead, own team) → `{"doc_id","markdown"}` with `source_type: test`, `origin: escalation` (R10) |
| `GET` | `/v1/kb/{doc_id}` | web via core | ACL-checked document body with the test badge |
| `GET` | `/v1/agent/config` | debug panel | prompt version, provider, read tools of the role, MCP mode per server |
| `GET` | `/healthz`, `/readyz`, `/metrics` | compose, Grafana | liveness, readiness (KB, intent model), Prometheus text metrics |

## Error codes emitted by the agent
| code | retryable | meaning |
|---|---|---|
| `unauthorized` | no | missing / invalid / expired JWT or service token (HTTP 401) |
| `context_mismatch` | no | `user_context` disagrees with JWT claims (HTTP 403) |
| `bad_request`, `bad_proposal` | no | malformed body or draft (HTTP 400) |
| `confirmation_invalid` | no | bad / expired / foreign confirmation token, or a draft changed after it was proposed |
| `no_rights` | no | tool not allowed for the role, or MCP answered `forbidden` |
| `validation_failed`, `conflict` | no | MCP rejected the request (overlap, already decided…) |
| `service_timeout`, `service_unavailable` | yes | MCP server problem; repeating the same confirm is safe (idempotency) |
| `llm_timeout`, `llm_unavailable` | yes | LLM problem when `LLM_FALLBACK_TO_STUB=false` (otherwise the stub answers) |
| `internal` | yes | unexpected error, logged with the request id |
