package plan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/plan"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/pgtest"
)

var secret = []byte("test-secret")

type user struct{ id, role, team string }

var (
	anna   = user{"intern.anna", "intern", "delivery"}
	ivan   = user{"intern.ivan", "intern", "search"}
	oleg   = user{"mentor.oleg", "mentor", "delivery"}
	sergey = user{"mentor.sergey", "mentor", "search"}
	maria  = user{"lead.maria", "lead", "delivery"}
	dmitry = user{"lead.dmitry", "lead", "search"}
	elena  = user{"hrbp.elena", "hrbp", "people"}
	petr   = user{"alumni.petr", "alumni", "delivery"}
)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    *plan.Handler
	srv  *httptest.Server
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	h := plan.New(pool, analytics.NewEmitter(false), nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(authn.Middleware(secret)(mux))
	t.Cleanup(srv.Close)
	return &env{t, pool, h, srv}
}

func (e *env) do(u user, method, path, body string) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader([]byte(body)))
	if u.id != "" {
		tok, _ := authn.Issue(secret, authn.Claims{Sub: u.id, Role: u.role, TeamID: u.team}, time.Hour)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) taskID(owner, role, item string) string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(context.Background(), `SELECT t.id::text FROM core.plan_tasks t JOIN core.plans p ON p.id=t.plan_id
		WHERE p.user_id=$1 AND p.plan_role=$2 AND t.checklist_item_id=$3 AND t.parent_task_id IS NULL`, owner, role, item).Scan(&id)
	if err != nil {
		e.t.Fatalf("task %s/%s: %v", owner, item, err)
	}
	return id
}

func (e *env) setItem(owner, role, item, status string) {
	e.t.Helper()
	e.exec(`UPDATE core.plan_tasks t SET status=$4 FROM core.plans p WHERE p.id=t.plan_id AND p.user_id=$1 AND p.plan_role=$2
		AND t.checklist_item_id=$3 AND t.parent_task_id IS NULL`, owner, role, item, status)
}

func (e *env) getPlan(u user, query string) (int, map[string]any) {
	e.t.Helper()
	code, b := e.do(u, "GET", "/api/plan"+query, "")
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return code, m
}

func items(p map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, per := range p["periods"].([]any) {
		for _, t := range per.(map[string]any)["tasks"].([]any) {
			tm := t.(map[string]any)
			if id, ok := tm["checklist_item_id"].(string); ok {
				out[id] = tm
			}
		}
	}
	return out
}

func TestPlan_S1_InternPlanShape_R18(t *testing.T) {
	e := setup(t)
	code, p := e.getPlan(anna, "")
	if code != 200 {
		t.Fatalf("%d %v", code, p)
	}
	if p["user_id"] != "intern.anna" || p["plan_role"] != "intern" || p["intern_id"] != "intern.anna" || p["current_period"] != "week_2" {
		t.Fatalf("header: %v", p)
	}
	periods := p["periods"].([]any)
	wantIDs := []string{"week_1", "week_2", "month_1", "month_2", "after_3_months"}
	wantLabels := []string{"1 неделя", "2 неделя", "1 месяц", "2 месяц", "После 3-х месяцев"}
	for i, per := range periods {
		pm := per.(map[string]any)
		if pm["id"] != wantIDs[i] || pm["label"] != wantLabels[i] {
			t.Fatalf("period %d: %v", i, pm)
		}
		if _, ok := pm["tasks"].([]any); !ok {
			t.Fatalf("tasks must be an array: %v", pm)
		}
	}
	it := items(p)
	if len(it) != 10 {
		t.Fatalf("intern plan must have 10 checklist items, got %d", len(it))
	}
	for _, mentorOnly := range []string{"CL-01", "CL-02", "CL-06", "CL-12", "CL-13", "CL-16"} {
		if _, ok := it[mentorOnly]; ok {
			t.Errorf("%s must not be in the intern plan", mentorOnly)
		}
	}
	if it["CL-03"]["status"] != "done" || it["CL-03"]["human_only"] != true || it["CL-07"]["status"] != "in_progress" {
		t.Fatalf("seed statuses: %v %v", it["CL-03"], it["CL-07"])
	}
	cl04 := it["CL-04"]
	subs := cl04["subtasks"].([]any)
	if len(subs) != 3 || subs[0].(map[string]any)["status"] != "done" || subs[0].(map[string]any)["parent_task_id"] != cl04["id"] {
		t.Fatalf("CL-04 subtasks: %v", subs)
	}
	if subs[0].(map[string]any)["title"] != "Попроси добавить себя во все ключевые каналы и чаты" || subs[2].(map[string]any)["title"] == subs[0].(map[string]any)["title"] {
		t.Fatalf("subtask order: %v", subs)
	}
	if _, ok := it["CL-08"]["subtasks"].([]any); !ok || cl04["source"] != "checklist" || cl04["parent_task_id"] != nil || cl04["linked_action_id"] != nil {
		t.Fatalf("task fields: %v", cl04)
	}
	if goals, ok := p["goals"].([]any); !ok || len(goals) != 0 {
		t.Fatalf("goals: %v", p["goals"])
	}
}

