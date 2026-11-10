// Package access implements the mock "access" MCP server: required accesses, team channels and access requests (S4).
// Data is synthetic (R68). Identity and team come only from the verified JWT (R71): a caller sees and requests only
// resources of their own team or of every team. Elevated resources go to the caller's lead for approval; requests are
// idempotent by key (R79).
package access

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

// Register binds every tool of the access contract to srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	if srv == nil || pool == nil {
		return errors.New("access: server and pool are required")
	}
	if srv.Name() != "access" {
		return fmt.Errorf("access: cannot register on server %q", srv.Name())
	}
	s := &service{pool: pool}
	srv.Handle("list_required", s.listRequired)
	srv.Handle("list_my_access", s.listMyAccess)
	srv.Handle("list_team_channels", s.listTeamChannels)
	srv.Handle("request_access", s.requestAccess)
	return nil
}

type service struct {
	pool *pgxpool.Pool
}

// latestStatus is a LATERAL subquery: the caller's ($1) most recent request for resource r.
const latestStatus = `
LATERAL (SELECT q.status FROM access.requests q
         WHERE q.resource_id = r.id AND q.employee_id = $1
         ORDER BY q.created_at DESC, q.id DESC LIMIT 1) q`

// resourceStatus is a resource with the caller's status for it.
type resourceStatus struct {
	ResourceID      string `json:"resource_id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Elevated        bool   `json:"elevated"`
	Status          string `json:"status"`
	ChecklistItemID string `json:"checklist_item_id,omitempty"`
}

type listOut struct {
	Items []resourceStatus `json:"items"`
}

func (s *service) listResources(ctx context.Context, sql string, args ...any) (any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list access resources: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (resourceStatus, error) {
		var r resourceStatus
		err := row.Scan(&r.ResourceID, &r.Name, &r.Kind, &r.Elevated, &r.Status, &r.ChecklistItemID)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan access resources: %w", err)
	}
	if items == nil {
		items = []resourceStatus{}
	}
	return listOut{Items: items}, nil
}

// listRequired returns the tools and spaces required for the caller's team with the caller's status.
func (s *service) listRequired(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return s.listResources(ctx, `
SELECT r.id, r.name, r.kind, r.elevated, coalesce(q.status, 'not_requested'), coalesce(r.checklist_item_id, '')
FROM access.resources r
LEFT JOIN `+latestStatus+` ON true
WHERE r.kind IN ('tool', 'space') AND (r.team_id IS NULL OR r.team_id = $2)
ORDER BY r.kind DESC, r.team_id NULLS FIRST, r.id`, c.Sub, c.TeamID)
}

// listMyAccess returns every resource the caller has requested, with the latest status.
func (s *service) listMyAccess(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return s.listResources(ctx, `
SELECT r.id, r.name, r.kind, r.elevated, q.status, coalesce(r.checklist_item_id, '')
FROM access.resources r
JOIN `+latestStatus+` ON true
ORDER BY r.kind DESC, r.team_id NULLS FIRST, r.id`, c.Sub)
}

type channel struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose,omitempty"`
	Closed  bool   `json:"closed"`
}

type channelsOut struct {
	Items []channel `json:"items"`
}

