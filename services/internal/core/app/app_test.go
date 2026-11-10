package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"aibuddy/services/internal/core/agentclient/agenttest"
	"aibuddy/services/internal/core/mcpclient/mcptest"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/pgtest"
)

const (
	jwtSecret  = "test-jwt-secret-0123456789abcdef"
	hmacSecret = "test-hmac-secret-0123456789abcdef"
)

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfig_DefaultsAndOverrides_R80(t *testing.T) {
	cfg := LoadConfig(getenv(map[string]string{"JWT_SECRET": "a", "CONFIRMATION_HMAC_SECRET": "b"}))
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 || cfg.AgentURL != "http://agent:8090" || cfg.MCPBaseURL != "http://mcp-mocks:8100/mcp" ||
		cfg.LLMTimeout != 30*time.Second || cfg.ChatRatePerMin != 20 || cfg.JWTTTL != 12*time.Hour || cfg.CronInterval != 24*time.Hour ||
		len(cfg.KafkaBrokers) != 0 || len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "http://localhost:5173" || cfg.RedisURL == "" {
		t.Fatalf("defaults %+v", cfg)
	}
	cfg = LoadConfig(getenv(map[string]string{"JWT_SECRET": "a", "CONFIRMATION_HMAC_SECRET": "b", "CORE_PORT": "9090",
		"LLM_TIMEOUT_S": "45", "KAFKA_BROKERS": "k1:9092,k2:9092", "CORS_ORIGINS": "http://a,http://b", "AGENT_SERVICE_TOKEN": "svc",
		"CHAT_RATE_PER_MIN": "5", "MCP_BASE_URL": "http://localhost:8100/mcp"}))
	if cfg.Port != 9090 || cfg.LLMTimeout != 45*time.Second || len(cfg.KafkaBrokers) != 2 || len(cfg.CORSOrigins) != 2 ||
		cfg.AgentServiceToken != "svc" || cfg.ChatRatePerMin != 5 || cfg.MCPBaseURL != "http://localhost:8100/mcp" {
		t.Fatalf("overrides %+v", cfg)
	}
}

func TestConfig_Validate_RefusesEmptySecrets(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no secrets":   {},
		"bad port":     {"JWT_SECRET": "a", "CONFIRMATION_HMAC_SECRET": "b", "CORE_PORT": "abc"},
		"port range":   {"JWT_SECRET": "a", "CONFIRMATION_HMAC_SECRET": "b", "CORE_PORT": "70000"},
		"bad duration": {"JWT_SECRET": "a", "CONFIRMATION_HMAC_SECRET": "b", "LLM_TIMEOUT_S": "soon"},
	} {
		cfg := LoadConfig(getenv(env))
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
		if _, err := New(cfg, nil, nil, nil); err == nil {
			t.Errorf("%s: New must refuse", name)
		}
	}
}

type stack struct {
	t     *testing.T
	url   string
	agent *agenttest.Agent
	mcp   *mcptest.Server
}

func newStack(t *testing.T, agentURL func(*agenttest.Agent) string) *stack {
	t.Helper()
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fake := mcptest.New(t, []byte(jwtSecret), []byte(hmacSecret))
	ag := agenttest.New(t, fake.URL)
	cfg := LoadConfig(getenv(map[string]string{"JWT_SECRET": jwtSecret, "CONFIRMATION_HMAC_SECRET": hmacSecret,
		"MCP_BASE_URL": fake.URL, "AGENT_URL": agentURL(ag), "LLM_TIMEOUT_S": "2"}))
	a, err := New(cfg, pool, rdb, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return &stack{t: t, url: srv.URL, agent: ag, mcp: fake}
}

func (s *stack) do(method, path, token, body string, headers ...string) (int, http.Header, []byte) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.url+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func (s *stack) login(user string) string {
	s.t.Helper()
	status, _, body := s.do(http.MethodPost, "/api/auth/login", "", `{"login":"`+user+`","password":"demo"}`)
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &out)
	if status != http.StatusOK || out.Token == "" {
		s.t.Fatalf("login %s: %d %s", user, status, body)
	}
	return out.Token
}

