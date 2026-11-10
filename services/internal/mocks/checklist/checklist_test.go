package checklist

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

type tmpl struct {
	Periods []struct{ ID, Label string } `json:"periods"`
	Items   []struct {
		ID          string   `json:"id"`
		Role        string   `json:"role"`
		Period      string   `json:"period"`
		Description string   `json:"description"`
		Steps       []string `json:"steps"`
		HumanOnly   bool     `json:"human_only"`
	} `json:"items"`
}

func newServer(t *testing.T) *mcpx.Server {
	t.Helper()
	srv, err := mcpx.NewServer("checklist", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(srv, pgtest.New(t)); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatal(err)
	}
	return srv
}

func get(t *testing.T, srv *mcpx.Server, role string) tmpl {
	t.Helper()
	c := mcpx.Caller{Claims: authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}}
	raw, aerr := srv.Invoke(context.Background(), c, "get_plan_template", map[string]any{"role": role})
	if aerr != nil {
		t.Fatal(aerr)
	}
	var out tmpl
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGetPlanTemplate_S1_Counts_R02(t *testing.T) {
	srv := newServer(t)
	for _, tc := range []struct {
		role  string
		count int
		first string
	}{{"intern", 10, "CL-03"}, {"mentor", 7, "CL-01"}} {
		out := get(t, srv, tc.role)
		if len(out.Items) != tc.count || out.Items[0].ID != tc.first {
			t.Fatalf("%s: %d items, first %s", tc.role, len(out.Items), out.Items[0].ID)
		}
		both := 0
		for i, it := range out.Items {
			if it.Role == "both" {
				both++
			} else if it.Role != tc.role {
				t.Fatalf("%s got item of role %s", tc.role, it.Role)
			}
			if i > 0 && it.ID < out.Items[i-1].ID {
				t.Fatalf("items not ordered by position: %s after %s", it.ID, out.Items[i-1].ID)
			}
			if it.Steps == nil {
				t.Fatalf("%s: steps must be an array", it.ID)
			}
		}
		if both != 1 {
			t.Fatalf("%s: want exactly one shared item (CL-14), got %d", tc.role, both)
		}
	}
}

func TestGetPlanTemplate_S1_PeriodsAndFields_R02(t *testing.T) {
	out := get(t, newServer(t), "intern")
	want := []string{"week_1", "week_2", "month_1", "month_2", "after_3_months"}
	if len(out.Periods) != 5 {
		t.Fatalf("periods: %v", out.Periods)
	}
	for i, p := range out.Periods {
		if p.ID != want[i] || p.Label == "" {
			t.Fatalf("period %d: %v", i, p)
		}
	}
	if out.Periods[0].Label != "1 неделя" {
		t.Fatalf("label: %q", out.Periods[0].Label)
	}
	var cl04 = -1
	for i, it := range out.Items {
		if it.ID == "CL-04" {
			cl04 = i
		}
	}
	if cl04 < 0 || len(out.Items[cl04].Steps) == 0 || out.Items[cl04].Period != "week_1" {
		t.Fatalf("CL-04 steps missing: %+v", out.Items)
	}
}

func TestGetPlanTemplate_S1_Validation_R71(t *testing.T) {
	srv := newServer(t)
	c := mcpx.Caller{Claims: authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}}
	for _, args := range []map[string]any{{"role": "lead"}, {}, {"role": "intern", "team_id": "search"}} {
		if _, aerr := srv.Invoke(context.Background(), c, "get_plan_template", args); aerr == nil || aerr.Code != apierr.CodeValidation {
			t.Fatalf("args %v: got %v", args, aerr)
		}
	}
}

func TestRegister_Errors(t *testing.T) {
	if err := Register(nil, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTP_S1_RoundTrip_R79(t *testing.T) {
	srv := newServer(t)
	mux := http.NewServeMux()
	if err := mcpx.Mount(mux, srv); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mux)
	defer ts.Close()
	tok, _ := authn.Issue(jwtSecret, authn.Claims{Sub: "mentor.oleg", Role: "mentor", TeamID: "delivery"}, time.Hour)
	raw, aerr, err := (&mcpx.Client{BaseURL: ts.URL + "/mcp/"}).Call(context.Background(), tok, "checklist", "get_plan_template", map[string]any{"role": "mentor"})
	if err != nil || aerr != nil {
		t.Fatalf("%v %v", err, aerr)
	}
	var out tmpl
	_ = json.Unmarshal(raw, &out)
	if len(out.Items) != 7 {
		t.Fatalf("got %d items", len(out.Items))
	}
}

func TestGetPlanTemplate_S1_NullStepsAndDBFailure(t *testing.T) {
	pool := pgtest.New(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO checklist.items (id, role, period, position, title, steps, scenarios, human_only)
		VALUES ('CL-99', 'intern', 'week_1', 99, 'Тест', 'null'::jsonb, ARRAY[]::text[], false)`); err != nil {
		t.Fatal(err)
	}
	srv, _ := mcpx.NewServer("checklist", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err := Register(srv, pool); err != nil {
		t.Fatal(err)
	}
	out := get(t, srv, "intern")
	last := out.Items[len(out.Items)-1]
	if last.ID != "CL-99" || last.Steps == nil || len(last.Steps) != 0 {
		t.Fatalf("got %+v", last)
	}
	pool.Close()
	c := mcpx.Caller{Claims: authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}}
	if _, aerr := srv.Invoke(context.Background(), c, "get_plan_template", map[string]any{"role": "intern"}); aerr == nil || aerr.Code != apierr.CodeUnavailable {
		t.Fatalf("got %v", aerr)
	}
}
