// Package escalations lets an intern send a question drafted by buddy to a person (mentor, lead, HR BP, curator),
// lets the addressee answer it and optionally publishes the answer to the team knowledge base (S2, R10).
//
// Rights (R71): an escalation is visible only to its sender and its addressee; only the addressee can answer.
// Foreign or unknown ids answer 403.
package escalations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/flags"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// FAQWriter publishes an answered question to the team knowledge base (kb.add_faq). Core's MCP client implements it
// and issues the confirmation token itself; mentorToken is the raw JWT of the answering user and idempotencyKey is
// the escalation id.
type FAQWriter interface {
	AddFAQ(ctx context.Context, mentorToken, title, body, idempotencyKey string) error
}

// FlagChecker tells whether a runtime flag is on for a team (flags.Service implements it).
type FlagChecker interface {
	Enabled(ctx context.Context, key, team string) bool
}

// Handler serves the escalation endpoints.
type Handler struct {
	pool    *pgxpool.Pool
	notify  notify.Store
	emitter *analytics.Emitter
	flags   FlagChecker
	faq     FAQWriter
	log     *slog.Logger
}

// New returns the escalation HTTP handler. emitter, faq and log may be nil (nil faq = KB publishing off).
func New(pool *pgxpool.Pool, emitter *analytics.Emitter, flags FlagChecker, faq FAQWriter, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, emitter: emitter, flags: flags, faq: faq, log: log}
}

// Register adds the escalation routes (auth required).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/escalations", h.create)
	mux.HandleFunc("GET /api/escalations", h.list)
	mux.HandleFunc("POST /api/escalations/{escalation_id}/answer", h.answer)
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const maxText = 4000

// Escalation is the openapi Escalation.
type Escalation struct {
	ID              string  `json:"id"`
	FromDisplayName string  `json:"from_display_name"`
	ToRole          string  `json:"to_role"`
	Text            string  `json:"text"`
	Status          string  `json:"status"`
	Answer          *string `json:"answer"`
	AddedToKB       bool    `json:"added_to_kb"`
	CreatedAt       string  `json:"created_at"`
}

const selectCols = `e.id::text, u.display_name, e.to_role, e.text, e.status, e.answer, e.added_to_kb,
	to_char(e.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`

func scanEsc(row pgx.Row) (Escalation, error) {
	var e Escalation
	err := row.Scan(&e.ID, &e.FromDisplayName, &e.ToRole, &e.Text, &e.Status, &e.Answer, &e.AddedToKB, &e.CreatedAt)
	return e, err
}

func claims(w http.ResponseWriter, r *http.Request) (authn.Claims, bool) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
	}
	return c, ok
}

