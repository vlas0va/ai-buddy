package analytics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
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

func TestSanitizeProps_NoTextOrNames_R72(t *testing.T) {
	long := strings.Repeat("x", 101)
	cases := []struct {
		name  string
		in    map[string]any
		wantK []string
	}{
		{"chat.answered", map[string]any{"session_id": "s", "message_text": "secret", "latency_ms": 12, "provider": "stub", "nested": map[string]any{"a": 1}}, []string{"session_id", "latency_ms", "provider"}},
		{"plan.task_done", map[string]any{"checklist_item_id": "CL-05", "on_time": true, "user_name": "Анна"}, []string{"checklist_item_id", "on_time"}},
		{"chat.refused", map[string]any{"reason": long}, nil},
		{"ui.task_toggle", map[string]any{"to_status": "done", "latency_ms": 3.5, "list": []int{1}, "n": json.Number("4")}, []string{"to_status", "latency_ms"}},
		{"ui.something_new", map[string]any{"page": "plan", "text": "hello", "latency_ms": json.Number("7")}, []string{"page", "latency_ms"}},
		{"plan.goals_set", nil, nil},
	}
	for _, c := range cases {
		got := analytics.SanitizeProps(c.name, c.in)
		if len(got) != len(c.wantK) {
			t.Errorf("%s: got %v, want keys %v", c.name, got, c.wantK)
			continue
		}
		for _, k := range c.wantK {
			if _, ok := got[k]; !ok {
				t.Errorf("%s: missing %s in %v", c.name, k, got)
			}
		}
	}
}

func TestEmit_WithoutKafka_R24(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	e := analytics.NewEmitter(false)
	ts := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	err := e.Emit(ctx, pool, analytics.Event{Name: "plan.task_done", UserID: "intern.anna", Role: "intern", TeamID: "delivery",
		Props: map[string]any{"checklist_item_id": "CL-05", "period": "week_1", "on_time": false, "free_text": "no"}, TS: ts})
	if err != nil {
		t.Fatal(err)
	}
	var props string
	var gotTS time.Time
	if err := pool.QueryRow(ctx, `SELECT props::text, ts FROM core.analytics_events WHERE name='plan.task_done'`).Scan(&props, &gotTS); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(props, "free_text") || !strings.Contains(props, `"CL-05"`) || !gotTS.Equal(ts) {
		t.Fatalf("props=%s ts=%v", props, gotTS)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.outbox`); n != 0 {
		t.Fatalf("outbox rows without Kafka: %d", n)
	}
	// system event: NULL user, now() timestamp
	if err := e.Emit(ctx, pool, analytics.Event{Name: "notification.created"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.analytics_events WHERE name='notification.created' AND user_id IS NULL`); n != 1 {
		t.Fatalf("system event rows: %d", n)
	}
	for _, bad := range []string{"", "NoDots", "Bad.Name", "x..y", "a.b c"} {
		if err := e.Emit(ctx, pool, analytics.Event{Name: bad}); !errors.Is(err, analytics.ErrInvalid) {
			t.Errorf("%q: err=%v", bad, err)
		}
	}
	var nilE *analytics.Emitter
	if err := nilE.Emit(ctx, pool, analytics.Event{Name: "x.y"}); err != nil {
		t.Fatal(err)
	}
}