func events(t *testing.T, body []byte) []httpx.Event {
	t.Helper()
	rd := httpx.NewSSEReader(strings.NewReader(string(body)))
	var out []httpx.Event
	for {
		ev, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
}

func find(evs []httpx.Event, name string) map[string]any {
	for _, e := range evs {
		if e.Name == name {
			var m map[string]any
			_ = json.Unmarshal(e.Data, &m)
			return m
		}
	}
	return nil
}

// The main scenario through the real wiring: login → chat → proposal → confirm → action saved → visible after reload.
func TestApp_S3_DayOffThroughRealWiring_R66(t *testing.T) {
	s := newStack(t, func(a *agenttest.Agent) string { return a.URL })
	if status, _, body := s.do(http.MethodGet, "/healthz", "", ""); status != http.StatusOK || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("healthz %d %s", status, body)
	}
	if status, _, _ := s.do(http.MethodGet, "/api/actions", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", status)
	}
	status, h, _ := s.do(http.MethodOptions, "/api/chat/sessions", "", "", "Origin", "http://localhost:5173", "Access-Control-Request-Method", "POST")
	if status != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("CORS preflight %d %v", status, h)
	}
	anna := s.login("intern.anna")
	for _, path := range []string{"/api/me", "/api/plan", "/api/notifications", "/api/flags", "/api/escalations", "/api/actions"} {
		if status, h, body := s.do(http.MethodGet, path, anna, ""); status != http.StatusOK || h.Get(httpx.HeaderRequestID) == "" {
			t.Fatalf("%s: %d %s", path, status, body)
		}
	}
	if status, _, _ := s.do(http.MethodGet, "/api/approvals", anna, ""); status != http.StatusForbidden {
		t.Fatalf("intern approvals: %d", status)
	}

	_, _, body := s.do(http.MethodPost, "/api/chat/sessions", anna, "")
	var sess struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(body, &sess)
	_, _, body = s.do(http.MethodPost, "/api/chat/sessions/"+sess.SessionID+"/messages", anna, `{"text":"Хочу отгул в пятницу"}`)
	p := find(events(t, body), "action_proposal")
	if p == nil {
		t.Fatalf("no proposal: %s", body)
	}
	_, _, body = s.do(http.MethodPost, "/api/chat/sessions/"+sess.SessionID+"/proposals/"+p["proposal_id"].(string)+"/confirm", anna, "")
	ar := find(events(t, body), "action_result")
	if ar == nil || ar["status"] != "pending_approval" || s.mcp.Calls("absence.create_request") != 1 {
		t.Fatalf("confirm: %s", body)
	}
	_, _, body = s.do(http.MethodGet, "/api/actions", anna, "")
	if !strings.Contains(string(body), ar["action_id"].(string)) {
		t.Fatalf("actions after reload: %s", body)
	}
	if got := s.agent.Last("message").Raw["flags"]; got == nil {
		t.Fatal("flags not sent to the agent")
	}

	maria := s.login("lead.maria")
	_, _, body = s.do(http.MethodGet, "/api/approvals", maria, "")
	var pending []map[string]any
	_ = json.Unmarshal(body, &pending)
	if len(pending) != 1 {
		t.Fatalf("approvals: %s", body)
	}
	status, _, body = s.do(http.MethodPost, "/api/approvals/"+pending[0]["id"].(string)+"/decision", maria, `{"decision":"approved"}`,
		"Idempotency-Key", agenttest.NewID())
	if status != http.StatusOK {
		t.Fatalf("decision: %d %s", status, body)
	}
	if _, _, body = s.do(http.MethodGet, "/api/actions", anna, ""); !strings.Contains(string(body), `"status":"approved"`) {
		t.Fatalf("approved action: %s", body)
	}
}

func TestApp_AgentDown_CoreStillServes_R79(t *testing.T) {
	s := newStack(t, func(*agenttest.Agent) string { return "http://127.0.0.1:1" })
	if status, _, _ := s.do(http.MethodGet, "/healthz", "", ""); status != http.StatusOK {
		t.Fatalf("healthz %d", status)
	}
	anna := s.login("intern.anna")
	_, _, body := s.do(http.MethodPost, "/api/chat/sessions", anna, "")
	var sess struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(body, &sess)
	_, _, body = s.do(http.MethodPost, "/api/chat/sessions/"+sess.SessionID+"/messages", anna, `{"text":"Привет"}`)
	if e := find(events(t, body), "error"); e == nil || e["code"] != "upstream_unavailable" || e["retryable"] != true {
		t.Fatalf("%s", body)
	}
}

func TestHealthz_DatabaseDown_503(t *testing.T) {
	pool := pgtest.New(t)
	cfg := LoadConfig(getenv(map[string]string{"JWT_SECRET": jwtSecret, "CONFIRMATION_HMAC_SECRET": hmacSecret}))
	a, err := New(cfg, pool, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

func TestRun_ServesUntilCancelled_R80(t *testing.T) {
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	port := freePort(t)
	cfg := LoadConfig(getenv(map[string]string{"JWT_SECRET": jwtSecret, "CONFIRMATION_HMAC_SECRET": hmacSecret, "CORE_PORT": port,
		"POSTGRES_URL": pgtest.URL(t, pool), "REDIS_URL": "redis://" + mr.Addr() + "/0", "KAFKA_BROKERS": "127.0.0.1:1",
		"CRON_INTERVAL": "1h"}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, nil) }()
	url := "http://127.0.0.1:" + port + "/healthz"
	deadline := time.Now().Add(10 * time.Second)
	for Healthcheck(url) != nil {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("core did not become healthy: %v", Healthcheck(url))
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRun_Errors(t *testing.T) {
	if err := Run(context.Background(), LoadConfig(getenv(nil)), nil); err == nil {
		t.Fatal("missing secrets must fail")
	}
	pool := pgtest.New(t)
	cfg := LoadConfig(getenv(map[string]string{"JWT_SECRET": jwtSecret, "CONFIRMATION_HMAC_SECRET": hmacSecret,
		"POSTGRES_URL": pgtest.URL(t, pool), "REDIS_URL": "mysql://nope"}))
	if err := Run(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
		t.Fatalf("bad REDIS_URL: %v", err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := serve(context.Background(), busy.Addr().String(), http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("busy port must fail")
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer bad.Close()
	if err := Healthcheck(ok.URL); err != nil {
		t.Fatal(err)
	}
	if err := Healthcheck(bad.URL); err == nil {
		t.Fatal("503 must fail")
	}
	if err := Healthcheck("http://127.0.0.1:1/healthz"); err == nil {
		t.Fatal("unreachable must fail")
	}
}