// target finds the addressee: the mentor of the sender, the lead of the sender's team, or the first user of the role.
func (h *Handler) target(ctx context.Context, c authn.Claims, role string) (string, error) {
	var id string
	var err error
	switch role {
	case "mentor":
		err = h.pool.QueryRow(ctx, `SELECT user_id FROM core.plans WHERE intern_id = $1 AND plan_role = 'mentor' ORDER BY created_at, user_id LIMIT 1`, c.Sub).Scan(&id)
	case "lead":
		err = h.pool.QueryRow(ctx, `SELECT id FROM core.users WHERE role = 'lead' AND team_id = $1 ORDER BY id LIMIT 1`, c.TeamID).Scan(&id)
	default: // hrbp, curator
		err = h.pool.QueryRow(ctx, `SELECT id FROM core.users WHERE role = $1 ORDER BY id LIMIT 1`, role).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apierr.NotFound().WithMessage("Адресат не найден")
	}
	if err != nil {
		return "", fmt.Errorf("find addressee: %w", err)
	}
	return id, nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	if c.Role != "intern" {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	var body struct {
		ToRole    string  `json:"to_role"`
		Text      string  `json:"text"`
		SessionID *string `json:"session_id"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	switch body.ToRole {
	case "mentor", "lead", "hrbp", "curator":
	default:
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Адресат: mentor, lead, hrbp или curator"))
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || utf8.RuneCountInString(text) > maxText {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Текст обращения: от 1 до 4000 символов"))
		return
	}
	ctx := r.Context()
	var session any
	if body.SessionID != nil {
		if !uuidRE.MatchString(*body.SessionID) {
			apierr.WriteJSON(w, apierr.Validation().WithMessage("Некорректный session_id"))
			return
		}
		var own bool
		err := h.pool.QueryRow(ctx, `SELECT true FROM core.chat_sessions WHERE id = $1 AND user_id = $2`, *body.SessionID, c.Sub).Scan(&own)
		if errors.Is(err, pgx.ErrNoRows) {
			apierr.WriteJSON(w, apierr.Forbidden())
			return
		}
		if err != nil {
			apierr.WriteJSON(w, fmt.Errorf("check session: %w", err))
			return
		}
		session = *body.SessionID
	}
	to, err := h.target(ctx, c, body.ToRole)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	out, err := h.insert(ctx, c, to, body.ToRole, text, session)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) insert(ctx context.Context, c authn.Claims, to, toRole, text string, session any) (Escalation, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return Escalation{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO core.escalations (from_user_id, to_user_id, to_role, session_id, text)
		VALUES ($1, $2, $3, $4, $5) RETURNING id::text`, c.Sub, to, toRole, session, text).Scan(&id); err != nil {
		return Escalation{}, fmt.Errorf("insert escalation: %w", err)
	}
	if err := h.notify.Create(ctx, tx, notify.Notification{UserID: to, Kind: notify.KindReminder,
		Title: "Новый вопрос от стажёра", Body: "Откройте раздел обращений, чтобы ответить.", DedupeKey: "escalation:" + id}); err != nil {
		return Escalation{}, err
	}
	if err := h.emitter.Emit(ctx, tx, analytics.Event{Name: "escalation.sent", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
		Props: map[string]any{"to_role": toRole}}); err != nil {
		return Escalation{}, err
	}
	out, err := scanEsc(tx.QueryRow(ctx, `SELECT `+selectCols+` FROM core.escalations e JOIN core.users u ON u.id = e.from_user_id WHERE e.id = $1`, id))
	if err != nil {
		return Escalation{}, fmt.Errorf("reload escalation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Escalation{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	rows, err := h.pool.Query(r.Context(), `SELECT `+selectCols+` FROM core.escalations e JOIN core.users u ON u.id = e.from_user_id
		WHERE e.from_user_id = $1 OR e.to_user_id = $1 ORDER BY e.created_at DESC, e.id LIMIT 100`, c.Sub)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("list escalations: %w", err))
		return
	}
	defer rows.Close()
	out := []Escalation{}
	for rows.Next() {
		e, err := scanEsc(rows)
		if err != nil {
			apierr.WriteJSON(w, fmt.Errorf("scan escalation: %w", err))
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("read escalations: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) answer(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	id := r.PathValue("escalation_id")
	if !uuidRE.MatchString(id) {
		apierr.WriteJSON(w, apierr.NotFound())
		return
	}
	var body struct {
		Text    string `json:"text"`
		AddToKB bool   `json:"add_to_kb"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || utf8.RuneCountInString(text) > maxText {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Текст ответа: от 1 до 4000 символов"))
		return
	}
	question, err := h.saveAnswer(r.Context(), c, id, text)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	ctx := r.Context()
	added := false
	if body.AddToKB && h.faq != nil && h.flags != nil && h.flags.Enabled(ctx, flags.EscalationToKB, c.TeamID) {
		token, _ := authn.TokenFromContext(ctx)
		title := faqTitle(question)
		// The answer is already saved; a KB failure must not lose it, so it is logged and reported as added_to_kb=false.
		if err := h.faq.AddFAQ(ctx, token, title, "Вопрос: "+question+"\n\nОтвет: "+text, id); err != nil {
			h.log.Warn("kb.add_faq failed", "escalation_id", id, "err", err)
		} else {
			added = true
		}
	}
	if added {
		if _, err := h.pool.Exec(ctx, `UPDATE core.escalations SET added_to_kb = true WHERE id = $1`, id); err != nil {
			h.log.Error("mark added_to_kb failed", "escalation_id", id, "err", err)
		}
	}
	if err := h.emitter.Emit(ctx, h.pool, analytics.Event{Name: "escalation.answered", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
		Props: map[string]any{"added_to_kb": added}}); err != nil {
		h.log.Warn("escalation.answered event failed", "err", err)
	}
	out, err := scanEsc(h.pool.QueryRow(ctx, `SELECT `+selectCols+` FROM core.escalations e JOIN core.users u ON u.id = e.from_user_id WHERE e.id = $1`, id))
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("reload escalation: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// saveAnswer stores the answer and notifies the sender; it returns the question text.
func (h *Handler) saveAnswer(ctx context.Context, c authn.Claims, id, text string) (string, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var from, to, status, question string
	err = tx.QueryRow(ctx, `SELECT from_user_id, to_user_id, status, text FROM core.escalations WHERE id = $1 FOR UPDATE`, id).
		Scan(&from, &to, &status, &question)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && to != c.Sub) {
		return "", apierr.Forbidden() // only the addressee may answer; unknown ids look the same
	}
	if err != nil {
		return "", fmt.Errorf("select escalation: %w", err)
	}
	if status != "open" {
		return "", apierr.Conflict().WithMessage("На это обращение уже ответили")
	}
	if _, err := tx.Exec(ctx, `UPDATE core.escalations SET status = 'answered', answer = $2, answered_at = now() WHERE id = $1`, id, text); err != nil {
		return "", fmt.Errorf("update escalation: %w", err)
	}
	if err := h.notify.Create(ctx, tx, notify.Notification{UserID: from, Kind: notify.KindEscalationAnswer,
		Title: "Получен ответ на ваш вопрос", Body: "Откройте раздел обращений, чтобы прочитать ответ.", DedupeKey: "escalation_answer:" + id}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return question, nil
}

// faqTitle is the first line of the question, shortened to 80 characters.
func faqTitle(question string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(question), "\n", 2)[0])
	r := []rune(line)
	if len(r) > 80 {
		return strings.TrimSpace(string(r[:79])) + "…"
	}
	return line
}
