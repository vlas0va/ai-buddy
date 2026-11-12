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

func TestTasks_ConfirmModes_Organizers1213(t *testing.T) {
	e := setup(t)
	patch := func(u user, id, status string) (int, map[string]any) {
		code, b := e.do(u, "PATCH", "/api/plan/tasks/"+id, `{"status":"`+status+`"}`)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return code, m
	}
	_, p := e.getPlan(anna, "")
	modes := map[string]string{}
	for _, it := range items(p) {
		modes[it["checklist_item_id"].(string)] = it["confirm"].(string)
	}
	if modes["CL-07"] != "quiz" || modes["CL-03"] != "mentor" || modes["CL-05"] != "self" {
		t.Fatalf("modes: %v", modes)
	}

	// a quiz task is not ticked by hand
	if code, m := patch(anna, e.taskID("intern.anna", "intern", "CL-07"), "done"); code != 409 {
		t.Fatalf("quiz task: %d %v", code, m)
	}
	// a mentor task: the intern asks, the mentor and the lead are told, the mentor confirms
	ivanTask := e.taskID("intern.ivan", "intern", "CL-03")
	code, m := patch(ivan, ivanTask, "done")
	if code != 200 || m["status"] != "waiting_approval" {
		t.Fatalf("intern asks: %d %v", code, m)
	}
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id IN ('mentor.sergey','lead.dmitry') AND title = 'Подтвердите задачу: Иван Белов'`); n != 2 {
		t.Fatalf("notifications: %d", n)
	}
	if code, _ := patch(oleg, ivanTask, "done"); code != 403 {
		t.Fatalf("another team's mentor: %d", code)
	}
	if code, m := patch(sergey, ivanTask, "done"); code != 200 || m["status"] != "done" {
		t.Fatalf("mentor confirms: %d %v", code, m)
	}
	// the mentor touches only tasks waiting for him
	if code, _ := patch(sergey, e.taskID("intern.ivan", "intern", "CL-05"), "done"); code != 403 {
		t.Fatalf("self task by mentor: %d", code)
	}
	// the intern may take a request back while it waits; the lead may return it
	annaTask := e.taskID("intern.anna", "intern", "CL-03")
	e.exec(`UPDATE core.plan_tasks SET status = 'in_progress' WHERE id = $1`, annaTask)
	if _, m := patch(anna, annaTask, "done"); m["status"] != "waiting_approval" {
		t.Fatalf("anna asks: %v", m)
	}
	if code, m := patch(maria, annaTask, "in_progress"); code != 200 || (m["status"] != "in_progress" && m["status"] != "overdue") {
		t.Fatalf("lead returns: %d %v", code, m)
	}
}
