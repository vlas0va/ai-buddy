package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pgtest"
)

const (
	jwtSecret  = "test-jwt-secret"
	confSecret = "test-confirmation-secret"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfig_DefaultsAndOverrides(t *testing.T) {
	def := LoadConfig(env(nil))
	if def.Port != 8100 || def.KBDir != "/app/data/kb" || !strings.Contains(def.PostgresURL, "postgres:5432") {
		t.Fatalf("defaults: %+v", def)
	}
	got := LoadConfig(env(map[string]string{
		"MCP_MOCKS_PORT": "9100", "POSTGRES_URL": "postgres://x", "JWT_SECRET": "j",
		"CONFIRMATION_HMAC_SECRET": "c", "KB_DIR": "/kb",
	}))
	want := Config{Port: 9100, PostgresURL: "postgres://x", JWTSecret: "j", ConfirmationSecret: "c", KBDir: "/kb"}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestConfig_Validate_R71(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"both missing", Config{}, "JWT_SECRET CONFIRMATION_HMAC_SECRET"},
		{"no confirmation secret", Config{JWTSecret: "j"}, "CONFIRMATION_HMAC_SECRET"},
		{"no jwt secret", Config{ConfirmationSecret: "c"}, "JWT_SECRET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %s", err, tc.want)
			}
		})
	}
	if err := (Config{JWTSecret: "j", ConfirmationSecret: "c"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func validConfig() Config {
	return Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret}
}

func TestNewHandler_HealthAndAllServers_R23(t *testing.T) {
	pool := pgtest.New(t)
	if _, err := NewHandler(Config{}, pool, quiet()); err == nil {
		t.Fatal("empty secrets must be rejected")
	}
	h, err := NewHandler(validConfig(), pool, quiet())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || body["status"] != "ok" || resp.Header.Get("X-Request-Id") == "" {
		t.Fatalf("healthz: %d %v", resp.StatusCode, body)
	}

	token, err := authn.Issue([]byte(jwtSecret), authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	client := mcpx.Client{BaseURL: ts.URL + "/mcp"}
	// по одному инструменту чтения на сервер: каждый сервер смонтирован по /mcp/<server> и отвечает с личностью
	// пользователя
	for _, c := range []struct {
		server, tool string
		args         map[string]any
		wantCode     string // код ошибки инструмента; "" — успех
	}{
		{"absence", "list_my_requests", map[string]any{}, ""},
		{"approvals", "list_pending", map[string]any{}, "forbidden"}, // только руководитель (R71)
		{"access", "list_required", map[string]any{}, ""},
		{"directory", "whoami", map[string]any{}, ""},
		{"checklist", "get_plan_template", map[string]any{"role": "intern"}, ""},
		{"kb", "search", map[string]any{"query": "отгул"}, ""},
	} {
		_, toolErr, err := client.Call(context.Background(), token, c.server, c.tool, c.args)
		if err != nil {
			t.Fatalf("%s.%s: %v", c.server, c.tool, err)
		}
		got := ""
		if toolErr != nil {
			got = string(toolErr.Code)
		}
		if got != c.wantCode {
			t.Fatalf("%s.%s: tool error %v, want code %q", c.server, c.tool, toolErr, c.wantCode)
		}
	}
}

func TestNewHandler_DegradedWhenDatabaseIsDown_R79(t *testing.T) {
	pool := pgtest.New(t)
	h, err := NewHandler(validConfig(), pool, quiet())
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "degraded") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRun_ServesUntilCancelled_R69(t *testing.T) {
	pool := pgtest.New(t)
	kbDir := t.TempDir()
	doc := "---\ndoc_id: absence-policy\ntitle: Отгул\nsource_type: test\nacl: {roles: [intern], teams: [\"*\"]}\n---\nТестовая инструкция.\n"
	if err := os.WriteFile(filepath.Join(kbDir, "absence-policy.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.Port = freePort(t)
	cfg.PostgresURL = pgtest.URL(t, pool)
	cfg.KBDir = kbDir

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, quiet()) }()

	url := "http://127.0.0.1:" + strconv.Itoa(cfg.Port) + "/healthz"
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := Healthcheck(url); err == nil {
			break
		} else if time.Now().After(deadline) {
			cancel()
			t.Fatalf("service did not become healthy: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM kb.documents WHERE doc_id='absence-policy'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("kb not loaded: %d %v", n, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRun_Errors(t *testing.T) {
	if err := Run(context.Background(), Config{}, quiet()); err == nil {
		t.Fatal("missing secrets must fail")
	}
	pool := pgtest.New(t)
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "x.md"), []byte("без frontmatter"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.PostgresURL = pgtest.URL(t, pool)
	cfg.KBDir = bad
	if err := Run(context.Background(), cfg, quiet()); err == nil || !strings.Contains(err.Error(), "knowledge base") {
		t.Fatalf("invalid KB must stop the start: %v", err)
	}
	// порт уже занят: serve падает, а не зависает
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	cfg.KBDir = filepath.Join(t.TempDir(), "missing")
	cfg.Port = l.Addr().(*net.TCPAddr).Port
	errc := make(chan error, 1)
	go func() { errc <- serve(context.Background(), l.Addr().String(), http.NotFoundHandler(), quiet()) }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("serve on a busy port must fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not fail on a busy port")
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ok.Close()
	if err := Healthcheck(ok.URL); err != nil {
		t.Fatal(err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if err := Healthcheck(bad.URL); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("got %v", err)
	}
	if err := Healthcheck("http://127.0.0.1:1/healthz"); err == nil {
		t.Fatal("unreachable must fail")
	}
}
