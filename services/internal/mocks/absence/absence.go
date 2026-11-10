// Package absence implements the mock "absence" MCP server: day-off policy, validation and requests (S3).
// Data is synthetic (R68). Identity comes only from the verified JWT (R71); a request is visible only to its author.
// create_request writes the request and its approval item in one transaction and is idempotent by key (R66, R79).
package absence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
)

// maxDays is the longest day off in consecutive calendar days.
const maxDays = 3

// moscow is the business time zone for "tomorrow". Europe/Moscow is UTC+3 without DST since 2014; a fixed zone
// avoids depending on tzdata in the container image.
var moscow = time.FixedZone("MSK", 3*60*60)

// Register binds every tool of the absence contract to srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	return register(srv, pool, time.Now)
}

// register is Register with an injectable clock (tests).
func register(srv *mcpx.Server, pool *pgxpool.Pool, now func() time.Time) error {
	if srv == nil || pool == nil || now == nil {
		return errors.New("absence: server, pool and clock are required")
	}
	if srv.Name() != "absence" {
		return fmt.Errorf("absence: cannot register on server %q", srv.Name())
	}
	s := &service{pool: pool, now: now}
	srv.Handle("get_policy", s.getPolicy)
	srv.Handle("list_my_requests", s.listMyRequests)
	srv.Handle("validate_request", s.validateRequest)
	srv.Handle("create_request", s.createRequest)
	srv.Handle("get_request", s.getRequest)
	return nil
}

type service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// querier is satisfied by *pgxpool.Pool and pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// request is one absence request as returned by the tools.
type request struct {
	ID                  string `json:"id"`
	Type                string `json:"type"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date"`
	Comment             string `json:"comment,omitempty"`
	Status              string `json:"status"`
	ApproverID          string `json:"approver_id"`
	ApproverDisplayName string `json:"approver_display_name,omitempty"`
	CreatedAt           string `json:"created_at"`
	Replayed            *bool  `json:"replayed,omitempty"`

	employeeID     string
	idempotencyKey string
}

const selectRequest = `
SELECT r.id, r.type, r.start_date, r.end_date, coalesce(r.comment, ''), r.status, r.approver_id,
       coalesce(a.display_name, ''), r.created_at, r.employee_id, r.idempotency_key::text
FROM absence.requests r
LEFT JOIN directory.employees a ON a.id = r.approver_id`

func scanRequest(row pgx.CollectableRow) (request, error) {
	var (
		r          request
		start, end time.Time
		created    time.Time
	)
	err := row.Scan(&r.ID, &r.Type, &start, &end, &r.Comment, &r.Status, &r.ApproverID, &r.ApproverDisplayName,
		&created, &r.employeeID, &r.idempotencyKey)
	r.StartDate, r.EndDate = start.Format(time.DateOnly), end.Format(time.DateOnly)
	r.CreatedAt = created.UTC().Format(time.RFC3339)
	return r, err
}

// findRequest returns the request matching where (a condition on alias r with parameter $1), or ok=false.
func findRequest(ctx context.Context, q querier, where string, arg any) (request, bool, error) {
	rows, err := q.Query(ctx, selectRequest+" WHERE "+where, arg)
	if err != nil {
		return request{}, false, fmt.Errorf("query absence request: %w", err)
	}
	r, err := pgx.CollectOneRow(rows, scanRequest)
	if errors.Is(err, pgx.ErrNoRows) {
		return request{}, false, nil
	}
	if err != nil {
		return request{}, false, fmt.Errorf("scan absence request: %w", err)
	}
	return r, true, nil
}

func forbiddenRequest() *apierr.Error {
	return apierr.Forbidden().WithMessage("Заявка недоступна: можно просматривать только свои заявки")
}

// --- get_policy ---

