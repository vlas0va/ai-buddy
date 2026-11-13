package escalations

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
)

// Organizers 13.11: buddy does not deliver messages to the mentor — the intern writes the mentor directly — but every
// question buddy could not answer goes to the addressee's stack. The mentor answers it in a batch when convenient:
// similar questions are grouped, one answer closes the group, notifies every asker and can be added to the knowledge base.

// QueueRoles are the addressees whose stack collects interns' questions.
var QueueRoles = map[string]bool{"mentor": true, "lead": true, "hrbp": true}

// Enqueue puts an intern's question into the addressee's stack without a notification (the addressee gets a daily
// digest from AI-cron). messageID is the assistant message that showed the draft: the same message is queued once.
// It reports whether a new question was stored.
func Enqueue(ctx context.Context, pool *pgxpool.Pool, c authn.Claims, toRole, question, sessionID, messageID string) (bool, error) {
	if c.Role != "intern" || !QueueRoles[toRole] {
		return false, nil
	}
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > maxText || !uuidRE.MatchString(sessionID) || !uuidRE.MatchString(messageID) {
		return false, nil
	}
	to, err := (&Handler{pool: pool}).target(ctx, c, toRole)
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, `INSERT INTO core.escalations (from_user_id, to_user_id, to_role, session_id, text, message_id)
		SELECT $1, $2, $3, s.id, $5, m.id FROM core.chat_sessions s JOIN core.chat_messages m ON m.session_id = s.id
		WHERE s.id = $4 AND s.user_id = $1 AND m.id = $6
		ON CONFLICT (message_id) WHERE message_id IS NOT NULL DO NOTHING`, c.Sub, to, toRole, sessionID, question, messageID)
	if err != nil {
		return false, fmt.Errorf("enqueue question: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// QueueGroup is one entry of the stack: similar open questions to the caller.
type QueueGroup struct {
	IDs        []string `json:"ids"`      // oldest first; the first id is answered, the others follow it
	Question   string   `json:"question"` // the question without greetings and names
	Count      int      `json:"count"`
	Askers     []string `json:"askers"`
	FirstAsked string   `json:"first_asked_at"`
	LastAsked  string   `json:"last_asked_at"`
}

// queue: GET /api/escalations/queue — the open questions addressed to the caller, similar ones grouped, the most asked
// first. Only the addressee sees them (R71): the query is scoped by to_user_id from the JWT.
func (h *Handler) queue(w http.ResponseWriter, r *http.Request) {
	c, ok := claims(w, r)
	if !ok {
		return
	}
	out, err := h.buildQueue(r.Context(), c.Sub)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) buildQueue(ctx context.Context, userID string) ([]QueueGroup, error) {
	rows, err := h.pool.Query(ctx, `SELECT e.id::text, u.display_name, e.text,
		to_char(e.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM core.escalations e JOIN core.users u ON u.id = e.from_user_id
		WHERE e.to_user_id = $1 AND e.status = 'open' ORDER BY e.created_at, e.id LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("select queue: %w", err)
	}
	defer rows.Close()
	names := h.userNames(ctx)
	type member struct{ terms map[string]bool }
	var groups []QueueGroup
	var keys [][]member
	for rows.Next() {
		var id, who, text, at string
		if err := rows.Scan(&id, &who, &text, &at); err != nil {
			return nil, fmt.Errorf("scan queue: %w", err)
		}
		q := generalize(text, names)
		t := terms(q)
		placed := false
		for i := range groups {
			if similar(t, keys[i][0].terms) {
				g := &groups[i]
				g.IDs = append(g.IDs, id)
				g.Count++
				g.LastAsked = at
				if !contains(g.Askers, who) {
					g.Askers = append(g.Askers, who)
				}
				keys[i] = append(keys[i], member{t})
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, QueueGroup{IDs: []string{id}, Question: q, Count: 1, Askers: []string{who}, FirstAsked: at, LastAsked: at})
			keys = append(keys, []member{{t}})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read queue: %w", err)
	}
	if groups == nil {
		groups = []QueueGroup{}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].LastAsked > groups[j].LastAsked
	})
	return groups, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

var stopTerms = map[string]bool{
	"как": true, "где": true, "что": true, "кто": true, "когда": true, "можно": true, "ли": true, "мне": true, "меня": true,
	"это": true, "для": true, "или": true, "есть": true, "нужно": true, "надо": true, "какой": true, "какие": true,
	"какая": true, "мой": true, "моя": true, "мои": true, "моей": true, "моего": true, "свой": true, "этом": true,
	"подскажи": true, "пожалуйста": true, "привет": true, "спасибо": true, "буду": true, "будет": true, "the": true,
}

// terms are the 5-letter stems of the meaningful words of a question.
func terms(q string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) < 3 || stopTerms[w] {
			continue
		}
		r := []rune(w)
		if len(r) > 5 {
			r = r[:5]
		}
		out[string(r)] = true
	}
	return out
}

// similar: two questions share at least 60% of the smaller set of stems (and at least two of them, or all of a
// one-word question).
func similar(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	common := 0
	for t := range a {
		if b[t] {
			common++
		}
	}
	small := min(len(a), len(b))
	return common*10 >= small*6 && (common >= 2 || small == 1)
}
