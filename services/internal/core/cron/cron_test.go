package cron_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/cron"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/pgtest"
)

var secret = []byte("test-secret")

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestCron_MarksOverdueAndNotifies_R54(t *testing.T) {
	pool := pgtest.New(t)
	r := cron.NewRunner(pool, analytics.NewEmitter(false), 0, nil)
	res, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Plans != 4 {
		t.Fatalf("plans: %+v", res)
	}
	// anna: CL-04 (in_progress, due passed) becomes overdue, CL-05 already was; done CL-03 stays done
	if n := count(t, pool, `SELECT count(*) FROM core.plan_tasks t JOIN core.plans p ON p.id=t.plan_id
		WHERE p.user_id='intern.anna' AND t.parent_task_id IS NULL AND t.checklist_item_id IN ('CL-04','CL-05') AND t.status='overdue'`); n != 2 {
		t.Fatalf("anna overdue tasks: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.plan_tasks t JOIN core.plans p ON p.id=t.plan_id
		WHERE p.user_id='intern.anna' AND t.checklist_item_id='CL-03' AND t.status='done'`); n < 1 {
		t.Fatal("done task was touched")
	}
	// the finished sub-step of CL-04 is not reopened
	if n := count(t, pool, `SELECT count(*) FROM core.plan_tasks WHERE title LIKE 'Попроси добавить себя%' AND status='done'`); n != 1 {
		t.Fatalf("done subtasks: %d", n)
	}
	// overdue notifications: one per overdue top-level task, to the plan owner
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='intern.anna' AND kind='overdue' AND checklist_item_id IN ('CL-04','CL-05')`); n != 2 {
		t.Fatalf("anna overdue notifications: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='mentor.oleg' AND kind='overdue' AND checklist_item_id='CL-01' AND title LIKE 'Просрочено%'`); n != 1 {
		t.Fatalf("mentor overdue notification: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE kind='overdue' AND body LIKE 'Срок был __.__.____%'`); n < 3 {
		t.Fatalf("date format in body")
	}
	// period reminders: anna is in week_2 -> CL-07 open (CL-14 etc are other periods); no reminder for done items
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='intern.anna' AND dedupe_key='period:CL-07:week_2' AND kind='reminder'`); n != 1 {
		t.Fatalf("anna CL-07 reminder: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='intern.anna' AND dedupe_key='period:CL-03:week_1'`); n != 0 {
		t.Fatalf("reminder for another period")
	}
	// ivan is in week_1: reminders for CL-03 (human_only, in progress), CL-04, CL-05
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='intern.ivan' AND kind='reminder' AND dedupe_key LIKE 'period:%:week_1'`); n != 3 {
		t.Fatalf("ivan reminders: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='intern.ivan' AND dedupe_key='period:CL-03:week_1' AND body LIKE '%только напоминает%'`); n != 1 {
		t.Fatalf("human_only text")
	}
	// mentor plans get reminders with the mentor wording
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='mentor.sergey' AND title LIKE 'Напоминание наставнику%'`); n < 1 {
		t.Fatalf("mentor reminder")
	}
	// events
	if n := count(t, pool, `SELECT count(*) FROM core.analytics_events WHERE name='plan.task_overdue'`); n != res.Overdue {
		t.Fatalf("task_overdue events %d vs %d", n, res.Overdue)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.analytics_events WHERE name='notification.created'`); n != res.Overdue+res.Reminders {
		t.Fatalf("notification.created events %d vs %d", n, res.Overdue+res.Reminders)
	}
}

