// Пакет absence реализует мок-сервер MCP «absence»: правила, проверку и заявки на отгул, отпуск, отсутствие и декрет
// (S3; чекпоинт 11.11). Сначала согласует руководитель, затем HR-администратор; пока HR-администратор не согласовал,
// заявку можно перенести на другие даты. Данные синтетические (R68). Личность берётся только из проверенного JWT (R71);
// заявку видит только её автор. create_request записывает заявку и её пункт согласования в одной транзакции и
// идемпотентна по ключу (R66, R79).
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

// Правила по видам (тестовые правила прототипа): самая длинная заявка в календарных днях и минимальный срок подачи в
// днях.
const (
	maxDays         = 3  // отгул
	maxVacationDays = 28 // отпуск
	vacationNotice  = 14 // отпуск: подаётся не меньше чем за две недели
	maxAbsenceDays  = 5  // отсутствие по личным причинам
	maxDocuments    = 3
	maxDocumentName = 200
)

// typeLabels называют каждый вид в сводках и сообщениях.
var typeLabels = map[string]string{"day_off": "Отгул", "vacation": "Отпуск", "absence": "Отсутствие", "parental_leave": "Декрет"}

// moscow — рабочий часовой пояс для «завтра». Europe/Moscow — UTC+3 без перехода на летнее время с 2014 года;
// фиксированная зона избавляет от зависимости от tzdata в образе контейнера.
var moscow = time.FixedZone("MSK", 3*60*60)

// Register привязывает каждый инструмент контракта absence к srv.
func Register(srv *mcpx.Server, pool *pgxpool.Pool) error {
	return register(srv, pool, time.Now)
}

// register — это Register с подменяемыми часами (для тестов).
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
	srv.Handle("reschedule_request", s.rescheduleRequest)
	srv.Handle("cancel_request", s.cancelRequest)
	return nil
}

type service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// querier удовлетворяют *pgxpool.Pool и pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// request — одна заявка на отсутствие в том виде, как её возвращают инструменты.
type request struct {
	ID                  string     `json:"id"`
	Type                string     `json:"type"`
	StartDate           string     `json:"start_date"`
	EndDate             string     `json:"end_date"`
	Comment             string     `json:"comment,omitempty"`
	Status              string     `json:"status"`
	ApproverID          string     `json:"approver_id"`
	ApproverDisplayName string     `json:"approver_display_name,omitempty"`
	CreatedAt           string     `json:"created_at"`
	Documents           []document `json:"documents"`
	Steps               []step     `json:"steps"`
	Replayed            *bool      `json:"replayed,omitempty"`
	// ReplacedID: несогласованная заявка внутри новых дат, которую отменил create_request (отзыв 12.13)
	ReplacedID string `json:"replaced_id,omitempty"`

	employeeID     string
	idempotencyKey string
	rescheduleKey  string
	cancelKey      string
}

