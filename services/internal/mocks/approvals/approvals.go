// Пакет approvals реализует мок-сервер MCP «approvals» («Согласия»), общий для S3 (отгул) и S4 (доступ). Данные
// синтетические (R68). Пункт видит и решает только указанный в нём согласующий (R71); решение обновляет пункт и
// исходную заявку на отсутствие или доступ в одной транзакции и идемпотентно по ключу (R79).
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

// Register привязывает каждый инструмент контракта approvals к srv.
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

// item — один пункт согласования в том виде, как его возвращают инструменты.
type item struct {
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	RefID                string `json:"ref_id"`
	RequesterID          string `json:"requester_id"`
	RequesterDisplayName string `json:"requester_display_name,omitempty"`
	Summary              string `json:"summary"`
	Status               string `json:"status"`
	Comment              string `json:"comment,omitempty"`
	Stage                string `json:"stage"`
	CreatedAt            string `json:"created_at"`
	Replayed             *bool  `json:"replayed,omitempty"`
	// TeamAbsent: кто ещё из команды заявителя отсутствует в эти дни (отзыв 12.13), только для пунктов об отсутствии
	TeamAbsent []string `json:"team_absent,omitempty"`

	approverID     string
	idempotencyKey string
}

const selectItem = `
SELECT i.id, i.kind, i.ref_id, i.requester_id, coalesce(e.display_name, ''), i.summary, i.status,
       coalesce(i.comment, ''), i.created_at, i.approver_id, coalesce(i.idempotency_key::text, ''), i.stage
FROM approvals.items i
LEFT JOIN directory.employees e ON e.id = i.requester_id`

func scanItem(row pgx.CollectableRow) (item, error) {
	var (
		it      item
		created time.Time
	)
	err := row.Scan(&it.ID, &it.Kind, &it.RefID, &it.RequesterID, &it.RequesterDisplayName, &it.Summary, &it.Status,
		&it.Comment, &created, &it.approverID, &it.idempotencyKey, &it.Stage)
	it.CreatedAt = created.UTC().Format(time.RFC3339)
	return it, err
}

type listOut struct {
	Items []item `json:"items"`
}

// listPending возвращает ожидающие пункты, где согласующий — вызывающий, самые старые сначала.
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
	for i := range items {
		if items[i].Kind != "absence_request" {
			continue
		}
		absent, err := s.teamAbsent(ctx, items[i].RefID)
		if err != nil {
			return nil, err
		}
		items[i].TeamAbsent = absent
	}
	return listOut{Items: items}, nil
}

