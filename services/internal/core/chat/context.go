package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/plan"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/period"
)

// userContext builds user_context for the agent. Identity (user_id, role, team_id) comes only from the verified
// claims (R71); the rest from core's DB: display name, current period, open top-level tasks of the caller's own
// plans, mentees of a mentor or lead and the runtime flags of the team. Mentees and flags are best effort.
func (h *Handler) userContext(ctx context.Context, c authn.Claims) (agentclient.UserContext, map[string]bool, error) {
	now := h.Now().UTC()
	uc := agentclient.UserContext{UserID: c.Sub, Role: c.Role, TeamID: c.TeamID, CurrentPeriod: string(period.Week1),
		OpenTasks: []agentclient.OpenTask{}}
	var name, cohort, planCohort, location *string
	err := h.pool.QueryRow(ctx, `SELECT u.display_name, u.cohort_start::text, u.location,
		(SELECT i.cohort_start::text FROM core.plans p JOIN core.users i ON i.id = p.intern_id
		 WHERE p.user_id = u.id ORDER BY (p.plan_role = 'intern') DESC, p.created_at, p.id LIMIT 1)
		FROM core.users u WHERE u.id = $1`, c.Sub).Scan(&name, &cohort, &location, &planCohort)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uc, nil, fmt.Errorf("load user: %w", err)
	}
	if name != nil {
		uc.DisplayName = *name
	}
	if location != nil {
		uc.Location = *location
	}
	// A mentor follows the periods of the intern of their (first) mentor plan.
	for _, s := range []*string{&c.CohortStart, cohort, planCohort} {
		if s == nil || *s == "" {
			continue
		}
		if start, err := period.ParseDate(*s); err == nil {
			uc.CurrentPeriod = string(period.Current(start, now))
			break
		}
	}
	tasks, err := h.openTasks(ctx, c.Sub, now.Format("2006-01-02"))
	if err != nil {
		return uc, nil, err
	}
	uc.OpenTasks = tasks
	if c.Role == "mentor" || c.Role == "lead" {
		if mentees, err := h.mentees(ctx, c, now); err != nil {
			h.log.Warn("user_context mentees failed", "user", c.Sub, "err", err)
		} else {
			uc.Mentees = mentees
		}
	}
	var flags map[string]bool
	if h.flags != nil {
		if f, err := h.flags.ForTeam(ctx, c.TeamID); err != nil {
			h.log.Warn("user_context flags failed", "team", c.TeamID, "err", err)
		} else {
			flags = f
		}
	}
	return uc, flags, nil
}

// Mentee is an item of user_context.mentees: the GET /api/team/interns progress of an intern plus their open
// tasks and the accesses they requested through the chat (S4: the mentor sees what the intern requested).
type Mentee struct {
	plan.InternProgress
	OpenTasks []agentclient.OpenTask `json:"open_tasks"`
	Accesses  []MenteeAccess         `json:"accesses"`
}

// MenteeAccess is an access request of a mentee.
type MenteeAccess struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (h *Handler) mentees(ctx context.Context, c authn.Claims, now time.Time) ([]Mentee, error) {
	interns, err := plan.TeamInterns(ctx, h.pool, c, now)
	if err != nil {
		return nil, err
	}
	out := make([]Mentee, 0, len(interns))
	for _, in := range interns {
		m := Mentee{InternProgress: in, Accesses: []MenteeAccess{}}
		if m.OpenTasks, err = h.openTasks(ctx, in.UserID, now.Format("2006-01-02")); err != nil {
			return nil, err
		}
		rows, err := h.pool.Query(ctx, `SELECT COALESCE(payload->'result'->>'resource_name', payload->'args'->>'resource_id', summary), status
			FROM core.actions WHERE user_id = $1 AND kind = 'access_request' ORDER BY created_at, id`, in.UserID)
		if err != nil {
			return nil, fmt.Errorf("load mentee accesses: %w", err)
		}
		for rows.Next() {
			var a MenteeAccess
			if err := rows.Scan(&a.Name, &a.Status); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan mentee access: %w", err)
			}
			m.Accesses = append(m.Accesses, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read mentee accesses: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

// openTasks returns the open top-level tasks of the caller's own plans in plan order. Status is reported like
// GET /api/plan: overdue when due_at is before today and the task is neither done nor waiting_approval.
func (h *Handler) openTasks(ctx context.Context, userID, today string) ([]agentclient.OpenTask, error) {
	rows, err := h.pool.Query(ctx, `SELECT t.id::text, t.checklist_item_id, t.title,
		CASE WHEN t.status NOT IN ('done', 'waiting_approval') AND t.due_at < $2::date THEN 'overdue' ELSE t.status END,
		t.period, t.due_at::text, t.human_only
		FROM core.plan_tasks t JOIN core.plans p ON p.id = t.plan_id
		WHERE p.user_id = $1 AND t.parent_task_id IS NULL AND t.status <> 'done'
		ORDER BY array_position(ARRAY['week_1','week_2','month_1','month_2','after_3_months'], t.period), t.position, t.id
		LIMIT $3`, userID, today, openTasksLimit)
	if err != nil {
		return nil, fmt.Errorf("load open tasks: %w", err)
	}
	defer rows.Close()
	out := []agentclient.OpenTask{}
	for rows.Next() {
		var t agentclient.OpenTask
		if err := rows.Scan(&t.TaskID, &t.ChecklistItemID, &t.Title, &t.Status, &t.Period, &t.DueAt, &t.HumanOnly); err != nil {
			return nil, fmt.Errorf("scan open task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read open tasks: %w", err)
	}
	return out, nil
}

// history returns the last historyLimit non-empty messages of the session in chronological order, excluding
// exceptID (the message of the current turn).
func (h *Handler) history(ctx context.Context, sessionID, exceptID string) ([]agentclient.HistoryItem, error) {
	rows, err := h.pool.Query(ctx, `SELECT author, text FROM (
			SELECT author, text, created_at, id FROM core.chat_messages
			WHERE session_id = $1 AND id <> $2::uuid AND text <> ''
			ORDER BY created_at DESC, id DESC LIMIT $3) m
		ORDER BY created_at, id`, sessionID, exceptID, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	defer rows.Close()
	out := []agentclient.HistoryItem{}
	for rows.Next() {
		var it agentclient.HistoryItem
		if err := rows.Scan(&it.Author, &it.Text); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	return out, nil
}
