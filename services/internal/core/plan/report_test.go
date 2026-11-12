package plan_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func (e *env) report(u user, query string) (int, map[string]any) {
	e.t.Helper()
	code, b := e.do(u, "GET", "/api/plan/report"+query, "")
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return code, m
}

func num(m map[string]any, path ...string) int {
	var cur any = m
	for _, p := range path {
		cur = cur.(map[string]any)[p]
	}
	return int(cur.(float64))
}

func TestReport_FactsFromThePlan_Idea1211(t *testing.T) {
	e := setup(t)
	code, m := e.report(anna, "")
	if code != 200 {
		t.Fatalf("code %d: %v", code, m)
	}
	if m["name"] != "Анна Котова" || num(m, "total_days") != 90 || m["final"] != false || num(m, "days") != 10 {
		t.Fatalf("header: %v", m)
	}
	if num(m, "tasks", "early") != 1 || num(m, "tasks", "done") != 1 || num(m, "tasks", "overdue_now") < 1 {
		t.Fatalf("tasks: %v", m["tasks"])
	}
	b, _ := json.Marshal(m["overdue"])
	if !strings.Contains(string(b), "каналы") && num(m, "tasks", "overdue_now") == 0 {
		t.Fatalf("overdue: %s", b)
	}
	if len(m["periods"].([]any)) == 0 || len(m["discuss"].([]any)) == 0 {
		t.Fatalf("periods/discuss: %v", m)
	}

	// The overdue CL-05 done today counts as late; going back to work clears the completion time.
	e.setItem("intern.anna", "intern", "CL-05", "done")
	_, m = e.report(anna, "")
	if num(m, "tasks", "late") != 1 || num(m, "tasks", "done") != 2 {
		t.Fatalf("late: %v", m["tasks"])
	}
	e.setItem("intern.anna", "intern", "CL-05", "in_progress")
	if n := e.count(`SELECT count(*) FROM core.plan_tasks WHERE id = $1 AND completed_at IS NULL AND was_overdue`, e.taskID("intern.anna", "intern", "CL-05")); n != 1 {
		t.Fatal("completed_at must be cleared and was_overdue kept")
	}

	// Questions: one asked in chat, one sent to the mentor, answered and added to the KB.
	e.exec(`INSERT INTO core.chat_sessions (id, user_id) VALUES ('00000000-0000-0000-0000-0000000000a1', 'intern.anna')`)
	e.exec(`INSERT INTO core.chat_messages (session_id, author, text) VALUES ('00000000-0000-0000-0000-0000000000a1', 'user', 'как дела?'),
		('00000000-0000-0000-0000-0000000000a1', 'assistant', 'хорошо')`)
	e.exec(`INSERT INTO core.escalations (from_user_id, to_user_id, to_role, text, status, answer, added_to_kb)
		VALUES ('intern.anna', 'mentor.oleg', 'mentor', 'где логи?', 'answered', 'в Grafana', true)`)
	_, m = e.report(oleg, "?user_id=intern.anna")
	if num(m, "questions", "asked") != 1 || num(m, "questions", "escalated") != 1 || num(m, "questions", "answered") != 1 ||
		num(m, "questions", "added_to_kb") != 1 {
		t.Fatalf("questions: %v", m["questions"])
	}
}

func TestReport_Rights_R71(t *testing.T) {
	e := setup(t)
	cases := []struct {
		name  string
		u     user
		query string
		want  int
	}{
		{"mentor of the team", oleg, "?user_id=intern.anna", 200},
		{"lead of the team", maria, "?user_id=intern.anna", 200},
		{"mentor without an intern", oleg, "", 400},
		{"mentor of another team", sergey, "?user_id=intern.anna", 403},
		{"another intern", ivan, "?user_id=intern.anna", 403},
		{"hr bp", elena, "?user_id=intern.anna", 403},
		{"no token", user{}, "", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, m := e.report(c.u, c.query); code != c.want {
				t.Fatalf("code %d, want %d: %v", code, c.want, m)
			}
		})
	}
}
