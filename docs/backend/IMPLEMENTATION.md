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

### Platform API reference (phase A)
Import path `aibuddy/services/internal/platform/<pkg>`. Standard library only. Package docs hold the details.

**canonjson** — output == Python `json.dumps(sort_keys=True, separators=(",",":"), ensure_ascii=False)` for objects/arrays/strings/ints/bools/null (golden vectors: `canonjson/testdata/vectors.json`, generator `gen_vectors.py`). Number text kept as given (only `-0` → `0`); floats are NOT normalised like Python, so keep them out of signed args.
- `func Marshal(v any) ([]byte, error)` — canonical JSON of any encoding/json value (structs, maps, `json.RawMessage`, `json.Number`)
- `func SHA256Hex(v any) (string, error)` — lowercase hex SHA-256 of `Marshal(v)` (= `draft_hash`)

**authn** — HS256 JWT, header exactly `{"alg":"HS256","typ":"JWT"}`, base64url without padding.
- `type Claims struct{ Sub, Role, TeamID, CohortStart string; Iat, Exp int64 }` — JSON `sub, role, team_id, cohort_start (omitempty, YYYY-MM-DD), iat, exp`
- `const DefaultLeeway = 30 * time.Second`
- `var ErrNoSecret, ErrMalformed, ErrAlgorithm, ErrSignature, ErrExpired, ErrNotYetValid, ErrMissingClaims error`
- `func Issue(secret []byte, c Claims, ttl time.Duration) (string, error)` — sets Iat = now, Exp = now + ttl
- `func Sign(secret []byte, c Claims) (string, error)` — signs claims as given (tests: expired tokens)
- `func Verify(secret []byte, token string, now time.Time) (Claims, error)` — strict checks, DefaultLeeway
- `type Verifier struct{ Secret []byte; Leeway time.Duration; Now func() time.Time }`
- `func NewVerifier(secret []byte) *Verifier` / `func (v *Verifier) Verify(token string) (Claims, error)`
- `func (v *Verifier) Middleware(next http.Handler) http.Handler` — 401 apierr + `WWW-Authenticate: Bearer` on any failure
- `func Middleware(secret []byte) func(http.Handler) http.Handler` — `NewVerifier(secret).Middleware`
- `func BearerToken(r *http.Request) (string, bool)` — raw token from `Authorization: Bearer`
- `func NewContext(ctx, c Claims) context.Context` / `func FromContext(ctx) (Claims, bool)`
- `func WithToken(ctx, token string) context.Context` / `func TokenFromContext(ctx) (string, bool)` — raw JWT to forward to agent/MCP

**confirm** — token `P.S`, `P = base64url(canonjson{"args_hash","exp","key","sub"})`, `S = base64url(HMAC-SHA256(secret, P))`, base64url WITHOUT padding, MAC over the ASCII of P, `exp` Unix seconds, `args_hash` lowercase hex.
- `const KeyIdempotency = "idempotency_key"`, `KeyConfirmation = "confirmation_token"`
- `var ErrInvalid` (wrapped by all of:) `ErrMalformed, ErrSignature, ErrExpired, ErrSubject, ErrKey, ErrArgsHash`; `var ErrNoSecret` (config error, not ErrInvalid)
- `func ArgsHash(args any) (string, error)` — args must be a JSON object; drops the two top-level keys above; == `canonjson.SHA256Hex(draft.args)`
- `func Issue(secret []byte, sub, key, argsHash string, ttl time.Duration) (string, error)`
- `func IssueUntil(secret []byte, sub, key, argsHash string, exp time.Time) (string, error)` — tests: expired tokens
- `func Verify(secret []byte, token, sub, key string, args any, now time.Time) error` — order: signature, exp, sub, key, args

