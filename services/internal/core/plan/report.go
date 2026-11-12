package plan

import (
	"context"
	"fmt"
	"net/http"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/period"
)

// InternshipDays is the length of the internship the report covers (the equator meeting is about three months in).
const InternshipDays = 90

// Report is the internship report (idea 12.11): facts from the plan, the questions and the actions of one intern. It is
// not an assessment: buddy counts, the mentor draws conclusions. It is for the mentor and the lead only (feedback 12.11), and
// absences are not part of it (R72).
type Report struct {
	UserID        string         `json:"user_id"`
	Name          string         `json:"name"`
	CohortStart   *string        `json:"cohort_start"`
	Days          int            `json:"days"`
	TotalDays     int            `json:"total_days"`
	Final         bool           `json:"final"`
	CurrentPeriod string         `json:"current_period"`
	Tasks         ReportTasks    `json:"tasks"`
	Periods       []ReportPeriod `json:"periods"`
	Late          []ReportTask   `json:"late"`
	Overdue       []ReportTask   `json:"overdue"`
	Questions     ReportQuestion `json:"questions"`
	Accesses      int            `json:"accesses_requested"`
	Goals         int            `json:"goals"`
	GoalsAchieved int            `json:"goals_achieved"`
	GoalsProposed int            `json:"goals_proposed"`
	Discuss       []string       `json:"discuss"`
}

// ReportTasks counts the top-level checklist tasks of the intern's plan.
type ReportTasks struct {
	Total      int `json:"total"`
	Done       int `json:"done"`
	Early      int `json:"early"`
	OnTime     int `json:"on_time"`
	Late       int `json:"late"`
	OverdueNow int `json:"overdue_now"`
	Upcoming   int `json:"upcoming"`
}

// ReportPeriod is one period of the plan.
type ReportPeriod struct {
	Period string `json:"period"`
	Total  int    `json:"total"`
	Done   int    `json:"done"`
	Late   int    `json:"late"`
}

// ReportTask is a task named in the report.
type ReportTask struct {
	Title       string  `json:"title"`
	DueAt       *string `json:"due_at"`
	CompletedAt *string `json:"completed_at"`
}

// MinutesPerQuestion is the estimate of a mentor's time per question that buddy closed on its own (an explanation in a
// chat with context switching). The organizers put a mentor's load at 40–48 hours per 6-month internship (Q&A 12.11).
const MinutesPerQuestion = 10

// ReportQuestion counts the intern's chat questions and the questions sent to people.
type ReportQuestion struct {
	Asked        int `json:"asked"`
	Escalated    int `json:"escalated"`
	Answered     int `json:"answered"`
	AddedToKB    int `json:"added_to_kb"`
	SelfServed   int `json:"self_served"`   // asked buddy and did not go to a person
	SavedMinutes int `json:"saved_minutes"` // SelfServed × MinutesPerQuestion: the mentor's time buddy saved (estimate)
}

