package directory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pgtest"
)

var (
	jwtSecret  = []byte("test-jwt-secret")
	confSecret = []byte("test-confirm-secret")
)

type env struct {
	srv *mcpx.Server
	now time.Time
}

func newEnv(t *testing.T, now time.Time) *env {
	t.Helper()
	pool := pgtest.New(t)
	srv, err := mcpx.NewServer("directory", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := register(srv, pool, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatal(err)
	}
	return &env{srv: srv, now: now}
}

func caller(sub, role, team string) mcpx.Caller {
	return mcpx.Caller{Claims: authn.Claims{Sub: sub, Role: role, TeamID: team}}
}

func (e *env) call(t *testing.T, c mcpx.Caller, tool string) (map[string]any, *apierr.Error) {
	t.Helper()
	raw, aerr := e.srv.Invoke(context.Background(), c, tool, map[string]any{})
	if aerr != nil {
		return nil, aerr
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestWhoami_S2_Period_R02(t *testing.T) {
	today := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	pool := pgtest.New(t)
	for _, tc := range []struct {
		name, sub, role, team string
		start                 string // переопределить cohort_start, "" — оставить
		wantPeriod            string
		wantTeam              string
	}{
		{"intern anna 10 days in", "intern.anna", "intern", "delivery", "2026-10-30", "week_2", "Доставка"},
		{"intern ivan 3 days in", "intern.ivan", "intern", "search", "2026-11-06", "week_1", "Поиск"},
		{"month_1", "intern.anna", "intern", "delivery", "2026-10-15", "month_1", "Доставка"},
		{"month_2", "intern.anna", "intern", "delivery", "2026-09-20", "month_2", "Доставка"},
		{"after 3 months", "alumni.petr", "alumni", "delivery", "2026-04-01", "after_3_months", "Доставка"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(context.Background(), `UPDATE directory.employees SET cohort_start = $2 WHERE id = $1`, tc.sub, tc.start); err != nil {
				t.Fatal(err)
			}
			srv, err := mcpx.NewServer("directory", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
			if err != nil {
				t.Fatal(err)
			}
			if err := register(srv, pool, func() time.Time { return today }); err != nil {
				t.Fatal(err)
			}
			raw, aerr := srv.Invoke(context.Background(), caller(tc.sub, tc.role, tc.team), "whoami", map[string]any{})
			if aerr != nil {
				t.Fatal(aerr)
			}
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			if out["current_period"] != tc.wantPeriod || out["team_name"] != tc.wantTeam || out["cohort_start"] != tc.start {
				t.Fatalf("got %v", out)
			}
			if out["employee_id"] != tc.sub || out["role"] != tc.role {
				t.Fatalf("identity mismatch: %v", out)
			}
		})
	}
}

func TestWhoami_S2_NoCohortAndJWTFallback_R02(t *testing.T) {
	e := newEnv(t, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
	out, aerr := e.call(t, caller("mentor.oleg", "mentor", "delivery"), "whoami")
	if aerr != nil {
		t.Fatal(aerr)
	}
	if _, ok := out["current_period"]; ok {
		t.Fatalf("mentor without cohort must not have a period: %v", out)
	}
	// cohort_start нет в БД: используется claim из JWT.
	c := caller("mentor.oleg", "mentor", "delivery")
	c.CohortStart = "2026-11-01"
	out, aerr = e.call(t, c, "whoami")
	if aerr != nil || out["current_period"] != "week_2" || out["cohort_start"] != "2026-11-01" {
		t.Fatalf("got %v %v", out, aerr)
	}
	c.CohortStart = "garbage"
	out, _ = e.call(t, c, "whoami")
	if _, ok := out["current_period"]; ok {
		t.Fatalf("bad claim must be ignored: %v", out)
	}
}

func TestWhoami_R71_UnknownEmployeeForbidden(t *testing.T) {
	e := newEnv(t, time.Now())
	if _, aerr := e.call(t, caller("ghost", "intern", "delivery"), "whoami"); aerr == nil || aerr.Code != apierr.CodeForbidden {
		t.Fatalf("got %v", aerr)
	}
	if _, aerr := e.srv.Invoke(context.Background(), caller("intern.anna", "intern", "delivery"), "whoami", map[string]any{"employee_id": "lead.maria"}); aerr == nil || aerr.Code != apierr.CodeValidation {
		t.Fatalf("identity in args must be rejected, got %v", aerr)
	}
}

func TestGetMyContacts_S2_Roles_R71(t *testing.T) {
	e := newEnv(t, time.Now())
	for _, tc := range []struct {
		sub, role, team string
		want            []string // роли контактов по порядку
		ids             []string
	}{
		{"intern.anna", "intern", "delivery", []string{"mentor", "lead", "hrbp", "curator"}, []string{"mentor.oleg", "lead.maria", "hrbp.elena", "curator.irina"}},
		{"mentor.oleg", "mentor", "delivery", []string{"lead", "hrbp", "curator"}, []string{"lead.maria", "hrbp.elena", "curator.irina"}},
		{"lead.maria", "lead", "delivery", []string{"hrbp", "curator"}, []string{"hrbp.elena", "curator.irina"}},
		{"hrbp.elena", "hrbp", "people", []string{}, []string{}},
	} {
		t.Run(tc.sub, func(t *testing.T) {
			out, aerr := e.call(t, caller(tc.sub, tc.role, tc.team), "get_my_contacts")
			if aerr != nil {
				t.Fatal(aerr)
			}
			items := out["items"].([]any)
			if len(items) != len(tc.want) {
				t.Fatalf("got %d items: %v", len(items), items)
			}
			for i, it := range items {
				m := it.(map[string]any)
				if m["role"] != tc.want[i] || m["employee_id"] != tc.ids[i] {
					t.Fatalf("item %d: %v", i, m)
				}
				if a, _ := m["ask_about"].([]any); len(a) == 0 {
					t.Fatalf("ask_about empty: %v", m)
				}
				for k := range m {
					switch k {
					case "role", "employee_id", "display_name", "channel", "ask_about":
					default:
						t.Fatalf("unexpected field %q (only name and channel allowed)", k)
					}
				}
			}
		})
	}
}

func TestGetMyContacts_S2_AskAbout_R71(t *testing.T) {
	e := newEnv(t, time.Now())
	out, _ := e.call(t, caller("intern.ivan", "intern", "search"), "get_my_contacts")
	by := map[string]map[string]any{}
	for _, it := range out["items"].([]any) {
		m := it.(map[string]any)
		by[m["role"].(string)] = m
	}
	if by["mentor"]["employee_id"] != "mentor.sergey" || by["lead"]["channel"] != "#search-team" {
		t.Fatalf("wrong team contacts: %v", by)
	}
	if got := by["lead"]["ask_about"].([]any); got[0] != "отгул и отсутствия" || got[1] != "цели стажировки" {
		t.Fatalf("lead ask_about: %v", got)
	}
	if got := by["curator"]["ask_about"].([]any); len(got) != 3 || got[0] != "методичка наставника" {
		t.Fatalf("curator ask_about: %v", got)
	}
	if got := by["hrbp"]["ask_about"].([]any); len(got) != 1 || got[0] != "кадровые вопросы" {
		t.Fatalf("hrbp ask_about: %v", got)
	}
}

func TestRegister_Errors(t *testing.T) {
	if err := Register(nil, nil); err == nil {
		t.Fatal("expected error for nil args")
	}
	pool := pgtest.New(t)
	srv, _ := mcpx.NewServer("directory", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err := Register(srv, pool); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatalf("all tools must be bound: %v", err)
	}
}

func TestHTTP_S2_RoundTrip_R79(t *testing.T) {
	pool := pgtest.New(t)
	srv, _ := mcpx.NewServer("directory", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err := Register(srv, pool); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := mcpx.Mount(mux, srv); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mux)
	defer ts.Close()
	cl := &mcpx.Client{BaseURL: ts.URL + "/mcp/"}
	tok, err := authn.Issue(jwtSecret, authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw, aerr, err := cl.Call(context.Background(), tok, "directory", "get_my_contacts", map[string]any{})
	if err != nil || aerr != nil {
		t.Fatalf("%v %v", err, aerr)
	}
	var out struct {
		Items []struct{ Role string } `json:"items"`
	}
	_ = json.Unmarshal(raw, &out)
	if len(out.Items) != 4 {
		t.Fatalf("got %s", raw)
	}
	raw, aerr, err = cl.Call(context.Background(), tok, "directory", "whoami", map[string]any{})
	if err != nil || aerr != nil || !json.Valid(raw) {
		t.Fatalf("%v %v", err, aerr)
	}
	if _, _, err := cl.Call(context.Background(), "bad", "directory", "whoami", map[string]any{}); err == nil {
		t.Fatal("bad token must fail")
	}
}

func TestDirectory_DBFailure(t *testing.T) {
	pool := pgtest.New(t)
	srv, _ := mcpx.NewServer("directory", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err := Register(srv, pool); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	for _, tool := range []string{"whoami", "get_my_contacts"} {
		if _, aerr := srv.Invoke(context.Background(), caller("intern.anna", "intern", "delivery"), tool, map[string]any{}); aerr == nil || aerr.Code != apierr.CodeUnavailable {
			t.Fatalf("%s: got %v", tool, aerr)
		}
	}
}
