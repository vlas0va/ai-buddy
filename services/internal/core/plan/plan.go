// Package plan serves the onboarding plan built from the Avito checklist (S1, R18-R20): GET /api/plan,
// PATCH /api/plan/tasks/{task_id}, PUT /api/plan/goals and GET /api/team/interns.
//
// Rights (R71): an intern sees only the own plan; a mentor sees the own mentor plans and the intern plans of their
// team; a lead sees the intern and mentor plans of their team; everyone else gets 403. A foreign or unknown resource
// is always 403, never 404, so existence is not revealed.
//
// Status "overdue" is computed on read: a task whose due_at is before today and which is neither done nor
// waiting_approval is reported as overdue (AI-cron also stores it, see package cron).
package plan

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/period"
)

// Handler serves the plan endpoints.
type Handler struct {
	pool    *pgxpool.Pool
	emitter *analytics.Emitter
	log     *slog.Logger
	// Now is the clock (tests); defaults to time.Now.
	Now func() time.Time
}

// New returns the plan HTTP handler. emitter and log may be nil.
func New(pool *pgxpool.Pool, emitter *analytics.Emitter, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pool: pool, emitter: emitter, log: log, Now: time.Now}
}

// Register adds the plan routes (auth required).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/plan", h.getPlan)
	mux.HandleFunc("PATCH /api/plan/tasks/{task_id}", h.patchTask)
	mux.HandleFunc("PUT /api/plan/goals", h.putGoals)
	mux.HandleFunc("GET /api/team/interns", h.teamInterns)
	mux.HandleFunc("GET /api/plan/report", h.getReport)
}

func (h *Handler) today() string { return h.Now().UTC().Format("2006-01-02") }

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func claims(w http.ResponseWriter, r *http.Request) (authn.Claims, bool) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
	}
	return c, ok
}

// ---- JSON shapes (openapi Plan, Task, NextStep, Goal) ----

// Task is the openapi Task without nested subtasks.
type Task struct {
	ID              string  `json:"id"`
	Source          string  `json:"source"`
	ChecklistItemID *string `json:"checklist_item_id"`
	ParentTaskID    *string `json:"parent_task_id"`
	Period          string  `json:"period"`
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	Status          string  `json:"status"`
	DueAt           *string `json:"due_at"`
	HumanOnly       bool    `json:"human_only"`
	LinkedActionID  *string `json:"linked_action_id"`

	stored string // status as stored, before the overdue-on-read rule
}

// TopTask is a top-level task with its subtasks (always present, possibly empty).
type TopTask struct {
	Task
	Subtasks []Task `json:"subtasks"`
}

// PeriodTasks is one period of the plan.
type PeriodTasks struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Tasks []TopTask `json:"tasks"`
}

// NextStep is the openapi NextStep.
type NextStep struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Action string `json:"action"`
	Prompt string `json:"prompt,omitempty"`
}

// Goal is the openapi Goal.
type Goal struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Criteria  *string `json:"criteria,omitempty"`
	CreatedBy string  `json:"created_by"`
}

// Plan is the openapi Plan.
type Plan struct {
	UserID        string        `json:"user_id"`
	PlanRole      string        `json:"plan_role"`
	InternID      string        `json:"intern_id"`
	CurrentPeriod string        `json:"current_period"`
	Periods       []PeriodTasks `json:"periods"`
	Goals         []Goal        `json:"goals"`
	NextStep      *NextStep     `json:"next_step"`
}

// effectiveStatus applies the "overdue on read" rule. today and due are YYYY-MM-DD.
func effectiveStatus(status string, due *string, today string) string {
	if status == "done" || status == "waiting_approval" || status == "overdue" {
		return status
	}
	if due != nil && *due < today {
		return "overdue"
	}
	return status
}

// ---- plan resolution (R71) ----

type planRef struct {
	id       string
	userID   string
	role     string
	internID string
	cohort   *string
}

const planSelect = `SELECT p.id::text, p.user_id, p.plan_role, p.intern_id, u.cohort_start::text
	FROM core.plans p JOIN core.users u ON u.id = p.intern_id`

