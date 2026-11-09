# Agent stream contract

Owner: **A**. Status: **draft by B** (derived from docs/context/architecture.md so core and web can start). A confirms or changes it via PR.

Two hops use the same events:
1. agent → core: `POST {AGENT_URL}/v1/turn` answers `text/event-stream`.
2. core → web: `POST /api/chat/sessions/{id}/messages` and `.../proposals/{pid}/confirm` relay the events unchanged
   (core adds nothing to `data`, but persists messages, proposals and actions on the way).

## Wire format
Standard SSE. One JSON object per `data:` line. Every event has an incrementing `id:` so a client can detect gaps.
```
id: 3
event: status
data: {"stage":"searching_kb"}

```
Keep-alive: core sends a comment line `: ping` every 15 s. A stream always ends with `done` or `error` (then the connection closes).

## Events
| event | data | Who emits | Web behaviour |
|---|---|---|---|
| `status` | `{"stage": "thinking" \| "searching_kb" \| "calling_service" \| "saving"}` | agent | Waiting text (R78): «Думаю…», «Ищу в базе знаний…», «Отправляю заявку…», «Сохраняю…» |
| `token` | `{"text": "..."}` | agent | Append to the current answer |
| `sources` | `{"items": [{"doc_id","title","source_type":"test","url"}]}` | agent | Source chips + badge «Тестовая инструкция прототипа» (R69) |
| `action_proposal` | `{"proposal_id","kind":"absence_request"\|"access_request","draft":{...},"summary"}` | agent; core persists to `core.proposals` | Draft card with «Отправить» / «Отмена» |
| `action_result` | `{"action_id","kind","external_id","status","summary","approver_display_name","task_id"}` | core (after the MCP write succeeds and `core.actions` is saved) | Result card; invalidate `plan` and `actions` queries (R66) |
| `escalation_draft` | `{"to_role":"mentor"\|"lead"\|"curator"\|"hrbp","text"}` | agent | Editable draft + «Отправить» → `POST /api/escalations` |
| `refusal` | `{"reason":"no_rights"\|"out_of_scope","redirect_role":"hrbp"\|"lead"\|"mentor"\|"curator"\|null}` | agent | Polite refusal block (R71, R72) |
| `error` | `{"code","message","retryable"}` | agent or core | Message + «Повторить» if retryable (R79) |
| `done` | `{"message_id"}` | core | Unlock input |

Draft shape: `draft = {"tool": "<server>.<tool>", "args": {...}, "display": {...}}`.
- `args` are EXACTLY the write tool's arguments without `idempotency_key` and `confirmation_token`. Core stores
  `draft_hash = sha256(canonical_json(args))` (keys sorted, no spaces, UTF-8) and signs that hash; the MCP server recomputes it
  from the arguments it receives. Nothing outside `args` is covered by the signature, so `display` never reaches the tool.
- `display` is for the card only (approver name, elevated flag, human-readable dates).
- `absence_request`: `{"tool":"absence.create_request","args":{"type":"day_off","start_date":"2026-11-14","end_date":"2026-11-14","comment":""},"display":{"approver_display_name":"Мария Лебедева","dates":"14.11.2026"}}`
- `access_request`: `{"tool":"access.request_access","args":{"resource_id":"space-delivery","reason":""},"display":{"resource_name":"Пространство кластера «Доставка»","elevated":false}}`

## core → agent request (`POST /v1/turn`)
```json
{
  "session_id": "uuid",
  "turn": {"kind": "message", "text": "хочу отгул в пятницу"},
  "user_context": {
    "user_id": "intern.anna", "role": "intern", "team_id": "delivery",
    "current_period": "week_2", "open_tasks": [{"checklist_item_id": "CL-07", "title": "..."}]
  },
  "history": [{"author": "user", "text": "..."}, {"author": "assistant", "text": "..."}]
}
```
Confirmation turn: `"turn": {"kind": "confirm", "proposal_id": "uuid", "draft": {...}, "confirmation_token": "..."}`.
Headers: `Authorization: Bearer <user JWT>` (the agent forwards it to MCP servers unchanged), `X-Request-Id`.

## Rules both sides rely on
- The agent never calls a write tool in a `message` turn; it emits `action_proposal` and stops (R71).
- In a `confirm` turn the agent calls `draft.tool` with `draft.args` unchanged, `idempotency_key = proposal_id` and the given `confirmation_token`; on success it returns the MCP result in an internal event `tool_result`, which core turns into `action_result` after saving. `tool_result` is never relayed to web.
- Confirming an already executed proposal does not call the agent: core replays the stored `action_result` and `done` (R79). A cancelled or expired proposal answers 409.
- `action_result.status` values: `pending_approval`, `approved`, `rejected`, `granted`, `created`, `failed` (same enum as `Action.status` in openapi.yaml).
- Every action is linked to a plan task: S4 actions to the checklist task of the resource (`access.resources.checklist_item_id`), S3 actions to a task core creates in the current period with `source = "action"` (the checklist has no absence item).
- Timeout: core cancels the agent call after `LLM_TIMEOUT_S` + 10 s and emits `error{"code":"upstream_unavailable","retryable":true}`.
- The agent receives only ACL-filtered documents and only the tools allowed for `user_context.role`.
