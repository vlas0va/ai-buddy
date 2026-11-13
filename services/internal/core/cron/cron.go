// Пакет cron — AI-cron: периодическая задача, которая отмечает просроченные задачи плана и отправляет напоминания
// стажёрам и наставникам (R54). Тексты — фиксированные русские шаблоны, LLM не участвует.
//
// Для каждого плана задача (1) выставляет статус 'overdue' открытым задачам с прошедшим сроком, (2) создаёт одно
// уведомление на каждую просроченную задачу верхнего уровня (ключ дедупликации overdue:<task_id>) и (3) напоминает о
// каждой открытой задаче верхнего уровня текущего этапа стажёра (ключ дедупликации
// period:<checklist_item_id>:<period>). У опросов CL-15 и CL-16 свой текст. Повторные запуски никогда не дублируют
// уведомления. Пункты human_only только напоминаются, их не трогают.
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

// Result считает, что сделал один запуск.
type Result struct {
	Plans         int `json:"plans"`
	MarkedOverdue int `json:"marked_overdue"`
	Overdue       int `json:"overdue_notifications"`
	Reminders     int `json:"reminders"`
	Reports       int `json:"reports"`
	Questions     int `json:"question_digests"` // организаторы 13.11: одна сводка в день каждому адресату с открытыми вопросами
}

// Runner выполняет задачу.
type Runner struct {
	pool     *pgxpool.Pool
	notify   notify.Store
	emitter  *analytics.Emitter
	log      *slog.Logger
	interval time.Duration
	// Now — часы (для тестов); по умолчанию time.Now.
	Now func() time.Time
}

// NewRunner возвращает Runner, срабатывающий раз в interval (<= 0 — DefaultInterval). emitter и log могут быть nil.
func NewRunner(pool *pgxpool.Pool, emitter *analytics.Emitter, interval time.Duration, log *slog.Logger) *Runner {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{pool: pool, emitter: emitter, interval: interval, log: log, Now: time.Now}
}

// Run выполняет задачу один раз при старте, а затем раз в interval, пока ctx не отменён. Ошибки логируются.
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

// RunOnce обрабатывает каждый план в отдельной транзакции и возвращает итоги. Первая ошибка останавливает запуск (уже
// обработанные планы остаются зафиксированными; задача идемпотентна, поэтому следующий запуск продолжит).
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
	n, err := r.questionDigests(ctx)
	if err != nil {
		return res, err
	}
	res.Questions = n
	return res, nil
}

// questionDigests: вопросы стажёров ждут в списке адресата без уведомления на каждый (организаторы 13.11); раз в день
// адресат получает одну сводку «Вопросы стажёров ждут ответа: N».
func (r *Runner) questionDigests(ctx context.Context) (int, error) {
	rows, err := r.pool.Query(ctx, `SELECT to_user_id, count(*)::int FROM core.escalations WHERE status = 'open'
		GROUP BY to_user_id ORDER BY to_user_id`)
	if err != nil {
		return 0, fmt.Errorf("select open questions: %w", err)
	}
	type pending struct {
		user string
		n    int
	}
	var all []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.user, &p.n); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan open questions: %w", err)
		}
		all = append(all, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read open questions: %w", err)
	}
	day := r.Now().UTC().Format("2006-01-02")
	sent := 0
	for _, p := range all {
		n := notify.Notification{UserID: p.user, Kind: notify.KindReminder, DedupeKey: "questions:" + day,
			Title: fmt.Sprintf("Вопросы стажёров ждут ответа: %d", p.n),
			Body:  "Ответьте в разделе «Вопросы стажёров» — похожие вопросы собраны вместе, ответ можно добавить в базу знаний."}
		created, err := r.notify.CreateIfNew(ctx, r.pool, n)
		if err != nil {
			return sent, fmt.Errorf("question digest %s: %w", p.user, err)
		}
		if created {
			sent++
		}
	}
	return sent, nil
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
	if p.cohort != nil && p.role == "mentor" {
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

// ReportAfter: через три месяца buddy сообщает наставнику, что отчёт за стажировку готов (идея 12.11; отчёт только для
// наставника).
const ReportAfter = 90 * 24 * time.Hour

func reportText(p planRow) (title, body string) {
	return "Отчёт о стажёре " + p.intern + " за 3 месяца готов",
		"Buddy собрал факты за стажировку: задачи плана раньше срока, в срок и с опозданием, вопросы и доступы. " +
			"Открой «Мои стажёры» → «Отчёт за стажировку». Выводы — за тобой: это не оценка, а подготовка к разговору."
}

func (r *Runner) emit(ctx context.Context, tx analytics.DBTX, base analytics.Event, name string, props map[string]any) error {
	base.Name, base.Props = name, props
	return r.emitter.Emit(ctx, tx, base)
}

// topTasks возвращает задачи чек-листа верхнего уровня из плана, подходящие под cond ($1 = id плана, $2 = arg, если не
// пусто).
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

// ruDate превращает YYYY-MM-DD в DD.MM.YYYY.
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

// Handler обслуживает POST /api/cron/run.
type Handler struct {
	runner *Runner
	log    *slog.Logger
	// Demo включает эндпоинт; по умолчанию buildflags.Demo, это поле, чтобы тесты могли его переключать.
	Demo bool
	// Sync заставляет обработчик выполнить задачу до ответа 202 (для тестов). По умолчанию задача идёт в фоне.
	Sync bool
}

// New возвращает HTTP-обработчик cron.
func New(runner *Runner, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{runner: runner, log: log, Demo: buildflags.Demo}
}

// Register добавляет POST /api/cron/run (нужна авторизация; вне демо-сборок отвечает 404).
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
		// Запрос заканчивается сразу после 202, поэтому у задачи свой контекст.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			job(ctx)
		}()
	}
	w.WriteHeader(http.StatusAccepted)
}
