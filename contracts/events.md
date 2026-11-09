# Analytics events contract

Owner: **B**. Producers: core (domain events), web via `POST /api/events` (clickstream, names start with `ui.`).
Core always inserts events into `core.analytics_events` (Grafana reads them in both profiles, R24).
When `KAFKA_BROKERS` is set (profile `full`), core also writes each event to `core.outbox` in the same transaction and a
publisher goroutine sends it to Kafka topics `buddy.domain` / `buddy.clickstream` — the hand-off to Avito's Clickstream (R62).

## Envelope
```json
{"event_id": "uuid", "name": "action.created", "ts": "2026-11-09T13:00:00Z",
 "user_id": "intern.anna", "role": "intern", "team_id": "delivery", "props": {}}
```
`props` never contains message text, names of other people or any sensitive data (R72); only ids, enums, counts, durations.

## Domain events
| name | props | Metric (docs/context/product.md) |
|---|---|---|
| `session.started` | `{"period"}` | Use after graduation (role `alumni`) |
| `chat.message_sent` | `{"session_id"}` | Volume |
| `chat.answered` | `{"session_id","message_id","source_count","latency_ms","provider":"stub"\|"openai_compat"}` | Share answered without escalation, latency |
| `chat.refused` | `{"reason":"no_rights"\|"out_of_scope"}` | Correct refusals |
| `chat.escalated` | `{"to_role"}` | Routine questions to mentor per intern per week |
| `chat.feedback` | `{"message_id","useful"}` | Share «полезно» |
| `escalation.answered` | `{"added_to_kb"}` | KB growth |
| `action.proposed` | `{"kind","proposal_id"}` | Time from intent to request |
| `action.created` | `{"kind","action_id","proposal_id","duration_ms_since_proposal","attempt"}` | Success on first attempt |
| `action.failed` | `{"kind","code","retryable"}` | Failure rate |
| `action.status_changed` | `{"kind","action_id","status"}` | Approval time |
| `plan.task_done` | `{"checklist_item_id","period","on_time"}` | Checklist items on time; time to first independent task |
| `plan.task_overdue` | `{"checklist_item_id","period"}` | Overdue items |
| `plan.goals_set` | `{"count","by_role"}` | — |
| `notification.created` | `{"kind","checklist_item_id"}` | AI-cron reminders |
| `flag.changed` | `{"key","enabled","teams_count"}` | Pilot audit |

## Clickstream events (web)
| name | props |
|---|---|
| `ui.page_view` | `{"page":"plan"\|"chat"\|"mentor"\|"approvals"}` |
| `ui.next_step_clicked` | `{"action"}` |
| `ui.task_toggle` | `{"checklist_item_id","to_status","latency_ms"}` (R76: p95 < 1000) |
| `ui.retry_clicked` | `{"code"}` |
| `ui.source_opened` | `{"doc_id"}` |
