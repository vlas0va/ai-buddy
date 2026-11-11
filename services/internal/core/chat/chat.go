// Package chat serves the chat API (S2, S3, S4): sessions, messages streamed from the agent as SSE, and the
// confirmation of write proposals (contracts/agent-stream.md, contracts/openapi.yaml).
//
// Message turn: core saves the user message, builds user_context from verified JWT claims and its own DB (R71),
// calls the agent and relays its events to the browser unchanged and in order (unknown events too; tool_result and
// agent-sent action_result never), persisting on the way: tokens become the assistant message, sources are stored
// with it, action_proposal is stored in core.proposals (only for the allow-listed write tools). The agent's done is
// relayed with message_id set to the saved assistant message (core sends done{message_id} when the agent did not)
// and feeds chat.answered / chat.refused / chat.escalated.
//
// Confirm turn: core checks the proposal (owner, session, status, expiry), signs a confirmation token over
// draft_hash for the caller (key = proposal_id) and sends the agent a confirm turn. The agent calls the MCP write
// and answers with the internal event tool_result, which core saves (core.actions + plan task, R66) and turns into
// action_result. A repeated confirm of an executed proposal replays the stored result without calling anyone (R79).
package chat

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/actions"
	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// Agent opens a turn on the agent service; *agentclient.Client implements it.
type Agent interface {
	Turn(ctx context.Context, userToken string, req agentclient.TurnRequest) (*agentclient.Stream, error)
}

// Limiter is the chat rate limiter; *ratelimit.Limiter implements it (fail-open: true with an error).
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// FlagSource gives the runtime flags of a team; *flags.Service implements it.
type FlagSource interface {
	ForTeam(ctx context.Context, team string) (map[string]bool, error)
}

// Emitter records analytics events; *analytics.Emitter implements it (a nil *analytics.Emitter drops events).
type Emitter interface {
	Emit(ctx context.Context, q analytics.DBTX, ev analytics.Event) error
}

// MCP executes a confirmed write with a core-signed confirmation token; *mcpclient.Client implements it. Core uses it
// to read back a lead's approvals.decide by its idempotency key instead of trusting the agent's tool_result (R71).
type MCP interface {
	Execute(ctx context.Context, userToken, tool string, args map[string]any, key string) (json.RawMessage, error)
}

// Defaults of Config.
const (
	DefaultProposalTTL     = 30 * time.Minute // same as the agent (PROPOSAL_TTL_S) and the core.proposals default
	DefaultConfirmationTTL = 5 * time.Minute
	DefaultRatePerMin      = 20
	historyLimit           = 10
	maxTextRunes           = 4000
	openTasksLimit         = 50
	// Bounds of what one agent turn may make core relay and store (the agent stream itself is bounded by
	// agentclient: 256 KiB per event, 4 MiB per answer). Over a bound the turn ends with upstream_unavailable.
	maxTurnText   = 64 << 10 // assistant text (tokens and follow-up) per turn, bytes
	maxSources    = 50       // source items per turn
	maxDraftBytes = 16 << 10 // stored proposal draft
)

// Config holds chat settings.
type Config struct {
	ConfirmationSecret []byte        // CONFIRMATION_HMAC_SECRET (required for confirm)
	ProposalTTL        time.Duration // how long a proposal can be confirmed; <= 0 means DefaultProposalTTL
	ConfirmationTTL    time.Duration // exp of the confirmation token; <= 0 means DefaultConfirmationTTL
	RatePerMin         int           // CHAT_RATE_PER_MIN; 0 means DefaultRatePerMin, < 0 disables limiting
	KeepAlive          time.Duration // SSE ping interval; <= 0 means httpx.DefaultKeepAlive
}

// Deps are the collaborators of the chat handler. Pool, Agent and Actions are required.
type Deps struct {
	Pool    *pgxpool.Pool
	Agent   Agent
	Actions *actions.Service
	Limiter Limiter    // nil: no rate limiting
	Flags   FlagSource // nil: no flags in user_context
	Emitter Emitter    // nil: no analytics
	MCP     MCP        // nil: lead decisions in chat (approval_decision) are refused
	Log     *slog.Logger
}