**apierr**
- `type Code string`; API codes `CodeUnauthorized, CodeForbidden, CodeNotFound, CodeValidation, CodeConflict, CodeExpired, CodeRateLimited, CodeUpstreamUnavailable, CodeInternal`; MCP-only `CodeInvalidConfirmation, CodeUnavailable`
- `type Error struct{ Code Code; Message string; Retryable bool; Status int }` — JSON `{code, message, retryable}`, Status not serialised
- `func New(code Code, message string) *Error` — "" = default Russian message; status/retryable from the code
- `func Unauthorized() / Forbidden() / NotFound() / Validation() / Conflict() / Expired() / RateLimited() / UpstreamUnavailable() / Internal() / InvalidConfirmation() / Unavailable() *Error` — statuses 401/403/404/400/409/409/429(r)/502(r)/500/403/503(r), (r) = retryable
- `func (e *Error) WithMessage(msg string) *Error` — copy with another message
- `func (e *Error) Error() string`; `func (e *Error) Is(target error) bool` — same code, so `errors.Is(err, apierr.Forbidden())` works
- `func As(err error) (*Error, bool)`; `func HasCode(err error, code Code) bool`
- `func From(err error) *Error` — *Error as is; ctx deadline/cancel → UpstreamUnavailable; other → Internal; nil → nil
- `func ToAPI(e *Error) *Error` — unavailable → upstream_unavailable, invalid_confirmation → expired, unknown → internal
- `func ToMCP(e *Error) *Error` — unauthorized → forbidden, expired → invalid_confirmation, rate_limited/upstream_unavailable/internal → unavailable
- `func WriteJSON(w http.ResponseWriter, err error)` — ToAPI + status + body; non-apierr errors logged and answered as internal

**httpx**
- `const DefaultMaxBody int64 = 1 << 20`; `func ReadJSON(w, r, dst any) error`; `func ReadJSONLimit(w, r, dst any, limit int64) error` — unknown fields, trailing data, empty/oversized body → apierr validation_error (Russian)
- `func WriteJSON(w http.ResponseWriter, status int, v any)`
- `type Middleware = func(http.Handler) http.Handler`; `func Chain(mws ...Middleware) Middleware` — first = outermost
- `const HeaderRequestID = "X-Request-Id"`; `func RequestID(next http.Handler) http.Handler` — keeps a sane incoming id or generates a UUID v4
- `func WithRequestID(ctx, id string) context.Context` / `func RequestIDFromContext(ctx) string`
- `func Recover(log *slog.Logger) Middleware` — panic → apierr internal (re-panics http.ErrAbortHandler)
- `func AccessLog(log *slog.Logger) Middleware` — method, path, status, bytes, duration_ms, request_id; `/healthz` at debug
- `func CORS(origins []string) Middleware` — exact allow-list (or "*"), preflight 204/403, no credentials, exposes X-Request-Id
- `const DefaultKeepAlive = 15 * time.Second`; `var ErrSSEClosed`
- `func NewSSEWriter(ctx, w http.ResponseWriter, keepAlive time.Duration) (*SSEWriter, error)` — headers + 200 + flush, `: ping` goroutine; keepAlive <= 0 → default
- `func (s *SSEWriter) Send(event string, data any) error` — `id: n` (from 1), `event:`, one `data:` line of compact JSON; concurrency-safe
- `func (s *SSEWriter) LastID() int64`; `func (s *SSEWriter) Close() error` — stops pings; always `defer sse.Close()` in the handler
- `type Event struct{ ID, Name string; Data json.RawMessage }`; `const MaxSSELine = 1 << 20`; `var ErrSSEData`
- `func NewSSEReader(r io.Reader) *SSEReader`; `func (r *SSEReader) Next() (Event, error)` — io.EOF at end; ErrSSEData (with the event) when data is not JSON; final event without blank line is delivered

**period**
- `type Period string`; `const Week1, Week2, Month1, Month2, After3Months Period` (`week_1` … `after_3_months`)
- `func All() []Period`; `func Parse(s string) (Period, error)`; `func Order(p Period) int` (-1 unknown)
- `func Label(p Period) string` — «1 неделя», «2 неделя», «1 месяц», «2 месяц», «После 3-х месяцев»
- `func DueOffsetDays(p Period) int` — 7/14/30/60/100
- `func Current(cohortStart, today time.Time) Period` — <7 week_1 (also before start), <14, <30, <90, else after_3_months
- `func DaysSince(cohortStart, today time.Time) int` — calendar days, time of day ignored
- `func DueDate(cohortStart time.Time, p Period) time.Time` — cohort start + offset (midnight UTC)
- `func ParseDate(s string) (time.Time, error)` — YYYY-MM-DD → midnight UTC

