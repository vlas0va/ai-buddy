# MCP tools contract

Owner: **B**. Machine-readable definitions: `contracts/schemas/<server>.json` (one file per server, array of tools with
`inputSchema` / `outputSchema` in JSON Schema 2020-12). Servers are mock Avito systems (R39, R52, R56); all data is synthetic (R68).

## Transport and auth (every server)
- MCP over streamable HTTP (stateless mode, JSON responses). All six servers run in one binary `mcp-mocks` (port 8100):
  `http://mcp-mocks:8100/mcp/<server>`, e.g. `/mcp/absence`. Each is an independent MCP server with its own tool set and
  its own DB schema; moving one to its own process or replacing it with a real Avito API is a URL change in `agent.yaml` (R23).
- The agent forwards the user's JWT in `Authorization: Bearer <token>` on every MCP request. The server validates the JWT
  itself (same `JWT_SECRET`) and derives `user_id`, `role`, `team_id` from claims — never from tool arguments (R71).
- Write tools additionally require `confirmation_token` and `idempotency_key`. Core issues the token when the user confirms:
  in chat (`.../proposals/{id}/confirm`, `idempotency_key = proposal_id`) or by pressing a decision button
  (`POST /api/approvals/{id}/decision`, `idempotency_key` = the request's `Idempotency-Key` header).
- Token format: `base64url(json{"sub","key","args_hash","exp"}) + "." + base64url(HMAC-SHA256(CONFIRMATION_HMAC_SECRET, first part))`,
  where `args_hash = sha256(canonical_json(args))` over the tool arguments WITHOUT `confirmation_token` and `idempotency_key`.
  The server checks: signature, `exp` not passed, `sub` = JWT subject, `key` = `idempotency_key`, `args_hash` = hash of the
  arguments it actually received.
- Exact encoding (reference: `services/internal/platform/confirm`, golden vectors in `platform/canonjson/testdata/vectors.json`):
  base64url WITHOUT padding for both parts; payload JSON with keys in order `args_hash, exp, key, sub`, no whitespace;
  `exp` = Unix seconds (integer); `args_hash` = lowercase hex; the MAC is computed over the ASCII text of the first part.
- Canonical JSON = Python `json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")`:
  keys sorted by code point, integers as is. Signed arguments must not contain floating-point numbers (Python and Go
  print floats differently); no write tool has a number field.
- A repeated call with the same `idempotency_key` returns the stored result with `"replayed": true` and writes nothing (R79).

## Errors
Tool errors are returned as MCP tool results with `isError: true` and structured content:
`{"code": "forbidden" | "not_found" | "validation_error" | "conflict" | "invalid_confirmation" | "unavailable", "message": "<Russian, user-facing>", "retryable": bool}`.
`forbidden` never reveals whether the resource exists.

## Servers and tools
| Server (path) | Tool | Roles | Write | Scenario |
|---|---|---|---|---|
| absence (`/mcp/absence`) | `get_policy` | intern, mentor, lead | no | S3 step 1 |
| absence | `list_my_requests` | intern, mentor, lead | no | S3 |
| absence | `validate_request` | intern, mentor, lead | no | S3 step 2 |
| absence | `create_request` | intern, mentor, lead | **yes** | S3 steps 5–7 |
| absence | `get_request` | intern, mentor, lead (own only) | no | S3 step 8 |
| absence | `reschedule_request` | intern, mentor, lead (own only) | **yes** | перенос до согласования HR-администратором |
| approvals (`/mcp/approvals`) | `list_pending` | lead, hr_admin | no | S3 step 8, S4 |
| approvals | `decide` | lead (own team only), hr_admin (second stage) | **yes** | S3 step 8, S4 |
| directory (`/mcp/directory`) | `whoami` | all | no | S2 |
| directory | `get_my_contacts` | all | no | S2 «к кому идти», CL-02, CL-03 |
| checklist (`/mcp/checklist`) | `get_plan_template` | all | no | S1 |
| access (`/mcp/access`) | `list_required` | intern, mentor | no | S4 step 1 |
| access | `list_my_access` | intern, mentor | no | S4 |
| access | `list_team_channels` | intern, mentor | no | S4 step 4, CL-04 |
| access | `request_access` | intern, mentor | **yes** | S4 steps 2–3 |
| kb (`/mcp/kb`) | `search` | all | no | S2 |
| trips (`/mcp/trips`) | `get_policy` | intern, mentor, lead | no | командировка: порядок, офисы |
| trips | `list_my_requests` | intern, mentor, lead | no | командировка: статус |
| trips | `validate_request` | intern, mentor, lead | no | командировка: проверка города и дат |
| trips | `create_request` | intern, mentor, lead | **yes** | командировка → согласование руководителя |
| consents (`/mcp/consents`) | `list` | intern, mentor, lead, hrbp, alumni | no | согласия и их статусы |
| consents | `sign` | intern, mentor, lead, hrbp, alumni | **yes** | добровольные согласия (фото, опросы) |

Access request (checkpoint 11.11): a concrete resource — a Jira project (`jira-dlv`), a Bitbucket repository
(`bb-delivery-api`), a database (`db-delivery-orders`, environments dev/stage/prod) — with `environment` (only for
resources that have environments) and `level` (read, write, admin; default read). Reading dev/stage is granted at once;
write, admin and prod go to the lead via `approvals` (summary «Доступ: … (stage, запись)»). `list_required` returns
`environments`, `levels`, `optional` (requested on demand) and `approver_label` for the table in the chat.

Absence request (checkpoint 11.11): one request with dates, a type (`day_off`, `vacation` ≥ 14 days ahead and ≤ 28 days,
`absence` ≤ 5 days, `parental_leave` with a document) and up to 3 documents (metadata). `approvals.decide` by the lead
moves it to `pending_hr` and opens an item with `stage: hr_admin`; the HR admin's approval makes it `approved`, registers
it and notifies the HR BP. Until then `reschedule_request` moves the dates (validated with `exclude_id`), cancels the open
item and sends the request back to the lead. `absence.get_request` returns `steps`: lead → HR admin → HR BP.
Trip rules: an Avito office city other than the employee's own, start at least 5 days ahead, at most 14 days, no overlap,
purpose required; the lead approves via `approvals.decide` (kind `trip_request`). Consents signed in the HR portal at
hire (personal data, NDA) are shown only; `sign` on them → `forbidden`.

Callers: the agent (A) calls read tools and the S3/S4 write tools after confirmation; core (B) calls `approvals.*`
(lead decision buttons) and `kb.add_faq` (mentor answer) and issues the confirmation token itself, because the button press
is the confirmation.

Not provided on purpose (R72): salary, compensation amounts, medical data, hiring, dismissal, other people's absences.

## Rights matrix (must be covered by tests in each server)
| Case | Expected |
|---|---|
| intern `create_request` for self, valid token | created, `pending_approval` |
| intern `create_request` with `employee_id` of someone else in args | `validation_error` (unknown field) — identity comes from JWT only |
| any role, write without / with expired / with foreign `confirmation_token` | `invalid_confirmation` |
| same `idempotency_key` twice | one record, second call `replayed: true` |
| intern `get_request` of another user | `forbidden` |
| intern `decide` | tool not listed for role; direct call → `forbidden` |
| lead `decide` on another team's request | `forbidden` |
| `search` as intern of team A | only docs with `acl.roles ∋ intern` and `acl.teams ∋ A` (or `*`); private notes only of the caller |
| `request_access` for an `elevated` resource | created with `pending_approval`, approval item for the lead |
| `consents.sign` on `pdn` / `nda` | `forbidden` (signed only in the HR portal) |
| `trips.create_request` to the employee's own city or in less than 5 days | `validation_error` |
| `get_my_contacts` | only own mentor, lead, HR BP, internship curator — no contact data beyond name and channel |
