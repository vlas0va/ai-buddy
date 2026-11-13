// Пакет trips реализует мок-сервер MCP «trips»: командировки между офисами Авито (кейс «Пространство решений»:
// командировки). Данные синтетические (R68). Личность берётся только из проверенного JWT (R71): вызывающий видит и
// создаёт только свои заявки; согласующий — руководитель вызывающего, который решает через approvals.decide. Заявки
// идемпотентны по ключу (R79).
package trips

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
)

// Офисы, куда можно поехать (тестовые данные прототипа).
var Offices = []string{"Москва", "Санкт-Петербург", "Казань", "Екатеринбург", "Новосибирск"}

const (
	minNoticeDays = 5  // командировка запрашивается не меньше чем за столько дней
	maxDays       = 14 // самая длинная командировка в календарных днях
)

// moscow — рабочий часовой пояс (UTC+3, без летнего времени), как в моке absence.
var moscow = time.FixedZone("MSK", 3*60*60)

// Register привязывает каждый инструмент контракта trips к srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	return register(srv, pool, time.Now)
}

func register(srv *mcpx.Server, pool *pgxpool.Pool, now func() time.Time) error {
	if srv == nil || pool == nil || now == nil {
		return errors.New("trips: server, pool and clock are required")
	}
	if srv.Name() != "trips" {
		return fmt.Errorf("trips: cannot register on server %q", srv.Name())
	}
	s := &service{pool: pool, now: now}
	srv.Handle("get_policy", s.getPolicy)
	srv.Handle("list_my_requests", s.listMyRequests)
	srv.Handle("validate_request", s.validateRequest)
	srv.Handle("create_request", s.createRequest)
	return nil
}

type service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type request struct {
	ID                  string `json:"id"`
	City                string `json:"city"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date"`
	Purpose             string `json:"purpose"`
	Status              string `json:"status"`
	ApproverID          string `json:"approver_id"`
	ApproverDisplayName string `json:"approver_display_name,omitempty"`
	CreatedAt           string `json:"created_at"`
	Replayed            *bool  `json:"replayed,omitempty"`

	employeeID string
}

const selectRequest = `
SELECT r.id, r.city, r.start_date, r.end_date, r.purpose, r.status, r.approver_id, coalesce(a.display_name, ''),
       r.created_at, r.employee_id
FROM trips.requests r
LEFT JOIN directory.employees a ON a.id = r.approver_id`

func scanRequest(row pgx.CollectableRow) (request, error) {
	var (
		r          request
		start, end time.Time
		created    time.Time
	)
	err := row.Scan(&r.ID, &r.City, &start, &end, &r.Purpose, &r.Status, &r.ApproverID, &r.ApproverDisplayName,
		&created, &r.employeeID)
	r.StartDate, r.EndDate = start.Format(time.DateOnly), end.Format(time.DateOnly)
	r.CreatedAt = created.UTC().Format(time.RFC3339)
	return r, err
}

func findRequest(ctx context.Context, q querier, where string, arg any) (request, bool, error) {
	rows, err := q.Query(ctx, selectRequest+" WHERE "+where, arg)
	if err != nil {
		return request{}, false, fmt.Errorf("query trip request: %w", err)
	}
	r, err := pgx.CollectOneRow(rows, scanRequest)
	if errors.Is(err, pgx.ErrNoRows) {
		return request{}, false, nil
	}
	if err != nil {
		return request{}, false, fmt.Errorf("scan trip request: %w", err)
	}
	return r, true, nil
}

// --- get_policy ---

type policy struct {
	Offices       []string `json:"offices"`
	MinNoticeDays int      `json:"min_notice_days"`
	MaxDays       int      `json:"max_days"`
	Text          string   `json:"text"`
}

func (s *service) getPolicy(context.Context, mcpx.Caller, json.RawMessage) (any, error) {
	return policy{Offices: Offices, MinNoticeDays: minNoticeDays, MaxDays: maxDays, Text: fmt.Sprintf(
		"Командировка в другой офис Авито согласуется руководителем. Подать заявку нужно не позже чем за %d дней, "+
			"длительность — до %d дней. Билеты и гостиницу после согласования бронирует служба командировок "+
			"(тестовые правила прототипа).", minNoticeDays, maxDays)}, nil
}

// --- list_my_requests ---

type listOut struct {
	Items []request `json:"items"`
}

