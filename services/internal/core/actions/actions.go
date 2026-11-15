// Пакет actions хранит результаты подтверждённых записей (core.actions), связывает каждую с задачей плана и держит
// действие и задачу в согласии с решением согласующего (R66, S3, S4). GET /api/actions отдаёт действия вызывающего.
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/period"
)

// DBTX — подмножество *pgxpool.Pool и pgx.Tx, которое здесь используется.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Emitter пишет аналитические события в транзакции вызывающего; реализован в *analytics.Emitter.
type Emitter interface {
	Emit(ctx context.Context, q analytics.DBTX, ev analytics.Event) error
}

// Notifier создаёт уведомления в приложении в транзакции вызывающего; реализован в notify.Store.
type Notifier interface {
	Create(ctx context.Context, q notify.DBTX, n notify.Notification) error
}

// Виды действий (openapi Action.kind).
const (
	KindAbsence = "absence_request"
	KindAccess  = "access_request"
	KindTrip    = "trip_request"
	KindConsent = "consent"
	// KindReschedule переносит заявку на отсутствие на другие даты: обновляет существующее действие, нового не
	// добавляет.
	KindReschedule = "absence_reschedule"
	// KindCancel отзывает заявку на отсутствие (отзыв 12.13): существующее действие становится cancelled, его задача
	// удаляется.
	KindCancel = "absence_cancel"
)

// Статусы действий (openapi Action.status).
const (
	StatusPendingApproval = "pending_approval"
	StatusApproved        = "approved"
	StatusRejected        = "rejected"
	StatusGranted         = "granted"
	StatusCreated         = "created"
	StatusFailed          = "failed"
	StatusSigned          = "signed"
	StatusPendingHR       = "pending_hr" // отсутствие: руководитель согласовал, дальше решает HR-администратор
)

// ErrNotFound возвращается, когда подходящего действия нет (ApplyDecision для заявки, созданной вне чата, ByProposal).
var ErrNotFound = errors.New("actions: action not found")