func TestEmit_KafkaOutboxSameTx_R62(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	e := analytics.NewEmitter(true)
	tx, _ := pool.Begin(ctx)
	if err := e.Emit(ctx, tx, analytics.Event{Name: "action.created", UserID: "intern.anna", Role: "intern", TeamID: "delivery", Props: map[string]any{"kind": "absence_request"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.Emit(ctx, tx, analytics.Event{Name: "ui.page_view", UserID: "intern.anna", Props: map[string]any{"page": "plan"}}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if n := count(t, pool, `SELECT (SELECT count(*) FROM core.outbox) + (SELECT count(*) FROM core.analytics_events)`); n != 0 {
		t.Fatalf("rolled back tx left %d rows", n)
	}
	// with the pool the pair is still written by one statement
	if err := e.Emit(ctx, pool, analytics.Event{Name: "action.created", UserID: "intern.anna", Role: "intern", TeamID: "delivery"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Emit(ctx, pool, analytics.Event{Name: "ui.page_view", UserID: "intern.anna", Props: map[string]any{"page": "plan"}}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.outbox WHERE topic='buddy.domain' AND payload->>'name'='action.created' AND payload->>'role'='intern'`); n != 1 {
		t.Fatalf("domain outbox rows: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.outbox o JOIN core.analytics_events a ON a.event_id = (o.payload->>'event_id')::uuid WHERE o.topic='buddy.clickstream' AND a.name='ui.page_view'`); n != 1 {
		t.Fatalf("clickstream outbox rows: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.outbox WHERE payload->>'team_id' IS NULL AND payload->'props'->>'page'='plan'`); n != 1 {
		t.Fatalf("null team envelope: %d", n)
	}
}

type fakeProducer struct {
	mu     sync.Mutex
	sent   []string
	failAt int // 1-based index of the message that fails; 0 = never
}

func (f *fakeProducer) Produce(_ context.Context, topic, key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAt > 0 && len(f.sent)+1 == f.failAt {
		return errors.New("broker down")
	}
	f.sent = append(f.sent, topic+"|"+key+"|"+string(value))
	return nil
}

func TestPublisher_DrainOutbox_R62(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	e := analytics.NewEmitter(true)
	for i := 0; i < 3; i++ {
		if err := e.Emit(ctx, pool, analytics.Event{Name: "chat.message_sent", UserID: "intern.anna", Props: map[string]any{"session_id": "s"}}); err != nil {
			t.Fatal(err)
		}
	}
	fp := &fakeProducer{failAt: 3}
	p := analytics.NewPublisher(pool, fp, nil)
	n, err := p.DrainOnce(ctx)
	if err == nil || n != 2 {
		t.Fatalf("first drain: n=%d err=%v", n, err)
	}
	if c := count(t, pool, `SELECT count(*) FROM core.outbox WHERE published_at IS NOT NULL`); c != 2 {
		t.Fatalf("published rows %d", c)
	}
	fp.failAt = 0
	n, err = p.DrainOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("second drain: n=%d err=%v", n, err)
	}
	n, err = p.DrainOnce(ctx)
	if err != nil || n != 0 {
		t.Fatalf("empty drain: n=%d err=%v", n, err)
	}
	if len(fp.sent) != 3 || !strings.HasPrefix(fp.sent[0], "buddy.domain|intern.anna|") {
		t.Fatalf("sent: %v", fp.sent)
	}
}

func TestPublisher_RunLoop_R62(t *testing.T) {
	pool := pgtest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := analytics.NewEmitter(true)
	_ = e.Emit(ctx, pool, analytics.Event{Name: "ui.page_view", Props: map[string]any{"page": "chat"}})
	fp := &fakeProducer{}
	p := analytics.NewPublisher(pool, fp, nil)
	p.Interval = 20 * time.Millisecond
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for count(t, pool, `SELECT count(*) FROM core.outbox WHERE published_at IS NULL`) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("outbox not drained by Run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// a failing producer is logged, not fatal
	fp.failAt = 1
	_ = e.Emit(ctx, pool, analytics.Event{Name: "ui.page_view"})
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done
	p2 := analytics.NewPublisher(pool, fp, nil)
	p2.Interval = 0
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel2()
	p2.Run(ctx2)
}

func TestNewKafkaProducer_R62(t *testing.T) {
	if _, err := analytics.NewKafkaProducer(nil); err == nil {
		t.Fatal("expected error without brokers")
	}
	kp, err := analytics.NewKafkaProducer([]string{"127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer kp.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := kp.Produce(ctx, analytics.TopicDomain, "k", []byte("{}")); err == nil {
		t.Fatal("expected produce error with unreachable broker")
	}
}

type env struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
}

func setup(t *testing.T, kafka bool) env {
	t.Helper()
	pool := pgtest.New(t)
	mux := http.NewServeMux()
	analytics.New(pool, analytics.NewEmitter(kafka), nil).Register(mux)
	srv := httptest.NewServer(authn.Middleware(secret)(mux))
	t.Cleanup(srv.Close)
	return env{srv, pool}
}

func (e env) post(t *testing.T, path, user, role, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+path, bytes.NewReader([]byte(body)))
	if user != "" {
		tok, _ := authn.Issue(secret, authn.Claims{Sub: user, Role: role, TeamID: "delivery"}, time.Hour)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestEvents_Clickstream_R62(t *testing.T) {
	e := setup(t, true)
	now := time.Now().UTC().Format(time.RFC3339)
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	code, body := e.post(t, "/api/events", "intern.anna", "intern", `{"events":[
		{"name":"ui.page_view","ts":"`+now+`","props":{"page":"plan","note":"leak"}},
		{"name":"ui.task_toggle","ts":"`+future+`","props":{"checklist_item_id":"CL-05","to_status":"done","latency_ms":42}}]}`)
	if code != 202 {
		t.Fatalf("%d %s", code, body)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM core.analytics_events WHERE user_id='intern.anna' AND role='intern' AND team_id='delivery' AND name LIKE 'ui.%' AND NOT props ? 'note' AND ts < now() + interval '1 minute'`); n != 2 {
		t.Fatalf("events stored: %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM core.outbox WHERE topic='buddy.clickstream'`); n != 2 {
		t.Fatalf("outbox: %d", n)
	}

	var many []string
	for i := 0; i < 101; i++ {
		many = append(many, `{"name":"ui.page_view","ts":"`+now+`"}`)
	}
	bad := map[string]string{
		"too many":     `{"events":[` + strings.Join(many, ",") + `]}`,
		"domain name":  `{"events":[{"name":"action.created","ts":"` + now + `"}]}`,
		"bad ts":       `{"events":[{"name":"ui.page_view","ts":"yesterday"}]}`,
		"unknown prop": `{"events":[],"x":1}`,
		"empty":        ``,
	}
	for name, b := range bad {
		if code, _ := e.post(t, "/api/events", "intern.anna", "intern", b); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := e.post(t, "/api/events", "", "", `{"events":[]}`); code != 401 {
		t.Errorf("anon: %d", code)
	}
	if code, _ := e.post(t, "/api/events", "intern.anna", "intern", `{"events":[]}`); code != 202 {
		t.Errorf("empty batch: %d", code)
	}
}

func TestFeedback_OwnAssistantMessagesOnly_R24_R71(t *testing.T) {
	e := setup(t, false)
	ctx := context.Background()
	var sid, mid, umid string
	if err := e.pool.QueryRow(ctx, `INSERT INTO core.chat_sessions (user_id) VALUES ('intern.anna') RETURNING id::text`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO core.chat_messages (session_id, author, text) VALUES ($1,'assistant','ответ') RETURNING id::text`, sid).Scan(&mid); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO core.chat_messages (session_id, author, text) VALUES ($1,'user','вопрос') RETURNING id::text`, sid).Scan(&umid); err != nil {
		t.Fatal(err)
	}
	if code, b := e.post(t, "/api/feedback", "intern.anna", "intern", `{"message_id":"`+mid+`","useful":true,"comment":" ok "}`); code != 204 {
		t.Fatalf("%d %s", code, b)
	}
	// second vote overwrites
	if code, _ := e.post(t, "/api/feedback", "intern.anna", "intern", `{"message_id":"`+mid+`","useful":false}`); code != 204 {
		t.Fatalf("revote %d", code)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM core.feedback WHERE message_id=$1 AND useful=false AND comment IS NULL`, mid); n != 1 {
		t.Fatalf("feedback rows: %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM core.analytics_events WHERE name='chat.feedback' AND props->>'message_id'=$1`, mid); n != 2 {
		t.Fatalf("events: %d", n)
	}
	// foreign user, user message and unknown id all look the same
	for name, c := range map[string][2]string{
		"foreign":      {"intern.ivan", mid},
		"own user msg": {"intern.anna", umid},
		"unknown":      {"intern.anna", "00000000-0000-4000-8000-000000000000"},
	} {
		if code, _ := e.post(t, "/api/feedback", c[0], "intern", `{"message_id":"`+c[1]+`","useful":true}`); code != 403 {
			t.Errorf("%s: %d", name, code)
		}
	}
	for name, b := range map[string]string{
		"no useful":    `{"message_id":"` + mid + `"}`,
		"bad id":       `{"message_id":"zzz","useful":true}`,
		"long comment": `{"message_id":"` + mid + `","useful":true,"comment":"` + strings.Repeat("я", 1001) + `"}`,
	} {
		if code, _ := e.post(t, "/api/feedback", "intern.anna", "intern", b); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := e.post(t, "/api/feedback", "", "", `{}`); code != 401 {
		t.Errorf("anon: %d", code)
	}
}

func TestHandler_NoClaims(t *testing.T) {
	mux := http.NewServeMux()
	analytics.New(nil, nil, nil).Register(mux)
	for _, p := range []string{"/api/events", "/api/feedback"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", p, strings.NewReader("{}")))
		if rec.Code != 401 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}