func scanPlan(row pgx.Row) (planRef, error) {
	var p planRef
	err := row.Scan(&p.id, &p.userID, &p.role, &p.internID, &p.cohort)
	return p, err
}

func (h *Handler) internInTeam(ctx context.Context, id, team string) (bool, error) {
	var ok bool
	err := h.pool.QueryRow(ctx, `SELECT true FROM core.users WHERE id = $1 AND role = 'intern' AND team_id = $2`, id, team).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return ok, err
}

// resolve picks the plan the caller asked for or returns a 400/403/404 apierr.
func (h *Handler) resolve(ctx context.Context, c authn.Claims, userID, internID string) (planRef, error) {
	one := func(where string, args ...any) (planRef, error) {
		p, err := scanPlan(h.pool.QueryRow(ctx, planSelect+" WHERE "+where+" ORDER BY p.created_at, p.intern_id LIMIT 1", args...))
		if errors.Is(err, pgx.ErrNoRows) {
			return p, apierr.NotFound().WithMessage("План не найден")
		}
		return p, err
	}
	switch c.Role {
	case "intern":
		if (userID != "" && userID != c.Sub) || (internID != "" && internID != c.Sub) {
			return planRef{}, apierr.Forbidden()
		}
		return one("p.user_id = $1 AND p.plan_role = 'intern'", c.Sub)
	case "mentor":
		if userID != "" {
			ok, err := h.internInTeam(ctx, userID, c.TeamID)
			if err != nil {
				return planRef{}, err
			}
			if !ok {
				return planRef{}, apierr.Forbidden()
			}
			return one("p.user_id = $1 AND p.plan_role = 'intern'", userID)
		}
		if internID != "" {
			p, err := one("p.user_id = $1 AND p.plan_role = 'mentor' AND p.intern_id = $2", c.Sub, internID)
			if apierr.HasCode(err, apierr.CodeNotFound) {
				return planRef{}, apierr.Forbidden()
			}
			return p, err
		}
		return one("p.user_id = $1 AND p.plan_role = 'mentor'", c.Sub)
	case "lead":
		target := userID
		if target == "" {
			target = internID
		}
		if target == "" {
			return planRef{}, apierr.Validation().WithMessage("Укажите user_id стажёра")
		}
		ok, err := h.internInTeam(ctx, target, c.TeamID)
		if err != nil {
			return planRef{}, err
		}
		if !ok {
			return planRef{}, apierr.Forbidden()
		}
		if userID != "" {
			return one("p.user_id = $1 AND p.plan_role = 'intern'", userID)
		}
		return one("p.plan_role = 'mentor' AND p.intern_id = $1", internID)
	default:
		return planRef{}, apierr.Forbidden()
	}
}

// ---- loading ----

const taskCols = `t.id::text, t.parent_task_id::text, t.source, t.checklist_item_id, t.period, t.title, t.description,
	t.status, t.due_at::text, t.human_only, t.linked_action_id::text`

func scanTask(row pgx.Row, today string) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ParentTaskID, &t.Source, &t.ChecklistItemID, &t.Period, &t.Title, &t.Description,
		&t.Status, &t.DueAt, &t.HumanOnly, &t.LinkedActionID)
	t.stored = t.Status
	t.Status = effectiveStatus(t.Status, t.DueAt, today)
	return t, err
}

