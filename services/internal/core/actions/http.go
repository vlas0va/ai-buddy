package actions

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// Handler обслуживает GET /api/actions.
type Handler struct {
	pool *pgxpool.Pool
	svc  *Service
	log  *slog.Logger
}

// New возвращает HTTP-обработчик действий.
func New(pool *pgxpool.Pool, svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, svc: svc, log: log}
}

// Register добавляет GET /api/actions (за authn.Middleware).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/actions", h.list)
}

// list отдаёт собственные действия вызывающего, новые сначала; они сохраняются после перезагрузки (R66).
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	items, err := h.svc.List(r.Context(), h.pool, c.Sub)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}
