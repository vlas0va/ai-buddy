// Пакет access реализует мок-сервер MCP «access»: обязательные доступы, каналы команды и заявки на доступ (S4). Данные
// синтетические (R68). Личность и команда берутся только из проверенного JWT (R71): вызывающий видит и запрашивает
// только ресурсы своей команды или общие для всех. Ресурсы с повышенными правами уходят на согласование руководителю
// вызывающего; заявки идемпотентны по ключу (R79).
package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
)

// Register привязывает каждый инструмент контракта access к srv.
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

// latestStatus — LATERAL-подзапрос: самая свежая заявка вызывающего ($1) на ресурс r.
const latestStatus = `
LATERAL (SELECT q.status, coalesce(q.environment, '') AS environment, q.level FROM access.requests q
         WHERE q.resource_id = r.id AND q.employee_id = $1
         ORDER BY q.created_at DESC, q.id DESC LIMIT 1) q`

// resourceStatus — ресурс со статусом вызывающего по нему.
type resourceStatus struct {
	ResourceID      string   `json:"resource_id"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Elevated        bool     `json:"elevated"`
	Status          string   `json:"status"`
	ChecklistItemID string   `json:"checklist_item_id,omitempty"`
	Environments    []string `json:"environments"`
	Levels          []string `json:"levels"`
	Optional        bool     `json:"optional"`
	ApproverLabel   string   `json:"approver_label"`
	Environment     string   `json:"environment,omitempty"` // последней заявки
	Level           string   `json:"level,omitempty"`
}

const resourceCols = `r.id, r.name, r.kind, r.elevated, coalesce(q.status, 'not_requested'), coalesce(r.checklist_item_id, ''),
       r.environments, r.levels, r.optional, r.approver_label, coalesce(q.environment, ''), coalesce(q.level, '')`

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
		err := row.Scan(&r.ResourceID, &r.Name, &r.Kind, &r.Elevated, &r.Status, &r.ChecklistItemID,
			&r.Environments, &r.Levels, &r.Optional, &r.ApproverLabel, &r.Environment, &r.Level)
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

// listRequired возвращает инструменты и пространства, обязательные для команды вызывающего, со статусом вызывающего.
func (s *service) listRequired(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return s.listResources(ctx, `
SELECT `+resourceCols+`
FROM access.resources r
LEFT JOIN `+latestStatus+` ON true
WHERE r.kind IN ('tool', 'space', 'project', 'repo', 'database') AND (r.team_id IS NULL OR r.team_id = $2)
ORDER BY array_position(ARRAY['tool','project','repo','space','database'], r.kind), r.team_id NULLS FIRST, r.id`, c.Sub, c.TeamID)
}

// listMyAccess возвращает все ресурсы, которые запрашивал вызывающий, с последним статусом.
func (s *service) listMyAccess(ctx context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return s.listResources(ctx, `
SELECT `+resourceCols+`
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

// listTeamChannels возвращает каналы команды вызывающего и общие для всех: только название, назначение и признак
// закрытости.
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
	Environment    string `json:"environment"`
	Level          string `json:"level"`
	IdempotencyKey string `json:"idempotency_key"`
}

var levelLabels = map[string]string{"read": "чтение", "write": "редактирование", "admin": "администрирование"}

type requestOut struct {
	ID              string  `json:"id"`
	ResourceID      string  `json:"resource_id"`
	ResourceName    string  `json:"resource_name"`
	Status          string  `json:"status"`
	ApproverID      *string `json:"approver_id"`
	ChecklistItemID *string `json:"checklist_item_id"`
	Environment     *string `json:"environment"`
	Level           string  `json:"level"`
	CreatedAt       string  `json:"created_at"`
	Replayed        bool    `json:"replayed"`

	employeeID string
	reason     string
}