func TestPlan_OverdueComputedOnRead_R19(t *testing.T) {
	e := setup(t)
	_, p := e.getPlan(anna, "")
	it := items(p)
	// CL-04 is stored in_progress but its due date (cohort start + 7 days) has passed.
	if it["CL-04"]["status"] != "overdue" || it["CL-05"]["status"] != "overdue" || it["CL-07"]["status"] != "in_progress" {
		t.Fatalf("statuses: 04=%v 05=%v 07=%v", it["CL-04"]["status"], it["CL-05"]["status"], it["CL-07"]["status"])
	}
	if it["CL-05"]["due_at"] == nil || len(it["CL-05"]["due_at"].(string)) != 10 {
		t.Fatalf("due_at format: %v", it["CL-05"]["due_at"])
	}
	// waiting_approval and done are never reported as overdue
	e.setItem("intern.anna", "intern", "CL-05", "waiting_approval")
	e.setItem("intern.anna", "intern", "CL-04", "done")
	_, p = e.getPlan(anna, "")
	it = items(p)
	if it["CL-05"]["status"] != "waiting_approval" || it["CL-04"]["status"] != "done" {
		t.Fatalf("statuses: %v %v", it["CL-05"]["status"], it["CL-04"]["status"])
	}
	// the clock drives the rule: a month later CL-07 is overdue too
	e.h.Now = func() time.Time { return time.Now().AddDate(0, 0, 5) }
	_, p = e.getPlan(anna, "")
	if items(p)["CL-07"]["status"] != "overdue" || p["current_period"] != "month_1" {
		t.Fatalf("after 5 days: %v %v", items(p)["CL-07"]["status"], p["current_period"])
	}
}

func TestPlan_S1_NextStepOverdueFirst_R20(t *testing.T) {
	type step struct{ item, status string }
	cases := []struct {
		name       string
		setup      []step
		extraSQL   string
		daysAhead  int
		wantItem   string // "" = next_step must be null
		wantAction string
		wantPrompt bool
	}{
		{name: "seed: overdue untouched item beats overdue in-progress one", wantItem: "CL-05", wantAction: "request_access"},
		{name: "access done: draft message to mentor", setup: []step{{"CL-05", "done"}}, wantItem: "CL-04", wantAction: "draft_message"},
		{name: "week_1 done: in-progress document study", setup: []step{{"CL-05", "done"}, {"CL-04", "done"}}, wantItem: "CL-07", wantAction: "ask_buddy", wantPrompt: true},
		{name: "everything of current period done: nothing next", setup: []step{{"CL-05", "done"}, {"CL-04", "done"}, {"CL-07", "done"}}, wantItem: ""},
		{name: "waiting_approval is not next", setup: []step{{"CL-05", "waiting_approval"}, {"CL-04", "done"}, {"CL-07", "done"}}, wantItem: ""},
		{name: "action tasks are skipped", setup: []step{{"CL-05", "done"}, {"CL-04", "done"}, {"CL-07", "done"}},
			extraSQL: `INSERT INTO core.plan_tasks (plan_id, source, period, title, status) SELECT id, 'action', 'week_2', 'Отгул', 'not_started' FROM core.plans WHERE user_id='intern.anna' AND plan_role='intern'`, wantItem: ""},
		{name: "next period opens its items", setup: []step{{"CL-05", "done"}, {"CL-04", "done"}, {"CL-07", "done"}}, daysAhead: 8, wantItem: "CL-08", wantAction: "ask_buddy", wantPrompt: true},
		{name: "human-only items come last", setup: []step{{"CL-05", "done"}, {"CL-04", "done"}, {"CL-07", "done"}, {"CL-03", "not_started"}}, wantItem: "CL-03", wantAction: "mark_done"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			for _, s := range c.setup {
				e.setItem("intern.anna", "intern", s.item, s.status)
			}
			if c.extraSQL != "" {
				e.exec(c.extraSQL)
			}
			if c.daysAhead > 0 {
				e.h.Now = func() time.Time { return time.Now().AddDate(0, 0, c.daysAhead) }
			}
			_, p := e.getPlan(anna, "")
			ns, _ := p["next_step"].(map[string]any)
			if c.wantItem == "" {
				if p["next_step"] != nil {
					t.Fatalf("want null, got %v", p["next_step"])
				}
				return
			}
			if ns == nil {
				t.Fatalf("next_step is null")
			}
			it := items(p)[c.wantItem]
			if ns["task_id"] != it["id"] || ns["title"] != it["title"] || ns["action"] != c.wantAction {
				t.Fatalf("next_step %v, want %s/%s", ns, c.wantItem, c.wantAction)
			}
			if _, has := ns["prompt"]; has != c.wantPrompt {
				t.Fatalf("prompt presence: %v", ns)
			}
		})
	}
}