// getReport: GET /api/plan/report?user_id= — the mentor and the lead of the team read the report of an intern of their
// team (same rules as the plan, R71). The report is not for the intern (feedback 12.11): 403.
func (h *Handler) getReport(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	userID := r.URL.Query().Get("user_id")
	if c.Role != "mentor" && c.Role != "lead" {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	if userID == "" {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Укажите user_id стажёра"))
		return
	}
	ref, err := h.resolve(r.Context(), c, userID, "")
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if ref.role != "intern" {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	rep, err := h.buildReport(r.Context(), ref)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) buildReport(ctx context.Context, ref planRef) (Report, error) {
	now := h.Now().UTC()
	today := now.Format("2006-01-02")
	rep := Report{UserID: ref.userID, CohortStart: ref.cohort, TotalDays: InternshipDays, CurrentPeriod: string(period.Week1),
		Periods: []ReportPeriod{}, Late: []ReportTask{}, Overdue: []ReportTask{}, Discuss: []string{}}
	if err := h.pool.QueryRow(ctx, `SELECT display_name FROM core.users WHERE id = $1`, ref.userID).Scan(&rep.Name); err != nil {
		return rep, fmt.Errorf("report user: %w", err)
	}
	if ref.cohort != nil {
		if start, err := period.ParseDate(*ref.cohort); err == nil {
			rep.CurrentPeriod = string(period.Current(start, now))
			rep.Days = int(now.Sub(start).Hours() / 24)
			if rep.Days < 0 {
				rep.Days = 0
			}
			rep.Final = rep.Days >= InternshipDays
		}
	}

	rows, err := h.pool.Query(ctx, `SELECT period, title, status, due_at::text, completed_at::date::text, was_overdue
		FROM core.plan_tasks WHERE plan_id = $1 AND parent_task_id IS NULL AND source = 'checklist'
		ORDER BY due_at NULLS LAST, position`, ref.id)
	if err != nil {
		return rep, fmt.Errorf("report tasks: %w", err)
	}
	byPeriod := map[string]*ReportPeriod{}
	var order []string
	for rows.Next() {
		var p, title, status string
		var due, done *string
		var wasOverdue bool
		if err := rows.Scan(&p, &title, &status, &due, &done, &wasOverdue); err != nil {
			rows.Close()
			return rep, fmt.Errorf("scan report task: %w", err)
		}
		bp, ok := byPeriod[p]
		if !ok {
			bp = &ReportPeriod{Period: p}
			byPeriod[p] = bp
			order = append(order, p)
		}
		bp.Total++
		rep.Tasks.Total++
		t := ReportTask{Title: title, DueAt: due, CompletedAt: done}
		switch {
		case status == "done":
			rep.Tasks.Done++
			bp.Done++
			switch {
			case wasOverdue || (due != nil && done != nil && *done > *due):
				rep.Tasks.Late++
				bp.Late++
				rep.Late = append(rep.Late, t)
			case due != nil && done != nil && *done == *due:
				rep.Tasks.OnTime++
			default:
				rep.Tasks.Early++
			}
		case status == "overdue" || (due != nil && *due < today):
			rep.Tasks.OverdueNow++
			rep.Overdue = append(rep.Overdue, t)
		default:
			rep.Tasks.Upcoming++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, fmt.Errorf("read report tasks: %w", err)
	}
	for _, p := range period.All() {
		if bp, ok := byPeriod[string(p)]; ok {
			rep.Periods = append(rep.Periods, *bp)
		}
	}
	if len(rep.Periods) != len(order) { // a period unknown to the package: keep it anyway
		rep.Periods = rep.Periods[:0]
		for _, p := range order {
			rep.Periods = append(rep.Periods, *byPeriod[p])
		}
	}

	err = h.pool.QueryRow(ctx, `SELECT
		  (SELECT count(*) FROM core.chat_messages m JOIN core.chat_sessions s ON s.id = m.session_id
		    WHERE s.user_id = $1 AND m.author = 'user'),
		  (SELECT count(*) FROM core.escalations WHERE from_user_id = $1),
		  (SELECT count(*) FROM core.escalations WHERE from_user_id = $1 AND status = 'answered'),
		  (SELECT count(*) FROM core.escalations WHERE from_user_id = $1 AND added_to_kb),
		  (SELECT count(*) FROM core.actions WHERE user_id = $1 AND kind = 'access_request' AND status <> 'failed'),
		  (SELECT count(*) FROM core.goals WHERE plan_id = $2::uuid),
		  (SELECT count(*) FROM core.goals WHERE plan_id = $2::uuid AND status = 'achieved'),
		  (SELECT count(*) FROM core.goals WHERE plan_id = $2::uuid AND status = 'proposed')`, ref.userID, ref.id).
		Scan(&rep.Questions.Asked, &rep.Questions.Escalated, &rep.Questions.Answered, &rep.Questions.AddedToKB,
			&rep.Accesses, &rep.Goals, &rep.GoalsAchieved, &rep.GoalsProposed)
	if err != nil {
		return rep, fmt.Errorf("report counters: %w", err)
	}
	rep.Questions.SelfServed = max(rep.Questions.Asked-rep.Questions.Escalated, 0)
	rep.Questions.SavedMinutes = rep.Questions.SelfServed * MinutesPerQuestion
	rep.Discuss = discuss(rep)
	return rep, nil
}

// discuss suggests topics for the 1:1: facts only, the wording never judges the intern.
func discuss(rep Report) []string {
	var out []string
	for i, t := range rep.Overdue {
		if i == 3 {
			out = append(out, fmt.Sprintf("Ещё просроченных задач: %d.", len(rep.Overdue)-3))
			break
		}
		out = append(out, "Просрочена задача «"+t.Title+"» — что мешает и чем помочь.")
	}
	if rep.GoalsProposed > 0 {
		out = append(out, fmt.Sprintf("Стажёр предложил целей: %d — согласуйте их в плане стажёра.", rep.GoalsProposed))
	}
	if rep.Goals == 0 {
		out = append(out, "Цели на стажировку ещё не внесены — их ставят наставник и руководитель (CL-06).")
	}
	if open := rep.Questions.Escalated - rep.Questions.Answered; open > 0 {
		out = append(out, fmt.Sprintf("Вопросов стажёра без ответа: %d — загляни в «Вопросы стажёров».", open))
	}
	if len(out) == 0 {
		out = append(out, "Просрочек нет — обсудите, что получилось и какие задачи взять дальше.")
	}
	return out
}
