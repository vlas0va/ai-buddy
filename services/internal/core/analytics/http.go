package analytics

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// Handler serves POST /api/events and POST /api/feedback.
type Handler struct {
	pool    *pgxpool.Pool
	emitter *Emitter
	log     *slog.Logger
}

// New returns the analytics HTTP handler.
func New(pool *pgxpool.Pool, emitter *Emitter, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, emitter: emitter, log: log}
}

// Register adds POST /api/events and POST /api/feedback (auth required).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/events", h.events)
	mux.HandleFunc("POST /api/feedback", h.feedback)
}

var (
	uiNameRE = regexp.MustCompile(`^ui\.[a-z_.]+$`)
	uuidRE   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const maxBatch = 100

type clientEvent struct {
	Name  string         `json:"name"`
	TS    string         `json:"ts"`
	Props map[string]any `json:"props"`
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	var body struct {
		Events []clientEvent `json:"events"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if len(body.Events) > maxBatch {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Слишком много событий в одном запросе (не более 100)"))
		return
	}
	now := time.Now()
	events := make([]Event, 0, len(body.Events))
	for _, ce := range body.Events {
		if !uiNameRE.MatchString(ce.Name) {
			apierr.WriteJSON(w, apierr.Validation().WithMessage("Недопустимое имя события"))
			return
		}
		ts, err := time.Parse(time.RFC3339, ce.TS)
		if err != nil {
			apierr.WriteJSON(w, apierr.Validation().WithMessage("Время события должно быть в формате RFC 3339"))
			return
		}
		// A client clock far in the future would corrupt time series: fall back to server time.
		if ts.After(now.Add(5 * time.Minute)) {
			ts = now
		}
		events = append(events, Event{Name: ce.Name, UserID: c.Sub, Role: c.Role, TeamID: c.TeamID, Props: ce.Props, TS: ts})
	}
	if err := h.emitAll(r, events); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) emitAll(r *http.Request, events []Event) error {
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	for _, ev := range events {
		if err := h.emitter.Emit(r.Context(), tx, ev); err != nil {
			return err
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (h *Handler) feedback(w http.ResponseWriter, r *http.Request) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	var body struct {
		MessageID string  `json:"message_id"`
		Useful    *bool   `json:"useful"`
		Comment   *string `json:"comment"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if !uuidRE.MatchString(body.MessageID) || body.Useful == nil {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Укажите message_id и useful"))
		return
	}
	comment := ""
	if body.Comment != nil {
		comment = strings.TrimSpace(*body.Comment)
	}
	if len([]rune(comment)) > 1000 {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Комментарий не длиннее 1000 символов"))
		return
	}
	ctx := r.Context()
	// Only assistant messages of the caller's own sessions can be rated; anything else is 403 (R71).
	var owned bool
	err := h.pool.QueryRow(ctx, `SELECT true FROM core.chat_messages m JOIN core.chat_sessions s ON s.id = m.session_id
		WHERE m.id = $1 AND s.user_id = $2 AND m.author = 'assistant'`, body.MessageID, c.Sub).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("check message owner: %w", err))
		return
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("begin: %w", err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO core.feedback (message_id, user_id, useful, comment)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (message_id, user_id) DO UPDATE SET useful = EXCLUDED.useful, comment = EXCLUDED.comment, created_at = now()`,
		body.MessageID, c.Sub, *body.Useful, comment); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("save feedback: %w", err))
		return
	}
	if err := h.emitter.Emit(ctx, tx, Event{Name: "chat.feedback", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
		Props: map[string]any{"message_id": body.MessageID, "useful": *body.Useful}}); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("commit: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