func (s *service) listMyRequests(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	rows, err := s.pool.Query(ctx, selectRequest+`
WHERE r.employee_id = $1
ORDER BY r.start_date DESC, r.id DESC
LIMIT 100`, c.Sub)
	if err != nil {
		return nil, fmt.Errorf("list trip requests: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanRequest)
	if err != nil {
		return nil, fmt.Errorf("scan trip requests: %w", err)
	}
	if items == nil {
		items = []request{}
	}
	return listOut{Items: items}, nil
}

// --- validate_request ---

type tripIn struct {
	City      string `json:"city"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Purpose   string `json:"purpose"`
}

type validateOut struct {
	Valid               bool     `json:"valid"`
	Problems            []string `json:"problems"`
	ApproverID          string   `json:"approver_id,omitempty"`
	ApproverDisplayName string   `json:"approver_display_name,omitempty"`
}

func (s *service) validateRequest(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in tripIn
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	return s.validate(ctx, s.pool, c, in)
}

// validate проверяет новую командировку c: известный офис не в родном городе сотрудника, даты, срок подачи,
// длительность, без пересечения с другой командировкой; согласующий — руководитель сотрудника.
func (s *service) validate(ctx context.Context, q querier, c mcpx.Caller, in tripIn) (validateOut, error) {
	out := validateOut{Problems: []string{}}
	start, errStart := time.Parse(time.DateOnly, in.StartDate)
	end, errEnd := time.Parse(time.DateOnly, in.EndDate)
	if errStart != nil || errEnd != nil {
		return out, apierr.Validation().WithMessage("Даты должны быть в формате ГГГГ-ММ-ДД")
	}
	var leadID, leadName, home *string
	err := q.QueryRow(ctx, `
SELECT e.lead_id, l.display_name, e.location
FROM directory.employees e LEFT JOIN directory.employees l ON l.id = e.lead_id
WHERE e.id = $1`, c.Sub).Scan(&leadID, &leadName, &home)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, apierr.Forbidden().WithMessage("Сотрудник не найден в справочнике")
	}
	if err != nil {
		return out, fmt.Errorf("load employee: %w", err)
	}
	city := strings.TrimSpace(in.City)
	switch {
	case !slices.Contains(Offices, city):
		out.Problems = append(out.Problems, "Офиса в этом городе нет. Доступно: "+strings.Join(Offices, ", "))
	case home != nil && *home == city:
		out.Problems = append(out.Problems, "Это ваш город: командировка оформляется только в другой офис")
	}
	y, m, d := s.now().In(moscow).Date()
	earliest := time.Date(y, m, d+minNoticeDays, 0, 0, 0, 0, time.UTC)
	if start.Before(earliest) {
		out.Problems = append(out.Problems, fmt.Sprintf("Заявку подают не позже чем за %d дней: начало не раньше %s",
			minNoticeDays, earliest.Format("02.01.2006")))
	}
	if end.Before(start) {
		out.Problems = append(out.Problems, "Дата окончания не может быть раньше даты начала")
	} else {
		if days := int(end.Sub(start).Hours()/24) + 1; days > maxDays {
			out.Problems = append(out.Problems, fmt.Sprintf("Командировка длится не больше %d дней, у вас %d", maxDays, days))
		}
		var other string
		err := q.QueryRow(ctx, `
SELECT id FROM trips.requests
WHERE employee_id = $1 AND status IN ('pending_approval', 'approved') AND start_date <= $3::date AND end_date >= $2::date
ORDER BY start_date LIMIT 1`, c.Sub, in.StartDate, in.EndDate).Scan(&other)
		if err == nil {
			out.Problems = append(out.Problems, "Даты пересекаются с другой командировкой ("+other+")")
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, fmt.Errorf("check overlap: %w", err)
		}
	}
	if strings.TrimSpace(in.Purpose) == "" {
		out.Problems = append(out.Problems, "Укажите цель командировки")
	}
	if leadID == nil {
		out.Problems = append(out.Problems, "Не удалось определить согласующего: в справочнике не указан руководитель")
	} else {
		out.ApproverID, out.ApproverDisplayName = *leadID, deref(leadName)
	}
	out.Valid = len(out.Problems) == 0
	return out, nil
}

// --- create_request ---

type createIn struct {
	tripIn
	IdempotencyKey string `json:"idempotency_key"`
}

// createRequest повторно проверяет и в одной транзакции вставляет заявку (pending_approval, согласующий = руководитель)
// и её пункт согласования. Тот же idempotency_key возвращает сохранённую заявку с replayed=true (R79).
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "trips:"+c.Sub); err != nil {
		return nil, fmt.Errorf("lock employee: %w", err)
	}
	prev, found, err := findRequest(ctx, tx, "r.idempotency_key = $1::uuid", in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if found {
		if prev.employeeID != c.Sub || prev.City != strings.TrimSpace(in.City) || prev.StartDate != in.StartDate ||
			prev.EndDate != in.EndDate || prev.Purpose != strings.TrimSpace(in.Purpose) {
			return nil, keyConflict()
		}
		replayed := true
		prev.Replayed = &replayed
		return prev, nil
	}
	check, err := s.validate(ctx, tx, c, in.tripIn)
	if err != nil {
		return nil, err
	}
	if !check.Valid {
		return nil, apierr.Validation().WithMessage("Заявку нельзя создать: " + strings.Join(check.Problems, "; "))
	}
	var id string
	err = tx.QueryRow(ctx, `
INSERT INTO trips.requests (employee_id, city, start_date, end_date, purpose, approver_id, idempotency_key)
VALUES ($1, $2, $3::date, $4::date, $5, $6, $7::uuid)
RETURNING id`, c.Sub, strings.TrimSpace(in.City), in.StartDate, in.EndDate, strings.TrimSpace(in.Purpose),
		check.ApproverID, in.IdempotencyKey).Scan(&id)
	if isUniqueViolation(err) {
		return nil, keyConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("insert trip request: %w", err)
	}
	start, _ := time.Parse(time.DateOnly, in.StartDate)
	end, _ := time.Parse(time.DateOnly, in.EndDate)
	summary := fmt.Sprintf("Командировка: %s, %s", strings.TrimSpace(in.City), period(start, end))
	if _, err := tx.Exec(ctx, `
INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
VALUES ('trip_request', $1, $2, $3, $4)`, id, c.Sub, check.ApproverID, summary); err != nil {
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

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован для другой заявки, подтвердите действие ещё раз")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func period(start, end time.Time) string {
	if start.Equal(end) {
		return start.Format("02.01.2006")
	}
	return start.Format("02.01.2006") + "–" + end.Format("02.01.2006")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