**config** — collects all problems, then `Err()`; blank values count as unset.
- `func New(getenv func(string) string) *Env` — nil = os.Getenv
- `func (e *Env) String(key, def string) string`; `func (e *Env) Required(key string) string`
- `func (e *Env) Int(key string, def int) int`; `func (e *Env) Duration(key string, def time.Duration) time.Duration` — "24h" or bare seconds "30"
- `func (e *Env) List(key string, def []string) []string` — comma-separated (CORS_ORIGINS)
- `func (e *Env) Err() error` — lists every missing required variable and invalid value

### Platform API reference (phase B)
**pg**
- `type Options struct{ MaxConns int32; StartupWait, RetryBackoff time.Duration }` — zero = 10 conns, 30 s, 1 s
- `func Open(ctx, url string, opts Options) (*pgxpool.Pool, error)` — ping with retry while Postgres starts

**pgtest** (tests only)
- `const EnvVar = "TEST_DATABASE_URL"`
- `func New(t testing.TB) *pgxpool.Pool` — fresh `t_<random>` DB with all migrations (schema + demo seed), dropped on cleanup; skips when EnvVar unset
- `func URL(t testing.TB, pool *pgxpool.Pool) string` — connection string of that DB

**mcpx** — server and client for `/mcp/<server>`; go-sdk streamable HTTP, stateless, JSON responses (no session id, no SSE).
- `type Caller struct{ authn.Claims; Token string }` — verified JWT claims + raw token; the ONLY source of identity in handlers (R71)
- `type HandlerFunc func(ctx context.Context, c Caller, args json.RawMessage) (any, error)` — args validated, defaults applied (write tools still contain `idempotency_key`/`confirmation_token`); return value must match outputSchema; return `*apierr.Error` for expected failures
- `type Config struct{ JWTSecret, ConfirmationSecret []byte; Logger *slog.Logger }`
- `func NewServer(name string, cfg Config) (*Server, error)` — tools from `contracts/schemas/<name>.json`
- `func (s *Server) Handle(tool string, h HandlerFunc)` — problems (unknown tool, duplicate, nil) are reported by Validate
- `func (s *Server) Validate() error` — also: every contract tool has a handler, secrets set
- `func (s *Server) Handler() (http.Handler, error)` — Validate + JWT check (401 JSON-RPC error, `data` = apierr JSON) + MCP handler
- `func Mount(mux *http.ServeMux, servers ...*Server) error` — serves each at `PathPrefix + name` (`/mcp/<name>`)
- `func (s *Server) Invoke(ctx, c Caller, tool string, args any) (json.RawMessage, *apierr.Error)` — same pipeline without HTTP (unit tests of mock servers)
- `func (s *Server) Name() string`; `func (s *Server) Tools() []ToolDef`; `func LoadTools(server string) ([]ToolDef, error)`
- `type ToolDef struct{ Name, Description string; Roles []string; Write bool; InputSchema, OutputSchema json.RawMessage }`
- `func DecodeArgs(args json.RawMessage, dst any) error` — validation_error on failure
- `func WithConfirmation(secret []byte, sub, key string, args map[string]any, ttl time.Duration) (map[string]any, error)` — copy of args + `idempotency_key` + signed `confirmation_token` (core for approvals.decide / kb.add_faq; tests)
- `func CallerFrom(ctx, req mcp.Request) (Caller, bool)` — for raw go-sdk handlers; mcpx handlers get Caller directly
- `const PathPrefix = "/mcp/"`, `const MaxRequestBody = 1 << 20`
- `type Client struct{ BaseURL string; HTTPClient *http.Client }`
- `func (c *Client) Call(ctx, userToken, server, tool string, args any) (json.RawMessage, *apierr.Error, error)` — success: structured JSON; tool error: `*apierr.Error` with MCP code; transport/protocol: `error` wrapping apierr `unauthorized` (token rejected) / `internal` (JSON-RPC error, e.g. unknown tool) / `upstream_unavailable` (network, ctx); forwards X-Request-Id

