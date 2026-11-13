// Пакет chat обслуживает API чата (S2, S3, S4): сессии, сообщения, которые приходят от агента потоком SSE, и
// подтверждение черновиков записи (contracts/agent-stream.md, contracts/openapi.yaml).
//
// Ход-сообщение: ядро сохраняет сообщение пользователя, собирает user_context из проверенных claims JWT и своей базы
// (R71), вызывает агента и пересылает его события в браузер без изменений и по порядку (неизвестные тоже; tool_result и
// присланный агентом action_result — никогда), по пути сохраняя: токены становятся сообщением ассистента, источники
// сохраняются вместе с ним, action_proposal сохраняется в core.proposals (только для разрешённых инструментов записи).
// done агента пересылается с message_id сохранённого сообщения ассистента (ядро само отправляет done{message_id}, если
// агент этого не сделал) и питает chat.answered / chat.refused / chat.escalated.
//
// Ход подтверждения: ядро проверяет черновик (владелец, сессия, статус, срок), подписывает для вызывающего токен
// подтверждения по draft_hash (key = proposal_id) и отправляет агенту ход подтверждения. Агент вызывает запись в MCP и
// отвечает внутренним событием tool_result, которое ядро сохраняет (core.actions + задача плана, R66) и превращает в
// action_result. Повторное подтверждение выполненного черновика воспроизводит сохранённый результат, никого не вызывая
// (R79).
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

// Agent открывает ход в сервисе агента; реализован в *agentclient.Client.
type Agent interface {
	Turn(ctx context.Context, userToken string, req agentclient.TurnRequest) (*agentclient.Stream, error)
}

// Limiter — ограничитель частоты чата; реализован в *ratelimit.Limiter (при ошибке пропускает: true с ошибкой).
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// FlagSource выдаёт runtime-флаги команды; реализован в *flags.Service.
type FlagSource interface {
	ForTeam(ctx context.Context, team string) (map[string]bool, error)
}

// Emitter пишет аналитические события; реализован в *analytics.Emitter (nil *analytics.Emitter отбрасывает события).
type Emitter interface {
	Emit(ctx context.Context, q analytics.DBTX, ev analytics.Event) error
}

// MCP выполняет подтверждённую запись с токеном подтверждения, подписанным ядром; реализован в *mcpclient.Client. Ядро
// через него перечитывает approvals.decide руководителя по ключу идемпотентности, а не верит tool_result агента (R71).
type MCP interface {
	Execute(ctx context.Context, userToken, tool string, args map[string]any, key string) (json.RawMessage, error)
}

// Значения Config по умолчанию.
const (
	DefaultProposalTTL     = 30 * time.Minute // так же, как у агента (PROPOSAL_TTL_S) и по умолчанию в core.proposals
	DefaultConfirmationTTL = 5 * time.Minute
	DefaultRatePerMin      = 20
	historyLimit           = 10
	maxTextRunes           = 4000
	openTasksLimit         = 50
	// Ограничения того, что один ход агента может заставить ядро переслать и сохранить (сам поток агента ограничен в
	// agentclient: 256 КиБ на событие, 4 МиБ на ответ). При превышении ход заканчивается upstream_unavailable.
	maxTurnText   = 64 << 10 // текст ассистента (токены и дополнение) за ход, байт
	maxSources    = 50       // источников за ход
	maxDraftBytes = 16 << 10 // сохранённый черновик
)

// Config хранит настройки чата.
type Config struct {
	ConfirmationSecret []byte        // CONFIRMATION_HMAC_SECRET (обязателен для подтверждения)
	ProposalTTL        time.Duration // сколько черновик можно подтверждать; <= 0 — DefaultProposalTTL
	ConfirmationTTL    time.Duration // exp токена подтверждения; <= 0 — DefaultConfirmationTTL
	RatePerMin         int           // CHAT_RATE_PER_MIN; 0 — DefaultRatePerMin, < 0 — без ограничения
	KeepAlive          time.Duration // интервал пингов SSE; <= 0 — httpx.DefaultKeepAlive
}

