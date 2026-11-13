// Пакет authapi реализует вход через мок-IdP (POST /api/auth/login, публичный) и GET /api/me (R34, R52, R70). Личность
// берётся только из core.users; пароли — bcrypt-хеши, проверяются внутри Postgres через crypt().
package authapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/period"
)

// FlagSource выдаёт feature flags, видимые команде (реализован в flags.Service).
type FlagSource interface {
	ForTeam(ctx context.Context, team string) (map[string]bool, error)
}

// Handler обслуживает вход и /api/me.
type Handler struct {
	pool    *pgxpool.Pool
	secret  []byte
	ttl     time.Duration
	flags   FlagSource
	emitter *analytics.Emitter
	log     *slog.Logger
	// Now — часы (для тестов); по умолчанию time.Now.
	Now func() time.Time
}

// DefaultTTL — время жизни JWT, если в New передан ttl <= 0 (JWT_TTL по умолчанию).
const DefaultTTL = 12 * time.Hour

// New возвращает обработчик авторизации. emitter и log могут быть nil.
func New(pool *pgxpool.Pool, secret []byte, ttl time.Duration, flags FlagSource, emitter *analytics.Emitter, log *slog.Logger) *Handler {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, secret: secret, ttl: ttl, flags: flags, emitter: emitter, log: log, Now: time.Now}
}

// RegisterPublic добавляет POST /api/auth/login; монтировать ВНЕ authn.Middleware.
func (h *Handler) RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", h.login)
}

// Register добавляет GET /api/me (нужна авторизация).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me", h.me)
}

// User — схема openapi User.
type User struct {
	ID          string  `json:"id"`
	Login       string  `json:"login"`
	DisplayName string  `json:"display_name"`
	Role        string  `json:"role"`
	TeamID      string  `json:"team_id"`
	TeamName    string  `json:"team_name,omitempty"`
	CohortStart *string `json:"cohort_start,omitempty"`
}

const userSelect = `SELECT u.id, u.display_name, u.role, u.team_id, COALESCE(t.name, ''), u.cohort_start::text
	FROM core.users u LEFT JOIN directory.teams t ON t.id = u.team_id`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.DisplayName, &u.Role, &u.TeamID, &u.TeamName, &u.CohortStart)
	u.Login = u.ID
	return u, err
}

func badLogin() *apierr.Error {
	return apierr.Unauthorized().WithMessage("Неверный логин или пароль")
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

const maxCredLen = 128

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.ReadJSONLimit(w, r, &req, 4<<10); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if req.Login == "" || req.Password == "" || len(req.Login) > maxCredLen || len(req.Password) > maxCredLen {
		apierr.WriteJSON(w, badLogin())
		return
	}
	ctx := r.Context()
	// Пароль проверяется в SQL; неизвестный логин и неверный пароль неразличимы (общий 401, R52).
	u, err := scanUser(h.pool.QueryRow(ctx, userSelect+`
		WHERE u.id = $1 AND u.can_login AND u.password_hash = crypt($2, u.password_hash)`, req.Login, req.Password))
	if errors.Is(err, pgx.ErrNoRows) {
		// Тратим сопоставимое время на неизвестные логины.
		_, _ = h.pool.Exec(ctx, `SELECT crypt($1, gen_salt('bf', 8))`, req.Password)
		apierr.WriteJSON(w, badLogin())
		return
	}
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("login query: %w", err))
		return
	}
	claims := authn.Claims{Sub: u.ID, Role: u.Role, TeamID: u.TeamID}
	if u.CohortStart != nil {
		claims.CohortStart = *u.CohortStart
	}
	token, err := authn.Issue(h.secret, claims, h.ttl)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("issue token: %w", err))
		return
	}
	props := map[string]any{}
	if u.CohortStart != nil {
		if start, err := period.ParseDate(*u.CohortStart); err == nil {
			props["period"] = string(period.Current(start, h.Now()))
		}
	}
	if err := h.emitter.Emit(ctx, h.pool, analytics.Event{Name: "session.started", UserID: u.ID, Role: u.Role, TeamID: u.TeamID, Props: props}); err != nil {
		h.log.Warn("session.started event failed", "err", err) // аналитика не должна блокировать вход
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"token": token, "user": u})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	u, err := scanUser(h.pool.QueryRow(r.Context(), userSelect+` WHERE u.id = $1 AND u.can_login`, c.Sub))
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.WriteJSON(w, apierr.Unauthorized()) // токен пользователя, которого больше нет
		return
	}
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("me query: %w", err))
		return
	}
	fl, err := h.flags.ForTeam(r.Context(), u.TeamID)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("flags: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": u, "flags": fl})
}
