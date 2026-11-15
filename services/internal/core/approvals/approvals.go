// Пакет approvals обслуживает очередь согласований руководителя (S3 шаг 8, S4): GET /api/approvals отдаёт заявки,
// ожидающие вызывающего (approvals.list_pending), а POST /api/approvals/{approval_id}/decision решает одну
// (approvals.decide). Нажатие кнопки и есть подтверждение, поэтому ядро само подписывает запись (key =
// Idempotency-Key), а затем обновляет действие заявителя, связанную задачу плана и уведомления (R66).
package approvals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/actions"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// MCP — часть *mcpclient.Client, которая здесь используется.
type MCP interface {
	ListPendingApprovals(ctx context.Context, userToken string) ([]mcpclient.Approval, error)
	DecideApproval(ctx context.Context, userToken, approvalID, decision, comment, key string) (mcpclient.Approval, error)
}

// Emitter пишет аналитические события; реализован в *analytics.Emitter.
type Emitter interface {
	Emit(ctx context.Context, q analytics.DBTX, ev analytics.Event) error
}

// Approval — это openapi Approval.
type Approval struct {
	ID                   string  `json:"id"`
	Kind                 string  `json:"kind"`
	RequesterDisplayName string  `json:"requester_display_name"`
	Summary              string  `json:"summary"`
	Status               string  `json:"status"`
	Comment              *string `json:"comment"`
	Stage                string  `json:"stage"` // lead | hr_admin (отсутствие: HR-администратор решает после руководителя)
	CreatedAt            string  `json:"created_at"`
	// TeamAbsent: коллеги, отсутствующие в те же дни (отзыв 12.13)
	TeamAbsent []string `json:"team_absent,omitempty"`
}

// Handler обслуживает эндпоинты согласований.
type Handler struct {
	pool    *pgxpool.Pool
	mcp     MCP
	actions *actions.Service
	emitter Emitter
	log     *slog.Logger
}

// New возвращает HTTP-обработчик согласований; emitter и log могут быть nil.
func New(pool *pgxpool.Pool, mcp MCP, acts *actions.Service, emitter Emitter, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, mcp: mcp, actions: acts, emitter: emitter, log: log}
}

// Register добавляет GET /api/approvals и POST /api/approvals/{approval_id}/decision (за authn.Middleware).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/approvals", h.list)
	mux.HandleFunc("POST /api/approvals/{approval_id}/decision", h.decide)
}

var (
	uuidRE       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	approvalIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// lead возвращает вызывающего, если это руководитель или HR-администратор (второй этап отсутствия) с токеном; остальным
// ролям — forbidden (R71). Какие пункты вызывающий может решать, проверяет сервис согласований (approver_id).
func lead(w http.ResponseWriter, r *http.Request) (authn.Claims, string, bool) {
	c, ok := authn.FromContext(r.Context())
	token, tok := authn.TokenFromContext(r.Context())
	switch {
	case !ok || !tok:
		apierr.WriteJSON(w, apierr.Unauthorized())
	case c.Role != "lead" && c.Role != "hr_admin":
		apierr.WriteJSON(w, apierr.Forbidden())
	default:
		return c, token, true
	}
	return authn.Claims{}, "", false
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	_, token, ok := lead(w, r)
	if !ok {
		return
	}
	items, err := h.mcp.ListPendingApprovals(r.Context(), token)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	out := make([]Approval, 0, len(items))
	for _, it := range items {
		a, err := h.toAPI(r.Context(), it)
		if err != nil {
			apierr.WriteJSON(w, err)
			return
		}
		out = append(out, a)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	c, token, ok := lead(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !uuidRE.MatchString(key) {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Нужен заголовок Idempotency-Key в формате UUID"))
		return
	}
	id := r.PathValue("approval_id")
	if !approvalIDRE.MatchString(id) {
		apierr.WriteJSON(w, apierr.NotFound())
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}
	if err := httpx.ReadJSONLimit(w, r, &body, 16<<10); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if body.Decision != "approved" && body.Decision != "rejected" {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Решение: approved или rejected"))
		return
	}
	if utf8.RuneCountInString(body.Comment) > 1000 {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Комментарий: не больше 1000 символов"))
		return
	}
	res, err := h.mcp.DecideApproval(r.Context(), token, id, body.Decision, strings.TrimSpace(body.Comment), key)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	// Решение записано сервисом согласований; отражаем его в ядре, даже если клиент уже ушёл.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	if err := h.apply(ctx, c, res); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	out, err := h.toAPI(ctx, res)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// apply обновляет действие и задачу плана заявителя и уведомляет его в одной транзакции. Заявки, созданные не через
// чат, не имеют действия в ядре и пропускаются.
func (h *Handler) apply(ctx context.Context, c authn.Claims, res mcpclient.Approval) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Только действие заявителя, в заявке которого согласующим указан решающий руководитель (R71).
	a, changed, err := h.actions.ApplyDecision(ctx, tx, res.Kind, res.RefID, res.Status,
		actions.Scope{RequesterID: res.RequesterID, ApproverID: c.Sub, Stage: res.Stage, Comment: res.Comment})
	switch {
	case errors.Is(err, actions.ErrNotFound):
		h.log.Info("decision for a request without a core action", "approval", res.ID, "ref", res.RefID)
		return nil
	case apierr.HasCode(err, apierr.CodeValidation):
		h.log.Warn("unexpected approval result", "approval", res.ID, "status", res.Status, "kind", res.Kind)
		return nil
	case err != nil:
		return fmt.Errorf("apply decision: %w", err)
	}
	if changed && h.emitter != nil {
		if err := h.emitter.Emit(ctx, tx, analytics.Event{Name: "action.status_changed", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
			Props: map[string]any{"kind": a.Kind, "action_id": a.ID, "status": a.Status}}); err != nil {
			return fmt.Errorf("emit action.status_changed: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// toAPI переводит согласование из MCP в форму openapi и подставляет имя заявителя из ядра, если MCP его не прислал.
func (h *Handler) toAPI(ctx context.Context, it mcpclient.Approval) (Approval, error) {
	stage := it.Stage
	if stage == "" {
		stage = "lead"
	}
	a := Approval{ID: it.ID, Kind: it.Kind, Stage: stage, RequesterDisplayName: it.RequesterDisplayName, Summary: it.Summary,
		Status: it.Status, CreatedAt: it.CreatedAt, TeamAbsent: it.TeamAbsent}
	if it.Comment != "" {
		comment := it.Comment
		a.Comment = &comment
	}
	if a.RequesterDisplayName == "" {
		err := h.pool.QueryRow(ctx, `SELECT COALESCE((SELECT display_name FROM core.users WHERE id = $1), '')`, it.RequesterID).
			Scan(&a.RequesterDisplayName)
		if err != nil {
			return a, fmt.Errorf("load requester name: %w", err)
		}
	}
	return a, nil
}