// Action — это openapi Action плюс поля, которые ядро хранит для себя.
type Action struct {
	ID                  string  `json:"id"`
	Kind                string  `json:"kind"`
	ExternalID          *string `json:"external_id"`
	Status              string  `json:"status"`
	Summary             string  `json:"summary"`
	ApproverDisplayName *string `json:"approver_display_name"`
	TaskID              *string `json:"task_id"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
	// DecisionComment — комментарий согласующего к решению (например, причина отказа), DecidedBy — кто решил.
	DecisionComment *string `json:"decision_comment"`
	DecidedBy       *string `json:"decided_by"`

	UserID     string `json:"-"`
	ProposalID string `json:"-"`
	MessageID  string `json:"-"` // сообщение ассистента, сохранённое вместе с действием (чат повторяет его в done)
}

// Result — событие action_result из contracts/agent-stream.md.
type Result struct {
	ActionID            string  `json:"action_id"`
	Kind                string  `json:"kind"`
	ExternalID          *string `json:"external_id"`
	Status              string  `json:"status"`
	Summary             string  `json:"summary"`
	ApproverDisplayName *string `json:"approver_display_name"`
	TaskID              *string `json:"task_id"`
	// ProposalID связывает результат с его черновиком (список доступов показывает результаты внутри своей карточки)
	ProposalID string `json:"proposal_id,omitempty"`
}

// Result возвращает событие action_result для a.
func (a Action) Result() Result {
	return Result{ActionID: a.ID, Kind: a.Kind, ExternalID: a.ExternalID, Status: a.Status, Summary: a.Summary,
		ApproverDisplayName: a.ApproverDisplayName, TaskID: a.TaskID, ProposalID: a.ProposalID}
}

// SaveInput — успешная запись через MCP, которую нужно сохранить.
type SaveInput struct {
	Claims     authn.Claims    // владелец (проверенные claims JWT)
	ProposalID string          // ключ идемпотентности записи (= id черновика)
	Kind       string          // KindAbsence or KindAccess
	Tool       string          // draft.tool
	Args       json.RawMessage // draft.args
	Result     json.RawMessage // структурированный результат инструмента
	MessageID  string          // необязательно: сообщение ассистента, которое объявляет результат
	ProposedAt time.Time       // необязательно: для duration_ms_since_proposal
	Attempt    int             // необязательно: 1 для первого подтверждения, 2+ после сбоя
	// Необязательные подсказки из tool_result агента, если их нет в результате MCP: пункт чек-листа задачи плана для
	// связи (task_checklist_item_id) и отображаемое имя согласующего.
	ChecklistItemID string
	ApproverName    string
}

// Service реализует сценарии работы с действиями. Emitter и Notifier могут быть nil (события и уведомления
// отбрасываются).
type Service struct {
	emitter  Emitter
	notifier Notifier
	log      *slog.Logger
	// Now — часы для текущего этапа новых задач; тесты могут их подменить.
	Now func() time.Time
}

// NewService возвращает Service.
func NewService(emitter Emitter, notifier Notifier, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{emitter: emitter, notifier: notifier, log: log, Now: time.Now}
}

const actionCols = `a.id::text, a.user_id, a.kind, a.external_id, a.status, a.summary,
	a.payload->>'approver_display_name', a.task_id::text,
	to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
	to_char(a.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
	a.idempotency_key::text, COALESCE(a.payload->>'message_id', ''),
	a.payload->>'decision_comment', a.payload->>'decided_by'`

func scanAction(row pgx.Row) (Action, error) {
	var a Action
	err := row.Scan(&a.ID, &a.UserID, &a.Kind, &a.ExternalID, &a.Status, &a.Summary, &a.ApproverDisplayName, &a.TaskID,
		&a.CreatedAt, &a.UpdatedAt, &a.ProposalID, &a.MessageID, &a.DecisionComment, &a.DecidedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("scan action: %w", err)
	}
	return a, nil
}

// description — то, что Save извлекает из результата инструмента.
type description struct {
	externalID    string
	status        string
	summary       string
	approverName  string
	taskTitle     string
	checklistItem string  // доступ: связать задачу чек-листа с этим пунктом
	dueAt         *string // отсутствие, командировка: первый день
	noTask        bool    // согласие: подпись — не шаг плана адаптации
}

// Save записывает успешную запись в q (вызывать в транзакции, которая помечает черновик выполненным): вставляет
// core.actions с idempotency_key = ProposalID, связывает задачу плана (доступ → задача плана стажёра по пункту
// чек-листа ресурса, иначе новая задача с source = action в текущем этапе), выставляет linked_action_id и отправляет
// action.created. Повторное сохранение того же ProposalID возвращает сохранённое действие без изменений.
func (s *Service) Save(ctx context.Context, q DBTX, in SaveInput) (Action, error) {
	if in.Kind == KindReschedule {
		return s.reschedule(ctx, q, in)
	}
	if in.Kind == KindCancel {
		var r mcpclient.AbsenceRequest
		if err := json.Unmarshal(in.Result, &r); err != nil || r.ID == "" {
			return Action{}, fmt.Errorf("cancel result without id: %w", apierr.Internal())
		}
		return s.cancelAbsence(ctx, q, in.Claims, r.ID, in.ProposalID)
	}
	d, err := s.describe(ctx, q, in)
	if err != nil {
		return Action{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"proposal_id": in.ProposalID, "tool": in.Tool, "args": in.Args, "result": in.Result,
		"approver_display_name": nullable(d.approverName), "message_id": nullable(in.MessageID),
	})
	if err != nil {
		return Action{}, fmt.Errorf("encode action payload: %w", err)
	}
	var id string
	err = q.QueryRow(ctx, `INSERT INTO core.actions (user_id, kind, external_id, status, summary, payload, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id::text`,
		in.Claims.Sub, in.Kind, nullable(d.externalID), d.status, d.summary, payload, in.ProposalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		a, err := s.ByProposal(ctx, q, in.Claims.Sub, in.ProposalID)
		if errors.Is(err, ErrNotFound) {
			return Action{}, apierr.Conflict()
		}
		return a, err
	}
	if err != nil {
		return Action{}, fmt.Errorf("insert action: %w", err)
	}
	var taskID *string
	if !d.noTask && d.status != StatusRejected { // сразу отклонённая заявка — не шаг плана
		taskID, err = s.linkTask(ctx, q, in.Claims.Sub, id, d)
		if err != nil {
			return Action{}, err
		}
	}
	if taskID != nil {
		if _, err := q.Exec(ctx, `UPDATE core.actions SET task_id = $2 WHERE id = $1`, id, *taskID); err != nil {
			return Action{}, fmt.Errorf("link action task: %w", err)
		}
	}
	if in.Kind == KindAbsence {
		// отпуск, накрывший несогласованный отгул, заменил его (absence.create_request с replaces, отзыв 12.13)
		var r struct {
			ReplacedID string `json:"replaced_id"`
		}
		if json.Unmarshal(in.Result, &r) == nil && r.ReplacedID != "" {
			if _, err := s.cancelAbsence(ctx, q, in.Claims, r.ReplacedID, in.ProposalID); err != nil && !errors.Is(err, ErrNotFound) {
				return Action{}, err
			}
		}
	}
	props := map[string]any{"kind": in.Kind, "action_id": id, "proposal_id": in.ProposalID}
	if !in.ProposedAt.IsZero() {
		props["duration_ms_since_proposal"] = s.Now().Sub(in.ProposedAt).Milliseconds()
	}
	if in.Attempt > 0 {
		props["attempt"] = in.Attempt
	}
	if err := s.emit(ctx, q, analytics.Event{Name: "action.created", UserID: in.Claims.Sub, Role: in.Claims.Role,
		TeamID: in.Claims.TeamID, Props: props}); err != nil {
		return Action{}, err
	}
	return scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a WHERE a.id = $1`, id))
}

