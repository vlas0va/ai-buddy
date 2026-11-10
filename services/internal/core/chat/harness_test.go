package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/actions"
	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/agentclient/agenttest"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/approvals"
	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/core/mcpclient/mcptest"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/pgtest"
)

var (
	jwtSecret  = []byte("test-jwt-secret-0123456789abcdef")
	hmacSecret = []byte("test-hmac-secret-0123456789abcdef")
)

type stubLimiter struct {
	allow bool
	err   error
	keys  []string
}

func (l *stubLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, error) {
	l.keys = append(l.keys, key)
	return l.allow, l.err
}

type stubFlags struct {
	flags map[string]bool
	err   error
}

func (f stubFlags) ForTeam(context.Context, string) (map[string]bool, error) { return f.flags, f.err }

// env is a core API (chat + actions + approvals behind authn) over a fresh database, fake MCP servers and a fake agent.
type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	mcp     *mcptest.Server
	agent   *agenttest.Agent
	chat    *Handler
	limiter *stubLimiter
	url     string
}

type envOpt func(*Deps, *Config)

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	pool := pgtest.New(t)
	fake := mcptest.New(t, jwtSecret, hmacSecret)
	ag := agenttest.New(t, fake.URL)
	mcp, err := mcpclient.New(mcpclient.Config{BaseURL: fake.URL, JWTSecret: jwtSecret, ConfirmationSecret: hmacSecret})
	if err != nil {
		t.Fatal(err)
	}
	emitter := analytics.NewEmitter(false)
	acts := actions.NewService(emitter, notify.Store{}, nil)
	lim := &stubLimiter{allow: true}
	d := Deps{Pool: pool, Agent: agentclient.New(ag.URL, 2*time.Second), Actions: acts, Limiter: lim,
		Flags: stubFlags{flags: map[string]bool{"scenario_s3": true}}, Emitter: emitter, MCP: mcp}
	cfg := Config{ConfirmationSecret: hmacSecret}
	for _, o := range opts {
		o(&d, &cfg)
	}
	h := New(d, cfg)
	mux := http.NewServeMux()
	h.Register(mux)
	actions.New(pool, acts, nil).Register(mux)
	approvals.New(pool, mcp, acts, emitter, nil).Register(mux)
	srv := httptest.NewServer(httpx.RequestID(authn.Middleware(jwtSecret)(mux)))
	t.Cleanup(srv.Close)
	return &env{t: t, pool: pool, mcp: fake, agent: ag, chat: h, limiter: lim, url: srv.URL}
}

// Demo users (migrations/0003_seed_demo.sql). Anna's cohort started 10 days ago (week_2).
func (e *env) anna() string {
	return agenttest.Token(e.t, jwtSecret, "intern.anna", "intern", "delivery", time.Now().AddDate(0, 0, -10).Format(time.DateOnly))
}
func (e *env) ivan() string {
	return agenttest.Token(e.t, jwtSecret, "intern.ivan", "intern", "search", time.Now().AddDate(0, 0, -3).Format(time.DateOnly))
}
func (e *env) oleg() string {
	return agenttest.Token(e.t, jwtSecret, "mentor.oleg", "mentor", "delivery", "")
}
func (e *env) maria() string {
	return agenttest.Token(e.t, jwtSecret, "lead.maria", "lead", "delivery", "")
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.body, dst); err != nil {
		t.Fatalf("decode %d %s: %v", r.status, r.body, err)
	}
}

func (r response) code(t *testing.T) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	r.json(t, &e)
	return e.Code
}

func (e *env) do(method, path, token string, body any, headers ...string) response {
	e.t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

// sse is a parsed event stream.
type sse struct {
	events []httpx.Event
}

func (s sse) names() string {
	var n []string
	for _, e := range s.events {
		n = append(n, e.Name)
	}
	return strings.Join(n, ",")
}

func (s sse) find(name string) map[string]any {
	for _, e := range s.events {
		if e.Name == name {
			var m map[string]any
			_ = json.Unmarshal(e.Data, &m)
			return m
		}
	}
	return nil
}

func (s sse) last() map[string]any {
	if len(s.events) == 0 {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(s.events[len(s.events)-1].Data, &m)
	return m
}

func (e *env) stream(path, token string, body any) sse {
	e.t.Helper()
	r := e.do(http.MethodPost, path, token, body)
	if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") {
		e.t.Fatalf("POST %s: %d %s", path, r.status, r.body)
	}
	rd := httpx.NewSSEReader(strings.NewReader(string(r.body)))
	var out sse
	for {
		ev, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			e.t.Fatalf("parse stream: %v", err)
		}
		out.events = append(out.events, ev)
	}
	for i, ev := range out.events {
		if ev.ID != itoa(i+1) {
			e.t.Fatalf("event %d has id %q: ids must increase from 1", i, ev.ID)
		}
	}
	return out
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func (e *env) session(token string) string {
	e.t.Helper()
	r := e.do(http.MethodPost, "/api/chat/sessions", token, nil)
	var out struct {
		SessionID string `json:"session_id"`
	}
	r.json(e.t, &out)
	if r.status != http.StatusCreated || out.SessionID == "" {
		e.t.Fatalf("create session: %d %s", r.status, r.body)
	}
	return out.SessionID
}

func (e *env) message(sid, token, text string) sse {
	e.t.Helper()
	return e.stream("/api/chat/sessions/"+sid+"/messages", token, map[string]string{"text": text})
}

// propose sends a day-off message and returns the stored proposal id.
func (e *env) propose(sid, token string) string {
	e.t.Helper()
	s := e.message(sid, token, "Хочу отгул в пятницу")
	p := s.find("action_proposal")
	if p == nil {
		e.t.Fatalf("no proposal: %s", s.names())
	}
	return p["proposal_id"].(string)
}

func (e *env) confirmPath(sid, pid string) string {
	return "/api/chat/sessions/" + sid + "/proposals/" + pid + "/confirm"
}

func (e *env) cancelPath(sid, pid string) string {
	return "/api/chat/sessions/" + sid + "/proposals/" + pid + "/cancel"
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) str(sql string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) proposalStatus(pid string) string {
	return e.str(`SELECT status FROM core.proposals WHERE id = $1`, pid)
}