// document — метаданные файла, приложенного к заявке; его содержимое в сервис не попадает.
type document struct {
	Name string `json:"name"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// step — один этап отгула после его создания (ответы организаторов 10.11): руководитель согласует, HR-администратор
// оформляет согласованный отгул в HR-системе, HR BP получает уведомление.
type step struct {
	Role        string `json:"role"`
	DisplayName string `json:"display_name,omitempty"`
	Status      string `json:"status"`
	At          string `json:"at,omitempty"`
}

const selectRequest = `
SELECT r.id, r.type, r.start_date, r.end_date, coalesce(r.comment, ''), r.status, r.approver_id,
       coalesce(a.display_name, ''), r.created_at, r.employee_id, r.idempotency_key::text,
       r.decided_at, coalesce(ha.display_name, ''), r.registered_at, coalesce(hb.display_name, ''), r.hrbp_notified_at,
       r.documents, r.lead_decided_at, coalesce(r.reschedule_key::text, ''), coalesce(r.cancel_key::text, '')
FROM absence.requests r
LEFT JOIN directory.employees a ON a.id = r.approver_id
LEFT JOIN directory.employees emp ON emp.id = r.employee_id
LEFT JOIN directory.employees ha ON ha.id = coalesce(r.hr_admin_id,
    (SELECT x.id FROM directory.employees x WHERE x.role = 'hr_admin' ORDER BY x.id LIMIT 1))
LEFT JOIN directory.employees hb ON hb.id = coalesce(r.hrbp_id, emp.hrbp_id)`

func scanRequest(row pgx.CollectableRow) (request, error) {
	var (
		r                             request
		start, end                    time.Time
		created                       time.Time
		decided, registered, notified *time.Time
		leadDecided                   *time.Time
		hrAdmin, hrbp                 string
		docs                          []byte
	)
	err := row.Scan(&r.ID, &r.Type, &start, &end, &r.Comment, &r.Status, &r.ApproverID, &r.ApproverDisplayName,
		&created, &r.employeeID, &r.idempotencyKey, &decided, &hrAdmin, &registered, &hrbp, &notified,
		&docs, &leadDecided, &r.rescheduleKey, &r.cancelKey)
	r.StartDate, r.EndDate = start.Format(time.DateOnly), end.Format(time.DateOnly)
	r.CreatedAt = created.UTC().Format(time.RFC3339)
	if json.Unmarshal(docs, &r.Documents) != nil || r.Documents == nil {
		r.Documents = []document{}
	}
	r.Steps = steps(r.Status, r.ApproverDisplayName, hrAdmin, hrbp, leadDecided, decided, registered, notified)
	return r, err
}

// steps выводит этапы заявки из её статуса и отметок времени: решает руководитель (leadDecided), затем решает
// HR-администратор (decided) и оформляет её, затем уведомляется HR BP.
func steps(status, lead, hrAdmin, hrbp string, leadDecided, decided, registered, notified *time.Time) []step {
	at := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	out := []step{{Role: "lead", DisplayName: lead, Status: "waiting"}, {Role: "hr_admin", DisplayName: hrAdmin, Status: "waiting"},
		{Role: "hrbp", DisplayName: hrbp, Status: "waiting"}}
	switch {
	case status == "pending_hr", status == "approved":
		out[0].Status, out[0].At = "approved", at(leadDecided)
		if leadDecided == nil {
			out[0].At = at(decided)
		}
	case (status == "rejected" || status == "cancelled") && leadDecided != nil:
		out[0].Status, out[0].At = "approved", at(leadDecided)
		out[1].Status, out[1].At = "rejected", at(decided)
		out[2].Status = "skipped"
	case status == "rejected", status == "cancelled":
		out[0].Status, out[0].At = "rejected", at(decided)
		out[1].Status, out[2].Status = "skipped", "skipped"
	}
	if registered != nil {
		out[1].Status, out[1].At = "registered", at(registered)
	}
	if notified != nil {
		out[2].Status, out[2].At = "notified", at(notified)
	}
	return out
}

// findRequest возвращает заявку, подходящую под where (условие на псевдоним r с параметром $1), или ok=false.
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

// getPolicy возвращает тестовую инструкцию команды (absence-policy-<team>) или общую (absence-policy). ACL базы знаний
// применяется как в kb.search, поэтому документ, который вызывающему читать нельзя, никогда не возвращается (R71).
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
		return nil, forbiddenRequest() // никогда не раскрывать, существует ли заявка другого пользователя (R71)
	}
	return r, nil
}

// --- validate_request ---

type dates struct {
	Type      string     `json:"type"`
	StartDate string     `json:"start_date"`
	EndDate   string     `json:"end_date"`
	Documents []document `json:"documents"`
	ExcludeID string     `json:"exclude_id"`
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

// validate проверяет даты новой заявки для c и находит согласующего (руководителя сотрудника).
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

	if _, ok := typeLabels[in.Type]; !ok {
		return out, apierr.Validation().WithMessage("Вид заявки: отгул, отпуск, отсутствие или декрет")
	}
	if len(in.Documents) > maxDocuments {
		return out, apierr.Validation().WithMessage(fmt.Sprintf("Можно приложить не больше %d документов", maxDocuments))
	}
	for _, d := range in.Documents {
		if strings.TrimSpace(d.Name) == "" || len([]rune(d.Name)) > maxDocumentName {
			return out, apierr.Validation().WithMessage("Имя документа: от 1 до 200 символов")
		}
	}
	out.Problems = append(out.Problems, typeProblems(in.Type, start, end, len(in.Documents), s.now())...)
	if !end.Before(start) {
		overlaps, err := overlapProblems(ctx, q, c.Sub, in.StartDate, in.EndDate, in.ExcludeID)
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

// typeProblems применяет правила каждого вида; у отгула остаются правила по рабочим дням (dateProblems).
func typeProblems(kind string, start, end time.Time, docs int, now time.Time) []string {
	y, m, d := now.In(moscow).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	days := int(end.Sub(start).Hours()/24) + 1
	var problems []string
	switch kind {
	case "day_off":
		return dateProblems(start, end, now)
	case "vacation":
		if earliest := today.AddDate(0, 0, vacationNotice); start.Before(earliest) {
			problems = append(problems, fmt.Sprintf("Заявку на отпуск подают не позже чем за %d дней: начало не раньше %s",
				vacationNotice, ru(earliest)))
		}
		if end.Before(start) {
			return append(problems, "Дата окончания не может быть раньше даты начала")
		}
		if days > maxVacationDays {
			problems = append(problems, fmt.Sprintf("Отпуск за один раз — не больше %d календарных дней, выбрано %d", maxVacationDays, days))
		}
	case "absence":
		if start.Before(today.AddDate(0, 0, 1)) {
			problems = append(problems, fmt.Sprintf("Дата начала должна быть не раньше завтрашнего дня (%s)", ru(today.AddDate(0, 0, 1))))
		}
		if end.Before(start) {
			return append(problems, "Дата окончания не может быть раньше даты начала")
		}
		if days > maxAbsenceDays {
			problems = append(problems, fmt.Sprintf("Отсутствие оформляется не больше чем на %d дней, выбрано %d", maxAbsenceDays, days))
		}
	case "parental_leave":
		if start.Before(today) {
			problems = append(problems, "Дата начала уже прошла")
		}
		if end.Before(start) {
			return append(problems, "Дата окончания не может быть раньше даты начала")
		}
		if docs == 0 {
			problems = append(problems, "Для декрета нужен подтверждающий документ — приложите заявление")
		}
	}
	return problems
}

// dateProblems применяет правила отгула: начало не раньше завтра (московская дата now), конец не раньше начала, только
// рабочие дни, не больше maxDays дней подряд.
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

// overlapProblems сообщает о несогласованных или согласованных заявках вызывающего, пересекающих [start, end], кроме
// excludeID (переносимой заявки).
func overlapProblems(ctx context.Context, q querier, sub, start, end, excludeID string) ([]string, error) {
	rows, err := q.Query(ctx, `
SELECT id, start_date, end_date, status
FROM absence.requests
WHERE employee_id = $1 AND status IN ('pending_approval', 'pending_hr', 'approved') AND id <> $4
  AND start_date <= $3::date AND end_date >= $2::date
ORDER BY start_date, id`, sub, start, end, excludeID)
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
		if status != "approved" {
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
	Replaces       string `json:"replaces"` // несогласованная заявка вызывающего внутри новых дат: отменяется тем же шагом
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
	// Сериализуем записи вызывающего: поиск повтора и проверка пересечений должны видеть коммиты друг друга.
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
	if in.ExcludeID != "" {
		return nil, apierr.Validation().WithMessage("exclude_id используется только при проверке переноса")
	}
	d := in.dates
	if in.Replaces != "" {
		// отзыв 12.13: отпуск или декрет, накрывающий несогласованный отгул, заменяет его
		old, ok, err := findRequest(ctx, tx, "r.id = $1", in.Replaces)
		if err != nil {
			return nil, err
		}
		if !ok || old.employeeID != c.Sub {
			return nil, forbiddenRequest()
		}
		if old.Status != "pending_approval" && old.Status != "pending_hr" {
			return nil, apierr.Conflict().WithMessage("Заменить можно только заявку, которую HR-администратор ещё не согласовал")
		}
		if old.StartDate < in.StartDate || old.EndDate > in.EndDate {
			return nil, apierr.Validation().WithMessage("Заменяемая заявка должна целиком попадать в новые даты")
		}
		d.ExcludeID = old.ID
	}
	check, err := s.validate(ctx, tx, c, d)
	if err != nil {
		return nil, err
	}
	if !check.Valid {
		return nil, apierr.Validation().WithMessage("Заявку нельзя создать: " + strings.Join(check.Problems, ". "))
	}
	if in.Replaces != "" {
		if err := cancelInTx(ctx, tx, in.Replaces, ""); err != nil {
			return nil, err
		}
	}
	docs, _ := json.Marshal(nonNilDocs(in.Documents))
	var id string
	err = tx.QueryRow(ctx, `
INSERT INTO absence.requests (employee_id, type, start_date, end_date, comment, approver_id, idempotency_key, documents)
VALUES ($1, $2, $3::date, $4::date, nullif($5, ''), $6, $7::uuid, $8)
RETURNING id`, c.Sub, in.Type, in.StartDate, in.EndDate, in.Comment, check.ApproverID, in.IdempotencyKey, docs).Scan(&id)
	if isUniqueViolation(err) {
		return nil, keyConflict() // ключ принадлежит заявке другого сотрудника
	}
	if err != nil {
		return nil, fmt.Errorf("insert absence request: %w", err)
	}
	start, _ := time.Parse(time.DateOnly, in.StartDate)
	end, _ := time.Parse(time.DateOnly, in.EndDate)
	if _, err := tx.Exec(ctx, `
INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
VALUES ('absence_request', $1, $2, $3, $4)`, id, c.Sub, check.ApproverID, typeLabels[in.Type]+" "+period(start, end)); err != nil {
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
	created.ReplacedID = in.Replaces
	return created, nil
}

// --- cancel_request ---

type cancelIn struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// cancelRequest отзывает заявку вызывающего, пока HR-администратор её не согласовал (отзыв 12.13): заявка становится
// cancelled, а её открытые пункты согласования исчезают из «Согласований». Повтор ключа снова возвращает её.
func (s *service) cancelRequest(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in cancelIn
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "absence:"+c.Sub); err != nil {
		return nil, fmt.Errorf("lock employee: %w", err)
	}
	r, ok, err := findRequest(ctx, tx, "r.id = $1", in.ID)
	if err != nil {
		return nil, err
	}
	if !ok || r.employeeID != c.Sub {
		return nil, forbiddenRequest()
	}
	if r.cancelKey == in.IdempotencyKey && r.Status == "cancelled" {
		replayed := true
		r.Replayed = &replayed
		return r, nil
	}
	if r.Status != "pending_approval" && r.Status != "pending_hr" {
		return nil, apierr.Conflict().WithMessage("Отменить можно только заявку, которую HR-администратор ещё не согласовал. " +
			"Согласованную заявку отменяет HR-администратор — напиши ему.")
	}
	if err := cancelInTx(ctx, tx, r.ID, in.IdempotencyKey); err != nil {
		return nil, err
	}
	out, _, err := findRequest(ctx, tx, "r.id = $1", r.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	out.Replayed = new(bool)
	return out, nil
}

func cancelInTx(ctx context.Context, tx pgx.Tx, id, key string) error {
	if _, err := tx.Exec(ctx, `UPDATE absence.requests SET status = 'cancelled', decided_at = now(),
    cancel_key = coalesce(nullif($2, '')::uuid, cancel_key) WHERE id = $1`, id, key); err != nil {
		if isUniqueViolation(err) {
			return keyConflict()
		}
		return fmt.Errorf("cancel absence request: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE approvals.items SET status = 'cancelled', decided_at = now()
WHERE kind = 'absence_request' AND ref_id = $1 AND status = 'pending'`, id); err != nil {
		return fmt.Errorf("cancel approval items: %w", err)
	}
	return nil
}