func TestPlan_S1_NextStepDefaultActions_R20(t *testing.T) {
	e := setup(t)
	// late in the internship, a non-human item without a dedicated action falls back to ask_buddy with a prompt
	e.h.Now = func() time.Time { return time.Now().AddDate(0, 0, 150) }
	for _, it := range []string{"CL-03", "CL-04", "CL-05", "CL-07", "CL-08", "CL-09", "CL-10", "CL-14", "CL-15"} {
		e.setItem("intern.anna", "intern", it, "done")
	}
	_, p := e.getPlan(anna, "")
	ns := p["next_step"].(map[string]any)
	if p["current_period"] != "after_3_months" || ns["action"] != "ask_buddy" || !strings.Contains(ns["prompt"].(string), "Экваторная встреча") {
		t.Fatalf("CL-11 fallback: %v (period %v)", ns, p["current_period"])
	}
	// CL-10 has its own prompt
	e.setItem("intern.anna", "intern", "CL-10", "not_started")
	_, p = e.getPlan(anna, "")
	ns = p["next_step"].(map[string]any)
	if ns["action"] != "ask_buddy" || !strings.Contains(ns["prompt"].(string), "экваторной") {
		t.Fatalf("CL-10: %v", ns)
	}
}

func TestPlan_MentorViews_R08_R71(t *testing.T) {
	e := setup(t)
	code, p := e.getPlan(oleg, "")
	if code != 200 || p["plan_role"] != "mentor" || p["user_id"] != "mentor.oleg" || p["intern_id"] != "intern.anna" {
		t.Fatalf("mentor plan: %d %v", code, p)
	}
	it := items(p)
	for _, want := range []string{"CL-01", "CL-02", "CL-06", "CL-12", "CL-13", "CL-14", "CL-16"} {
		if _, ok := it[want]; !ok {
			t.Errorf("mentor plan misses %s", want)
		}
	}
	if len(it) != 7 {
		t.Errorf("mentor plan has %d items", len(it))
	}
	if ns := p["next_step"].(map[string]any); ns["action"] != "request_access" || ns["title"] != it["CL-01"]["title"] {
		t.Fatalf("mentor next step: %v", ns)
	}
	if p["current_period"] != "week_2" {
		t.Fatalf("period follows the intern: %v", p["current_period"])
	}

	// explicit intern_id of the own mentee works, foreign one is 403
	if code, p := e.getPlan(oleg, "?intern_id=intern.anna"); code != 200 || p["intern_id"] != "intern.anna" {
		t.Fatalf("intern_id: %d", code)
	}
	if code, _ := e.getPlan(oleg, "?intern_id=intern.ivan"); code != 403 {
		t.Fatalf("foreign intern_id: %d", code)
	}
	// user_id: intern plan of the own team
	if code, p := e.getPlan(oleg, "?user_id=intern.anna"); code != 200 || p["plan_role"] != "intern" || p["user_id"] != "intern.anna" {
		t.Fatalf("mentor user_id: %d %v", code, p)
	}
	for _, q := range []string{"?user_id=intern.ivan", "?user_id=lead.maria", "?user_id=ghost", "?user_id=mentor.oleg"} {
		if code, _ := e.getPlan(oleg, q); code != 403 {
			t.Errorf("mentor %s: %d", q, code)
		}
	}
	// a mentor without mentees
	e.exec(`INSERT INTO core.users (id, display_name, role, team_id, password_hash) VALUES ('mentor.new','Новый','mentor','delivery','x')`)
	if code, _ := e.getPlan(user{"mentor.new", "mentor", "delivery"}, ""); code != 404 {
		t.Fatalf("mentor without plan: %d", code)
	}
}

