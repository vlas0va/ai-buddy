package plan

import (
	"context"
	"fmt"
	"net/http"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// Benefit is the «Польза buddy» panel of the team (idea 13.11): how many questions of the caller's interns buddy closed
// without a person, the mentor's time that saved (an estimate, MinutesPerQuestion per question) and the interns' «Полезно» /
// «Не полезно» marks on buddy's answers. Counters only — no message texts (R71) and no absences (R72).
type Benefit struct {
	Interns            int              `json:"interns"`
	MinutesPerQuestion int              `json:"minutes_per_question"`
	Asked              int              `json:"asked"`
	Escalated          int              `json:"escalated"`
	Answered           int              `json:"answered"`
	AddedToKB          int              `json:"added_to_kb"`
	SelfServed         int              `json:"self_served"`
	SelfServedPercent  int              `json:"self_served_percent"` // share of the questions closed by buddy, 0 without questions
	SavedMinutes       int              `json:"saved_minutes"`
	Useful             int              `json:"useful"`
	NotUseful          int              `json:"not_useful"`
	Rows               []BenefitInterns `json:"rows"`
}

// BenefitInterns is one intern's line of the panel.
type BenefitInterns struct {
	UserID       string `json:"user_id"`
	DisplayName  string `json:"display_name"`
	Asked        int    `json:"asked"`
	Escalated    int    `json:"escalated"`
	SelfServed   int    `json:"self_served"`
	SavedMinutes int    `json:"saved_minutes"`
	Useful       int    `json:"useful"`
	NotUseful    int    `json:"not_useful"`
}

const benefitSQL = `SELECT u.id, u.display_name,
		(SELECT count(*) FROM core.chat_messages m JOIN core.chat_sessions s ON s.id = m.session_id
		  WHERE s.user_id = u.id AND m.author = 'user')::int,
		(SELECT count(*) FROM core.escalations e WHERE e.from_user_id = u.id)::int,
		(SELECT count(*) FROM core.escalations e WHERE e.from_user_id = u.id AND e.status = 'answered')::int,
		(SELECT count(*) FROM core.escalations e WHERE e.from_user_id = u.id AND e.added_to_kb)::int,
		(SELECT count(*) FROM core.feedback f WHERE f.user_id = u.id AND f.useful)::int,
		(SELECT count(*) FROM core.feedback f WHERE f.user_id = u.id AND NOT f.useful)::int
	FROM core.users u
	WHERE u.role = 'intern' AND %s
	ORDER BY u.display_name, u.id`

// internScope is the SQL filter and its argument for the interns the caller looks after: a mentor — the interns of
// their mentor plans, a lead — the interns of their team; any other role is forbidden.
func internScope(c authn.Claims) (string, any, error) {
	switch c.Role {
	case "mentor":
		return `u.id IN (SELECT intern_id FROM core.plans WHERE user_id = $1 AND plan_role = 'mentor')`, c.Sub, nil
	case "lead":
		return `u.team_id = $1`, c.TeamID, nil
	default:
		return "", nil, apierr.Forbidden()
	}
}

// TeamBenefit builds the panel for the caller (GET /api/team/benefit).
func TeamBenefit(ctx context.Context, q Querier, c authn.Claims) (Benefit, error) {
	out := Benefit{MinutesPerQuestion: MinutesPerQuestion, Rows: []BenefitInterns{}}
	scope, arg, err := internScope(c)
	if err != nil {
		return out, err
	}
	rows, err := q.Query(ctx, fmt.Sprintf(benefitSQL, scope), arg)
	if err != nil {
		return out, fmt.Errorf("select team benefit: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r BenefitInterns
		var answered, added int
		if err := rows.Scan(&r.UserID, &r.DisplayName, &r.Asked, &r.Escalated, &answered, &added, &r.Useful, &r.NotUseful); err != nil {
			return out, fmt.Errorf("scan team benefit: %w", err)
		}
		r.SelfServed = max(r.Asked-r.Escalated, 0)
		r.SavedMinutes = r.SelfServed * MinutesPerQuestion
		out.Interns++
		out.Asked += r.Asked
		out.Escalated += r.Escalated
		out.Answered += answered
		out.AddedToKB += added
		out.SelfServed += r.SelfServed
		out.SavedMinutes += r.SavedMinutes
		out.Useful += r.Useful
		out.NotUseful += r.NotUseful
		out.Rows = append(out.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("read team benefit: %w", err)
	}
	if out.Asked > 0 {
		out.SelfServedPercent = (out.SelfServed*100 + out.Asked/2) / out.Asked
	}
	return out, nil
}

// teamBenefit: GET /api/team/benefit — the mentor and the lead see the panel of their interns; others get 403.
func (h *Handler) teamBenefit(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	out, err := TeamBenefit(r.Context(), h.pool, c)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