// Deps — зависимости обработчика чата. Pool, Agent и Actions обязательны.
type Deps struct {
	Pool    *pgxpool.Pool
	Agent   Agent
	Actions *actions.Service
	Limiter Limiter    // nil: без ограничения частоты
	Flags   FlagSource // nil: без флагов в user_context
	Emitter Emitter    // nil: без аналитики
	MCP     MCP        // nil: решения руководителя в чате (approval_decision) отклоняются
	Log     *slog.Logger
}

// Handler обслуживает эндпоинты чата.
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
	// Now — часы (срок черновика, текущий этап); тесты могут их подменить.
	Now func() time.Time
}

// New возвращает HTTP-обработчик чата.
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

// Register добавляет маршруты чата (за authn.Middleware).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/chat/sessions", h.createSession)
	mux.HandleFunc("GET /api/chat/sessions", h.listSessions)
	mux.HandleFunc("GET /api/chat/sessions/{session_id}", h.getSession)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/messages", h.postMessage)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/proposals/{proposal_id}/confirm", h.confirm)
	mux.HandleFunc("POST /api/chat/sessions/{session_id}/proposals/{proposal_id}/cancel", h.cancel)
	mux.HandleFunc("GET /api/kb/{doc_id}", h.kbDoc)
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// caller возвращает проверенные claims и исходный токен запроса или отвечает 401.
func caller(w http.ResponseWriter, r *http.Request) (authn.Claims, string, bool) {
	c, ok := authn.FromContext(r.Context())
	token, tok := authn.TokenFromContext(r.Context())
	if !ok || !tok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return authn.Claims{}, "", false
	}
	return c, token, true
}

// ownSession проверяет, что sessionID — сессия userID: некорректный id — not_found, неизвестный или чужой — forbidden
// (существование не раскрывается, R71).
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