// teamAbsent перечисляет коллег заявителя с несогласованным или согласованным отсутствием, пересекающимся с заявкой:
// руководитель сразу видит, что половины команды не будет (отзыв 12.13).
func (s *service) teamAbsent(ctx context.Context, requestID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
SELECT e.display_name, o.type, o.start_date, o.end_date
FROM absence.requests r
JOIN directory.employees me ON me.id = r.employee_id
JOIN absence.requests o ON o.employee_id <> r.employee_id AND o.status IN ('pending_approval', 'pending_hr', 'approved')
     AND o.start_date <= r.end_date AND o.end_date >= r.start_date
JOIN directory.employees e ON e.id = o.employee_id AND e.team_id = me.team_id
WHERE r.id = $1
ORDER BY o.start_date, e.display_name
LIMIT 5`, requestID)
	if err != nil {
		return nil, fmt.Errorf("team absences: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var name, kind string
		var start, end time.Time
		err := row.Scan(&name, &kind, &start, &end)
		label := map[string]string{"day_off": "отгул", "vacation": "отпуск", "absence": "отсутствие", "parental_leave": "декрет"}[kind]
		when := start.Format("02.01")
		if !end.Equal(start) {
			when += "–" + end.Format("02.01")
		}
		return fmt.Sprintf("%s: %s %s", name, label, when), err
	})
}

type decideIn struct {
	ApprovalID     string `json:"approval_id"`
	Decision       string `json:"decision"`
	Comment        string `json:"comment"`
	IdempotencyKey string `json:"idempotency_key"`
}

// underlying сопоставляет виду пункта и решению таблицу заявок и её новый статус.
var underlying = map[string]struct {
	table    string
	statuses map[string]string
}{
	"absence_request": {"absence.requests", map[string]string{"approved": "approved", "rejected": "rejected"}},
	"access_request":  {"access.requests", map[string]string{"approved": "granted", "rejected": "rejected"}},
	"trip_request":    {"trips.requests", map[string]string{"approved": "approved", "rejected": "rejected"}},
}

// decide согласует или отклоняет один из ожидающих пунктов вызывающего и распространяет решение на заявку.
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
		// Пункты других команд и неизвестные id выглядят одинаково (R71).
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
		return nil, keyConflict() // ключ уже использован для другого решения
	}
	if err != nil {
		return nil, fmt.Errorf("update approval item: %w", err)
	}
	if it.Kind == "absence_request" {
		if err := decideAbsence(ctx, tx, it, in.Decision, c.Sub); err != nil {
			return nil, err
		}
	} else {
		// kind ограничен CHECK-ограничением ключами underlying; имя таблицы никогда не берётся из ввода.
		target := underlying[it.Kind]
		tag, err := tx.Exec(ctx, `UPDATE `+target.table+` SET status = $2, decided_at = now()
WHERE id = $1 AND status = 'pending_approval'`, it.RefID, target.statuses[in.Decision])
		if err != nil {
			return nil, fmt.Errorf("update %s: %w", target.table, err)
		}
		if tag.RowsAffected() != 1 {
			return nil, apierr.Conflict().WithMessage("Заявка больше не ожидает согласования")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	it.Status, it.Comment, it.Replayed = in.Decision, in.Comment, new(bool)
	return it, nil
}

// decideAbsence применяет решение к заявке на отсутствие в два этапа (чекпоинт 11.11): руководитель согласует → заявка
// ждёт HR-администратора (pending_hr), и HR-администратор получает пункт согласования; HR-администратор согласует →
// заявка согласована, оформлена в HR-системе, а HR BP сотрудника получает уведомление. Отказ на любом этапе отклоняет
// заявку.
func decideAbsence(ctx context.Context, tx pgx.Tx, it item, decision, approver string) error {
	var (
		sql  string
		args []any
	)
	switch {
	case it.Stage == "lead" && decision == "approved":
		sql = `UPDATE absence.requests SET status = 'pending_hr', lead_decided_at = now()
WHERE id = $1 AND status = 'pending_approval'`
		args = []any{it.RefID}
	case it.Stage == "lead":
		sql = `UPDATE absence.requests SET status = 'rejected', decided_at = now()
WHERE id = $1 AND status = 'pending_approval'`
		args = []any{it.RefID}
	case decision == "approved":
		sql = `UPDATE absence.requests r SET status = 'approved', decided_at = now(), hr_admin_id = $2,
    registered_at = now(), hrbp_id = (SELECT e.hrbp_id FROM directory.employees e WHERE e.id = r.employee_id),
    hrbp_notified_at = now()
WHERE r.id = $1 AND r.status = 'pending_hr'`
		args = []any{it.RefID, approver}
	default:
		sql = `UPDATE absence.requests SET status = 'rejected', decided_at = now(), hr_admin_id = $2
WHERE id = $1 AND status = 'pending_hr'`
		args = []any{it.RefID, approver}
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("update absence request: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return apierr.Conflict().WithMessage("Заявка больше не ожидает согласования")
	}
	if it.Stage == "lead" && decision == "approved" {
		tag, err := tx.Exec(ctx, `
INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary, stage)
SELECT 'absence_request', $1::text, $2::text, x.id, $3::text, 'hr_admin'
FROM directory.employees x WHERE x.role = 'hr_admin' ORDER BY x.id LIMIT 1`, it.RefID, it.RequesterID, it.Summary)
		if err != nil {
			return fmt.Errorf("insert hr admin approval item: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return apierr.Conflict().WithMessage("В справочнике нет HR-администратора")
		}
	}
	return nil
}

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован для другого решения, повторите действие")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
