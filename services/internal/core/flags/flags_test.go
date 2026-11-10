package flags_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/flags"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/pgtest"
)

var secret = []byte("test-secret")

type env struct {
	pool *pgxpool.Pool
	mr   *miniredis.Miniredis
	svc  *flags.Service
	h    *flags.Handler
	srv  *httptest.Server
}

func setup(t *testing.T, demo bool) env {
	t.Helper()
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { rdb.Close() })
	svc := flags.NewService(pool, rdb, analytics.NewEmitter(false), nil)
	h := flags.New(svc, nil)
	h.Demo = demo
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(authn.Middleware(secret)(mux))
	t.Cleanup(srv.Close)
	return env{pool, mr, svc, h, srv}
}

func (e env) call(t *testing.T, method, path, user, role, team, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader([]byte(body)))
	if user != "" {
		tok, _ := authn.Issue(secret, authn.Claims{Sub: user, Role: role, TeamID: team}, time.Hour)
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

func TestGetFlags_Defaults_R55(t *testing.T) {
	e := setup(t, false)
	code, body := e.call(t, "GET", "/api/flags", "intern.anna", "intern", "delivery", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var got map[string]bool
	_ = json.Unmarshal([]byte(body), &got)
	if len(got) != 6 || !got["scenario_s3"] || got["escalation_to_kb"] {
		t.Fatalf("flags: %v", got)
	}
	if code, _ := e.call(t, "GET", "/api/flags", "", "", "", ""); code != 401 {
		t.Fatalf("anon: %d", code)
	}
}

func TestPutFlags_NotDemoIs404_R55(t *testing.T) {
	e := setup(t, false)
	if code, _ := e.call(t, "PUT", "/api/flags/escalation_to_kb", "lead.maria", "lead", "delivery", `{"enabled":true}`); code != 404 {
		t.Fatalf("non-demo build: %d", code)
	}
}

func TestPutFlags_DemoLeadOnlyAndTargeting_R55(t *testing.T) {
	e := setup(t, true)
	ctx := context.Background()
	put := func(user, role, team, key, body string) (int, string) {
		return e.call(t, "PUT", "/api/flags/"+key, user, role, team, body)
	}
	if code, _ := put("mentor.oleg", "mentor", "delivery", "escalation_to_kb", `{"enabled":true}`); code != 403 {
		t.Fatalf("mentor: %d", code)
	}
	if code, _ := put("", "", "", "escalation_to_kb", `{"enabled":true}`); code != 401 {
		t.Fatalf("anon: %d", code)
	}
	for name, c := range map[string][2]string{
		"unknown key": {"nope", `{"enabled":true}`},
		"no enabled":  {"escalation_to_kb", `{}`},
		"bad team":    {"escalation_to_kb", `{"enabled":true,"teams":["Bad Team"]}`},
		"extra field": {"escalation_to_kb", `{"enabled":true,"x":1}`},
	} {
		if code, _ := put("lead.maria", "lead", "delivery", c[0], c[1]); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	// warm the cache, then change: the new value must be visible immediately (cache invalidated)
	if !e.svc.Enabled(ctx, "scenario_s1", "delivery") || e.svc.Enabled(ctx, "escalation_to_kb", "delivery") {
		t.Fatal("unexpected defaults")
	}
	code, body := put("lead.maria", "lead", "delivery", "escalation_to_kb", `{"enabled":true,"teams":["delivery"]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var got map[string]bool
	_ = json.Unmarshal([]byte(body), &got)
	if !got["escalation_to_kb"] {
		t.Fatalf("response flags: %v", got)
	}
	if !e.svc.Enabled(ctx, "escalation_to_kb", "delivery") || e.svc.Enabled(ctx, "escalation_to_kb", "search") {
		t.Fatal("per-team targeting not applied")
	}
	_, body = e.call(t, "GET", "/api/flags", "intern.ivan", "intern", "search", "")
	_ = json.Unmarshal([]byte(body), &got)
	if got["escalation_to_kb"] {
		t.Fatalf("search team must not see the flag: %v", got)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.analytics_events WHERE name='flag.changed' AND props->>'key'='escalation_to_kb' AND (props->>'teams_count')::int = 1 AND user_id='lead.maria'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("flag.changed events: %d %v", n, err)
	}
	// disabling for everyone
	if code, _ := put("lead.maria", "lead", "delivery", "escalation_to_kb", `{"enabled":false}`); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	if e.svc.Enabled(ctx, "escalation_to_kb", "delivery") {
		t.Fatal("flag must be off")
	}
}

func TestFlags_CacheFailOpenAndTTL_R55(t *testing.T) {
	e := setup(t, false)
	ctx := context.Background()
	if _, err := e.svc.All(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.mr.Exists("flags:v1") || e.mr.TTL("flags:v1") != flags.CacheTTL {
		t.Fatalf("cache entry missing or wrong ttl: %v", e.mr.TTL("flags:v1"))
	}
	// the cache wins while fresh
	if _, err := e.pool.Exec(ctx, `UPDATE core.feature_flags SET enabled=false WHERE key='scenario_s1'`); err != nil {
		t.Fatal(err)
	}
	if !e.svc.Enabled(ctx, "scenario_s1", "delivery") {
		t.Fatal("expected cached value")
	}
	// after expiry the database is read again
	e.mr.FastForward(31 * time.Second)
	if e.svc.Enabled(ctx, "scenario_s1", "delivery") {
		t.Fatal("expected fresh value after TTL")
	}
	// garbage in the cache falls back to the database
	_ = e.mr.Set("flags:v1", "not json")
	if e.svc.Enabled(ctx, "scenario_s1", "delivery") {
		t.Fatal("garbage cache must fall back to DB")
	}
	// Redis down: fail-open to the database
	e.mr.Close()
	m, err := e.svc.ForTeam(ctx, "delivery")
	if err != nil || m["scenario_s2"] != true {
		t.Fatalf("redis down: %v %v", m, err)
	}
	if e.svc.Enabled(ctx, "no_such_flag", "delivery") {
		t.Fatal("unknown flag must be off")
	}
}

func TestFlags_NoRedisAndDBDown(t *testing.T) {
	pool := pgtest.New(t)
	svc := flags.NewService(pool, nil, nil, nil)
	if !svc.Enabled(context.Background(), "scenario_s3", "x") {
		t.Fatal("no cache: expected DB value")
	}
	if err := svc.Set(context.Background(), authn.Claims{Sub: "lead.maria", Role: "lead"}, "scenario_s3", false, nil); err != nil {
		t.Fatal(err)
	}
	if svc.Enabled(context.Background(), "scenario_s3", "x") {
		t.Fatal("expected off")
	}
	pool.Close()
	if svc.Enabled(context.Background(), "scenario_s3", "x") {
		t.Fatal("DB error must count as off")
	}
	if _, err := svc.ForTeam(context.Background(), "x"); err == nil {
		t.Fatal("expected error")
	}
	if err := svc.Set(context.Background(), authn.Claims{}, "scenario_s3", true, nil); err == nil {
		t.Fatal("expected error on closed pool")
	}
	h := flags.New(svc, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/flags", nil)
	mux.ServeHTTP(rec, req.WithContext(authn.NewContext(req.Context(), authn.Claims{Sub: "a", TeamID: "t"})))
	if rec.Code != 500 {
		t.Fatalf("db down: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/flags", nil))
	if rec.Code != 401 {
		t.Fatalf("no claims: %d", rec.Code)
	}
}
