// Package actions stores the results of confirmed writes (core.actions), links each one to a plan task and keeps
// action and task in step with the approver's decision (R66, S3, S4). GET /api/actions lists the caller's actions.
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

// DBTX is the subset of *pgxpool.Pool and pgx.Tx used here.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Emitter records analytics events in the caller's transaction; *analytics.Emitter implements it.
type Emitter interface {
	Emit(ctx context.Context, q analytics.DBTX, ev analytics.Event) error
}

// Notifier creates in-app notifications in the caller's transaction; notify.Store implements it.
type Notifier interface {
	Create(ctx context.Context, q notify.DBTX, n notify.Notification) error
}

// Action kinds (openapi Action.kind).
const (
	KindAbsence = "absence_request"
	KindAccess  = "access_request"
	KindTrip    = "trip_request"
	KindConsent = "consent"
	// KindReschedule moves an absence request to other dates: it updates the existing absence action, never adds one.
	KindReschedule = "absence_reschedule"
)

// Action statuses (openapi Action.status).
const (
	StatusPendingApproval = "pending_approval"
	StatusApproved        = "approved"
	StatusRejected        = "rejected"
	StatusGranted         = "granted"
	StatusCreated         = "created"
	StatusFailed          = "failed"
	StatusSigned          = "signed"
	StatusPendingHR       = "pending_hr" // absence: the lead approved, the HR admin decides next
)

// ErrNotFound is returned when no action matches (ApplyDecision for a request made outside the chat, ByProposal).
var ErrNotFound = errors.New("actions: action not found")

// Action is the openapi Action plus fields core keeps for itself.
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

	UserID     string `json:"-"`
	ProposalID string `json:"-"`
	MessageID  string `json:"-"` // assistant message saved with the action (chat replays it in done)
}

// Result is the action_result event of contracts/agent-stream.md.
type Result struct {
	ActionID            string  `json:"action_id"`
	Kind                string  `json:"kind"`
	ExternalID          *string `json:"external_id"`
	Status              string  `json:"status"`
	Summary             string  `json:"summary"`
	ApproverDisplayName *string `json:"approver_display_name"`
	TaskID              *string `json:"task_id"`
}

// Result returns the action_result event of a.
func (a Action) Result() Result {
	return Result{ActionID: a.ID, Kind: a.Kind, ExternalID: a.ExternalID, Status: a.Status, Summary: a.Summary,
		ApproverDisplayName: a.ApproverDisplayName, TaskID: a.TaskID}
}

// SaveInput is a successful MCP write to record.
type SaveInput struct {
	Claims     authn.Claims    // owner (verified JWT claims)
	ProposalID string          // idempotency key of the write (= proposal id)
	Kind       string          // KindAbsence or KindAccess
	Tool       string          // draft.tool
	Args       json.RawMessage // draft.args
	Result     json.RawMessage // structured result of the tool
	MessageID  string          // optional: assistant message that announces the result
	ProposedAt time.Time       // optional: for duration_ms_since_proposal
	Attempt    int             // optional: 1 for the first confirmation, 2+ after a failure
	// Optional hints from the agent's tool_result, used when the MCP result lacks them: the checklist item of the
	// plan task to link (task_checklist_item_id) and the approver's display name.
	ChecklistItemID string
	ApproverName    string
}

// Service implements the action use cases. Emitter and Notifier may be nil (events/notifications dropped).
type Service struct {
	emitter  Emitter
	notifier Notifier
	log      *slog.Logger
	// Now is the clock used for the current period of new tasks; tests may replace it.
	Now func() time.Time
}

// NewService returns a Service.
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
	a.idempotency_key::text, COALESCE(a.payload->>'message_id', '')`

func scanAction(row pgx.Row) (Action, error) {
	var a Action
	err := row.Scan(&a.ID, &a.UserID, &a.Kind, &a.ExternalID, &a.Status, &a.Summary, &a.ApproverDisplayName, &a.TaskID,
		&a.CreatedAt, &a.UpdatedAt, &a.ProposalID, &a.MessageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("scan action: %w", err)
	}
	return a, nil
}

// description is what Save derives from a tool result.
type description struct {
	externalID    string
	status        string
	summary       string
	approverName  string
	taskTitle     string
	checklistItem string  // access: link the checklist task with this item
	dueAt         *string // absence, trip: the first day
	noTask        bool    // consent: a signature is not a step of the onboarding plan
}

// Save records a successful write in q (run it in the transaction that marks the proposal executed): inserts
// core.actions with idempotency_key = ProposalID, links a plan task (access → the intern-plan task of the resource's
// checklist item, otherwise a new task with source = action in the current period), sets linked_action_id and emits
// action.created. Saving the same ProposalID again returns the stored action unchanged.
func (s *Service) Save(ctx context.Context, q DBTX, in SaveInput) (Action, error) {
	if in.Kind == KindReschedule {
		return s.reschedule(ctx, q, in)
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
	if !d.noTask {
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
		// S3: never a checklist task (the agent's task_checklist_item_id hint is ignored); a source = action task is created.
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

// displayName returns given, or the display name of the user id (empty when unknown).
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

// linkTask links action id to a plan task of userID and returns the task id (nil when the user has no plan).
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
			// A done task stays done and keeps its link: a later request for another resource of the same item
			// must not reopen it.
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

// ByProposal returns the action of userID saved for proposalID, or ErrNotFound.
func (s *Service) ByProposal(ctx context.Context, q DBTX, userID, proposalID string) (Action, error) {
	return scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a
		WHERE a.idempotency_key = $2::uuid AND a.user_id = $1`, userID, proposalID))
}