func (s *Service) describe(ctx context.Context, q DBTX, in SaveInput) (description, error) {
	switch in.Kind {
	case KindAbsence:
		var r mcpclient.AbsenceRequest
		if err := json.Unmarshal(in.Result, &r); err != nil || r.ID == "" {
			return description{}, fmt.Errorf("absence result without id: %w", apierr.Internal())
		}
		name, err := s.displayName(ctx, q, firstNonEmpty(r.ApproverDisplayName, in.ApproverName), &r.ApproverID)
		if err != nil {
			return description{}, err
		}
		dates := ruDate(r.StartDate)
		if r.EndDate != "" && r.EndDate != r.StartDate {
			dates += "–" + ruDate(r.EndDate)
		}
		label := absenceLabel(r.Type)
		d := description{externalID: r.ID, status: absenceStatus(r.Status), summary: label + " " + dates,
			taskTitle: label + " " + dates, approverName: name}
		if r.StartDate != "" {
			d.dueAt = &r.StartDate
		}
		// S3: никогда не задача чек-листа (подсказка агента task_checklist_item_id игнорируется); создаётся задача с
		// source = action.
		if name != "" {
			d.summary += ", согласующий — " + name
		}
		return d, nil
	case KindAccess:
		var r mcpclient.AccessRequest
		if err := json.Unmarshal(in.Result, &r); err != nil || r.ID == "" {
			return description{}, fmt.Errorf("access result without id: %w", apierr.Internal())
		}
		name, err := s.displayName(ctx, q, in.ApproverName, r.ApproverID)
		if err != nil {
			return description{}, err
		}
		resource := r.ResourceName
		if resource == "" {
			resource = r.ResourceID
		}
		resource += accessScope(r.Environment, r.Level)
		d := description{externalID: r.ID, status: accessStatus(r.Status), taskTitle: "Доступ: " + resource, approverName: name,
			checklistItem: in.ChecklistItemID}
		if r.ChecklistItemID != nil && *r.ChecklistItemID != "" {
			d.checklistItem = *r.ChecklistItemID
		}
		switch {
		case d.status == StatusGranted:
			d.summary = "Доступ: " + resource + " — выдан"
		case d.status == StatusRejected:
			d.summary = "Доступ: " + resource + " — отклонён"
		case name != "":
			d.summary = "Доступ: " + resource + ", согласующий — " + name
		default:
			d.summary = "Доступ: " + resource + " — на согласовании"
		}
		return d, nil
	case KindTrip:
		var r mcpclient.TripRequest
		if err := json.Unmarshal(in.Result, &r); err != nil || r.ID == "" {
			return description{}, fmt.Errorf("trip result without id: %w", apierr.Internal())
		}
		name, err := s.displayName(ctx, q, firstNonEmpty(r.ApproverDisplayName, in.ApproverName), &r.ApproverID)
		if err != nil {
			return description{}, err
		}
		dates := ruDate(r.StartDate)
		if r.EndDate != "" && r.EndDate != r.StartDate {
			dates += "–" + ruDate(r.EndDate)
		}
		d := description{externalID: r.ID, status: absenceStatus(r.Status), summary: "Командировка: " + r.City + ", " + dates,
			taskTitle: "Командировка: " + r.City, approverName: name}
		if r.StartDate != "" {
			d.dueAt = &r.StartDate
		}
		if name != "" {
			d.summary += ", согласующий — " + name
		}
		return d, nil
	case KindConsent:
		var r mcpclient.ConsentSignature
		if err := json.Unmarshal(in.Result, &r); err != nil || r.ID == "" {
			return description{}, fmt.Errorf("consent result without id: %w", apierr.Internal())
		}
		title := firstNonEmpty(r.Title, r.ConsentID)
		return description{externalID: r.ID, status: StatusSigned, summary: "Согласие: " + title + " — подписано",
			noTask: true}, nil
	default:
		return description{}, apierr.Validation().WithMessage("Неизвестный тип действия")
	}
}