// querier удовлетворяют pgx.Tx и *pgxpool.Pool.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// findRequest возвращает заявку на доступ, подходящую под where (условие на псевдоним q с параметрами $1, $2), или
// ok=false.
func findRequest(ctx context.Context, db querier, where string, args ...any) (requestOut, bool, error) {
	rows, err := db.Query(ctx, `
SELECT q.id, q.resource_id, r.name, q.status, q.approver_id, r.checklist_item_id, q.environment, q.level, q.created_at,
       q.employee_id, coalesce(q.reason, '')
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
		err := row.Scan(&o.ID, &o.ResourceID, &o.ResourceName, &o.Status, &o.ApproverID, &o.ChecklistItemID, &o.Environment,
			&o.Level, &created, &o.employeeID, &o.reason)
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

// requestAccess создаёт заявку на доступ для вызывающего. Ресурс вне команды вызывающего запрещён; обычный ресурс
// выдаётся сразу; ресурс с повышенными правами ждёт руководителя вызывающего (пункт согласования). Повтор
// idempotency_key возвращает сохранённую заявку с replayed=true.
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
	// Сериализуем записи вызывающего, чтобы поиск повтора и проверка дубликатов видели коммиты друг друга.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "access:"+c.Sub); err != nil {
		return nil, fmt.Errorf("lock employee: %w", err)
	}
	prev, found, err := findRequest(ctx, tx, "q.idempotency_key = $1::uuid", in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if in.Level == "" {
		in.Level = "read"
	}
	if found {
		if prev.employeeID != c.Sub || prev.ResourceID != in.ResourceID || prev.reason != in.Reason || prev.Level != in.Level ||
			deref(prev.Environment) != in.Environment {
			return nil, keyConflict()
		}
		prev.Replayed = true
		return prev, nil
	}

	var (
		name         string
		elevated     bool
		environments []string
		levels       []string
	)
	err = tx.QueryRow(ctx, `
SELECT name, elevated, environments, levels FROM access.resources WHERE id = $1 AND (team_id IS NULL OR team_id = $2)`,
		in.ResourceID, c.TeamID).Scan(&name, &elevated, &environments, &levels)
	if errors.Is(err, pgx.ErrNoRows) {
		// Неизвестные ресурсы и ресурсы других команд выглядят одинаково (R71).
		return nil, apierr.Forbidden().WithMessage("Ресурс недоступен для запроса из вашей команды")
	}
	if err != nil {
		return nil, fmt.Errorf("load resource: %w", err)
	}
	if err := checkScope(name, environments, levels, in); err != nil {
		return nil, err
	}
	// чекпоинт 11.11: права на запись и администрирование и всё на prod согласуются; чтение dev/stage выдаётся сразу
	elevated = elevated || in.Level != "read" || in.Environment == "prod"
	last, found, err := findRequest(ctx, tx, "q.employee_id = $1 AND q.resource_id = $2 AND coalesce(q.environment, '') = $3",
		c.Sub, in.ResourceID, in.Environment)
	if err != nil {
		return nil, err
	}
	if found && last.Status == "granted" && last.Level == in.Level {
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
INSERT INTO access.requests (employee_id, resource_id, reason, status, approver_id, idempotency_key, decided_at, environment, level)
VALUES ($1, $2, nullif($3, ''), $4, $5, $6::uuid, CASE WHEN $4 = 'granted' THEN now() END, nullif($7, ''), $8)
RETURNING id`, c.Sub, in.ResourceID, in.Reason, status, approver, in.IdempotencyKey, in.Environment, in.Level).Scan(&id)
	if isUniqueViolation(err) {
		return nil, keyConflict() // ключ принадлежит заявке другого сотрудника
	}
	if err != nil {
		return nil, fmt.Errorf("insert access request: %w", err)
	}
	if elevated {
		if _, err := tx.Exec(ctx, `
INSERT INTO approvals.items (kind, ref_id, requester_id, approver_id, summary)
VALUES ('access_request', $1, $2, $3, $4)`, id, c.Sub, *approver, "Доступ: "+name+scopeLabel(in)); err != nil {
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

// checkScope проверяет окружение и уровень по ресурсу: базе данных нужно окружение, у инструмента его нет; уровень
// должен быть из предлагаемых ресурсом.
func checkScope(name string, environments, levels []string, in requestIn) error {
	switch {
	case len(environments) > 0 && in.Environment == "":
		return apierr.Validation().WithMessage(fmt.Sprintf("Для ресурса «%s» укажите окружение: %v", name, environments))
	case len(environments) == 0 && in.Environment != "":
		return apierr.Validation().WithMessage(fmt.Sprintf("У ресурса «%s» нет окружений", name))
	case in.Environment != "" && !slices.Contains(environments, in.Environment):
		return apierr.Validation().WithMessage(fmt.Sprintf("Окружение %q недоступно для ресурса «%s»", in.Environment, name))
	case !slices.Contains(levels, in.Level):
		return apierr.Validation().WithMessage(fmt.Sprintf("Права «%s» недоступны для ресурса «%s»", levelLabels[in.Level], name))
	}
	return nil
}

// scopeLabel — « (stage, редактирование)» для сводки согласования; пусто для обычного чтения инструмента.
func scopeLabel(in requestIn) string {
	switch {
	case in.Environment != "":
		return fmt.Sprintf(" (%s, %s)", in.Environment, levelLabels[in.Level])
	case in.Level != "read":
		return fmt.Sprintf(" (%s)", levelLabels[in.Level])
	}
	return ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func keyConflict() *apierr.Error {
	return apierr.Conflict().WithMessage("Ключ идемпотентности уже использован для другой заявки, подтвердите действие ещё раз")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