type policy struct {
	DocID      string `json:"doc_id"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	SourceType string `json:"source_type"`
	URL        string `json:"url,omitempty"`
}

// getPolicy returns the team's test instruction (absence-policy-<team>) or the common one (absence-policy).
// The KB ACL is applied as in kb.search, so a document the caller may not read is never returned (R71).
func (s *service) getPolicy(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	var p policy
	err := s.pool.QueryRow(ctx, `
SELECT doc_id, title, body, source_type, coalesce(url, '')
FROM kb.documents
WHERE doc_id IN ($1, 'absence-policy')
  AND owner_id IS NULL
  AND acl_roles @> ARRAY[$2::text]
  AND ('*' = ANY(acl_teams) OR $3 = ANY(acl_teams))
ORDER BY doc_id = $1 DESC
LIMIT 1`, "absence-policy-"+c.TeamID, c.Role, c.TeamID).Scan(&p.DocID, &p.Title, &p.Text, &p.SourceType, &p.URL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.NotFound().WithMessage("Тестовая инструкция по оформлению отгула не найдена в базе знаний")
	}
	if err != nil {
		return nil, fmt.Errorf("load absence policy: %w", err)
	}
	return p, nil
}

// --- list_my_requests / get_request ---

type listOut struct {
	Items []request `json:"items"`
}

func (s *service) listMyRequests(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Status string `json:"status"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, selectRequest+`
WHERE r.employee_id = $1 AND ($2 = '' OR r.status = $2)
ORDER BY r.start_date DESC, r.id DESC
LIMIT 100`, c.Sub, in.Status)
	if err != nil {
		return nil, fmt.Errorf("list absence requests: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanRequest)
	if err != nil {
		return nil, fmt.Errorf("scan absence requests: %w", err)
	}
	if items == nil {
		items = []request{}
	}
	return listOut{Items: items}, nil
}

func (s *service) getRequest(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	r, ok, err := findRequest(ctx, s.pool, "r.id = $1", in.ID)
	if err != nil {
		return nil, err
	}
	if !ok || r.employeeID != c.Sub {
		return nil, forbiddenRequest() // never reveal whether another user's request exists (R71)
	}
	return r, nil
}

// --- validate_request ---

type dates struct {
	Type      string `json:"type"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type validateOut struct {
	Valid               bool     `json:"valid"`
	Problems            []string `json:"problems"`
	ApproverID          string   `json:"approver_id,omitempty"`
	ApproverDisplayName string   `json:"approver_display_name,omitempty"`
}

func (s *service) validateRequest(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in dates
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	return s.validate(ctx, s.pool, c, in)
}

// validate checks the dates of a new request for c and finds the approver (the employee's lead).
func (s *service) validate(ctx context.Context, q querier, c mcpx.Caller, in dates) (validateOut, error) {
	out := validateOut{Problems: []string{}}
	start, errStart := time.Parse(time.DateOnly, in.StartDate)
	end, errEnd := time.Parse(time.DateOnly, in.EndDate)
	if errStart != nil || errEnd != nil {
		return out, apierr.Validation().WithMessage("Даты должны быть в формате ГГГГ-ММ-ДД")
	}
	var leadID, leadName *string
	err := q.QueryRow(ctx, `
SELECT e.lead_id, l.display_name
FROM directory.employees e LEFT JOIN directory.employees l ON l.id = e.lead_id
WHERE e.id = $1`, c.Sub).Scan(&leadID, &leadName)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, apierr.Forbidden().WithMessage("Сотрудник не найден в справочнике")
	}
	if err != nil {
		return out, fmt.Errorf("load employee: %w", err)
	}

	out.Problems = append(out.Problems, dateProblems(start, end, s.now())...)
	if !end.Before(start) {
		overlaps, err := overlapProblems(ctx, q, c.Sub, in.StartDate, in.EndDate)
		if err != nil {
			return out, err
		}
		out.Problems = append(out.Problems, overlaps...)
	}
	if leadID == nil {
		out.Problems = append(out.Problems,
			"Не удалось определить согласующего: в справочнике не указан руководитель. Обратитесь к HR BP")
	} else {
		out.ApproverID, out.ApproverDisplayName = *leadID, deref(leadName)
	}
	out.Valid = len(out.Problems) == 0
	return out, nil
}

// dateProblems applies the calendar rules: start not before tomorrow (Moscow date of now), end not before start,
// working days only, at most maxDays consecutive days.
func dateProblems(start, end, now time.Time) []string {
	var problems []string
	y, m, d := now.In(moscow).Date()
	tomorrow := time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
	if start.Before(tomorrow) {
		problems = append(problems, fmt.Sprintf("Дата начала должна быть не раньше завтрашнего дня (%s)", ru(tomorrow)))
	}
	if end.Before(start) {
		return append(problems, "Дата окончания не может быть раньше даты начала")
	}
	days := int(end.Sub(start).Hours()/24) + 1
	var weekends []string
	for i := 0; i < days && i <= 31; i++ {
		day := start.AddDate(0, 0, i)
		switch day.Weekday() {
		case time.Saturday:
			weekends = append(weekends, ru(day)+" (суббота)")
		case time.Sunday:
			weekends = append(weekends, ru(day)+" (воскресенье)")
		}
	}
	if len(weekends) > 4 {
		weekends = append(weekends[:4], fmt.Sprintf("и ещё %d", len(weekends)-4))
	}
	if len(weekends) > 0 {
		problems = append(problems, "Отгул оформляется только на рабочие дни, выходные: "+strings.Join(weekends, ", "))
	}
	if days > maxDays {
		problems = append(problems, fmt.Sprintf("Отгул можно оформить не больше чем на %d дня подряд, выбрано %d %s",
			maxDays, days, plural(days, "день", "дня", "дней")))
	}
	return problems
}

// overlapProblems reports the caller's pending or approved requests that intersect [start, end].
func overlapProblems(ctx context.Context, q querier, sub, start, end string) ([]string, error) {
	rows, err := q.Query(ctx, `
SELECT id, start_date, end_date, status
FROM absence.requests
WHERE employee_id = $1 AND status IN ('pending_approval', 'approved')
  AND start_date <= $3::date AND end_date >= $2::date
ORDER BY start_date, id`, sub, start, end)
	if err != nil {
		return nil, fmt.Errorf("query overlapping requests: %w", err)
	}
	problems, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var (
			id, status string
			s, e       time.Time
		)
		err := row.Scan(&id, &s, &e, &status)
		label := "согласована"
		if status == "pending_approval" {
			label = "на согласовании"
		}
		return fmt.Sprintf("Даты пересекаются с заявкой %s на %s (%s)", id, period(s, e), label), err
	})
	if err != nil {
		return nil, fmt.Errorf("scan overlapping requests: %w", err)
	}
	return problems, nil
}

// --- create_request ---

type createIn struct {
	dates
	Comment        string `json:"comment"`
	IdempotencyKey string `json:"idempotency_key"`
}

// createRequest re-validates and, in one transaction, inserts the request (pending_approval, approver = lead)
// and its approval item. The same idempotency_key returns the stored request with replayed=true (R79).
func (s *service) createRequest(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in createIn
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Serialise the caller's writes: the replay lookup and the overlap check must see each other's commits.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "absence:"+c.Sub); err != nil {
		return nil, fmt.Errorf("lock employee: %w", err)
	}
	prev, found, err := findRequest(ctx, tx, "r.idempotency_key = $1::uuid", in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if found {
		return replay(prev, c, in)
	}
	check, err := s.validate(ctx, tx, c, in.dates)
	if err != nil {
		return nil, err
	}
	if !check.Valid {
		return nil, apierr.Validation().WithMessage("Заявку нельзя создать: " + strings.Join(check.Problems, "; "))
	}
	var id string
	err = tx.QueryRow(ctx, `
INSERT INTO absence.requests (employee_id, type, start_date, end_date, comment, approver_id, idempotency_key)
VALUES ($1, $2, $3::date, $4::date, nullif($5, ''), $6, $7::uuid)
RETURNING id`, c.Sub, in.Type, in.StartDate, in.EndDate, in.Comment, check.ApproverID, in.IdempotencyKey).Scan(&id)
	if isUniqueViolation(err) {
		return nil, keyConflict() // the key belongs to another employee's request
	}
	if err != nil {
		return nil, fmt.Errorf("insert absence request: %w", err)
	}
	start, _ := time.Parse(time.DateOnly, in.StartDate)
	end, _ := time.Parse(time.DateOnly, in.EndDate)
	if _, err := tx.Exec(ctx, `
INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
VALUES ('absence_request', $1, $2, $3, $4)`, id, c.Sub, check.ApproverID, "Отгул "+period(start, end)); err != nil {
		return nil, fmt.Errorf("insert approval item: %w", err)
	}
	created, _, err := findRequest(ctx, tx, "r.id = $1", id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	created.Replayed = new(bool)
	return created, nil
}

// replay returns the stored request for a repeated key, or conflict when the key was used for other arguments.
func replay(prev request, c mcpx.Caller, in createIn) (any, error) {
	if prev.employeeID != c.Sub || prev.Type != in.Type || prev.StartDate != in.StartDate ||
		prev.EndDate != in.EndDate || prev.Comment != in.Comment {
		return nil, keyConflict()
	}
	replayed := true
	prev.Replayed = &replayed
	return prev, nil
}

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован для другой заявки, подтвердите действие ещё раз")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- formatting ---

// ru formats a date as DD.MM.YYYY.
func ru(t time.Time) string { return t.Format("02.01.2006") }

// period formats a date range: one date for a single day, otherwise "start–end".
func period(start, end time.Time) string {
	if start.Equal(end) {
		return ru(start)
	}
	return ru(start) + "–" + ru(end)
}

// plural picks the Russian plural form for n: 1 день, 2 дня, 5 дней.
func plural(n int, one, few, many string) string {
	switch n10, n100 := n%10, n%100; {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	default:
		return many
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
