// Package cron is the AI-cron: a periodic job that marks overdue plan tasks and sends reminders to interns and
// mentors (R54). Texts are fixed Russian templates, no LLM is involved.
//
// Per plan the job (1) stores status 'overdue' on open tasks whose due date has passed, (2) creates one notification
// per overdue top-level task (dedupe key overdue:<task_id>) and (3) reminds about every open top-level task of the
// intern's current period (dedupe key period:<checklist_item_id>:<period>). Survey items CL-15 and CL-16 get their own
// text. Repeated runs never duplicate notifications. human_only items are only reminded, never touched.
package cron

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/buildflags"
	"aibuddy/services/internal/platform/period"
)

// DefaultInterval is CRON_INTERVAL's default.
const DefaultInterval = 24 * time.Hour

// Result counts what one run did.
type Result struct {
	Plans         int `json:"plans"`
	MarkedOverdue int `json:"marked_overdue"`
	Overdue       int `json:"overdue_notifications"`
	Reminders     int `json:"reminders"`
	Reports       int `json:"reports"`
}

// Runner executes the job.
type Runner struct {
	pool     *pgxpool.Pool
	notify   notify.Store
	emitter  *analytics.Emitter
	log      *slog.Logger
	interval time.Duration
	// Now is the clock (tests); defaults to time.Now.
	Now func() time.Time
}

// NewRunner returns a Runner that fires every interval (<= 0 means DefaultInterval). emitter and log may be nil.
func NewRunner(pool *pgxpool.Pool, emitter *analytics.Emitter, interval time.Duration, log *slog.Logger) *Runner {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{pool: pool, emitter: emitter, interval: interval, log: log, Now: time.Now}
}

