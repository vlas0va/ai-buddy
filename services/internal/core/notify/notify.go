// Пакет notify хранит уведомления пользователей (напоминания AI-cron, смена статусов, ответы на обращения; R54) и
// обслуживает GET /api/notifications и POST /api/notifications/{id}/read.
//
// Другие пакеты создают уведомления через Store.Create, передавая пул или pgx.Tx (всё, у чего есть
// Exec/Query/QueryRow), поэтому уведомление можно записать в той же транзакции, что и вызвавшее его изменение.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// DBTX — подмножество pgxpool.Pool и pgx.Tx, которое здесь используется.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Виды уведомлений (openapi Notification.kind).
const (
	KindReminder         = "reminder"
	KindOverdue          = "overdue"
	KindStatusChange     = "status_change"
	KindEscalationAnswer = "escalation_answer"
)

// ErrInvalid возвращает Create для уведомления, которое нельзя сохранить (пустой пользователь или заголовок,
// неизвестный вид).
var ErrInvalid = errors.New("notify: invalid notification")

// Notification — вход Store.Create. Пустые Body, ChecklistItemID и DedupeKey сохраняются как NULL.
type Notification struct {
	UserID          string
	Kind            string
	Title           string
	Body            string
	ChecklistItemID string
	// DedupeKey делает уведомление уникальным по (UserID, DedupeKey): второй Create с той же парой ничего не делает.
	DedupeKey string
}

// Store пишет уведомления. Нулевое значение готово к использованию.
type Store struct{}

// Create вставляет n через q. Повтор (UserID, DedupeKey) молча игнорируется (ON CONFLICT DO NOTHING).
func (s Store) Create(ctx context.Context, q DBTX, n Notification) error {
	_, err := s.CreateIfNew(ctx, q, n)
	return err
}

// CreateIfNew — это Create, который ещё и сообщает, вставлена ли строка (false — сработала дедупликация).
func (Store) CreateIfNew(ctx context.Context, q DBTX, n Notification) (bool, error) {
	switch n.Kind {
	case KindReminder, KindOverdue, KindStatusChange, KindEscalationAnswer:
	default:
		return false, fmt.Errorf("%w: kind %q", ErrInvalid, n.Kind)
	}
	if n.UserID == "" || n.Title == "" {
		return false, fmt.Errorf("%w: user and title are required", ErrInvalid)
	}
	tag, err := q.Exec(ctx, `INSERT INTO core.notifications (user_id, kind, title, body, checklist_item_id, dedupe_key)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''))
		ON CONFLICT DO NOTHING`, n.UserID, n.Kind, n.Title, n.Body, n.ChecklistItemID, n.DedupeKey)
	if err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Handler обслуживает эндпоинты уведомлений.
type Handler struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// New возвращает HTTP-обработчик уведомлений.
func New(pool *pgxpool.Pool, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, log: log}
}

// Register добавляет GET /api/notifications и POST /api/notifications/{notification_id}/read (нужна авторизация).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notifications", h.list)
	mux.HandleFunc("POST /api/notifications/{notification_id}/read", h.markRead)
}

type item struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	Title           string  `json:"title"`
	Body            *string `json:"body"`
	ChecklistItemID *string `json:"checklist_item_id"`
	Read            bool    `json:"read"`
	CreatedAt       string  `json:"created_at"`
}

const listLimit = 100

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	rows, err := h.pool.Query(r.Context(), `SELECT id::text, kind, title, body, checklist_item_id, read_at IS NOT NULL,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM core.notifications WHERE user_id = $1 ORDER BY created_at DESC, id LIMIT $2`, c.Sub, listLimit)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("list notifications: %w", err))
		return
	}
	defer rows.Close()
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Kind, &it.Title, &it.Body, &it.ChecklistItemID, &it.Read, &it.CreatedAt); err != nil {
			apierr.WriteJSON(w, fmt.Errorf("scan notification: %w", err))
			return
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("read notifications: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// markRead помечает своё уведомление прочитанным (идемпотентно). Чужие или неизвестные id отвечают 403, чтобы не
// раскрывать существование.
func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	id := r.PathValue("notification_id")
	if !uuidRE.MatchString(id) {
		apierr.WriteJSON(w, apierr.NotFound())
		return
	}
	tag, err := h.pool.Exec(r.Context(), `UPDATE core.notifications SET read_at = COALESCE(read_at, now())
		WHERE id = $1 AND user_id = $2`, id, c.Sub)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("mark notification read: %w", err))
		return
	}
	if tag.RowsAffected() == 0 {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
