package authapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/authapi"
	"aibuddy/services/internal/core/flags"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/pgtest"
)

var secret = []byte("test-secret")

func setup(t *testing.T) (*httptest.Server, func(sql string)) {
	t.Helper()
	pool := pgtest.New(t)
	h := authapi.New(pool, secret, time.Hour, flags.NewService(pool, nil, nil, nil), analytics.NewEmitter(false), nil)
	pub := http.NewServeMux()
	h.RegisterPublic(pub)
	priv := http.NewServeMux()
	h.Register(priv)
	root := http.NewServeMux()
	root.Handle("/api/auth/", pub)
	root.Handle("/api/", authn.Middleware(secret)(priv))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv, func(sql string) {
		if _, err := pool.Exec(context.Background(), sql); err != nil {
			t.Fatal(err)
		}
	}
}

func req(t *testing.T, method, url, token, body string) (int, []byte) {
	t.Helper()
	r, _ := http.NewRequest(method, url, bytes.NewReader([]byte(body)))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestLogin_Success_R52(t *testing.T) {
	srv, _ := setup(t)
	code, body := req(t, "POST", srv.URL+"/api/auth/login", "", `{"login":"intern.anna","password":"demo"}`)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			ID, Login, Role string
			DisplayName     string `json:"display_name"`
			TeamID          string `json:"team_id"`
			TeamName        string `json:"team_name"`
			CohortStart     string `json:"cohort_start"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.ID != "intern.anna" || out.User.Login != "intern.anna" || out.User.Role != "intern" || out.User.TeamID != "delivery" ||
		out.User.TeamName != "Доставка" || out.User.DisplayName != "Анна Котова" || len(out.User.CohortStart) != 10 {
		t.Fatalf("user: %+v", out.User)
	}
	c, err := authn.Verify(secret, out.Token, time.Now())
	if err != nil || c.Sub != "intern.anna" || c.Role != "intern" || c.TeamID != "delivery" || c.CohortStart != out.User.CohortStart {
		t.Fatalf("claims: %+v err=%v", c, err)
	}
	// user without cohort: no cohort_start in the response or claims
	code, body = req(t, "POST", srv.URL+"/api/auth/login", "", `{"login":"lead.maria","password":"demo"}`)
	if code != 200 || strings.Contains(string(body), "cohort_start") {
		t.Fatalf("lead: %d %s", code, body)
	}
}

func TestLogin_Rejects_R52(t *testing.T) {
	srv, _ := setup(t)
	cases := map[string]string{
		"wrong password": `{"login":"intern.anna","password":"nope"}`,
		"unknown user":   `{"login":"ghost","password":"demo"}`,
		"curator":        `{"login":"curator.irina","password":"demo"}`,
		"empty":          `{"login":"","password":""}`,
		"too long":       `{"login":"` + strings.Repeat("a", 200) + `","password":"demo"}`,
		"sql":            `{"login":"' OR 1=1 --","password":"x"}`,
	}
	var msgs = map[string]bool{}
	for name, b := range cases {
		code, body := req(t, "POST", srv.URL+"/api/auth/login", "", b)
		if code != 401 {
			t.Errorf("%s: %d %s", name, code, body)
		}
		var e struct{ Code, Message string }
		_ = json.Unmarshal(body, &e)
		msgs[e.Message] = true
		if e.Code != "unauthorized" {
			t.Errorf("%s: code %q", name, e.Code)
		}
	}
	if len(msgs) != 1 {
		t.Errorf("messages must be identical for every failure: %v", msgs)
	}
	if code, _ := req(t, "POST", srv.URL+"/api/auth/login", "", `{"login":"a"}garbage`); code != 400 {
		t.Errorf("malformed body: %d", code)
	}
}

func TestMe_UserAndFlags_R55(t *testing.T) {
	srv, exec := setup(t)
	tok, _ := authn.Issue(secret, authn.Claims{Sub: "mentor.oleg", Role: "mentor", TeamID: "delivery"}, time.Hour)
	code, body := req(t, "GET", srv.URL+"/api/me", tok, "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out struct {
		User  map[string]any  `json:"user"`
		Flags map[string]bool `json:"flags"`
	}
	_ = json.Unmarshal(body, &out)
	if out.User["id"] != "mentor.oleg" || out.User["role"] != "mentor" || out.User["team_name"] != "Доставка" || len(out.Flags) != 6 || !out.Flags["scenario_s3"] {
		t.Fatalf("me: %s", body)
	}
	// flags are per team
	exec(`UPDATE core.feature_flags SET teams = ARRAY['search'] WHERE key = 'scenario_s4'`)
	_, body = req(t, "GET", srv.URL+"/api/me", tok, "")
	_ = json.Unmarshal(body, &out)
	if out.Flags["scenario_s4"] {
		t.Fatalf("delivery must not get s4: %v", out.Flags)
	}
	// token of an unknown user and of a user who cannot log in
	for _, sub := range []string{"ghost", "curator.irina"} {
		bad, _ := authn.Issue(secret, authn.Claims{Sub: sub, Role: "mentor", TeamID: "delivery"}, time.Hour)
		if code, _ := req(t, "GET", srv.URL+"/api/me", bad, ""); code != 401 {
			t.Errorf("%s: %d", sub, code)
		}
	}
	if code, _ := req(t, "GET", srv.URL+"/api/me", "", ""); code != 401 {
		t.Errorf("anon: %d", code)
	}
}

func TestLogin_SessionStartedEvent_R24(t *testing.T) {
	pool := pgtest.New(t)
	h := authapi.New(pool, secret, 0, flags.NewService(pool, nil, nil, nil), analytics.NewEmitter(false), nil)
	h.Now = func() time.Time { return time.Now().AddDate(0, 0, 5) } // anna: 10 days + 5 = 15 days -> month_1
	mux := http.NewServeMux()
	h.RegisterPublic(mux)
	for _, u := range []string{"intern.anna", "lead.maria"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"login":"`+u+`","password":"demo"}`)))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", u, rec.Code)
		}
	}
	var p string
	if err := pool.QueryRow(context.Background(), `SELECT props->>'period' FROM core.analytics_events WHERE name='session.started' AND user_id='intern.anna'`).Scan(&p); err != nil || p != "month_1" {
		t.Fatalf("period=%q err=%v", p, err)
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM core.analytics_events WHERE name='session.started' AND user_id='lead.maria' AND props = '{}'`).Scan(&n)
	if n != 1 {
		t.Fatalf("lead event: %d", n)
	}
}

func TestMe_NoClaims(t *testing.T) {
	h := authapi.New(nil, secret, 0, nil, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/me", nil))
	if rec.Code != 401 {
		t.Fatalf("%d", rec.Code)
	}
}