Pipeline per tools/call: role (`roles`) → forbidden; write without token/key → invalid_confirmation; inputSchema (+ format date/date-time/uuid) → validation_error; write: `confirm.Verify` → invalid_confirmation; handler. Handler `*apierr.Error` → `isError: true`, `structuredContent` = `{code, message, retryable}` via `apierr.ToMCP`; other errors, panics and outputs that break outputSchema → logged, generic `unavailable` (not retryable). tools/list shows only tools the caller's role may call.

Mock server package shape: `func Register(srv *mcpx.Server, pool *pgxpool.Pool)` calling `srv.Handle` for every tool of its contract.
```go
srv, err := mcpx.NewServer("absence", mcpx.Config{JWTSecret: jwt, ConfirmationSecret: hmac, Logger: log})
if err != nil { return err }
srv.Handle("get_request", func(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct{ ID string `json:"id"` }
	if err := mcpx.DecodeArgs(args, &in); err != nil { return nil, err }
	return loadOwnRequest(ctx, pool, c.Sub, in.ID) // other users' ids → apierr.Forbidden()
}) // ...one Handle per contract tool
if err := mcpx.Mount(mux, srv); err != nil { return err } // fails at startup if a tool lacks a handler
out, toolErr, err := (&mcpx.Client{BaseURL: "http://mcp-mocks:8100/mcp"}).Call(ctx, userJWT, "absence", "get_request", map[string]any{"id": "ABS-000001"})
```

