package plan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// proposeGoals: POST /api/plan/goals/propose — после встречи с руководителем в первую неделю стажёр записывает
// согласованные цели; они ждут в статусе «proposed», пока наставник или руководитель их не утвердит (организаторы
// 12.11: руководитель согласует цели адаптации и следит за их выполнением; buddy помогает их записать и отслеживать).
func (h *Handler) proposeGoals(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	if c.Role != "intern" {
		apierr.WriteJSON(w, apierr.Forbidden().WithMessage("Цели предлагает стажёр; наставник и руководитель задают их в плане стажёра"))
		return
	}
	var body struct {
		Goals []goalInput `json:"goals"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	var in []goalInput
	for _, g := range body.Goals {
		g.Title, g.Criteria = strings.TrimSpace(g.Title), strings.TrimSpace(g.Criteria)
		if g.Title == "" {
			continue
		}
		if utf8.RuneCountInString(g.Title) > 300 || utf8.RuneCountInString(g.Criteria) > 1000 {
			apierr.WriteJSON(w, apierr.Validation().WithMessage("У цели нужен заголовок до 300 символов, критерии — до 1000"))
			return
		}
		in = append(in, g)
	}
	if len(in) == 0 || len(in) > 5 {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Предложи от 1 до 5 целей"))
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("begin: %w", err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var planID string
	var total int
	err = tx.QueryRow(ctx, `SELECT p.id::text, (SELECT count(*) FROM core.goals g WHERE g.plan_id = p.id)::int
		FROM core.plans p WHERE p.user_id = $1 AND p.plan_role = 'intern'`, c.Sub).Scan(&planID, &total)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.WriteJSON(w, apierr.NotFound().WithMessage("План не найден"))
		return
	}
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("select plan: %w", err))
		return
	}
	if total+len(in) > 10 {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Не более 10 целей"))
		return
	}
	out := make([]Goal, 0, len(in))
	for _, g := range in {
		var saved Goal
		err := tx.QueryRow(ctx, `INSERT INTO core.goals (plan_id, title, criteria, created_by, created_at, status)
			VALUES ($1, $2, NULLIF($3, ''), $4, clock_timestamp(), 'proposed') RETURNING id::text, title, criteria, created_by, status`,
			planID, g.Title, g.Criteria, c.Sub).Scan(&saved.ID, &saved.Title, &saved.Criteria, &saved.CreatedBy, &saved.Status)
		if err != nil {
			apierr.WriteJSON(w, fmt.Errorf("insert goal: %w", err))
			return
		}
		out = append(out, saved)
	}
	// наставник и руководитель команды узнают, что их ждут цели
	var name string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM core.users WHERE id = $1`, c.Sub).Scan(&name); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("select intern: %w", err))
		return
	}
	rows, err := tx.Query(ctx, `SELECT id FROM core.users WHERE team_id = $1 AND role IN ('mentor', 'lead') AND can_login`, c.TeamID)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("select team: %w", err))
		return
	}
	var people []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			apierr.WriteJSON(w, fmt.Errorf("scan team: %w", err))
			return
		}
		people = append(people, id)
	}
	rows.Close()
	for _, id := range people {
		n := notify.Notification{UserID: id, Kind: notify.KindStatusChange, Title: "Цели на стажировку ждут согласования: " + name,
			Body: fmt.Sprintf("Целей: %d. Откройте «Мои стажёры» → план стажёра → «Цели адаптации» и согласуйте их.", len(out))}
		if err := (notify.Store{}).Create(ctx, tx, n); err != nil {
			apierr.WriteJSON(w, err)
			return
		}
	}
	if err := h.emitter.Emit(ctx, tx, analytics.Event{Name: "plan.goals_proposed", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
		Props: map[string]any{"count": len(out)}}); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.WriteJSON(w, fmt.Errorf("commit: %w", err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// reviewerOf: вызывающий — наставник или руководитель команды стажёра.
func (h *Handler) reviewerOf(ctx context.Context, c authn.Claims, internID string) (bool, error) {
	if c.Role != "mentor" && c.Role != "lead" {
		return false, nil
	}
	return h.internInTeam(ctx, internID, c.TeamID)
}

// askReviewers сообщает наставнику и руководителю команды, что задача ждёт их подтверждения.
func (h *Handler) askReviewers(ctx context.Context, tx pgx.Tx, t taskRow) error {
	var name, team, title string
	if err := tx.QueryRow(ctx, `SELECT u.display_name, u.team_id, pt.title FROM core.users u, core.plan_tasks pt
		WHERE u.id = $1 AND pt.plan_id = $2::uuid AND pt.checklist_item_id IS NOT DISTINCT FROM $3 AND pt.parent_task_id IS NULL
		LIMIT 1`, t.owner, t.planID, t.item).Scan(&name, &team, &title); err != nil {
		return fmt.Errorf("select intern: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM core.users WHERE team_id = $1 AND role IN ('mentor', 'lead') AND can_login`, team)
	if err != nil {
		return fmt.Errorf("select team: %w", err)
	}
	var people []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan team: %w", err)
		}
		people = append(people, id)
	}
	rows.Close()
	item := ""
	if t.item != nil {
		item = *t.item
	}
	for _, id := range people {
		n := notify.Notification{UserID: id, Kind: notify.KindStatusChange, ChecklistItemID: item,
			Title: "Подтвердите задачу: " + name, Body: "«" + title + "» — стажёр отметил выполнение. Откройте план стажёра и подтвердите."}
		if err := (notify.Store{}).Create(ctx, tx, n); err != nil {
			return err
		}
	}
	return nil
}
