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

// Организаторы 13.11: buddy не передаёт сообщения наставнику — стажёр пишет наставнику сам, — но каждый вопрос, на
// который buddy не ответил, попадает в список адресата. Наставник отвечает пачкой, когда удобно: похожие вопросы
// собраны в группу, один ответ закрывает группу, уведомляет каждого, кто спрашивал, и может быть добавлен в базу
// знаний.

// QueueRoles — адресаты, у которых есть список вопросов стажёров.
var QueueRoles = map[string]bool{"mentor": true, "lead": true, "hrbp": true}

// Enqueue кладёт вопрос стажёра в список адресата без уведомления (адресат получает ежедневную сводку от AI-cron).
// messageID — сообщение ассистента, в котором показан черновик: одно и то же сообщение попадает в список один раз.
// Сообщает, сохранён ли новый вопрос.
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

// QueueGroup — один пункт списка: похожие открытые вопросы к вызывающему.
type QueueGroup struct {
	IDs        []string `json:"ids"`      // сначала самый старый; отвечают на первый id, остальные следуют за ним
	Question   string   `json:"question"` // вопрос без приветствий и имён
	Count      int      `json:"count"`
	Askers     []string `json:"askers"`
	FirstAsked string   `json:"first_asked_at"`
	LastAsked  string   `json:"last_asked_at"`
}

// queue: GET /api/escalations/queue — открытые вопросы к вызывающему, похожие сгруппированы, чаще задаваемые сначала.
// Видит их только адресат (R71): запрос ограничен to_user_id из JWT.
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

// terms — основы из 5 букв значимых слов вопроса.
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

// similar: у двух вопросов совпадает не меньше 60% меньшего набора основ (и не меньше двух основ, или все — у вопроса
// из одного слова).
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