func TestPlan_ForeignInternForbidden_R71(t *testing.T) {
	e := setup(t)
	cases := []struct {
		name  string
		u     user
		query string
		want  int
	}{
		{"intern own via user_id", anna, "?user_id=intern.anna", 200},
		{"intern reads other intern", anna, "?user_id=intern.ivan", 403},
		{"intern reads mentor plan of someone else", anna, "?intern_id=intern.ivan", 403},
		{"lead own team intern", maria, "?user_id=intern.anna", 200},
		{"lead other team intern", maria, "?user_id=intern.ivan", 403},
		{"lead mentor plan of own intern", maria, "?intern_id=intern.anna", 200},
		{"lead mentor plan of foreign intern", maria, "?intern_id=intern.ivan", 403},
		{"lead without target", maria, "", 400},
		{"lead unknown user", dmitry, "?user_id=ghost", 403},
		{"lead asks lead", maria, "?user_id=lead.maria", 403},
		{"hrbp", elena, "", 403},
		{"hrbp with user_id", elena, "?user_id=intern.anna", 403},
		{"alumni", petr, "", 403},
		{"anonymous", user{}, "", 401},
	}
	for _, c := range cases {
		if code, _ := e.getPlan(c.u, c.query); code != c.want {
			t.Errorf("%s: %d, want %d", c.name, code, c.want)
		}
	}
	// the 403 body is the same for existing and unknown ids
	_, b1 := e.do(anna, "GET", "/api/plan?user_id=intern.ivan", "")
	_, b2 := e.do(anna, "GET", "/api/plan?user_id=nobody", "")
	if !bytes.Equal(b1, b2) {
		t.Errorf("403 bodies differ: %s vs %s", b1, b2)
	}
	if code, p := e.getPlan(maria, "?intern_id=intern.anna"); code != 200 || p["plan_role"] != "mentor" {
		t.Errorf("lead mentor plan: %v", p)
	}
}

