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

// Defaults of Config.
const (
	DefaultProposalTTL     = 30 * time.Minute // same as the agent (PROPOSAL_TTL_S) and the core.proposals default
	DefaultConfirmationTTL = 5 * time.Minute
	DefaultRatePerMin      = 20
	historyLimit           = 10
	maxTextRunes           = 4000
	openTasksLimit         = 50
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
		emitter: d.Emitter, log: d.Log, cfg: cfg, Now: time.Now}
}

// Register adds the chat routes (behind authn.Middleware).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/chat/sessions", h.createSession)
	mux.HandleFunc("GET /api/chat/sessions/{session_id}", h.getSession)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/messages", h.postMessage)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/proposals/{proposal_id}/confirm", h.confirm)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/proposals/{proposal_id}/cancel", h.cancel)
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
	ID        string            `json:"id"`
	Author    string            `json:"author"`
	Text      string            `json:"text"`
	Sources   []json.RawMessage `json:"sources"`
	CreatedAt string            `json:"created_at"`
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
	rows, err := h.pool.Query(r.Context(), `SELECT id::text, author, text, sources,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM (SELECT * FROM core.chat_messages WHERE session_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2) m
		ORDER BY created_at, id`, sid, messagesLimit)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("load messages: %w", err))
		return
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var sources []byte
		if err := rows.Scan(&m.ID, &m.Author, &m.Text, &sources, &m.CreatedAt); err != nil {
			apierr.WriteJSON(w, fmt.Errorf("scan message: %w", err))
			return
		}
		if err := json.Unmarshal(sources, &m.Sources); err != nil || m.Sources == nil {
			m.Sources = []json.RawMessage{}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("read messages: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"session_id": sid, "messages": out})
}

// insertMessage stores a message with an explicit id (clock_timestamp keeps the order inside one request).
func insertMessage(ctx context.Context, q actions.DBTX, id, sessionID, author, text string, sources []json.RawMessage) error {
	if sources == nil {
		sources = []json.RawMessage{}
	}
	raw, err := json.Marshal(sources)
	if err != nil {
		return fmt.Errorf("encode sources: %w", err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO core.chat_messages (id, session_id, author, text, sources, created_at)
		VALUES ($1, $2, $3, $4, $5, clock_timestamp()) ON CONFLICT (id) DO NOTHING`, id, sessionID, author, text, raw); err != nil {
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