// Handler serves the chat endpoints.
type Handler struct {
	pool    *pgxpool.Pool
	agent   Agent
	actions *actions.Service
	limiter Limiter
	flags   FlagSource
	emitter Emitter
	mcp     MCP
	log     *slog.Logger
	cfg     Config
	// Now is the clock (proposal expiry, current period); tests may replace it.
	Now func() time.Time
}

// New returns the chat HTTP handler.
func New(d Deps, cfg Config) *Handler {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if cfg.ProposalTTL <= 0 {
		cfg.ProposalTTL = DefaultProposalTTL
	}
	if cfg.ConfirmationTTL <= 0 {
		cfg.ConfirmationTTL = DefaultConfirmationTTL
	}
	if cfg.RatePerMin == 0 {
		cfg.RatePerMin = DefaultRatePerMin
	}
	return &Handler{pool: d.Pool, agent: d.Agent, actions: d.Actions, limiter: d.Limiter, flags: d.Flags,
		emitter: d.Emitter, mcp: d.MCP, log: d.Log, cfg: cfg, Now: time.Now}
}

// Register adds the chat routes (behind authn.Middleware).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/chat/sessions", h.createSession)
	mux.HandleFunc("GET /api/chat/sessions/{session_id}", h.getSession)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/messages", h.postMessage)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/proposals/{proposal_id}/confirm", h.confirm)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/proposals/{proposal_id}/cancel", h.cancel)
	mux.HandleFunc("GET /api/kb/{doc_id}", h.kbDoc)
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// caller returns the verified claims and raw token of the request, or answers 401.
func caller(w http.ResponseWriter, r *http.Request) (authn.Claims, string, bool) {
	c, ok := authn.FromContext(r.Context())
	token, tok := authn.TokenFromContext(r.Context())
	if !ok || !tok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return authn.Claims{}, "", false
	}
	return c, token, true
}

// ownSession checks that sessionID is a session of userID: a malformed id is not_found, an unknown or foreign one
// is forbidden (existence is not revealed, R71).
func (h *Handler) ownSession(ctx context.Context, userID, sessionID string) error {
	if !uuidRE.MatchString(sessionID) {
		return apierr.NotFound()
	}
	var owner string
	err := h.pool.QueryRow(ctx, `SELECT user_id FROM core.chat_sessions WHERE id = $1`, sessionID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != userID) {
		return apierr.Forbidden()
	}
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	return nil
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	c, _, ok := caller(w, r)
	if !ok {
		return
	}
	var id string
	if err := h.pool.QueryRow(r.Context(), `INSERT INTO core.chat_sessions (user_id) VALUES ($1) RETURNING id::text`, c.Sub).Scan(&id); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("create session: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"session_id": id})
}

// Message is the openapi ChatMessage.
type Message struct {
	ID          string            `json:"id"`
	Author      string            `json:"author"`
	Text        string            `json:"text"`
	Sources     []json.RawMessage `json:"sources"`
	Attachments []attachmentMeta  `json:"attachments"`
	CreatedAt   string            `json:"created_at"`
	// cards restored after leaving the chat (feedback 11.11); the proposal state and the result come from core's tables
	Proposal      json.RawMessage `json:"proposal,omitempty"`
	ProposalState string          `json:"proposal_state,omitempty"`
	// several drafts of one message (the list of accesses with checkboxes), each with its state
	Proposals      []historyProposal `json:"proposals,omitempty"`
	Result         *actions.Result   `json:"result,omitempty"`
	Escalation     json.RawMessage   `json:"escalation,omitempty"`
	EscalationSent bool              `json:"escalation_sent,omitempty"` // a message to that role was sent after the draft
	Refusal        json.RawMessage   `json:"refusal,omitempty"`
	NextStep       json.RawMessage   `json:"next_step,omitempty"`
}

// attachmentMeta is what the history returns about an attachment: never the text, so a reload stays light.
type attachmentMeta struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size"`
}