func TestPatchTask_Status_R20_R76(t *testing.T) {
	e := setup(t)
	patch := func(u user, id, status string) (int, map[string]any) {
		code, b := e.do(u, "PATCH", "/api/plan/tasks/"+id, fmt.Sprintf(`{"status":%q}`, status))
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return code, m
	}
	cl07 := e.taskID("intern.anna", "intern", "CL-07")
	code, m := patch(anna, cl07, "done")
	if code != 200 || m["status"] != "done" || m["id"] != cl07 {
		t.Fatalf("done: %d %v", code, m)
	}
	if _, ok := m["subtasks"].([]any); !ok {
		t.Fatalf("top-level answer must carry subtasks: %v", m)
	}
	if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE name='plan.task_done' AND user_id='intern.anna' AND props->>'checklist_item_id'='CL-07' AND props->>'on_time'='true' AND props->>'period'='week_2'`); n != 1 {
		t.Fatalf("task_done on time: %d", n)
	}
	// repeated done does not emit a second event; reopening works
	patch(anna, cl07, "done")
	if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE name='plan.task_done'`); n != 1 {
		t.Fatalf("duplicate events: %d", n)
	}
	if code, m = patch(anna, cl07, "not_started"); code != 200 || m["status"] != "not_started" {
		t.Fatalf("reopen: %d %v", code, m)
	}
	// late completion
	cl05 := e.taskID("intern.anna", "intern", "CL-05")
	if code, m = patch(anna, cl05, "done"); code != 200 || m["status"] != "done" {
		t.Fatalf("cl05: %d %v", code, m)
	}
	if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE props->>'checklist_item_id'='CL-05' AND props->>'on_time'='false'`); n != 1 {
		t.Fatalf("late task_done: %d", n)
	}
	// a past-due task set back to in_progress is still reported as overdue (rule applies on read)
	if code, m = patch(anna, cl05, "in_progress"); code != 200 || m["status"] != "overdue" {
		t.Fatalf("in_progress while overdue: %d %v", code, m)
	}
	if n := e.count(`SELECT count(*) FROM core.plan_tasks WHERE id=$1 AND status='in_progress'`, cl05); n != 1 {
		t.Fatal("stored status must be what the user chose")
	}
}

func TestPatchTask_Subtasks_R20(t *testing.T) {
	e := setup(t)
	patch := func(u user, id, status string) (int, map[string]any) {
		code, b := e.do(u, "PATCH", "/api/plan/tasks/"+id, fmt.Sprintf(`{"status":%q}`, status))
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return code, m
	}
	cl04 := e.taskID("intern.anna", "intern", "CL-04")
	var sub2, sub3 string
	rows, _ := e.pool.Query(context.Background(), `SELECT id::text FROM core.plan_tasks WHERE parent_task_id=$1 AND status<>'done' ORDER BY position`, cl04)
	rows.Next()
	_ = rows.Scan(&sub2)
	rows.Next()
	_ = rows.Scan(&sub3)
	rows.Close()

	code, m := patch(anna, sub2, "done")
	if code != 200 || m["status"] != "done" || m["parent_task_id"] != cl04 {
		t.Fatalf("sub2: %d %v", code, m)
	}
	if _, has := m["subtasks"]; has {
		t.Fatalf("subtask answer must not nest subtasks: %v", m)
	}
	if n := e.count(`SELECT count(*) FROM core.plan_tasks WHERE id=$1 AND status<>'done'`, cl04); n != 1 {
		t.Fatal("parent must stay open while a subtask is open")
	}
	patch(anna, sub3, "done")
	if n := e.count(`SELECT count(*) FROM core.plan_tasks WHERE id=$1 AND status='done'`, cl04); n != 1 {
		t.Fatal("all subtasks done -> parent done")
	}
	if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE name='plan.task_done' AND props->>'checklist_item_id'='CL-04'`); n != 1 {
		t.Fatalf("parent completion event: %d", n)
	}
	patch(anna, sub3, "in_progress")
	if n := e.count(`SELECT count(*) FROM core.plan_tasks WHERE id=$1 AND status='in_progress'`, cl04); n != 1 {
		t.Fatal("reopened subtask reopens the parent")
	}
	// marking a parent done completes its subtasks
	cl08 := e.taskID("intern.anna", "intern", "CL-08")
	code, m = patch(anna, cl08, "done")
	if code != 200 {
		t.Fatalf("cl08: %d", code)
	}
	for _, s := range m["subtasks"].([]any) {
		if s.(map[string]any)["status"] != "done" {
			t.Fatalf("subtask not completed: %v", s)
		}
	}
}

func TestPatchTask_Rights_R71(t *testing.T) {
	e := setup(t)
	annaTask := e.taskID("intern.anna", "intern", "CL-07")
	ivanTask := e.taskID("intern.ivan", "intern", "CL-04")
	olegTask := e.taskID("mentor.oleg", "mentor", "CL-02")
	e.exec(`INSERT INTO core.plan_tasks (plan_id, source, period, title, status) SELECT id, 'action', 'week_2', 'Отгул', 'waiting_approval' FROM core.plans WHERE user_id='intern.anna' AND plan_role='intern'`)
	actionTask := ""
	_ = e.pool.QueryRow(context.Background(), `SELECT id::text FROM core.plan_tasks WHERE source='action'`).Scan(&actionTask)
	e.setItem("intern.anna", "intern", "CL-05", "waiting_approval")
	waiting := e.taskID("intern.anna", "intern", "CL-05")

	cases := []struct {
		name string
		u    user
		id   string
		body string
		want int
	}{
		{"own task", anna, annaTask, `{"status":"in_progress"}`, 200},
		{"mentor own mentor-plan task", oleg, olegTask, `{"status":"in_progress"}`, 200},
		{"other intern's task", anna, ivanTask, `{"status":"done"}`, 403},
		{"mentor edits intern's task", oleg, annaTask, `{"status":"done"}`, 403},
		{"lead edits intern's task", maria, annaTask, `{"status":"done"}`, 403},
		{"intern edits mentor task", anna, olegTask, `{"status":"done"}`, 403},
		{"unknown id", anna, "00000000-0000-4000-8000-000000000000", `{"status":"done"}`, 403},
		{"malformed id", anna, "nope", `{"status":"done"}`, 404},
		{"system status overdue", anna, annaTask, `{"status":"overdue"}`, 400},
		{"system status waiting_approval", anna, annaTask, `{"status":"waiting_approval"}`, 400},
		{"empty status", anna, annaTask, `{}`, 400},
		{"unknown field", anna, annaTask, `{"status":"done","x":1}`, 400},
		{"action task is system-managed", anna, actionTask, `{"status":"done"}`, 409},
		{"waiting_approval task is system-managed", anna, waiting, `{"status":"done"}`, 409},
		{"anonymous", user{}, annaTask, `{"status":"done"}`, 401},
	}
	for _, c := range cases {
		if code, b := e.do(c.u, "PATCH", "/api/plan/tasks/"+c.id, c.body); code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, code, c.want, b)
		}
	}
	if n := e.count(`SELECT count(*) FROM core.plan_tasks WHERE id=$1 AND status='waiting_approval'`, waiting); n != 1 {
		t.Fatal("waiting task changed")
	}
}

