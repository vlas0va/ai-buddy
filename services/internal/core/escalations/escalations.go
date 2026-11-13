// Пакет escalations позволяет стажёру отправить человеку (наставнику, руководителю, HR BP, куратору) вопрос,
// подготовленный buddy, позволяет адресату ответить и при желании публикует ответ в базу знаний команды (S2, R10).
//
// Права (R71): обращение видят только отправитель и адресат; ответить может только адресат. Чужие или неизвестные id
// отвечают 403.
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/flags"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// FAQWriter публикует отвеченный вопрос в базу знаний команды (kb.add_faq). Реализован в MCP-клиенте ядра, который сам
// выпускает токен подтверждения; mentorToken — исходный JWT отвечающего пользователя, idempotencyKey — id обращения.
type FAQWriter interface {
	AddFAQ(ctx context.Context, mentorToken, title, body, scope, idempotencyKey string) error
}

// FlagChecker сообщает, включён ли runtime-флаг для команды (реализован в flags.Service).
type FlagChecker interface {
	Enabled(ctx context.Context, key, team string) bool
}

// Handler обслуживает эндпоинты обращений.
type Handler struct {
	pool    *pgxpool.Pool
	notify  notify.Store
	emitter *analytics.Emitter
	flags   FlagChecker
	faq     FAQWriter
	log     *slog.Logger
}

// New возвращает HTTP-обработчик обращений. emitter, faq и log могут быть nil (nil faq — публикация в базу знаний
// выключена).
func New(pool *pgxpool.Pool, emitter *analytics.Emitter, flags FlagChecker, faq FAQWriter, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, emitter: emitter, flags: flags, faq: faq, log: log}
}

// Register добавляет маршруты обращений (нужна авторизация).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/escalations", h.create)
	mux.HandleFunc("GET /api/escalations", h.list)
	mux.HandleFunc("POST /api/escalations/{escalation_id}/answer", h.answer)
	mux.HandleFunc("GET /api/escalations/queue", h.queue)
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const maxText = 4000

// Escalation — это openapi Escalation.
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

// target находит адресата: наставника отправителя, руководителя команды отправителя или первого пользователя с этой
// ролью.
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
		MessageID *string `json:"message_id"` // сообщение ассистента, черновик из которого отправляется (отзыв 12.11)
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
	var message any
	if body.MessageID != nil {
		if !uuidRE.MatchString(*body.MessageID) {
			apierr.WriteJSON(w, apierr.Validation().WithMessage("Некорректный message_id"))
			return
		}
		var sid string
		err := h.pool.QueryRow(ctx, `SELECT m.session_id::text FROM core.chat_messages m JOIN core.chat_sessions s ON s.id = m.session_id
			WHERE m.id = $1 AND s.user_id = $2 AND m.author = 'assistant'`, *body.MessageID, c.Sub).Scan(&sid)
		if errors.Is(err, pgx.ErrNoRows) {
			apierr.WriteJSON(w, apierr.Forbidden())
			return
		}
		if err != nil {
			apierr.WriteJSON(w, fmt.Errorf("check message: %w", err))
			return
		}
		message, session = *body.MessageID, sid
	}
	to, err := h.target(ctx, c, body.ToRole)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	out, err := h.insert(ctx, c, to, body.ToRole, text, session, message)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) insert(ctx context.Context, c authn.Claims, to, toRole, text string, session, message any) (Escalation, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return Escalation{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO core.escalations (from_user_id, to_user_id, to_role, session_id, text, message_id)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`, c.Sub, to, toRole, session, text, message).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Escalation{}, apierr.Conflict().WithMessage("Это сообщение уже отправлено")
	}
	if err != nil {
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
		KBScope string `json:"kb_scope"` // team (по умолчанию) | all: общий ответ для всех команд
		// остальные вопросы той же группы списка (организаторы 13.11): один ответ закрывает их все
		AlsoIDs []string `json:"also_ids"`
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
	answered := []string{id}
	for _, other := range body.AlsoIDs {
		if len(answered) > 100 {
			break
		}
		if other == id || !uuidRE.MatchString(other) || contains(answered, other) {
			continue
		}
		// вопрос другого адресата или уже отвеченный за это время пропускается, остальная группа получает ответ
		if _, err := h.saveAnswer(ctx, c, other, text); err == nil {
			answered = append(answered, other)
		}
	}
	added := false
	if body.AddToKB && h.faq != nil && h.flags != nil && h.flags.Enabled(ctx, flags.EscalationToKB, c.TeamID) {
		token, _ := authn.TokenFromContext(ctx)
		// FAQ — для любого стажёра: без имён и дат (чекпоинт 11.11).
		names := h.userNames(ctx)
		q, a := generalize(question, names), generalize(text, names)
		title := faqTitle(q)
		// Ответ уже сохранён; сбой базы знаний не должен его потерять, поэтому он логируется и отражается как
		// added_to_kb=false.
		scope := "team"
		if body.KBScope == "all" {
			scope = "all"
		}
		if err := h.faq.AddFAQ(ctx, token, title, "Вопрос: "+q+"\n\nОтвет: "+a, scope, id); err != nil {
			h.log.Warn("kb.add_faq failed", "escalation_id", id, "err", err)
		} else {
			added = true
		}
	}
	if added {
		if _, err := h.pool.Exec(ctx, `UPDATE core.escalations SET added_to_kb = true WHERE id = ANY($1::uuid[])`, answered); err != nil {
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

// saveAnswer сохраняет ответ и уведомляет отправителя; возвращает текст вопроса.
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
		return "", apierr.Forbidden() // ответить может только адресат; неизвестные id выглядят так же
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

// faqTitle — первая строка вопроса, сокращённая до 80 символов. userNames перечисляет отображаемые имена всех
// пользователей, чтобы убрать их из FAQ; при ошибке FAQ получает только маскирование дат.
func (h *Handler) userNames(ctx context.Context) []string {
	rows, err := h.pool.Query(ctx, `SELECT display_name FROM core.users`)
	if err != nil {
		h.log.Warn("load user names", "err", err)
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}

func faqTitle(question string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(question), "\n", 2)[0])
	r := []rune(line)
	if len(r) > 80 {
		return strings.TrimSpace(string(r[:79])) + "…"
	}
	return line
}