const messagesLimit = 200

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	c, _, ok := caller(w, r)
	if !ok {
		return
	}
	sid := r.PathValue("session_id")
	if err := h.ownSession(r.Context(), c.Sub, sid); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	rows, err := h.pool.Query(r.Context(), `SELECT m.id::text, m.author, m.text, m.sources, m.attachments,
		to_char(m.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), m.cards,
		coalesce((SELECT jsonb_object_agg(p.id::text, p.status) FROM core.proposals p WHERE p.session_id = m.session_id
		          AND p.id::text IN (SELECT x->>'proposal_id' FROM jsonb_array_elements(
		              CASE WHEN jsonb_typeof(m.cards->'proposals') = 'array' THEN m.cards->'proposals' ELSE '[]' END) x)), '{}'),
		EXISTS (SELECT 1 FROM core.escalations e WHERE e.message_id = m.id)
		FROM (SELECT * FROM core.chat_messages WHERE session_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2) m
		ORDER BY m.created_at, m.id`, sid, messagesLimit)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("load messages: %w", err))
		return
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var sources, atts, rawCards, rawStatuses []byte
		if err := rows.Scan(&m.ID, &m.Author, &m.Text, &sources, &atts, &m.CreatedAt, &rawCards, &rawStatuses,
			&m.EscalationSent); err != nil {
			apierr.WriteJSON(w, fmt.Errorf("scan message: %w", err))
			return
		}
		if err := json.Unmarshal(sources, &m.Sources); err != nil || m.Sources == nil {
			m.Sources = []json.RawMessage{}
		}
		if err := json.Unmarshal(atts, &m.Attachments); err != nil || m.Attachments == nil {
			m.Attachments = []attachmentMeta{}
		}
		var cards map[string]json.RawMessage
		_ = json.Unmarshal(rawCards, &cards)
		m.Escalation, m.Refusal, m.NextStep = cards["escalation"], cards["refusal"], cards["next_step"]
		m.Proposals = restoreProposals(cards["proposals"], rawStatuses)
		if len(m.Proposals) == 1 {
			m.Proposal, m.ProposalState, m.Proposals = m.Proposals[0].Proposal, m.Proposals[0].State, nil
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("read messages: %w", err))
		return
	}
	// result cards with the current status of the action they announced (an approval made later is shown)
	if list, err := h.actions.List(r.Context(), h.pool, c.Sub); err == nil {
		byMessage := map[string]actions.Result{}
		for _, a := range list {
			if a.MessageID != "" {
				byMessage[a.MessageID] = a.Result()
			}
		}
		for i := range out {
			if res, ok := byMessage[out[i].ID]; ok {
				out[i].Result = &res
			}
		}
	} else {
		h.log.Warn("load actions for the history", "err", err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"session_id": sid, "messages": out})
}

type historyProposal struct {
	Proposal json.RawMessage `json:"proposal"`
	State    string          `json:"state"`
}

// restoreProposals pairs the stored drafts with the state of their proposal; a draft core does not know is dropped.
func restoreProposals(raw json.RawMessage, rawStatuses []byte) []historyProposal {
	var list []json.RawMessage
	var statuses map[string]string
	if json.Unmarshal(raw, &list) != nil || json.Unmarshal(rawStatuses, &statuses) != nil {
		return nil
	}
	var out []historyProposal
	for _, p := range list {
		var id struct {
			ProposalID string `json:"proposal_id"`
		}
		if json.Unmarshal(p, &id) != nil {
			continue
		}
		if st, ok := statuses[id.ProposalID]; ok {
			out = append(out, historyProposal{Proposal: p, State: proposalState(st)})
		}
	}
	return out
}

// KBReader reads one KB document through the agent (its ACL check); *agentclient.Client implements it.
type KBReader interface {
	KBDoc(ctx context.Context, userToken, docID string) (json.RawMessage, bool, error)
}

var docIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// kbDoc opens a source chip of an answer: the test document, only if the user may read it (R69, R71).
func (h *Handler) kbDoc(w http.ResponseWriter, r *http.Request) {
	_, token, ok := caller(w, r)
	if !ok {
		return
	}
	id := r.PathValue("doc_id")
	kb, isReader := h.agent.(KBReader)
	if !docIDRE.MatchString(id) || !isReader {
		apierr.WriteJSON(w, apierr.NotFound())
		return
	}
	doc, found, err := kb.KBDoc(r.Context(), token, id)
	switch {
	case err != nil:
		h.log.Warn("kb document unavailable", "doc", id, "err", err)
		apierr.WriteJSON(w, apierr.Unavailable())
	case !found:
		apierr.WriteJSON(w, apierr.NotFound())
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(doc)
	}
}

