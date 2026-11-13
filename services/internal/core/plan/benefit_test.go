package plan_test

import (
	"encoding/json"
	"testing"
)

func (e *env) benefit(u user) (int, map[string]any) {
	e.t.Helper()
	code, b := e.do(u, "GET", "/api/team/benefit", "")
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return code, m
}

func TestTeamBenefit_SharesAndSavedTime_Idea1311(t *testing.T) {
	e := setup(t)
	e.exec(`INSERT INTO core.chat_sessions (id, user_id) VALUES ('00000000-0000-0000-0000-0000000000b1', 'intern.anna'),
		('00000000-0000-0000-0000-0000000000b2', 'intern.ivan')`)
	e.exec(`INSERT INTO core.chat_messages (id, session_id, author, text) VALUES
		('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000b1', 'user', 'где логи?'),
		('00000000-0000-0000-0000-0000000000c2', '00000000-0000-0000-0000-0000000000b1', 'assistant', 'в Grafana'),
		('00000000-0000-0000-0000-0000000000c3', '00000000-0000-0000-0000-0000000000b1', 'user', 'кто мой наставник?'),
		('00000000-0000-0000-0000-0000000000c4', '00000000-0000-0000-0000-0000000000b1', 'user', 'как оформить отгул?'),
		('00000000-0000-0000-0000-0000000000c5', '00000000-0000-0000-0000-0000000000b1', 'user', 'что такое ревью?'),
		('00000000-0000-0000-0000-0000000000c6', '00000000-0000-0000-0000-0000000000b2', 'user', 'чужой вопрос')`)
	e.exec(`INSERT INTO core.escalations (from_user_id, to_user_id, to_role, text, status, answer, added_to_kb)
		VALUES ('intern.anna', 'mentor.oleg', 'mentor', 'что такое ревью?', 'answered', 'проверка кода', true)`)
	e.exec(`INSERT INTO core.feedback (message_id, user_id, useful) VALUES ('00000000-0000-0000-0000-0000000000c2', 'intern.anna', true)`)

	for _, u := range []user{oleg, maria} {
		code, m := e.benefit(u)
		if code != 200 {
			t.Fatalf("%s: code %d %v", u.id, code, m)
		}
		// 4 вопроса Анны, 1 передан наставнику: 3 закрыл buddy = 75%, 30 минут; Иван из другой команды не считается.
		if num(m, "asked") != 4 || num(m, "escalated") != 1 || num(m, "answered") != 1 || num(m, "added_to_kb") != 1 ||
			num(m, "self_served") != 3 || num(m, "self_served_percent") != 75 || num(m, "saved_minutes") != 30 ||
			num(m, "useful") != 1 || num(m, "not_useful") != 0 || num(m, "minutes_per_question") != 10 {
			t.Fatalf("%s: totals %v", u.id, m)
		}
		rows := m["rows"].([]any)
		anna := rows[0].(map[string]any)
		if anna["user_id"] != "intern.anna" || num(anna, "self_served") != 3 || num(anna, "useful") != 1 {
			t.Fatalf("%s: rows %v", u.id, rows)
		}
	}
	// Другая команда: только вопрос Ивана, ничего от Анны.
	if _, m := e.benefit(dmitry); num(m, "asked") != 1 || num(m, "self_served_percent") != 100 {
		t.Fatalf("other team: %v", m)
	}
}

func TestTeamBenefit_Rights_R71(t *testing.T) {
	e := setup(t)
	for _, u := range []user{anna, elena, petr} {
		if code, _ := e.benefit(u); code != 403 {
			t.Fatalf("%s: code %d, want 403", u.id, code)
		}
	}
	if code, _ := e.benefit(user{}); code != 401 {
		t.Fatalf("anonymous: code %d", code)
	}
	code, m := e.benefit(user{"mentor.new", "mentor", "delivery"})
	if code != 200 || num(m, "interns") != 0 || num(m, "self_served_percent") != 0 || len(m["rows"].([]any)) != 0 {
		t.Fatalf("no interns: %d %v", code, m)
	}
}