// loadTasks returns the top-level tasks of a plan grouped by period, in checklist order, with subtasks nested.
func (h *Handler) loadTasks(ctx context.Context, planID, today string) ([]PeriodTasks, []TopTask, error) {
	rows, err := h.pool.Query(ctx, `SELECT `+taskCols+` FROM core.plan_tasks t WHERE t.plan_id = $1
		ORDER BY (t.source <> 'checklist'), t.position, t.title, t.id`, planID)
	if err != nil {
		return nil, nil, fmt.Errorf("select tasks: %w", err)
	}
	defer rows.Close()
	var tops []TopTask
	subs := map[string][]Task{}
	for rows.Next() {
		t, err := scanTask(rows, today)
		if err != nil {
			return nil, nil, fmt.Errorf("scan task: %w", err)
		}
		if t.ParentTaskID != nil {
			subs[*t.ParentTaskID] = append(subs[*t.ParentTaskID], t)
		} else {
			tops = append(tops, TopTask{Task: t})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read tasks: %w", err)
	}
	periods := make([]PeriodTasks, 0, len(period.All()))
	idx := map[string]int{}
	for _, p := range period.All() {
		idx[string(p)] = len(periods)
		periods = append(periods, PeriodTasks{ID: string(p), Label: period.Label(p), Tasks: []TopTask{}})
	}
	for i := range tops {
		tops[i].Subtasks = subs[tops[i].ID]
		if tops[i].Subtasks == nil {
			tops[i].Subtasks = []Task{}
		}
		if j, ok := idx[tops[i].Period]; ok {
			periods[j].Tasks = append(periods[j].Tasks, tops[i])
		}
	}
	return periods, tops, nil
}

func (h *Handler) loadGoals(ctx context.Context, internID string) ([]Goal, error) {
	rows, err := h.pool.Query(ctx, `SELECT g.id::text, g.title, g.criteria, g.created_by FROM core.goals g
		JOIN core.plans p ON p.id = g.plan_id WHERE p.user_id = $1 AND p.plan_role = 'intern' ORDER BY g.created_at, g.id`, internID)
	if err != nil {
		return nil, fmt.Errorf("select goals: %w", err)
	}
	defer rows.Close()
	out := []Goal{}
	for rows.Next() {
		var g Goal
		if err := rows.Scan(&g.ID, &g.Title, &g.Criteria, &g.CreatedBy); err != nil {
			return nil, fmt.Errorf("scan goal: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (h *Handler) getPlan(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ref, err := h.resolve(r.Context(), c, q.Get("user_id"), q.Get("intern_id"))
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	now := h.Now().UTC()
	today := h.today()
	cur := period.Week1
	if ref.cohort != nil {
		if start, err := period.ParseDate(*ref.cohort); err == nil {
			cur = period.Current(start, now)
		}
	}
	periods, tops, err := h.loadTasks(r.Context(), ref.id, today)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	goals, err := h.loadGoals(r.Context(), ref.internID)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, Plan{
		UserID: ref.userID, PlanRole: ref.role, InternID: ref.internID, CurrentPeriod: string(cur),
		Periods: periods, Goals: goals, NextStep: nextStep(tops, cur),
	})
}

// ---- next step (R20) ----

// statusRank: overdue first, then in progress, then not started. waiting_approval and done are never next.
var statusRank = map[string]int{"overdue": 0, "in_progress": 1, "not_started": 2}

var askPrompts = map[string]string{
	"CL-07": "Расскажи, какие документы и мануалы по работе в моей команде мне нужно изучить",
	"CL-08": "Расскажи, что нужно изучить по защите персональных данных и другим инструкциям",
	"CL-09": "Расскажи, как закрывать практику и писать учебные работы по стажировке",
	"CL-10": "Помоги подготовиться к экваторной встрече: что нужно сделать и где взять материалы",
}

// nextStep picks the first not-done checklist task of the current or an earlier period: tasks that people do
// (human_only) come last; within a group overdue > in progress > not started. Among equals a task that nobody has
// started yet goes before one stored as in_progress (work on it is already under way), then period and position.
// Tasks created by actions (source = action) are skipped.
func nextStep(tops []TopTask, cur period.Period) *NextStep {
	var best *TopTask
	bestKey := [5]int{}
	curOrder := period.Order(cur)
	for i := range tops {
		t := &tops[i]
		rank, ok := statusRank[t.Status]
		if !ok || t.Source != "checklist" || period.Order(period.Period(t.Period)) > curOrder {
			continue
		}
		human := 0
		if t.HumanOnly {
			human = 1
		}
		started := 0
		if t.stored == "in_progress" {
			started = 1
		}
		key := [5]int{human, rank, started, period.Order(period.Period(t.Period)), i}
		if best == nil || less(key, bestKey) {
			best, bestKey = t, key
		}
	}
	if best == nil {
		return nil
	}
	ns := &NextStep{TaskID: best.ID, Title: best.Title}
	item := ""
	if best.ChecklistItemID != nil {
		item = *best.ChecklistItemID
	}
	switch {
	case item == "CL-05" || item == "CL-01":
		ns.Action = "request_access"
	case item == "CL-04":
		ns.Action = "draft_message"
	case askPrompts[item] != "":
		ns.Action, ns.Prompt = "ask_buddy", askPrompts[item]
	case best.HumanOnly:
		ns.Action = "mark_done"
	default:
		ns.Action, ns.Prompt = "ask_buddy", "Помоги выполнить пункт плана: «"+best.Title+"»"
	}
	return ns
}

func less(a, b [5]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// ---- PATCH /api/plan/tasks/{task_id} ----

type taskRow struct {
	planID, source, period, status string
	parent, item, due              *string
	owner                          string
}

func (h *Handler) emitDone(ctx context.Context, tx pgx.Tx, c authn.Claims, t taskRow) error {
	onTime := t.due == nil || *t.due >= h.today()
	props := map[string]any{"period": t.period, "on_time": onTime}
	if t.item != nil {
		props["checklist_item_id"] = *t.item
	}
	return h.emitter.Emit(ctx, tx, analytics.Event{Name: "plan.task_done", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID, Props: props})
}

func (h *Handler) patchTask(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	id := r.PathValue("task_id")
	if !uuidRE.MatchString(id) {
		apierr.WriteJSON(w, apierr.NotFound())
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	switch body.Status {
	case "not_started", "in_progress", "done":
	default:
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Допустимые статусы: not_started, in_progress, done"))
		return
	}
	out, err := h.setStatus(r.Context(), c, id, body.Status)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) setStatus(ctx context.Context, c authn.Claims, id, status string) (any, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var t taskRow
	err = tx.QueryRow(ctx, `SELECT t.plan_id::text, t.source, t.period, t.status, t.parent_task_id::text, t.checklist_item_id,
			t.due_at::text, p.user_id
		FROM core.plan_tasks t JOIN core.plans p ON p.id = t.plan_id WHERE t.id = $1 FOR UPDATE OF t`, id).
		Scan(&t.planID, &t.source, &t.period, &t.status, &t.parent, &t.item, &t.due, &t.owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.owner != c.Sub) {
		return nil, apierr.Forbidden() // unknown and foreign tasks look the same (R71)
	}
	if err != nil {
		return nil, fmt.Errorf("select task: %w", err)
	}
	if t.source != "checklist" || t.status == "waiting_approval" {
		return nil, apierr.Conflict().WithMessage("Статус этой задачи меняется автоматически после решения по заявке")
	}
	if _, err := tx.Exec(ctx, `UPDATE core.plan_tasks SET status = $2, updated_at = now() WHERE id = $1`, id, status); err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	becameDone := status == "done" && t.status != "done"
	if t.parent == nil {
		if becameDone {
			if _, err := tx.Exec(ctx, `UPDATE core.plan_tasks SET status = 'done', updated_at = now()
				WHERE parent_task_id = $1 AND status <> 'done'`, id); err != nil {
				return nil, fmt.Errorf("complete subtasks: %w", err)
			}
			if err := h.emitDone(ctx, tx, c, t); err != nil {
				return nil, err
			}
		}
	} else if err := h.syncParent(ctx, tx, c, *t.parent, status, t.status); err != nil {
		return nil, err
	}
	var out any
	if t.parent == nil {
		out, err = h.loadTop(ctx, tx, id)
	} else {
		out, err = scanTask(tx.QueryRow(ctx, `SELECT `+taskCols+` FROM core.plan_tasks t WHERE t.id = $1`, id), h.today())
	}
	if err != nil {
		return nil, fmt.Errorf("reload task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// syncParent completes the parent when all its subtasks are done and reopens a done parent when a subtask is reopened.
func (h *Handler) syncParent(ctx context.Context, tx pgx.Tx, c authn.Claims, parentID, newStatus, oldStatus string) error {
	if newStatus == "done" {
		var all bool
		if err := tx.QueryRow(ctx, `SELECT bool_and(status = 'done') FROM core.plan_tasks WHERE parent_task_id = $1`, parentID).Scan(&all); err != nil {
			return fmt.Errorf("check subtasks: %w", err)
		}
		if !all {
			return nil
		}
		var p taskRow
		err := tx.QueryRow(ctx, `SELECT status, period, checklist_item_id, due_at::text FROM core.plan_tasks WHERE id = $1 FOR UPDATE`, parentID).
			Scan(&p.status, &p.period, &p.item, &p.due)
		if err != nil {
			return fmt.Errorf("select parent: %w", err)
		}
		if p.status == "done" {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE core.plan_tasks SET status = 'done', updated_at = now() WHERE id = $1`, parentID); err != nil {
			return fmt.Errorf("complete parent: %w", err)
		}
		return h.emitDone(ctx, tx, c, p)
	}
	if oldStatus == "done" {
		if _, err := tx.Exec(ctx, `UPDATE core.plan_tasks SET status = 'in_progress', updated_at = now()
			WHERE id = $1 AND status = 'done'`, parentID); err != nil {
			return fmt.Errorf("reopen parent: %w", err)
		}
	}
	return nil
}

func (h *Handler) loadTop(ctx context.Context, q pgx.Tx, id string) (TopTask, error) {
	today := h.today()
	top, err := scanTask(q.QueryRow(ctx, `SELECT `+taskCols+` FROM core.plan_tasks t WHERE t.id = $1`, id), today)
	if err != nil {
		return TopTask{}, err
	}
	rows, err := q.Query(ctx, `SELECT `+taskCols+` FROM core.plan_tasks t WHERE t.parent_task_id = $1 ORDER BY t.position, t.id`, id)
	if err != nil {
		return TopTask{}, err
	}
	defer rows.Close()
	out := TopTask{Task: top, Subtasks: []Task{}}
	for rows.Next() {
		s, err := scanTask(rows, today)
		if err != nil {
			return TopTask{}, err
		}
		out.Subtasks = append(out.Subtasks, s)
	}
	return out, rows.Err()
}

// ---- PUT /api/plan/goals ----

type goalInput struct {
	Title    string `json:"title"`
	Criteria string `json:"criteria"`
}

func (h *Handler) putGoals(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	if c.Role != "mentor" && c.Role != "lead" {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	var body struct {
		UserID string      `json:"user_id"`
		Goals  []goalInput `json:"goals"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	ctx := r.Context()
	inTeam, err := h.internInTeam(ctx, body.UserID, c.TeamID)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("check intern: %w", err))
		return
	}
	if !inTeam {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	if len(body.Goals) > 10 {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Не более 10 целей"))
		return
	}
	for i := range body.Goals {
		g := &body.Goals[i]
		g.Title, g.Criteria = strings.TrimSpace(g.Title), strings.TrimSpace(g.Criteria)
		if g.Title == "" || utf8.RuneCountInString(g.Title) > 300 || utf8.RuneCountInString(g.Criteria) > 1000 {
			apierr.WriteJSON(w, apierr.Validation().WithMessage("У цели нужен заголовок до 300 символов, критерии — до 1000"))
			return
		}
	}
	goals, err := h.replaceGoals(ctx, c, body.UserID, body.Goals)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, goals)
}

func (h *Handler) replaceGoals(ctx context.Context, c authn.Claims, internID string, in []goalInput) ([]Goal, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var planID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM core.plans WHERE user_id = $1 AND plan_role = 'intern'`, internID).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.NotFound().WithMessage("План не найден")
	}
	if err != nil {
		return nil, fmt.Errorf("select plan: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.goals WHERE plan_id = $1`, planID); err != nil {
		return nil, fmt.Errorf("delete goals: %w", err)
	}
	out := make([]Goal, 0, len(in))
	for _, g := range in {
		var saved Goal
		// clock_timestamp() keeps the input order: created_at differs per row inside one transaction.
		err := tx.QueryRow(ctx, `INSERT INTO core.goals (plan_id, title, criteria, created_by, created_at)
			VALUES ($1, $2, NULLIF($3, ''), $4, clock_timestamp()) RETURNING id::text, title, criteria, created_by`,
			planID, g.Title, g.Criteria, c.Sub).Scan(&saved.ID, &saved.Title, &saved.Criteria, &saved.CreatedBy)
		if err != nil {
			return nil, fmt.Errorf("insert goal: %w", err)
		}
		out = append(out, saved)
	}
	if err := h.emitter.Emit(ctx, tx, analytics.Event{Name: "plan.goals_set", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
		Props: map[string]any{"count": len(out), "by_role": c.Role}}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// ---- GET /api/team/interns ----

// InternProgress is the openapi InternProgress.
type InternProgress struct {
	UserID                string `json:"user_id"`
	DisplayName           string `json:"display_name"`
	CurrentPeriod         string `json:"current_period"`
	Done                  int    `json:"done"`
	Total                 int    `json:"total"`
	Overdue               int    `json:"overdue"`
	PendingAccessRequests int    `json:"pending_access_requests"`
}

const teamInternsSQL = `SELECT u.id, u.display_name, u.cohort_start::text,
		COALESCE(SUM((t.status = 'done')::int), 0)::int,
		COUNT(t.id)::int,
		COALESCE(SUM((t.status NOT IN ('done', 'waiting_approval') AND (t.status = 'overdue' OR t.due_at < $2::date))::int), 0)::int,
		(SELECT count(*) FROM core.actions a WHERE a.user_id = u.id AND a.kind = 'access_request' AND a.status = 'pending_approval')::int
	FROM core.users u
	LEFT JOIN core.plans p ON p.user_id = u.id AND p.plan_role = 'intern'
	LEFT JOIN core.plan_tasks t ON t.plan_id = p.id AND t.parent_task_id IS NULL AND t.source = 'checklist'
	WHERE u.role = 'intern' AND %s
	GROUP BY u.id ORDER BY u.display_name, u.id`

func (h *Handler) teamInterns(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	out, err := TeamInterns(r.Context(), h.pool, c, h.Now())
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// Querier is the subset of *pgxpool.Pool and pgx.Tx used by TeamInterns.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// TeamInterns returns the progress of the interns the caller looks after (GET /api/team/interns; chat user_context
// mentees): a mentor gets the interns of their own mentor plans, a lead the interns of their team, any other role
// apierr.Forbidden. now is the clock for overdue tasks and the current period.
func TeamInterns(ctx context.Context, q Querier, c authn.Claims, now time.Time) ([]InternProgress, error) {
	var scope string
	switch c.Role {
	case "mentor":
		scope = `u.id IN (SELECT intern_id FROM core.plans WHERE user_id = $1 AND plan_role = 'mentor')`
	case "lead":
		scope = `u.team_id = $1`
	default:
		return nil, apierr.Forbidden()
	}
	arg := c.Sub
	if c.Role == "lead" {
		arg = c.TeamID
	}
	now = now.UTC()
	rows, err := q.Query(ctx, fmt.Sprintf(teamInternsSQL, scope), arg, now.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("select team interns: %w", err)
	}
	defer rows.Close()
	out := []InternProgress{}
	for rows.Next() {
		var p InternProgress
		var cohort *string
		if err := rows.Scan(&p.UserID, &p.DisplayName, &cohort, &p.Done, &p.Total, &p.Overdue, &p.PendingAccessRequests); err != nil {
			return nil, fmt.Errorf("scan intern: %w", err)
		}
		p.CurrentPeriod = string(period.Week1)
		if cohort != nil {
			if start, err := period.ParseDate(*cohort); err == nil {
				p.CurrentPeriod = string(period.Current(start, now))
			}
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read interns: %w", err)
	}
	return out, nil
}