// proposalState is what the chat card shows for a stored proposal.
func proposalState(status string) string {
	switch status {
	case "executed", "confirmed":
		return "confirmed"
	case "cancelled", "expired":
		return "cancelled"
	}
	return "pending" // pending or failed: the card can still be sent
}

// insertMessage stores a message with an explicit id (clock_timestamp keeps the order inside one request).
func insertMessage(ctx context.Context, q actions.DBTX, id, sessionID, author, text string, sources []json.RawMessage,
	cards map[string]json.RawMessage) error {
	if sources == nil {
		sources = []json.RawMessage{}
	}
	raw, err := json.Marshal(sources)
	if err != nil {
		return fmt.Errorf("encode sources: %w", err)
	}
	if cards == nil {
		cards = map[string]json.RawMessage{}
	}
	rawCards, err := json.Marshal(cards)
	if err != nil {
		return fmt.Errorf("encode cards: %w", err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO core.chat_messages (id, session_id, author, text, sources, created_at, cards)
		VALUES ($1, $2, $3, $4, $5, clock_timestamp(), $6) ON CONFLICT (id) DO NOTHING`, id, sessionID, author, text, raw, rawCards); err != nil {
		return fmt.Errorf("insert %s message: %w", author, err)
	}
	return nil
}

// emit records an analytics event; failures are logged, never shown to the user.
func (h *Handler) emit(ctx context.Context, c authn.Claims, name string, props map[string]any) {
	if h.emitter == nil {
		return
	}
	if err := h.emitter.Emit(ctx, h.pool, analytics.Event{Name: name, UserID: c.Sub, Role: c.Role, TeamID: c.TeamID, Props: props}); err != nil {
		h.log.Warn("analytics event failed", "name", name, "err", err)
	}
}

// newUUID returns a random UUID v4.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand does not fail on supported platforms
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// streamError is the data of an error event.
type streamError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Russian messages of errors core emits on a stream.
const (
	msgAgentUnavailable = "Ассистент временно недоступен, попробуйте ещё раз"
	msgNoResult         = "Не удалось получить результат заявки, попробуйте ещё раз"
	msgSaveFailed       = "Заявка отправлена, но результат не сохранён. Повторите подтверждение, повторной заявки не будет"
	msgBadProposal      = "Ассистент предложил действие, которое нельзя выполнить через чат"
	msgTooLarge         = "Ответ ассистента слишком большой, попробуйте ещё раз"
	msgBadResult        = "Не удалось подтвердить результат заявки, попробуйте ещё раз"
)

// agentError turns a failed agent call into the error event of the stream: an agent that answered 4xx with a JSON
// error keeps its code and message; everything else is upstream_unavailable, retryable.
func agentError(err error) streamError {
	var ue *agentclient.UnavailableError
	if errors.As(err, &ue) && ue.Status >= 400 && ue.Status < 500 && ue.Code != "" {
		msg := ue.Message
		if msg == "" {
			msg = msgAgentUnavailable
		}
		return streamError{Code: ue.Code, Message: msg, Retryable: ue.Retryable}
	}
	return streamError{Code: string(apierr.CodeUpstreamUnavailable), Message: msgAgentUnavailable, Retryable: true}
}

// errTooLarge ends a turn whose content exceeds the bounds (R76).
var errTooLarge = streamError{Code: string(apierr.CodeUpstreamUnavailable), Message: msgTooLarge, Retryable: true}

// exactObject decodes a JSON object and rejects keys that differ only in case from one of want: Go's struct decoding
// would match them case-insensitively while the agent reads the exact key, so both sides must see the same value.
func exactObject(raw json.RawMessage, want ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("not a JSON object")
	}
	for k := range m {
		for _, w := range want {
			if k != w && strings.EqualFold(k, w) {
				return nil, fmt.Errorf("key %q differs from %q only in case", k, w)
			}
		}
	}
	return m, nil
}

// str returns the string value of key in m ("" when missing or not a string).
func str(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}