// displayName возвращает given или отображаемое имя пользователя по id (пусто, если неизвестен).
func (s *Service) displayName(ctx context.Context, q DBTX, given string, id *string) (string, error) {
	if given != "" || id == nil || *id == "" {
		return given, nil
	}
	var name string
	err := q.QueryRow(ctx, `SELECT display_name FROM core.users WHERE id = $1`, *id).Scan(&name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("load approver name: %w", err)
	}
	return name, nil
}

// linkTask связывает действие id с задачей плана userID и возвращает id задачи (nil, если у пользователя нет плана).
func (s *Service) linkTask(ctx context.Context, q DBTX, userID, actionID string, d description) (*string, error) {
	status := taskStatus(d.status)
	if d.checklistItem != "" {
		var taskID string
		err := q.QueryRow(ctx, `SELECT t.id::text FROM core.plan_tasks t JOIN core.plans p ON p.id = t.plan_id
			WHERE p.user_id = $1 AND p.plan_role = 'intern' AND t.parent_task_id IS NULL AND t.checklist_item_id = $2
				AND NOT t.human_only -- buddy only reminds about human-only items, an action never completes them (R05)
			ORDER BY t.position, t.id LIMIT 1`, userID, d.checklistItem).Scan(&taskID)
		switch {
		case err == nil:
			// Выполненная задача остаётся выполненной и сохраняет связь: следующая заявка на другой ресурс того же
			// пункта не должна её переоткрыть.
			_, err = q.Exec(ctx, `UPDATE core.plan_tasks SET
				linked_action_id = CASE WHEN status = 'done' THEN linked_action_id ELSE $2::uuid END,
				status = CASE WHEN status = 'done' THEN status ELSE $3 END,
				updated_at = now()
				WHERE id = $1`, taskID, actionID, status)
			if err != nil {
				return nil, fmt.Errorf("link checklist task: %w", err)
			}
			return &taskID, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("find checklist task: %w", err)
		}
	}
	var planID string
	var cohort *string
	err := q.QueryRow(ctx, `SELECT p.id::text, u.cohort_start::text FROM core.plans p LEFT JOIN core.users u ON u.id = p.intern_id
		WHERE p.user_id = $1 ORDER BY (p.plan_role = 'intern') DESC, p.created_at, p.id LIMIT 1`, userID).Scan(&planID, &cohort)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find own plan: %w", err)
	}
	cur := period.Week1
	if cohort != nil {
		if start, err := period.ParseDate(*cohort); err == nil {
			cur = period.Current(start, s.Now())
		}
	}
	var taskID string
	err = q.QueryRow(ctx, `INSERT INTO core.plan_tasks (plan_id, source, period, position, title, description, status, due_at, linked_action_id)
		VALUES ($1::uuid, 'action', $2,
			COALESCE((SELECT MAX(position) FROM core.plan_tasks WHERE plan_id = $1::uuid AND period = $2), 0) + 1,
			$3, $4, $5, $6::date, $7::uuid)
		RETURNING id::text`,
		planID, string(cur), d.taskTitle, "Создано ассистентом по заявке "+d.externalID, status, d.dueAt, actionID).Scan(&taskID)
	if err != nil {
		return nil, fmt.Errorf("create action task: %w", err)
	}
	return &taskID, nil
}

