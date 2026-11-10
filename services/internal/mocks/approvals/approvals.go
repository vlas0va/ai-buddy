// Package approvals implements the mock "approvals" MCP server («Согласия»), shared by S3 (day off) and S4 (access).
// Data is synthetic (R68). Only the approver named on an item sees and decides it (R71); a decision updates the item
// and the underlying absence or access request in one transaction and is idempotent by key (R79).
package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
)

// Register binds every tool of the approvals contract to srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	if srv == nil || pool == nil {
		return errors.New("approvals: server and pool are required")
	}
	if srv.Name() != "approvals" {
		return fmt.Errorf("approvals: cannot register on server %q", srv.Name())
	}
	s := &service{pool: pool}
	srv.Handle("list_pending", s.listPending)
	srv.Handle("decide", s.decide)
	return nil
}

type service struct {
	pool *pgxpool.Pool
}

// item is one approval item as returned by the tools.
type item struct {
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	RefID                string `json:"ref_id"`
	RequesterID          string `json:"requester_id"`
	RequesterDisplayName string `json:"requester_display_name,omitempty"`
	Summary              string `json:"summary"`
	Status               string `json:"status"`
	Comment              string `json:"comment,omitempty"`
	CreatedAt            string `json:"created_at"`
	Replayed             *bool  `json:"replayed,omitempty"`

	approverID     string
	idempotencyKey string
}

const selectItem = `
SELECT i.id, i.kind, i.ref_id, i.requester_id, coalesce(e.display_name, ''), i.summary, i.status,
       coalesce(i.comment, ''), i.created_at, i.approver_id, coalesce(i.idempotency_key::text, '')
FROM approvals.items i
LEFT JOIN directory.employees e ON e.id = i.requester_id`

func scanItem(row pgx.CollectableRow) (item, error) {
	var (
		it      item
		created time.Time
	)
	err := row.Scan(&it.ID, &it.Kind, &it.RefID, &it.RequesterID, &it.RequesterDisplayName, &it.Summary, &it.Status,
		&it.Comment, &created, &it.approverID, &it.idempotencyKey)
	it.CreatedAt = created.UTC().Format(time.RFC3339)
	return it, err
}

type listOut struct {
	Items []item `json:"items"`
}

// listPending returns the pending items whose approver is the caller, oldest first.
func (s *service) listPending(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	rows, err := s.pool.Query(ctx, selectItem+`
WHERE i.approver_id = $1 AND i.status = 'pending'
ORDER BY i.created_at, i.id
LIMIT 200`, c.Sub)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanItem)
	if err != nil {
		return nil, fmt.Errorf("scan pending approvals: %w", err)
	}
	if items == nil {
		items = []item{}
	}
	return listOut{Items: items}, nil
}

type decideIn struct {
	ApprovalID     string `json:"approval_id"`
	Decision       string `json:"decision"`
	Comment        string `json:"comment"`
	IdempotencyKey string `json:"idempotency_key"`
}

// underlying maps an item kind and decision to the request table and its new status.
var underlying = map[string]struct {
	table    string
	statuses map[string]string
}{
	"absence_request": {"absence.requests", map[string]string{"approved": "approved", "rejected": "rejected"}},
	"access_request":  {"access.requests", map[string]string{"approved": "granted", "rejected": "rejected"}},
	"trip_request":    {"trips.requests", map[string]string{"approved": "approved", "rejected": "rejected"}},
}

// decide approves or rejects one of the caller's pending items and propagates the decision to the request.
func (s *service) decide(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in decideIn
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, selectItem+" WHERE i.id = $1 FOR UPDATE OF i", in.ApprovalID)
	if err != nil {
		return nil, fmt.Errorf("query approval item: %w", err)
	}
	it, err := pgx.CollectOneRow(rows, scanItem)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && it.approverID != c.Sub) {
		// Other teams' items and unknown ids look the same (R71).
		return nil, apierr.Forbidden().WithMessage("Запрос недоступен: решение принимает только назначенный согласующий")
	}
	if err != nil {
		return nil, fmt.Errorf("scan approval item: %w", err)
	}
	if it.idempotencyKey != "" && it.idempotencyKey == in.IdempotencyKey {
		if it.Status != in.Decision || it.Comment != in.Comment {
			return nil, keyConflict()
		}
		replayed := true
		it.Replayed = &replayed
		return it, nil
	}
	if it.Status != "pending" {
		return nil, apierr.Conflict().WithMessage("Запрос уже рассмотрен, решение изменить нельзя")
	}

	_, err = tx.Exec(ctx, `
UPDATE approvals.items SET status = $2, comment = nullif($3, ''), idempotency_key = $4::uuid, decided_at = now()
WHERE id = $1`, it.ID, in.Decision, in.Comment, in.IdempotencyKey)
	if isUniqueViolation(err) {
		return nil, keyConflict() // the key was already used for another decision
	}
	if err != nil {
		return nil, fmt.Errorf("update approval item: %w", err)
	}
	// kind is limited by a CHECK constraint to the keys of underlying; the table name never comes from input.
	target := underlying[it.Kind]
	tag, err := tx.Exec(ctx, `UPDATE `+target.table+` SET status = $2, decided_at = now()
WHERE id = $1 AND status = 'pending_approval'`, it.RefID, target.statuses[in.Decision])
	if err != nil {
		return nil, fmt.Errorf("update %s: %w", target.table, err)
	}
	if tag.RowsAffected() != 1 {
		return nil, apierr.Conflict().WithMessage("Заявка больше не ожидает согласования")
	}
	if it.Kind == "absence_request" && in.Decision == "approved" {
		// The HR admin registers the approved day off in the HR system and the HR BP of the employee is notified
		// (organizers' Q&A 10.11: a day off involves the lead, the HR admin and the HR BP). In this imitation both
		// happen at once; a real HR system would report them asynchronously.
		if _, err := tx.Exec(ctx, `
UPDATE absence.requests r SET
    hr_admin_id = (SELECT x.id FROM directory.employees x WHERE x.role = 'hr_admin' ORDER BY x.id LIMIT 1),
    registered_at = now(),
    hrbp_id = (SELECT e.hrbp_id FROM directory.employees e WHERE e.id = r.employee_id),
    hrbp_notified_at = now()
WHERE r.id = $1`, it.RefID); err != nil {
			return nil, fmt.Errorf("register absence: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	it.Status, it.Comment, it.Replayed = in.Decision, in.Comment, new(bool)
	return it, nil
}

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован для другого решения, повторите действие")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
