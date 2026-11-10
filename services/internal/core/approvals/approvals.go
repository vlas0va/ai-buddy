// Package approvals serves the lead's approval queue (S3 step 8, S4): GET /api/approvals lists the requests waiting
// for the caller (approvals.list_pending) and POST /api/approvals/{approval_id}/decision decides one
// (approvals.decide). The button press is the confirmation, so core signs the write itself (key = Idempotency-Key)
// and then updates the requester's action, linked plan task and notifications (R66).
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

// MCP is the part of *mcpclient.Client used here.
type MCP interface {
	ListPendingApprovals(ctx context.Context, userToken string) ([]mcpclient.Approval, error)
	DecideApproval(ctx context.Context, userToken, approvalID, decision, comment, key string) (mcpclient.Approval, error)
}

// Emitter records analytics events; *analytics.Emitter implements it.
type Emitter interface {
	Emit(ctx context.Context, q analytics.DBTX, ev analytics.Event) error
}

// Approval is the openapi Approval.
type Approval struct {
	ID                   string  `json:"id"`
	Kind                 string  `json:"kind"`
	RequesterDisplayName string  `json:"requester_display_name"`
	Summary              string  `json:"summary"`
	Status               string  `json:"status"`
	Comment              *string `json:"comment"`
	CreatedAt            string  `json:"created_at"`
}

// Handler serves the approval endpoints.
type Handler struct {
	pool    *pgxpool.Pool
	mcp     MCP
	actions *actions.Service
	emitter Emitter
	log     *slog.Logger
}

// New returns the approvals HTTP handler; emitter and log may be nil.
func New(pool *pgxpool.Pool, mcp MCP, acts *actions.Service, emitter Emitter, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, mcp: mcp, actions: acts, emitter: emitter, log: log}
}

// Register adds GET /api/approvals and POST /api/approvals/{approval_id}/decision (behind authn.Middleware).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/approvals", h.list)
	mux.HandleFunc("POST /api/approvals/{approval_id}/decision", h.decide)
}

var (
	uuidRE       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	approvalIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// lead returns the caller when it is a lead with a token; other roles get forbidden (R71).
func lead(w http.ResponseWriter, r *http.Request) (authn.Claims, string, bool) {
	c, ok := authn.FromContext(r.Context())
	token, tok := authn.TokenFromContext(r.Context())
	switch {
	case !ok || !tok:
		apierr.WriteJSON(w, apierr.Unauthorized())
	case c.Role != "lead":
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
	// The decision is recorded by the approvals service; mirror it in core even if the client has gone away.
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

// apply updates the requester's action and plan task and notifies them, in one transaction. Requests that were
// not made through the chat have no action in core and are skipped.
func (h *Handler) apply(ctx context.Context, c authn.Claims, res mcpclient.Approval) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Only the requester's action whose request names the deciding lead as approver (R71).
	a, changed, err := h.actions.ApplyDecision(ctx, tx, res.Kind, res.RefID, res.Status,
		actions.Scope{RequesterID: res.RequesterID, ApproverID: c.Sub})
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

// toAPI maps an MCP approval onto the openapi shape, filling the requester's name from core when MCP omits it.
func (h *Handler) toAPI(ctx context.Context, it mcpclient.Approval) (Approval, error) {
	a := Approval{ID: it.ID, Kind: it.Kind, RequesterDisplayName: it.RequesterDisplayName, Summary: it.Summary,
		Status: it.Status, CreatedAt: it.CreatedAt}
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