func TestPutGoals_MentorAndLead_R18_R71(t *testing.T) {
	e := setup(t)
	put := func(u user, body string) (int, []byte) { return e.do(u, "PUT", "/api/plan/goals", body) }

	code, b := put(oleg, `{"user_id":"intern.anna","goals":[{"title":"Освоить сервис доставки","criteria":"Первый PR влит"},{"title":"Вести конспект"}]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	var goals []map[string]any
	_ = json.Unmarshal(b, &goals)
	if len(goals) != 2 || goals[0]["title"] != "Освоить сервис доставки" || goals[0]["criteria"] != "Первый PR влит" ||
		goals[0]["created_by"] != "mentor.oleg" || goals[0]["id"] == "" || goals[1]["title"] != "Вести конспект" {
		t.Fatalf("goals: %v", goals)
	}
	if _, has := goals[1]["criteria"]; has {
		t.Fatalf("empty criteria must be omitted: %v", goals[1])
	}
	if n := e.count(`SELECT count(*) FROM core.analytics_events WHERE name='plan.goals_set' AND props->>'by_role'='mentor' AND (props->>'count')::int=2`); n != 1 {
		t.Fatalf("goals_set event: %d", n)
	}
	// visible in the intern's and the mentor's plan
	for _, u := range []user{anna, oleg} {
		_, p := e.getPlan(u, "")
		g := p["goals"].([]any)
		if len(g) != 2 || g[0].(map[string]any)["title"] != "Освоить сервис доставки" {
			t.Fatalf("%s sees goals %v", u.id, g)
		}
	}
	// replace by the lead, then clear
	code, b = put(maria, `{"user_id":"intern.anna","goals":[{"title":"Новая цель"}]}`)
	_ = json.Unmarshal(b, &goals)
	if code != 200 || len(goals) != 1 || goals[0]["created_by"] != "lead.maria" {
		t.Fatalf("lead replace: %d %s", code, b)
	}
	if n := e.count(`SELECT count(*) FROM core.goals`); n != 1 {
		t.Fatalf("goals replaced, rows=%d", n)
	}
	if code, b = put(oleg, `{"user_id":"intern.anna","goals":[]}`); code != 200 || string(b) != "[]" {
		t.Fatalf("clear: %d %s", code, b)
	}

	long := strings.Repeat("я", 301)
	var eleven []string
	for i := 0; i < 11; i++ {
		eleven = append(eleven, `{"title":"g"}`)
	}
	cases := []struct {
		name string
		u    user
		body string
		want int
	}{
		{"mentor of another team", sergey, `{"user_id":"intern.anna","goals":[]}`, 403},
		{"lead of another team", dmitry, `{"user_id":"intern.anna","goals":[]}`, 403},
		{"intern", anna, `{"user_id":"intern.anna","goals":[]}`, 403},
		{"hrbp", elena, `{"user_id":"intern.anna","goals":[]}`, 403},
		{"target is not an intern", oleg, `{"user_id":"lead.maria","goals":[]}`, 403},
		{"unknown target", oleg, `{"user_id":"ghost","goals":[]}`, 403},
		{"missing target", oleg, `{"goals":[]}`, 403},
		{"too many", oleg, `{"user_id":"intern.anna","goals":[` + strings.Join(eleven, ",") + `]}`, 400},
		{"blank title", oleg, `{"user_id":"intern.anna","goals":[{"title":"  "}]}`, 400},
		{"long title", oleg, `{"user_id":"intern.anna","goals":[{"title":"` + long + `"}]}`, 400},
		{"unknown field", oleg, `{"user_id":"intern.anna","goals":[{"title":"x","weight":1}]}`, 400},
		{"anonymous", user{}, `{}`, 401},
	}
	for _, c := range cases {
		if code, b := put(c.u, c.body); code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, code, c.want, b)
		}
	}
	// a team intern without a plan
	e.exec(`INSERT INTO core.users (id, display_name, role, team_id, cohort_start, password_hash) VALUES ('intern.new','Новый','intern','delivery',current_date,'x')`)
	if code, _ := put(oleg, `{"user_id":"intern.new","goals":[]}`); code != 404 {
		t.Errorf("intern without plan: %d", code)
	}
}

func TestTeamInterns_Progress_R08_R11(t *testing.T) {
	e := setup(t)
	e.exec(`INSERT INTO core.actions (user_id, kind, external_id, status, summary, idempotency_key)
		VALUES ('intern.anna','access_request','ACC-1','pending_approval','s', gen_random_uuid()),
		       ('intern.anna','access_request','ACC-2','granted','s', gen_random_uuid()),
		       ('intern.anna','absence_request','ABS-1','pending_approval','s', gen_random_uuid())`)
	list := func(u user) (int, []map[string]any) {
		code, b := e.do(u, "GET", "/api/team/interns", "")
		var out []map[string]any
		_ = json.Unmarshal(b, &out)
		return code, out
	}
	for _, u := range []user{oleg, maria} {
		code, out := list(u)
		if code != 200 || len(out) != 1 {
			t.Fatalf("%s: %d %v", u.id, code, out)
		}
		a := out[0]
		if a["user_id"] != "intern.anna" || a["display_name"] != "Анна Котова" || a["current_period"] != "week_2" ||
			a["done"] != 1.0 || a["total"] != 10.0 || a["overdue"] != 2.0 || a["pending_access_requests"] != 1.0 {
			t.Fatalf("%s sees %v", u.id, a)
		}
	}
	code, out := list(dmitry)
	if code != 200 || len(out) != 1 || out[0]["user_id"] != "intern.ivan" || out[0]["done"] != 0.0 || out[0]["overdue"] != 0.0 ||
		out[0]["pending_access_requests"] != 0.0 || out[0]["current_period"] != "week_1" {
		t.Fatalf("dmitry: %d %v", code, out)
	}
	if code, out = list(sergey); code != 200 || len(out) != 1 || out[0]["user_id"] != "intern.ivan" {
		t.Fatalf("sergey: %d %v", code, out)
	}
	for _, u := range []user{anna, elena, petr} {
		if code, _ := list(u); code != 403 {
			t.Errorf("%s: %d", u.id, code)
		}
	}
	if code, _ := list(user{}); code != 401 {
		t.Errorf("anon: %d", code)
	}
	// a mentor without mentees gets an empty list, not null
	e.exec(`INSERT INTO core.users (id, display_name, role, team_id, password_hash) VALUES ('mentor.new','Новый','mentor','delivery','x')`)
	if code, b := e.do(user{"mentor.new", "mentor", "delivery"}, "GET", "/api/team/interns", ""); code != 200 || string(b) != "[]" {
		t.Errorf("empty team: %d %s", code, b)
	}
}

func TestHandlers_NoClaims(t *testing.T) {
	mux := http.NewServeMux()
	plan.New(nil, nil, nil).Register(mux)
	for _, r := range []*http.Request{
		httptest.NewRequest("GET", "/api/plan", nil),
		httptest.NewRequest("PATCH", "/api/plan/tasks/x", strings.NewReader("{}")),
		httptest.NewRequest("PUT", "/api/plan/goals", strings.NewReader("{}")),
		httptest.NewRequest("GET", "/api/team/interns", nil),
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != 401 {
			t.Errorf("%s %s: %d", r.Method, r.URL.Path, rec.Code)
		}
	}
}
