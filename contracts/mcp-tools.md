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
  where `args_hash = sha256(canonical_json(args))` over the tool arguments WITHOUT `confirmation_token` and `idempotency_key`
  (keys sorted, no whitespace, UTF-8). The server checks: signature, `exp` not passed, `sub` = JWT subject,
  `key` = `idempotency_key`, `args_hash` = hash of the arguments it actually received.
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
| approvals (`/mcp/approvals`) | `list_pending` | lead | no | S3 step 8, S4 |
| approvals | `decide` | lead (own team only) | **yes** | S3 step 8, S4 |
| directory (`/mcp/directory`) | `whoami` | all | no | S2 |
| directory | `get_my_contacts` | all | no | S2 «к кому идти», CL-02, CL-03 |
| checklist (`/mcp/checklist`) | `get_plan_template` | all | no | S1 |
| access (`/mcp/access`) | `list_required` | intern, mentor | no | S4 step 1 |
| access | `list_my_access` | intern, mentor | no | S4 |
| access | `list_team_channels` | intern, mentor | no | S4 step 4, CL-04 |
| access | `request_access` | intern, mentor | **yes** | S4 steps 2–3 |
| kb (`/mcp/kb`) | `search` | all | no | S2 |

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
| `get_my_contacts` | only own mentor, lead, HR BP, internship curator — no contact data beyond name and channel |