// replay возвращает сохранённую заявку при повторе ключа или conflict, если ключ использовался с другими аргументами.
func replay(prev request, c mcpx.Caller, in createIn) (any, error) {
	if prev.employeeID != c.Sub || prev.Type != in.Type || prev.StartDate != in.StartDate ||
		prev.EndDate != in.EndDate || prev.Comment != in.Comment || len(prev.Documents) != len(in.Documents) {
		return nil, keyConflict()
	}
	replayed := true
	prev.Replayed = &replayed
	return prev, nil
}

func nonNilDocs(d []document) []document {
	if d == nil {
		return []document{}
	}
	return d
}

// --- reschedule_request ---

type rescheduleIn struct {
	ID             string `json:"id"`
	StartDate      string `json:"start_date"`
	EndDate        string `json:"end_date"`
	IdempotencyKey string `json:"idempotency_key"`
}

// rescheduleRequest переносит заявку вызывающего на другие даты, пока HR-администратор её не согласовал (чекпоинт
// 11.11). Согласование начинается заново: открытые пункты согласования отменяются, руководитель получает новый. Тот же
// ключ возвращает перенесённую заявку с replayed=true (R79).
func (s *service) rescheduleRequest(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in rescheduleIn
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "absence:"+c.Sub); err != nil {
		return nil, fmt.Errorf("lock employee: %w", err)
	}
	r, ok, err := findRequest(ctx, tx, "r.id = $1", in.ID)
	if err != nil {
		return nil, err
	}
	if !ok || r.employeeID != c.Sub {
		return nil, forbiddenRequest()
	}
	if r.rescheduleKey == in.IdempotencyKey {
		if r.StartDate != in.StartDate || r.EndDate != in.EndDate {
			return nil, keyConflict()
		}
		replayed := true
		r.Replayed = &replayed
		return r, nil
	}
	if r.Status != "pending_approval" && r.Status != "pending_hr" {
		return nil, apierr.Conflict().WithMessage("Перенести можно только заявку, которую HR-администратор ещё не согласовал")
	}
	check, err := s.validate(ctx, tx, c, dates{Type: r.Type, StartDate: in.StartDate, EndDate: in.EndDate,
		Documents: r.Documents, ExcludeID: r.ID})
	if err != nil {
		return nil, err
	}
	if !check.Valid {
		return nil, apierr.Validation().WithMessage("Перенести нельзя: " + strings.Join(check.Problems, ". "))
	}
	_, err = tx.Exec(ctx, `
UPDATE absence.requests SET start_date = $2::date, end_date = $3::date, status = 'pending_approval',
    approver_id = $4, lead_decided_at = NULL, decided_at = NULL, reschedule_key = $5::uuid
WHERE id = $1`, r.ID, in.StartDate, in.EndDate, check.ApproverID, in.IdempotencyKey)
	if isUniqueViolation(err) {
		return nil, keyConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("move absence request: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE approvals.items SET status = 'cancelled', decided_at = now()
WHERE kind = 'absence_request' AND ref_id = $1 AND status = 'pending'`, r.ID); err != nil {
		return nil, fmt.Errorf("cancel approval items: %w", err)
	}
	start, _ := time.Parse(time.DateOnly, in.StartDate)
	end, _ := time.Parse(time.DateOnly, in.EndDate)
	if _, err := tx.Exec(ctx, `
INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
VALUES ('absence_request', $1, $2, $3, $4)`, r.ID, c.Sub, check.ApproverID,
		typeLabels[r.Type]+" "+period(start, end)+" (перенос)"); err != nil {
		return nil, fmt.Errorf("insert approval item: %w", err)
	}
	moved, _, err := findRequest(ctx, tx, "r.id = $1", r.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	moved.Replayed = new(bool)
	return moved, nil
}

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован для другой заявки, подтвердите действие ещё раз")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- форматирование ---

// ru форматирует дату как DD.MM.YYYY.
func ru(t time.Time) string { return t.Format("02.01.2006") }

// period форматирует диапазон дат: одна дата для одного дня, иначе «начало–конец».
func period(start, end time.Time) string {
	if start.Equal(end) {
		return ru(start)
	}
	return ru(start) + "–" + ru(end)
}

// plural выбирает русскую форму множественного числа для n: 1 день, 2 дня, 5 дней.
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