// listLimit bounds GET /api/actions.
const listLimit = 100

// List returns userID's actions, newest first.
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

// Scope restricts ApplyDecision to the action of one requester whose request names one approver (R71). Both come
// from the approvals service's decide result and the deciding lead's verified claims, never from the agent.
type Scope struct {
	RequesterID string // owner of the action (approvals requester_id)
	ApproverID  string // approver_id in the saved MCP result of the request (the deciding lead)
	// Stage is the approval stage of an absence request: "" or "lead", then "hr_admin". At the HR admin stage the
	// deciding person is not the request's approver: the approvals service already checked the item is theirs.
	Stage string
}

// ApplyDecision applies the approver's decision ("approved" or "rejected", as returned by approvals.decide) to the
// action of kind with externalID (ABS-…/ACC-…) owned by scope.RequesterID whose request names scope.ApproverID as
// approver: action status approved/granted/rejected, the linked task done or not_started (never a human_only task),
// and a status_change notification for the owner, all through q. It reports whether anything changed (a repeated
// decision is a no-op) and returns ErrNotFound when core has no such action; both scope fields are required.
func (s *Service) ApplyDecision(ctx context.Context, q DBTX, kind, externalID, decision string, scope Scope) (Action, bool, error) {
	status, err := decisionStatus(kind, decision)
	if err != nil {
		return Action{}, false, err
	}
	hrStage := kind == KindAbsence && scope.Stage == "hr_admin"
	if kind == KindAbsence && !hrStage && status == StatusApproved {
		status = StatusPendingHR // the lead's approval passes the request to the HR admin
	}
	if scope.RequesterID == "" || scope.ApproverID == "" {
		return Action{}, false, ErrNotFound
	}
	approver := scope.ApproverID
	if hrStage {
		approver = "" // matched below by requester and request only
	}
	a, err := scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a
		WHERE a.kind = $1 AND a.external_id = $2 AND a.user_id = $3 AND ($4 = '' OR a.payload->'result'->>'approver_id' = $4)
		ORDER BY a.created_at LIMIT 1 FOR UPDATE`, kind, externalID, scope.RequesterID, approver))
	if err != nil {
		return Action{}, false, err
	}
	if a.Status == status || (status == StatusPendingHR && a.Status == StatusApproved) {
		return a, false, nil // a replayed lead decision never undoes the HR admin's approval
	}
	if _, err := q.Exec(ctx, `UPDATE core.actions SET status = $2, updated_at = now() WHERE id = $1`, a.ID, status); err != nil {
		return Action{}, false, fmt.Errorf("update action status: %w", err)
	}
	if a.TaskID != nil {
		if _, err := q.Exec(ctx, `UPDATE core.plan_tasks SET status = $2, updated_at = now()
			WHERE id = $1 AND linked_action_id = $3 AND NOT human_only`,
			*a.TaskID, taskStatus(status), a.ID); err != nil {
			return Action{}, false, fmt.Errorf("update linked task: %w", err)
		}
	}
	if s.notifier != nil {
		if err := s.notifier.Create(ctx, q, notify.Notification{UserID: a.UserID, Kind: notify.KindStatusChange,
			Title: decisionTitle(kind, status, a.Summary), Body: a.Summary, DedupeKey: "action:" + a.ID + ":" + status}); err != nil {
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

// notifyHRBP tells the HR BPs that an approved day off was registered by the HR admin (organizers' Q&A 10.11: a day off
// involves the lead, the HR admin and the HR BP). The HR BP gets the summary only: no comment, no reason.
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

// labelType returns the absence type whose label starts summary (day_off when none does).
func labelType(summary string) string {
	first := strings.SplitN(summary, " ", 2)[0]
	for k, v := range absenceLabels {
		if v == first {
			return k
		}
	}
	return "day_off"
}

// reschedule applies a moved absence request (absence.reschedule_request) to the existing action of the caller: new
// dates in the summary and the linked task, back to pending_approval, the new MCP result in the payload. A repeated
// confirmation applies the same update again and returns the same action.
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

// withoutApprover drops the «, согласующий — …» tail of a summary.
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

// absenceLabels name the absence types (checkpoint 11.11: vacation, day off, absence, parental leave).
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

// taskStatus maps an action status onto the status of its plan task.
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

// ruDate formats YYYY-MM-DD as DD.MM.YYYY (input returned as is when it is not a date).
func ruDate(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format("02.01.2006")
}

// RuDate is ruDate for other packages (chat summaries).
func RuDate(d string) string { return ruDate(d) }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ByID returns the action with id, or ErrNotFound.
func (s *Service) ByID(ctx context.Context, q DBTX, id string) (Action, error) {
	return scanAction(q.QueryRow(ctx, `SELECT `+actionCols+` FROM core.actions a WHERE a.id = $1::uuid`, id))
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
