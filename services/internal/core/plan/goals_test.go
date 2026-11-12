package plan_test

import (
	"encoding/json"
	"testing"
)

func TestGoals_InternProposesLeadApprovesAndTracks_Organizers1211(t *testing.T) {
	e := setup(t)
	propose := func(u user, body string) (int, []byte) { return e.do(u, "POST", "/api/plan/goals/propose", body) }

	code, b := propose(anna, `{"goals":[{"title":"Выкатить первую задачу в прод","criteria":"PR влит, фича за флагом"},{"title":" "}]}`)
	if code != 201 {
		t.Fatalf("propose: %d %s", code, b)
	}
	var got []map[string]any
	_ = json.Unmarshal(b, &got)
	if len(got) != 1 || got[0]["status"] != "proposed" || got[0]["created_by"] != "intern.anna" {
		t.Fatalf("proposed: %v", got)
	}
	// the mentor and the lead of the team are told; the other team is not
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id IN ('mentor.oleg','lead.maria') AND title LIKE 'Цели на стажировку ждут согласования: Анна Котова'`); n != 2 {
		t.Fatalf("notifications: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id IN ('mentor.sergey','lead.dmitry') AND title LIKE 'Цели%'`); n != 0 {
		t.Fatal("another team must not be notified")
	}
	_, p := e.getPlan(oleg, "?user_id=intern.anna")
	if g := p["goals"].([]any)[0].(map[string]any); g["status"] != "proposed" {
		t.Fatalf("plan goals: %v", p["goals"])
	}
	_, rep := e.report(oleg, "?user_id=intern.anna")
	if num(rep, "goals_proposed") != 1 {
		t.Fatalf("report: %v", rep)
	}

	// the lead approves it, later marks it achieved
	code, b = e.do(maria, "PUT", "/api/plan/goals", `{"user_id":"intern.anna","goals":[{"title":"Выкатить первую задачу в прод","criteria":"PR влит, фича за флагом","status":"active"}]}`)
	if code != 200 {
		t.Fatalf("approve: %d %s", code, b)
	}
	code, _ = e.do(maria, "PUT", "/api/plan/goals", `{"user_id":"intern.anna","goals":[{"title":"Выкатить первую задачу в прод","status":"achieved"}]}`)
	if code != 200 {
		t.Fatalf("achieved: %d", code)
	}
	_, rep = e.report(oleg, "?user_id=intern.anna")
	if num(rep, "goals_achieved") != 1 || num(rep, "goals_proposed") != 0 || num(rep, "goals") != 1 {
		t.Fatalf("report after: %v", rep)
	}

	// rights and validation
	for _, c := range []struct {
		u    user
		body string
		want int
	}{
		{oleg, `{"goals":[{"title":"x"}]}`, 403},
		{anna, `{"goals":[]}`, 400},
		{anna, `{"goals":[{"title":"1"},{"title":"2"},{"title":"3"},{"title":"4"},{"title":"5"},{"title":"6"}]}`, 400},
	} {
		if code, b := propose(c.u, c.body); code != c.want {
			t.Fatalf("%s %s: %d %s", c.u.id, c.body, code, b)
		}
	}
	if code, _ := e.do(maria, "PUT", "/api/plan/goals", `{"user_id":"intern.anna","goals":[{"title":"x","status":"done"}]}`); code != 400 {
		t.Fatalf("unknown status: %d", code)
	}
}