func TestCron_DedupeReminders_R54(t *testing.T) {
	pool := pgtest.New(t)
	r := cron.NewRunner(pool, analytics.NewEmitter(false), time.Hour, nil)
	first, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := count(t, pool, `SELECT count(*) FROM core.notifications`)
	events := count(t, pool, `SELECT count(*) FROM core.analytics_events`)
	for i := 0; i < 2; i++ {
		res, err := r.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Overdue != 0 || res.Reminders != 0 || res.MarkedOverdue != 0 || res.Plans != first.Plans {
			t.Fatalf("repeat run %d: %+v", i, res)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications`); n != before {
		t.Fatalf("notifications grew %d -> %d", before, n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.analytics_events`); n != events {
		t.Fatalf("events grew %d -> %d", events, n)
	}
	// a read notification is not recreated either
	exec(t, pool, `UPDATE core.notifications SET read_at = now()`)
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications`); n != before {
		t.Fatalf("recreated after read: %d", n)
	}
}

func TestCron_NewPeriodAndSurveys_R54(t *testing.T) {
	pool := pgtest.New(t)
	r := cron.NewRunner(pool, nil, 0, nil)
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 15 days later anna (25 days in) enters month_1: CL-08 is announced, once
	r.Now = func() time.Time { return time.Now().AddDate(0, 0, 15) }
	res, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id='intern.anna' AND dedupe_key='period:CL-08:month_1'`); n != 1 {
		t.Fatalf("CL-08 reminder: %d (%+v)", n, res)
	}
	// CL-07 (due week_2 end) is now overdue and announced
	if n := count(t, pool, `SELECT count(*) FROM core.notifications t JOIN core.plan_tasks k ON t.dedupe_key = 'overdue:' || k.id::text WHERE t.user_id='intern.anna' AND k.checklist_item_id='CL-07'`); n != 1 {
		t.Fatalf("CL-07 overdue notification: %d", n)
	}
	// after 3 months: survey texts for CL-15 (intern) and CL-16 (mentor)
	r.Now = func() time.Time { return time.Now().AddDate(0, 0, 120) }
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ user, item string }{{"intern.anna", "CL-15"}, {"mentor.oleg", "CL-16"}} {
		if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id=$1 AND checklist_item_id=$2 AND title='Пройдите опрос о стажировке' AND kind='reminder'`, c.user, c.item); n != 1 {
			t.Errorf("%s survey reminder for %s: %d", c.user, c.item, n)
		}
	}
	// a plan whose intern has no cohort start falls back to week_1 instead of failing
	exec(t, pool, `UPDATE core.users SET cohort_start = NULL WHERE id = 'intern.ivan'`)
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCron_RunLoopAndErrors_R54(t *testing.T) {
	pool := pgtest.New(t)
	r := cron.NewRunner(pool, nil, 20*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for count(t, pool, `SELECT count(*) FROM core.notifications`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Run did not execute the job at start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	// failures are returned by RunOnce and only logged by Run
	pool.Close()
	if _, err := r.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error on closed pool")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	r.Run(ctx2)
}

func TestCron_HTTPDemoOnly_R54(t *testing.T) {
	pool := pgtest.New(t)
	r := cron.NewRunner(pool, nil, 0, nil)
	h := cron.New(r, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(authn.Middleware(secret)(mux))
	defer srv.Close()
	post := func(auth bool) int {
		req, _ := http.NewRequest("POST", srv.URL+"/api/cron/run", strings.NewReader(""))
		if auth {
			tok, _ := authn.Issue(secret, authn.Claims{Sub: "lead.maria", Role: "lead", TeamID: "delivery"}, time.Hour)
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	h.Demo = false
	if c := post(true); c != 404 {
		t.Fatalf("non-demo: %d", c)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications`); n != 0 {
		t.Fatal("job ran in non-demo build")
	}
	h.Demo = true
	if c := post(false); c != 401 {
		t.Fatalf("anon: %d", c)
	}
	h.Sync = true
	if c := post(true); c != 202 {
		t.Fatalf("sync: %d", c)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications`); n == 0 {
		t.Fatal("job did not run")
	}
	// background mode: 202 immediately, job completes shortly after
	exec(t, pool, `DELETE FROM core.notifications`)
	h.Sync = false
	if c := post(true); c != 202 {
		t.Fatalf("async: %d", c)
	}
	deadline := time.Now().Add(5 * time.Second)
	for count(t, pool, `SELECT count(*) FROM core.notifications`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("background job did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// failing job still answers 202
	pool.Close()
	h.Sync = true
	if c := post(true); c != 202 {
		t.Fatalf("failing job: %d", c)
	}
}

func TestCron_DefaultInterval(t *testing.T) {
	if cron.DefaultInterval != 24*time.Hour {
		t.Fatal("CRON_INTERVAL default is 24h")
	}
}

func TestCron_ReportAfterThreeMonths_Idea1211(t *testing.T) {
	pool := pgtest.New(t)
	r := cron.NewRunner(pool, analytics.NewEmitter(false), time.Hour, nil)
	res, err := r.RunOnce(context.Background())
	if err != nil || res.Reports != 0 {
		t.Fatalf("day 10: %+v %v", res, err)
	}
	r.Now = func() time.Time { return time.Now().AddDate(0, 0, 85) } // Анна started 10 days ago: day 95
	res, err = r.RunOnce(context.Background())
	if err != nil || res.Reports < 1 {
		t.Fatalf("day 95: %+v %v", res, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE dedupe_key = 'report:3m:intern.anna'
		AND user_id = 'mentor.oleg' AND title LIKE '%Анна Котова%'`); n != 1 {
		t.Fatalf("report notification for Олег: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE dedupe_key LIKE 'report:%' AND user_id LIKE 'intern.%'`); n != 0 {
		t.Fatal("the report is for the mentor only")
	}
	if res, _ = r.RunOnce(context.Background()); res.Reports != 0 {
		t.Fatalf("report notification repeated: %+v", res)
	}
}