// SessionSummary — один из прежних разговоров в списке диалогов (отзыв 12.11: «Новый диалог» сохраняет старый).
type SessionSummary struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// listSessions возвращает разговоры вызывающего, в которых есть сообщения, новые сначала; заголовок — первый вопрос.
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	c, _, ok := caller(w, r)
	if !ok {
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT s.id::text,
		       coalesce((SELECT left(m.text, 80) FROM core.chat_messages m WHERE m.session_id = s.id AND m.author = 'user'
		                 AND m.text <> '' ORDER BY m.created_at, m.id LIMIT 1), 'Диалог'),
		       to_char(s.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(max(m.created_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM core.chat_sessions s JOIN core.chat_messages m ON m.session_id = s.id
		WHERE s.user_id = $1
		GROUP BY s.id ORDER BY max(m.created_at) DESC LIMIT 30`, c.Sub)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("list sessions: %w", err))
		return
	}
	defer rows.Close()
	out := []SessionSummary{}
	for rows.Next() {
		var s SessionSummary
		if err := rows.Scan(&s.SessionID, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			apierr.WriteJSON(w, fmt.Errorf("scan session: %w", err))
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("read sessions: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// Message — это openapi ChatMessage.
type Message struct {
	ID          string            `json:"id"`
	Author      string            `json:"author"`
	Text        string            `json:"text"`
	Sources     []json.RawMessage `json:"sources"`
	Attachments []attachmentMeta  `json:"attachments"`
	CreatedAt   string            `json:"created_at"`
	// карточки, восстанавливаемые после ухода из чата (отзыв 11.11); состояние черновика и результат берутся из таблиц
	// ядра
	Proposal      json.RawMessage `json:"proposal,omitempty"`
	ProposalState string          `json:"proposal_state,omitempty"`
	// несколько черновиков одного сообщения (список доступов с галочками), у каждого своё состояние
	Proposals      []historyProposal `json:"proposals,omitempty"`
	Result         *actions.Result   `json:"result,omitempty"`
	Escalation     json.RawMessage   `json:"escalation,omitempty"`
	EscalationSent bool              `json:"escalation_sent,omitempty"` // после черновика было отправлено сообщение этой роли
	Refusal        json.RawMessage   `json:"refusal,omitempty"`
	NextStep       json.RawMessage   `json:"next_step,omitempty"`
	DatePicker     json.RawMessage   `json:"date_picker,omitempty"`
}

// attachmentMeta — то, что история отдаёт о вложении: никогда не текст, чтобы перезагрузка оставалась лёгкой.
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
		m.Escalation, m.Refusal, m.NextStep, m.DatePicker = cards["escalation"], cards["refusal"], cards["next_step"], cards["date_picker"]
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
	// карточки результата с текущим статусом действия, о котором они сообщали (видно и согласование, сделанное позже)
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

// restoreProposals сопоставляет сохранённые черновики с состоянием их черновиков в ядре; незнакомый ядру черновик
// отбрасывается.
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

// KBReader читает один документ базы знаний через агента (с его проверкой ACL); реализован в *agentclient.Client.
type KBReader interface {
	KBDoc(ctx context.Context, userToken, docID string) (json.RawMessage, bool, error)
}

var docIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// kbDoc открывает чип источника из ответа: тестовый документ, только если пользователю можно его читать (R69, R71).
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

// proposalState — то, что карточка чата показывает для сохранённого черновика.
func proposalState(status string) string {
	switch status {
	case "executed", "confirmed":
		return "confirmed"
	case "cancelled", "expired":
		return "cancelled"
	}
	return "pending" // pending или failed: карточку ещё можно отправить
}

// insertMessage сохраняет сообщение с явным id (clock_timestamp сохраняет порядок внутри одного запроса).
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

// emit записывает аналитическое событие; сбои логируются и никогда не показываются пользователю.
func (h *Handler) emit(ctx context.Context, c authn.Claims, name string, props map[string]any) {
	if h.emitter == nil {
		return
	}
	if err := h.emitter.Emit(ctx, h.pool, analytics.Event{Name: name, UserID: c.Sub, Role: c.Role, TeamID: c.TeamID, Props: props}); err != nil {
		h.log.Warn("analytics event failed", "name", name, "err", err)
	}
}

// newUUID возвращает случайный UUID v4.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand не падает на поддерживаемых платформах
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// streamError — data события error.
type streamError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Русские тексты ошибок, которые ядро отправляет в поток.
const (
	msgAgentUnavailable = "Ассистент временно недоступен, попробуйте ещё раз"
	msgNoResult         = "Не удалось получить результат заявки, попробуйте ещё раз"
	msgSaveFailed       = "Заявка отправлена, но результат не сохранён. Повторите подтверждение, повторной заявки не будет"
	msgBadProposal      = "Ассистент предложил действие, которое нельзя выполнить через чат"
	msgTooLarge         = "Ответ ассистента слишком большой, попробуйте ещё раз"
	msgBadResult        = "Не удалось подтвердить результат заявки, попробуйте ещё раз"
)

// agentError превращает неудачный вызов агента в событие error потока: если агент ответил 4xx с JSON-ошибкой,
// сохраняются его код и текст; всё остальное — upstream_unavailable с возможностью повтора.
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

// errTooLarge завершает ход, содержимое которого превышает ограничения (R76).
var errTooLarge = streamError{Code: string(apierr.CodeUpstreamUnavailable), Message: msgTooLarge, Retryable: true}

// exactObject декодирует JSON-объект и отклоняет ключи, отличающиеся от одного из want только регистром: декодирование
// структур Go сопоставило бы их без учёта регистра, а агент читает точный ключ, поэтому обе стороны должны видеть одно
// и то же значение.
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

// str возвращает строковое значение key в m ("", если ключа нет или это не строка).
func str(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}