// listTeamChannels returns the channels of the caller's team and of every team: name, purpose, closed only.
func (s *service) listTeamChannels(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	rows, err := s.pool.Query(ctx, `
SELECT name, coalesce(purpose, ''), elevated
FROM access.resources
WHERE kind = 'channel' AND (team_id IS NULL OR team_id = $1)
ORDER BY team_id NULLS FIRST, elevated, name`, c.TeamID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (channel, error) {
		var ch channel
		err := row.Scan(&ch.Name, &ch.Purpose, &ch.Closed)
		return ch, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan channels: %w", err)
	}
	if items == nil {
		items = []channel{}
	}
	return channelsOut{Items: items}, nil
}

// --- request_access ---

type requestIn struct {
	ResourceID     string `json:"resource_id"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type requestOut struct {
	ID              string  `json:"id"`
	ResourceID      string  `json:"resource_id"`
	ResourceName    string  `json:"resource_name"`
	Status          string  `json:"status"`
	ApproverID      *string `json:"approver_id"`
	ChecklistItemID *string `json:"checklist_item_id"`
	CreatedAt       string  `json:"created_at"`
	Replayed        bool    `json:"replayed"`

	employeeID string
	reason     string
}

// querier is satisfied by pgx.Tx and *pgxpool.Pool.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// findRequest returns the access request matching where (a condition on alias q with parameters $1, $2), or ok=false.
func findRequest(ctx context.Context, db querier, where string, args ...any) (requestOut, bool, error) {
	rows, err := db.Query(ctx, `
SELECT q.id, q.resource_id, r.name, q.status, q.approver_id, r.checklist_item_id, q.created_at, q.employee_id,
       coalesce(q.reason, '')
FROM access.requests q JOIN access.resources r ON r.id = q.resource_id
WHERE `+where+`
ORDER BY q.created_at DESC, q.id DESC LIMIT 1`, args...)
	if err != nil {
		return requestOut{}, false, fmt.Errorf("query access request: %w", err)
	}
	out, err := pgx.CollectOneRow(rows, func(row pgx.CollectableRow) (requestOut, error) {
		var (
			o       requestOut
			created time.Time
		)
		err := row.Scan(&o.ID, &o.ResourceID, &o.ResourceName, &o.Status, &o.ApproverID, &o.ChecklistItemID, &created,
			&o.employeeID, &o.reason)
		o.CreatedAt = created.UTC().Format(time.RFC3339)
		return o, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return requestOut{}, false, nil
	}
	if err != nil {
		return requestOut{}, false, fmt.Errorf("scan access request: %w", err)
	}
	return out, true, nil
}

// requestAccess creates an access request for the caller. A resource outside the caller's team is forbidden;
// a plain resource is granted at once; an elevated one waits for the caller's lead (approvals item). A repeated
// idempotency_key returns the stored request with replayed=true.
func (s *service) requestAccess(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in requestIn
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Serialise the caller's writes so that the replay lookup and the duplicate check see each other's commits.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "access:"+c.Sub); err != nil {
		return nil, fmt.Errorf("lock employee: %w", err)
	}
	prev, found, err := findRequest(ctx, tx, "q.idempotency_key = $1::uuid", in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if found {
		if prev.employeeID != c.Sub || prev.ResourceID != in.ResourceID || prev.reason != in.Reason {
			return nil, keyConflict()
		}
		prev.Replayed = true
		return prev, nil
	}

	var (
		name     string
		elevated bool
	)
	err = tx.QueryRow(ctx, `
SELECT name, elevated FROM access.resources WHERE id = $1 AND (team_id IS NULL OR team_id = $2)`,
		in.ResourceID, c.TeamID).Scan(&name, &elevated)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown resources and other teams' resources look the same (R71).
		return nil, apierr.Forbidden().WithMessage("Ресурс недоступен для запроса из вашей команды")
	}
	if err != nil {
		return nil, fmt.Errorf("load resource: %w", err)
	}
	last, found, err := findRequest(ctx, tx, "q.employee_id = $1 AND q.resource_id = $2", c.Sub, in.ResourceID)
	if err != nil {
		return nil, err
	}
	if found && last.Status == "granted" {
		return nil, apierr.Conflict().WithMessage(fmt.Sprintf("Доступ к ресурсу «%s» уже выдан (заявка %s)", name, last.ID))
	}
	if found && last.Status == "pending_approval" {
		return nil, apierr.Conflict().WithMessage(
			fmt.Sprintf("Заявка на доступ к ресурсу «%s» уже на согласовании (%s)", name, last.ID))
	}

	var lead *string
	err = tx.QueryRow(ctx, `SELECT lead_id FROM directory.employees WHERE id = $1`, c.Sub).Scan(&lead)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.Forbidden().WithMessage("Сотрудник не найден в справочнике")
	}
	if err != nil {
		return nil, fmt.Errorf("load employee: %w", err)
	}
	status, approver := "granted", (*string)(nil)
	if elevated {
		if lead == nil {
			return nil, apierr.Validation().WithMessage(
				"Не удалось определить согласующего: в справочнике не указан руководитель. Обратитесь к наставнику")
		}
		status, approver = "pending_approval", lead
	}
	var id string
	err = tx.QueryRow(ctx, `
INSERT INTO access.requests (employee_id, resource_id, reason, status, approver_id, idempotency_key, decided_at)
VALUES ($1, $2, nullif($3, ''), $4, $5, $6::uuid, CASE WHEN $4 = 'granted' THEN now() END)
RETURNING id`, c.Sub, in.ResourceID, in.Reason, status, approver, in.IdempotencyKey).Scan(&id)
	if isUniqueViolation(err) {
		return nil, keyConflict() // the key belongs to another employee's request
	}
	if err != nil {
		return nil, fmt.Errorf("insert access request: %w", err)
	}
	if elevated {
		if _, err := tx.Exec(ctx, `
INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
VALUES ('access_request', $1, $2, $3, $4)`, id, c.Sub, *approver, "Доступ: "+name); err != nil {
			return nil, fmt.Errorf("insert approval item: %w", err)
		}
	}
	created, _, err := findRequest(ctx, tx, "q.id = $1", id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return created, nil
}

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован для другой заявки, подтвердите действие ещё раз")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