// ByProposal возвращает действие userID, сохранённое для proposalID, или ErrNotFound.
func (s *Service) ByProposal(ctx context.Context, q DBTX, userID, proposalID string) (Action, error) {
	return scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a
		WHERE a.idempotency_key = $2::uuid AND a.user_id = $1`, userID, proposalID))
}

// listLimit ограничивает GET /api/actions.
const listLimit = 100

// List возвращает действия userID, новые сначала.
func (s *Service) List(ctx context.Context, q DBTX, userID string) ([]Action, error) {
	rows, err := q.Query(ctx, `SELECT `+actionCols+` FROM core.actions a WHERE a.user_id = $1
		ORDER BY a.created_at DESC, a.id DESC LIMIT $2`, userID, listLimit)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	defer rows.Close()
	out := []Action{}
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	return out, nil
}

// Scope ограничивает ApplyDecision действием одного заявителя, в заявке которого указан один согласующий (R71). Оба
// поля берутся из результата decide сервиса согласований и проверенных claims решающего руководителя, никогда не от
// агента.
type Scope struct {
	RequesterID string // владелец действия (requester_id в согласованиях)
	ApproverID  string // approver_id в сохранённом результате MCP заявки (решающий руководитель)
	// Stage — этап согласования заявки на отсутствие: "" или "lead", затем "hr_admin". На этапе HR-администратора
	// решает не согласующий из заявки: сервис согласований уже проверил, что пункт его.
	Stage string
	// Comment — комментарий решающего из результата decide (причина отказа); стажёр видит его в «Моих заявках».
	Comment string
}

// ApplyDecision применяет решение согласующего ("approved" или "rejected", как вернул approvals.decide) к действию вида
// kind с externalID (ABS-…/ACC-…), владелец которого scope.RequesterID, а согласующий в заявке — scope.ApproverID:
// статус действия approved/granted/rejected и комментарий решающего, связанная задача done (при отказе задача,
// созданная по заявке, удаляется из плана, а задача чек-листа возвращается в not_started; задача human_only не
// меняется) и уведомление status_change владельцу с комментарием, всё через q. Сообщает, изменилось ли что-то (повторное решение ничего не
// делает), и возвращает ErrNotFound, если в ядре нет такого действия; оба поля scope обязательны.
func (s *Service) ApplyDecision(ctx context.Context, q DBTX, kind, externalID, decision string, scope Scope) (Action, bool, error) {
	status, err := decisionStatus(kind, decision)
	if err != nil {
		return Action{}, false, err
	}
	hrStage := kind == KindAbsence && scope.Stage == "hr_admin"
	if kind == KindAbsence && !hrStage && status == StatusApproved {
		status = StatusPendingHR // согласование руководителя передаёт заявку HR-администратору
	}
	if scope.RequesterID == "" || scope.ApproverID == "" {
		return Action{}, false, ErrNotFound
	}
	approver := scope.ApproverID
	if hrStage {
		approver = "" // ниже сопоставляется только по заявителю и заявке
	}
	a, err := scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a
		WHERE a.kind = $1 AND a.external_id = $2 AND a.user_id = $3 AND ($4 = '' OR a.payload->'result'->>'approver_id' = $4)
		ORDER BY a.created_at LIMIT 1 FOR UPDATE`, kind, externalID, scope.RequesterID, approver))
	if err != nil {
		return Action{}, false, err
	}
	if a.Status == status || (status == StatusPendingHR && a.Status == StatusApproved) {
		return a, false, nil // повторённое решение руководителя никогда не отменяет согласование HR-администратора
	}
	comment := strings.TrimSpace(scope.Comment)
	// Комментарий показывается вместе с автором: новый комментарий заменяет прежний, отказ без комментария убирает
	// прежний (например, «Хорошего отдыха» руководителя), решение без комментария его сохраняет.
	if _, err := q.Exec(ctx, `UPDATE core.actions SET status = $2, updated_at = now(),
		payload = CASE
			WHEN $3 <> '' THEN payload || jsonb_build_object('decision_comment', $3::text,
				'decided_by', (SELECT display_name FROM core.users WHERE id = $4))
			WHEN $2 = 'rejected' THEN payload - 'decision_comment' - 'decided_by'
			ELSE payload END
		WHERE id = $1`, a.ID, status, comment, scope.ApproverID); err != nil {
		return Action{}, false, fmt.Errorf("update action status: %w", err)
	}
	if a.TaskID != nil {
		if err := s.applyToTask(ctx, q, a, status); err != nil {
			return Action{}, false, err
		}
	}
	if s.notifier != nil {
		body := a.Summary
		if comment != "" {
			body += ". Комментарий: " + comment
		}
		if err := s.notifier.Create(ctx, q, notify.Notification{UserID: a.UserID, Kind: notify.KindStatusChange,
			Title: decisionTitle(kind, status, a.Summary), Body: body, DedupeKey: "action:" + a.ID + ":" + status}); err != nil {
			return Action{}, false, fmt.Errorf("notify owner: %w", err)
		}
		if kind == KindAbsence && status == StatusApproved {
			if err := s.notifyHRBP(ctx, q, a); err != nil {
				return Action{}, false, err
			}
		}
	}
	a, err = scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a WHERE a.id = $1`, a.ID))
	return a, err == nil, err
}

// applyToTask отражает решение в задаче плана действия a. Отклонённая заявка уходит из плана: задача, созданная по
// заявке (source = action), удаляется, а задача чек-листа (например, доступ по пункту CL-04) возвращается в not_started
// и отвязывается — её можно закрыть другой заявкой. Задачи human_only решение не трогает.
func (s *Service) applyToTask(ctx context.Context, q DBTX, a Action, status string) error {
	if status != StatusRejected {
		if _, err := q.Exec(ctx, `UPDATE core.plan_tasks SET status = $2, updated_at = now()
			WHERE id = $1 AND linked_action_id = $3 AND NOT human_only`,
			*a.TaskID, taskStatus(status), a.ID); err != nil {
			return fmt.Errorf("update linked task: %w", err)
		}
		return nil
	}
	var source string
	err := q.QueryRow(ctx, `SELECT source FROM core.plan_tasks WHERE id = $1 AND linked_action_id = $2 AND NOT human_only`,
		*a.TaskID, a.ID).Scan(&source)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load linked task: %w", err)
	}
	if source != "action" {
		if _, err := q.Exec(ctx, `UPDATE core.plan_tasks SET status = $2, linked_action_id = NULL, updated_at = now() WHERE id = $1`,
			*a.TaskID, taskStatus(status)); err != nil {
			return fmt.Errorf("reopen checklist task: %w", err)
		}
		return nil
	}
	if _, err := q.Exec(ctx, `UPDATE core.actions SET task_id = NULL WHERE id = $1`, a.ID); err != nil {
		return fmt.Errorf("unlink rejected task: %w", err)
	}
	if _, err := q.Exec(ctx, `DELETE FROM core.plan_tasks WHERE id = $1 AND linked_action_id = $2`, *a.TaskID, a.ID); err != nil {
		return fmt.Errorf("remove rejected task: %w", err)
	}
	return nil
}

// notifyHRBP сообщает HR BP, что HR-администратор оформил согласованный отгул (ответы организаторов 10.11: в отгуле
// участвуют руководитель, HR-администратор и HR BP). HR BP получает только сводку: без комментария и причины.
func (s *Service) notifyHRBP(ctx context.Context, q DBTX, a Action) error {
	rows, err := q.Query(ctx, `SELECT u.id, owner.display_name FROM core.users u, core.users owner
		WHERE u.role = 'hrbp' AND owner.id = $1 ORDER BY u.id`, a.UserID)
	if err != nil {
		return fmt.Errorf("load hr bp: %w", err)
	}
	type target struct{ id, owner string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.owner); err != nil {
			rows.Close()
			return fmt.Errorf("scan hr bp: %w", err)
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load hr bp: %w", err)
	}
	for _, t := range targets {
		if err := s.notifier.Create(ctx, q, notify.Notification{UserID: t.id, Kind: notify.KindStatusChange,
			Title: absenceLabel(labelType(a.Summary)) + " оформлен: " + t.owner, Body: withoutApprover(a.Summary) + ". Согласовано руководителем и HR-администратором, оформлено в кадровой системе.",
			DedupeKey: "hrbp:" + a.ID}); err != nil {
			return fmt.Errorf("notify hr bp: %w", err)
		}
	}
	return nil
}

func isAbsenceLabel(l string) bool {
	for _, v := range absenceLabels {
		if v == l {
			return true
		}
	}
	return false
}

// labelType возвращает вид отсутствия, с подписи которого начинается summary (day_off, если ни с какой).
func labelType(summary string) string {
	first := strings.SplitN(summary, " ", 2)[0]
	for k, v := range absenceLabels {
		if v == first {
			return k
		}
	}
	return "day_off"
}

// reschedule применяет перенос заявки на отсутствие (absence.reschedule_request) к существующему действию вызывающего:
// новые даты в summary и связанной задаче, возврат в pending_approval, новый результат MCP в payload. Повторное
// подтверждение применяет то же изменение и возвращает то же действие.
func (s *Service) reschedule(ctx context.Context, q DBTX, in SaveInput) (Action, error) {
	var r mcpclient.AbsenceRequest
	if err := json.Unmarshal(in.Result, &r); err != nil || r.ID == "" {
		return Action{}, fmt.Errorf("reschedule result without id: %w", apierr.Internal())
	}
	a, err := scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a
		WHERE a.kind = $1 AND a.external_id = $2 AND a.user_id = $3 ORDER BY a.created_at LIMIT 1 FOR UPDATE`,
		KindAbsence, r.ID, in.Claims.Sub))
	if err != nil {
		return Action{}, err
	}
	d, err := s.describe(ctx, q, SaveInput{Claims: in.Claims, Kind: KindAbsence, Result: in.Result, ApproverName: in.ApproverName})
	if err != nil {
		return Action{}, err
	}
	if _, err := q.Exec(ctx, `UPDATE core.actions SET status = $2, summary = $3,
		payload = jsonb_set(payload, '{result}', $4::jsonb), updated_at = now() WHERE id = $1`,
		a.ID, d.status, d.summary, string(in.Result)); err != nil {
		return Action{}, fmt.Errorf("update moved action: %w", err)
	}
	if a.TaskID != nil {
		if _, err := q.Exec(ctx, `UPDATE core.plan_tasks SET title = $2, due_at = $3::date, status = $4, updated_at = now()
			WHERE id = $1 AND linked_action_id = $5`, *a.TaskID, d.taskTitle, d.dueAt, taskStatus(d.status), a.ID); err != nil {
			return Action{}, fmt.Errorf("update moved task: %w", err)
		}
	}
	if err := s.emit(ctx, q, analytics.Event{Name: "action.rescheduled", UserID: in.Claims.Sub, Role: in.Claims.Role,
		TeamID: in.Claims.TeamID, Props: map[string]any{"action_id": a.ID, "proposal_id": in.ProposalID}}); err != nil {
		return Action{}, err
	}
	return scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a WHERE a.id = $1`, a.ID))
}

// cancelAbsence помечает действие отсутствия вызывающего как cancelled и удаляет его задачу плана (заявки в плане
// больше нет). Повторный вызов возвращает то же действие.
func (s *Service) cancelAbsence(ctx context.Context, q DBTX, c authn.Claims, externalID, proposalID string) (Action, error) {
	a, err := scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a
		WHERE a.kind = $1 AND a.external_id = $2 AND a.user_id = $3 ORDER BY a.created_at LIMIT 1 FOR UPDATE`,
		KindAbsence, externalID, c.Sub))
	if err != nil {
		return Action{}, err
	}
	if _, err := q.Exec(ctx, `UPDATE core.actions SET status = 'cancelled', task_id = NULL,
		summary = CASE WHEN summary LIKE '%(отменена)' THEN summary ELSE summary || ' (отменена)' END, updated_at = now()
		WHERE id = $1`, a.ID); err != nil {
		return Action{}, fmt.Errorf("cancel action: %w", err)
	}
	if a.TaskID != nil {
		if _, err := q.Exec(ctx, `DELETE FROM core.plan_tasks WHERE id = $1 AND linked_action_id = $2`, *a.TaskID, a.ID); err != nil {
			return Action{}, fmt.Errorf("remove cancelled task: %w", err)
		}
	}
	if err := s.emit(ctx, q, analytics.Event{Name: "action.cancelled", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
		Props: map[string]any{"action_id": a.ID, "proposal_id": proposalID}}); err != nil {
		return Action{}, err
	}
	return scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a WHERE a.id = $1`, a.ID))
}

var accessLevels = map[string]string{"read": "чтение", "write": "редактирование", "admin": "администрирование"}

// accessScope — « (stage, чтение)» или « (редактирование)»; пусто для обычного чтения без окружения.
func accessScope(env *string, level string) string {
	switch {
	case env != nil && *env != "":
		return " (" + *env + ", " + accessLevels[firstNonEmpty(level, "read")] + ")"
	case level != "" && level != "read":
		return " (" + accessLevels[level] + ")"
	}
	return ""
}

// withoutApprover убирает хвост «, согласующий — …» из summary.
func withoutApprover(s string) string {
	if i := strings.Index(s, ", согласующий"); i >= 0 {
		return s[:i]
	}
	return s
}

func (s *Service) emit(ctx context.Context, q DBTX, ev analytics.Event) error {
	if s.emitter == nil {
		return nil
	}
	if err := s.emitter.Emit(ctx, q, ev); err != nil {
		return fmt.Errorf("emit %s: %w", ev.Name, err)
	}
	return nil
}

func decisionStatus(kind, decision string) (string, error) {
	switch {
	case decision == "rejected" && (kind == KindAbsence || kind == KindAccess || kind == KindTrip):
		return StatusRejected, nil
	case decision == "approved" && (kind == KindAbsence || kind == KindTrip):
		return StatusApproved, nil
	case (decision == "approved" || decision == "granted") && kind == KindAccess:
		return StatusGranted, nil
	}
	return "", apierr.Validation().WithMessage("Неизвестное решение по заявке")
}

func decisionTitle(kind, status, summary string) string {
	label := strings.SplitN(summary, " ", 2)[0]
	if kind == KindAbsence && (label == "" || !isAbsenceLabel(label)) {
		label = "Отгул"
	}
	neuter := label == "Отсутствие"
	switch {
	case kind == KindAbsence && status == StatusPendingHR:
		return label + ": руководитель согласовал, ждёт HR-администратора"
	case kind == KindAbsence && status == StatusApproved && neuter:
		return label + " согласовано"
	case kind == KindAbsence && status == StatusApproved:
		return label + " согласован"
	case kind == KindAbsence && neuter:
		return label + " отклонено"
	case kind == KindAbsence:
		return label + " отклонён"
	case kind == KindTrip && status == StatusApproved:
		return "Командировка согласована"
	case kind == KindTrip:
		return "Командировка отклонена"
	case status == StatusGranted:
		return "Доступ выдан"
	default:
		return "Запрос доступа отклонён"
	}
}

// absenceLabels называют виды отсутствия (чекпоинт 11.11: отпуск, отгул, отсутствие, декрет).
var absenceLabels = map[string]string{"day_off": "Отгул", "vacation": "Отпуск", "absence": "Отсутствие", "parental_leave": "Декрет"}

func absenceLabel(kind string) string {
	if l, ok := absenceLabels[kind]; ok {
		return l
	}
	return "Отгул"
}

func absenceStatus(s string) string {
	switch s {
	case StatusPendingApproval, StatusPendingHR, StatusApproved, StatusRejected:
		return s
	}
	return StatusCreated
}

func accessStatus(s string) string {
	switch s {
	case StatusPendingApproval, StatusGranted, StatusRejected:
		return s
	}
	return StatusCreated
}

// taskStatus переводит статус действия в статус его задачи плана.
func taskStatus(actionStatus string) string {
	switch actionStatus {
	case StatusApproved, StatusGranted:
		return "done"
	case StatusRejected, StatusFailed:
		return "not_started"
	case StatusPendingApproval, StatusPendingHR:
		return "waiting_approval"
	}
	return "in_progress"
}

// ruDate форматирует YYYY-MM-DD как DD.MM.YYYY (если это не дата, возвращает вход как есть).
func ruDate(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format("02.01.2006")
}

// RuDate — это ruDate для других пакетов (сводки чата).
func RuDate(d string) string { return ruDate(d) }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ByID возвращает действие с id или ErrNotFound.
func (s *Service) ByID(ctx context.Context, q DBTX, id string) (Action, error) {
	return scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a WHERE a.id = $1::uuid`, id))
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