## 5. Mock MCP servers (agents M1, M2) — behaviour per tool (schemas in `contracts/schemas/`)
Each server package exposes `Register(srv *mcpx.Server, pool *pgxpool.Pool, opts...)` (exact shape set by agent P) and owns its schema.
- **absence** (M1). `get_policy`: KB document `absence-policy-<team_id>` else `absence-policy` from `kb.documents` (read-only use of kb schema allowed). `list_my_requests`, `get_request` (own only → else `forbidden`). `validate_request`: start ≥ tomorrow, end ≥ start, weekdays only, ≤ 3 consecutive days, no overlap with own `pending_approval`/`approved` requests; returns problems in Russian + approver (employee's `lead_id`). `create_request`: re-validates; inserts `absence.requests` (status `pending_approval`, approver = lead) and `approvals.items` (kind `absence_request`) in one tx; idempotent by key (`replayed: true`).
- **approvals** (M1). `list_pending`: items where approver = caller and status pending. `decide`: only own items (else `forbidden`), only pending (else `conflict`); updates the item and the underlying `absence.requests.status` (approved/rejected) or `access.requests.status` (granted/rejected); idempotent by key.
- **access** (M1). `list_required`: resources of kind tool/space with `team_id IS NULL OR = caller team`, each with the caller's status (not_requested/pending_approval/granted/rejected). `list_my_access`: resources the caller requested or holds. `list_team_channels`: channel resources of the team plus `team_id IS NULL`, names/purpose/closed only (closed = elevated). `request_access`: resource must be visible to the caller's team (else `forbidden`); non-elevated → `granted` immediately; elevated → `pending_approval` + `approvals.items` for the caller's lead; result carries `checklist_item_id`; idempotent.
- **directory** (M2). `whoami` (current_period via `period`), `get_my_contacts`: own mentor, lead, HR BP, curator with `channel` and `ask_about` (fixed Russian lists per role, e.g. lead → «отгул и отсутствия», «цели стажировки»; curator → «методичка наставника», «экватор и выпускной», «документы для вуза»; hrbp → «кадровые вопросы»).
- **checklist** (M2). `get_plan_template(role)`: periods + items where `role IN (<role>, 'both')`, ordered by position.
- **kb** (M2). Loader: on start read `KB_DIR/*.md` (YAML frontmatter: doc_id, title, source_type: test, acl {roles, teams}, checklist_item_id?, url?) and upsert into `kb.documents`; refuse docs whose source_type is not `test`. `search`: `websearch_to_tsquery('russian', q)`, ACL filter (`acl_roles @> [role]` and (`'*' = ANY(acl_teams)` or team in acl_teams)), private notes only for `owner_id = caller`, `ts_headline` snippet, rank, k ≤ 10. `add_faq` (mentor): doc_id `faq-<team>-<short id>`, kind faq, acl roles {intern, mentor, lead}, teams {caller team}, idempotent.
  The knowledge base is A's `data/kb` (37 documents). The loader reads `doc_id`, `title`, `source_type` (must be `test`), `acl {roles, teams}` (`"*"` = all teams) and `checklist_item_id`, ignores other fields (`checklist`, `tags`, `updated`, ...) and skips `README.md`. `absence.get_policy` reads `absence-policy` (a team document `absence-policy-<team>` wins if it exists). Search tests use fixtures in `services/internal/mocks/kb/testdata/kb`.

## 6. Core (agents C1, C2) — behaviour per endpoint (shapes in `contracts/openapi.yaml`)
Common: all `/api/*` except login require JWT (`authn.Middleware`); CORS from `CORS_ORIGINS`; errors via `apierr`.
**C1 (chat & actions)**
- `agentclient`: `Turn(ctx, userToken, TurnRequest) (<-chan Event, error)` — POST `AGENT_URL/v1/turn`, SSE parsed with `httpx`; timeout `LLM_TIMEOUT_S`+10 s; connection error → typed error.
- `mcpclient`: thin adapter over `mcpx.Client` with base URL `MCP_BASE_URL` (default `http://mcp-mocks:8100/mcp`).
- `chat`: POST sessions; GET session (own only); POST messages → save user message, build `user_context` (role, team, current_period, open tasks of own plan) and last 10 messages as history, call agent, relay events unchanged to the client SSE (`httpx` writer), persist: `action_proposal` → `core.proposals` (id = proposal_id, draft, draft_hash = canonjson hash of `draft.args`, expires 30 min); `token`s → assistant message text; `sources` → message sources; finally emit `done{message_id}`. Agent unreachable → `error{upstream_unavailable, retryable:true, "Ассистент временно недоступен, попробуйте ещё раз"}` then close. Rate limit: `ratelimit` (C2) per user `CHAT_RATE_PER_MIN`.
- Confirm (agent contract v1): proposal must belong to caller+session; `executed` → replay stored `action_result` + `done` (no calls); `cancelled`/`expired` → 409 JSON before streaming; `pending`/`failed` → status `confirmed`, core issues the confirmation token (`confirm.Issue`: sub = JWT subject, key = proposal_id, `ArgsHash(draft.args)`, 5 min) and sends the agent `turn {kind: confirm, proposal_id, draft, confirmation_token}`; the agent calls `draft.tool` and answers with the internal event `tool_result` (never relayed); core saves it (`actions.Save`, proposal `executed`; S4 linked by `task_checklist_item_id`, S3 to a `source = action` task) and emits `action_result` (+ `followup` as a token), then `done`. `approval_decision` (lead in chat, `approvals.decide`) applies the decision to the intern's action (`actions.ApplyDecision`). Agent `error` → proposal `failed`, error relayed (retryable from the agent). Cancel → `cancelled`, 204.
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

### Core API reference (C2)
Import path `aibuddy/services/internal/core/<pkg>`. `DBTX` = anything with `Exec/Query/QueryRow` (`*pgxpool.Pool` or `pgx.Tx`). Every HTTP package has
`New(...) *Handler` and `(*Handler).Register(mux)`; mount them behind `authn.Middleware`. `*analytics.Emitter` may be nil everywhere (events dropped).

| Package | Constructor | Routes registered |
|---|---|---|
| `authapi` | `New(pool, jwtSecret []byte, ttl, flags FlagSource, emitter, log)`; also `RegisterPublic(mux)` | public `POST /api/auth/login`; `GET /api/me` |
| `plan` | `New(pool, emitter, log)` | `GET /api/plan`, `PATCH /api/plan/tasks/{task_id}`, `PUT /api/plan/goals`, `GET /api/team/interns` |
| `escalations` | `New(pool, emitter, flags FlagChecker, faq FAQWriter, log)` | `POST/GET /api/escalations`, `POST /api/escalations/{escalation_id}/answer` |
| `notify` | `New(pool, log)` | `GET /api/notifications`, `POST /api/notifications/{notification_id}/read` |
| `analytics` | `New(pool, emitter, log)` | `POST /api/events`, `POST /api/feedback` |
| `flags` | `NewService(pool, rdb redis.Cmdable, emitter, log)`, `New(svc, log)` | `GET /api/flags`, `PUT /api/flags/{key}` (404 unless `Handler.Demo`, which defaults to `buildflags.Demo`; lead only) |
| `cron` | `NewRunner(pool, emitter, interval, log)`, `New(runner, log)` | `POST /api/cron/run` (404 unless `Handler.Demo`; 202, job runs in background) |
| `ratelimit` | `New(rdb redis.Cmdable) *Limiter` | none |

Interfaces other packages depend on:
- `notify.Store` (zero value usable): `Create(ctx, q DBTX, notify.Notification{UserID, Kind, Title, Body, ChecklistItemID, DedupeKey}) error`; `CreateIfNew(...) (created bool, err)`.
  Kinds: `reminder|overdue|status_change|escalation_answer` (`ErrInvalid` otherwise). A repeated `(UserID, DedupeKey)` is a no-op.
- `analytics.Emitter`: `NewEmitter(kafkaEnabled bool)`; `Emit(ctx, q DBTX, analytics.Event{Name, UserID, Role, TeamID, Props, TS}) error`.
  Writes `core.analytics_events` and, with Kafka on, `core.outbox` in one statement. Props are filtered by the whitelist of `contracts/events.md`
  (unknown keys, nested values and strings over 100 chars are dropped); name must look like `domain.event` (`ErrInvalid`).
- `analytics.Publisher`: `NewPublisher(pool, analytics.Producer, log)`, `Run(ctx)` (drains every 2 s, `Interval` field), `DrainOnce(ctx) (n, err)`;
  `Producer{Produce(ctx, topic, key string, value []byte) error}`; real one: `NewKafkaProducer(brokers []string) (*KafkaProducer, error)` (+ `Close()`).
- `ratelimit.Allower{Allow(ctx, key string, limit int, window time.Duration) (bool, error)}`; fixed window in Redis, fail-open (returns `true` with an error when Redis is down; log it).
- `flags.Service`: `All(ctx)`, `ForTeam(ctx, team) (map[string]bool, error)`, `Enabled(ctx, key, team) bool` (errors count as off), `Set(ctx, claims, key, enabled, teams)`;
  Redis key `flags:v1`, TTL 30 s, DB fallback. Key constants `flags.EscalationToKB` etc. `*flags.Service` satisfies `authapi.FlagSource` and `escalations.FlagChecker`.
- `escalations.FAQWriter{AddFAQ(ctx, mentorToken, title, body, idempotencyKey string) error}`: called only when `add_to_kb` is true and flag `escalation_to_kb` is on for the answering user's team;
  `mentorToken` is the raw JWT (`authn.TokenFromContext`), idempotency key = escalation id. A failure is logged and answered as `added_to_kb=false`; the answer itself is kept. Passing a nil writer disables KB publishing.
- `cron.Runner`: `Run(ctx)` (runs once at start, then every interval), `RunOnce(ctx) (cron.Result, error)`; `Now` field for tests.

Behaviour notes (deviations and decisions):
- `GET /api/plan` goals always belong to the intern (stored on the intern's plan) and are shown in both the intern and the mentor plan. `status=overdue` on read applies to open tasks with `due_at < today`, but not to `waiting_approval`.
  `next_step` skips `waiting_approval`, `done`, `source=action` and later periods; ties between overdue items prefer a task nobody started (stored status not `in_progress`), so for the demo seed Anna's next step is CL-05 `request_access`.
- Foreign or unknown ids answer 403 (tasks, notifications, escalations, feedback messages, sessions); only a malformed uuid answers 404. `PATCH` on a `source=action` or `waiting_approval` task is 409 (system-managed). Marking a parent done completes its subtasks; all subtasks done completes the parent; reopening a subtask reopens a done parent.
- `POST /api/escalations` also creates a `reminder` notification for the addressee (dedupe `escalation:<id>`, no question text) and emits `chat.escalated`. Only interns can escalate. Cron dedupe keys are exactly `overdue:<task_id>` and `period:<item>:<period>`
  (so a mentor with several interns gets one period reminder per item, not per intern). `POST /api/auth/login` emits `session.started`.
- `/api/events` accepts only `ui.*` names, replaces client timestamps more than 5 minutes in the future with server time, and rejects batches over 100 events.

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