// Run executes the job once at start and then every interval until ctx is cancelled. Errors are logged.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		if res, err := r.RunOnce(ctx); err != nil {
			r.log.Error("ai-cron run failed", "err", err)
		} else {
			r.log.Info("ai-cron run finished", "plans", res.Plans, "overdue", res.Overdue, "reminders", res.Reminders)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type planRow struct {
	id, userID, role string
	internID, intern string
	cohort           *string
}

type taskRow struct {
	id, title, period string
	item              string
	due               *string
	humanOnly         bool
}

// RunOnce processes every plan, each in its own transaction, and returns the totals. The first error stops the run
// (already processed plans stay committed; the job is idempotent, so the next run continues).
func (r *Runner) RunOnce(ctx context.Context) (Result, error) {
	var res Result
	rows, err := r.pool.Query(ctx, `SELECT p.id::text, p.user_id, p.plan_role, u.id, u.display_name, u.cohort_start::text
		FROM core.plans p JOIN core.users u ON u.id = p.intern_id ORDER BY p.created_at, p.id`)
	if err != nil {
		return res, fmt.Errorf("select plans: %w", err)
	}
	var plans []planRow
	for rows.Next() {
		var p planRow
		if err := rows.Scan(&p.id, &p.userID, &p.role, &p.internID, &p.intern, &p.cohort); err != nil {
			rows.Close()
			return res, fmt.Errorf("scan plan: %w", err)
		}
		plans = append(plans, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("read plans: %w", err)
	}
	for _, p := range plans {
		if err := r.runPlan(ctx, p, &res); err != nil {
			return res, fmt.Errorf("plan %s: %w", p.id, err)
		}
		res.Plans++
	}
	return res, nil
}

func (r *Runner) runPlan(ctx context.Context, p planRow, res *Result) error {
	now := r.Now().UTC()
	today := now.Format("2006-01-02")
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `UPDATE core.plan_tasks SET status = 'overdue', updated_at = now()
		WHERE plan_id = $1 AND due_at < $2::date AND status IN ('not_started', 'in_progress')`, p.id, today)
	if err != nil {
		return fmt.Errorf("mark overdue: %w", err)
	}
	res.MarkedOverdue += int(tag.RowsAffected())

	overdue, err := r.topTasks(ctx, tx, p.id, `status = 'overdue'`, "")
	if err != nil {
		return err
	}
	for _, t := range overdue {
		n := notify.Notification{UserID: p.userID, Kind: notify.KindOverdue, ChecklistItemID: t.item, DedupeKey: "overdue:" + t.id,
			Title: "Просрочено: «" + t.title + "»", Body: overdueBody(t)}
		created, err := r.notify.CreateIfNew(ctx, tx, n)
		if err != nil {
			return err
		}
		if !created {
			continue
		}
		res.Overdue++
		user := analytics.Event{UserID: p.userID, Role: p.role}
		if err := r.emit(ctx, tx, user, "plan.task_overdue", map[string]any{"checklist_item_id": t.item, "period": t.period}); err != nil {
			return err
		}
		if err := r.emit(ctx, tx, user, "notification.created", map[string]any{"kind": notify.KindOverdue, "checklist_item_id": t.item}); err != nil {
			return err
		}
	}

	cur := period.Week1
	if p.cohort != nil {
		if start, err := period.ParseDate(*p.cohort); err == nil {
			cur = period.Current(start, now)
		}
	}
	open, err := r.topTasks(ctx, tx, p.id, `status IN ('not_started', 'in_progress', 'overdue') AND period = $2`, string(cur))
	if err != nil {
		return err
	}
	for _, t := range open {
		title, body := reminderText(t, p.role)
		n := notify.Notification{UserID: p.userID, Kind: notify.KindReminder, ChecklistItemID: t.item,
			DedupeKey: "period:" + t.item + ":" + t.period, Title: title, Body: body}
		created, err := r.notify.CreateIfNew(ctx, tx, n)
		if err != nil {
			return err
		}
		if !created {
			continue
		}
		res.Reminders++
		if err := r.emit(ctx, tx, analytics.Event{UserID: p.userID, Role: p.role}, "notification.created",
			map[string]any{"kind": notify.KindReminder, "checklist_item_id": t.item}); err != nil {
			return err
		}
	}
	if p.cohort != nil {
		if start, err := period.ParseDate(*p.cohort); err == nil && now.Sub(start) >= ReportAfter {
			title, body := reportText(p)
			n := notify.Notification{UserID: p.userID, Kind: notify.KindReminder, DedupeKey: "report:3m:" + p.internID,
				Title: title, Body: body}
			created, err := r.notify.CreateIfNew(ctx, tx, n)
			if err != nil {
				return err
			}
			if created {
				res.Reports++
				if err := r.emit(ctx, tx, analytics.Event{UserID: p.userID, Role: p.role}, "notification.created",
					map[string]any{"kind": "report"}); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ReportAfter: after three months buddy tells the intern and the mentor that the internship report is ready (idea 12.11).
const ReportAfter = 90 * 24 * time.Hour

func reportText(p planRow) (title, body string) {
	if p.role == "mentor" {
		return "Отчёт о стажёре " + p.intern + " за 3 месяца готов",
			"Buddy собрал факты за стажировку: задачи плана раньше срока, в срок и с опозданием, вопросы и доступы. " +
				"Открой «Мои стажёры» → «Отчёт за стажировку». Выводы — за тобой: это не оценка, а подготовка к разговору."
	}
	return "Твой отчёт за 3 месяца стажировки готов",
		"Его видит и наставник. Открой «Мой план» → «Отчёт за стажировку» — там задачи, вопросы и что обсудить на 1:1."
}

func (r *Runner) emit(ctx context.Context, tx analytics.DBTX, base analytics.Event, name string, props map[string]any) error {
	base.Name, base.Props = name, props
	return r.emitter.Emit(ctx, tx, base)
}

// topTasks returns the top-level checklist tasks of a plan matching cond ($1 = plan id, $2 = arg when not empty).
func (r *Runner) topTasks(ctx context.Context, tx notify.DBTX, planID, cond, arg string) ([]taskRow, error) {
	args := []any{planID}
	if arg != "" {
		args = append(args, arg)
	}
	rows, err := tx.Query(ctx, `SELECT id::text, title, period, checklist_item_id, due_at::text, human_only FROM core.plan_tasks
		WHERE plan_id = $1 AND parent_task_id IS NULL AND source = 'checklist' AND `+cond+` ORDER BY position, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("select tasks: %w", err)
	}
	defer rows.Close()
	var out []taskRow
	for rows.Next() {
		var t taskRow
		if err := rows.Scan(&t.id, &t.title, &t.period, &t.item, &t.due, &t.humanOnly); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ruDate turns YYYY-MM-DD into DD.MM.YYYY.
func ruDate(iso *string) string {
	if iso == nil {
		return ""
	}
	t, err := time.Parse("2006-01-02", *iso)
	if err != nil {
		return *iso
	}
	return t.Format("02.01.2006")
}

func overdueBody(t taskRow) string {
	b := "Срок был " + ruDate(t.due) + ". Откройте план адаптации и отметьте прогресс или спросите ассистента, если нужна помощь."
	if t.humanOnly {
		b = "Срок был " + ruDate(t.due) + ". Этот пункт выполняют люди, ассистент только напоминает о нём."
	}
	return b
}

func reminderText(t taskRow, planRole string) (title, body string) {
	switch t.item {
	case "CL-15", "CL-16":
		return "Пройдите опрос о стажировке", "Ваши ответы помогут улучшить программу для следующих стажёров."
	}
	if planRole == "mentor" {
		title = "Напоминание наставнику: «" + t.title + "»"
	} else {
		title = "Пора заняться: «" + t.title + "»"
	}
	body = "Пункт относится к текущему периоду адаптации"
	if t.due != nil {
		body += ", срок — " + ruDate(t.due)
	}
	body += "."
	if t.humanOnly {
		body += " Этот пункт выполняют люди, ассистент только напоминает о нём."
	}
	return title, body
}

// Handler serves POST /api/cron/run.
type Handler struct {
	runner *Runner
	log    *slog.Logger
	// Demo enables the endpoint; it defaults to buildflags.Demo and is a field so tests can flip it.
	Demo bool
	// Sync makes the handler run the job before answering 202 (tests). By default it runs in the background.
	Sync bool
}

// New returns the cron HTTP handler.
func New(runner *Runner, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{runner: runner, log: log, Demo: buildflags.Demo}
}

// Register adds POST /api/cron/run (auth required; answers 404 outside demo builds).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/cron/run", h.run)
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request) {
	if !h.Demo {
		apierr.WriteJSON(w, apierr.NotFound())
		return
	}
	if _, ok := authn.FromContext(r.Context()); !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	job := func(ctx context.Context) {
		if res, err := h.runner.RunOnce(ctx); err != nil {
			h.log.Error("manual ai-cron run failed", "err", err)
		} else {
			h.log.Info("manual ai-cron run finished", "plans", res.Plans, "overdue", res.Overdue, "reminders", res.Reminders)
		}
	}
	if h.Sync {
		job(r.Context())
	} else {
		// The request ends right after 202, so the job gets its own context.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			job(ctx)
		}()
	}
	w.WriteHeader(http.StatusAccepted)
}
