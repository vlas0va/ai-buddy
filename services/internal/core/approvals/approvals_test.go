package approvals

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
	"aibuddy/services/internal/core/agentclient/agenttest"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/core/mcpclient/mcptest"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pgtest"
)

var (
	jwtSecret  = []byte("test-jwt-secret-0123456789abcdef")
	hmacSecret = []byte("test-hmac-secret-0123456789abcdef")
)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	mcp  *mcptest.Server
	acts *actions.Service
	url  string
}

func newEnv(t *testing.T, mcp MCP) *env {
	t.Helper()
	pool := pgtest.New(t)
	fake := mcptest.New(t, jwtSecret, hmacSecret)
	if mcp == nil {
		c, err := mcpclient.New(mcpclient.Config{BaseURL: fake.URL, JWTSecret: jwtSecret, ConfirmationSecret: hmacSecret})
		if err != nil {
			t.Fatal(err)
		}
		mcp = c
	}
	acts := actions.NewService(analytics.NewEmitter(false), notify.Store{}, nil)
	mux := http.NewServeMux()
	New(pool, mcp, acts, analytics.NewEmitter(false), nil).Register(mux)
	srv := httptest.NewServer(httpx.RequestID(authn.Middleware(jwtSecret)(mux)))
	t.Cleanup(srv.Close)
	return &env{t: t, pool: pool, mcp: fake, acts: acts, url: srv.URL}
}

func tok(t *testing.T, sub, role, team string) string {
	return agenttest.Token(t, jwtSecret, sub, role, team, "")
}

// write performs an MCP write as the agent would after a chat confirmation; with save the result is also saved
// as the intern's action (what chat does on tool_result).
func (e *env) write(sub, role, team, tool string, args map[string]any, kind string, save bool) string {
	e.t.Helper()
	key := agenttest.NewID()
	signed, err := mcpx.WithConfirmation(hmacSecret, sub, key, args, time.Minute)
	if err != nil {
		e.t.Fatal(err)
	}
	server, name, _ := strings.Cut(tool, ".")
	out, toolErr, err := (&mcpx.Client{BaseURL: e.mcp.URL}).Call(context.Background(), tok(e.t, sub, role, team), server, name, signed)
	if err != nil || toolErr != nil {
		e.t.Fatalf("%s: %v %v", tool, err, toolErr)
	}
	if save {
		if _, err := e.acts.Save(context.Background(), e.pool, actions.SaveInput{Claims: authn.Claims{Sub: sub, Role: role, TeamID: team},
			ProposalID: key, Kind: kind, Tool: tool, Result: out}); err != nil {
			e.t.Fatal(err)
		}
	}
	var res struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(out, &res)
	return res.ID
}

func (e *env) do(method, path, token, body string, headers ...string) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.url+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func code(t *testing.T, b []byte) string {
	t.Helper()
	var e apierr.Error
	_ = json.Unmarshal(b, &e)
	return string(e.Code)
}

func TestApprovals_S3_LeadQueueAndDecision_R66(t *testing.T) {
	e := newEnv(t, nil)
	absID := e.write("intern.anna", "intern", "delivery", "absence.create_request",
		map[string]any{"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13"}, actions.KindAbsence, true)
	e.write("intern.anna", "intern", "delivery", "access.request_access", map[string]any{"resource_id": "space-delivery-incidents"}, actions.KindAccess, false)
	maria, dmitry, anna := tok(t, "lead.maria", "lead", "delivery"), tok(t, "lead.dmitry", "lead", "search"), tok(t, "intern.anna", "intern", "delivery")

	status, body := e.do(http.MethodGet, "/api/approvals", maria, "")
	var items []map[string]any
	_ = json.Unmarshal(body, &items)
	if status != http.StatusOK || len(items) != 2 || items[0]["kind"] != "absence_request" || items[0]["requester_display_name"] != "Анна Котова" ||
		items[0]["status"] != "pending" || items[0]["comment"] != nil || items[0]["created_at"] == "" {
		t.Fatalf("%d %s", status, body)
	}
	for _, k := range []string{"ref_id", "requester_id"} {
		if _, ok := items[0][k]; ok {
			t.Fatalf("internal field %s leaked: %v", k, items[0])
		}
	}
	absApproval, accApproval := items[0]["id"].(string), items[1]["id"].(string)

	t.Run("R71_only_leads", func(t *testing.T) {
		if status, _ := e.do(http.MethodGet, "/api/approvals", anna, ""); status != http.StatusForbidden {
			t.Fatalf("intern list: %d", status)
		}
		if status, _ := e.do(http.MethodPost, "/api/approvals/"+absApproval+"/decision", anna, `{"decision":"approved"}`, "Idempotency-Key", agenttest.NewID()); status != http.StatusForbidden {
			t.Fatalf("intern decide: %d", status)
		}
		if status, body := e.do(http.MethodGet, "/api/approvals", dmitry, ""); status != http.StatusOK || string(body) != "[]\n" && string(body) != "[]" {
			t.Fatalf("other lead: %d %s", status, body)
		}
		if status, body := e.do(http.MethodPost, "/api/approvals/"+absApproval+"/decision", dmitry, `{"decision":"approved"}`, "Idempotency-Key", agenttest.NewID()); status != http.StatusForbidden {
			t.Fatalf("other team's request: %d %s", status, body)
		}
	})

	t.Run("R79_validation", func(t *testing.T) {
		for _, tc := range []struct {
			name, path, body, key string
			status                int
			code                  string
		}{
			{"missing Idempotency-Key", absApproval, `{"decision":"approved"}`, "", http.StatusBadRequest, "validation_error"},
			{"Idempotency-Key not a uuid", absApproval, `{"decision":"approved"}`, "click-1", http.StatusBadRequest, "validation_error"},
			{"unknown decision", absApproval, `{"decision":"maybe"}`, agenttest.NewID(), http.StatusBadRequest, "validation_error"},
			{"comment too long", absApproval, `{"decision":"approved","comment":"` + strings.Repeat("я", 1001) + `"}`, agenttest.NewID(), http.StatusBadRequest, "validation_error"},
			{"unknown field", absApproval, `{"decision":"approved","requester_id":"x"}`, agenttest.NewID(), http.StatusBadRequest, "validation_error"},
			{"bad approval id", "APR%20x", `{"decision":"approved"}`, agenttest.NewID(), http.StatusNotFound, "not_found"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				status, body := e.do(http.MethodPost, "/api/approvals/"+tc.path+"/decision", maria, tc.body, "Idempotency-Key", tc.key)
				if status != tc.status || code(t, body) != tc.code {
					t.Fatalf("%d %s", status, body)
				}
			})
		}
	})

	key := agenttest.NewID()
	status, body = e.do(http.MethodPost, "/api/approvals/"+absApproval+"/decision", maria, `{"decision":"approved","comment":"Хорошего отдыха"}`, "Idempotency-Key", key)
	var decided map[string]any
	_ = json.Unmarshal(body, &decided)
	if status != http.StatusOK || decided["status"] != "approved" || decided["comment"] != "Хорошего отдыха" || decided["id"] != absApproval {
		t.Fatalf("%d %s", status, body)
	}
	if e.mcp.AbsenceStatus(absID) != "approved" {
		t.Fatal("absence not approved in MCP")
	}
	if e.count(`SELECT count(*) FROM core.actions a JOIN core.plan_tasks t ON t.id = a.task_id
		WHERE a.external_id = $1 AND a.status = 'approved' AND t.status = 'done'`, absID) != 1 {
		t.Fatal("action and task not updated")
	}
	if e.count(`SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND kind = 'status_change' AND title = 'Отгул согласован'`) != 1 {
		t.Fatal("intern not notified")
	}
	if e.count(`SELECT count(*) FROM core.analytics_events WHERE name = 'action.status_changed' AND props->>'status' = 'approved'`) != 1 {
		t.Fatal("no analytics")
	}

	t.Run("R79_same_click_replays", func(t *testing.T) {
		status, again := e.do(http.MethodPost, "/api/approvals/"+absApproval+"/decision", maria, `{"decision":"approved","comment":"Хорошего отдыха"}`, "Idempotency-Key", key)
		if status != http.StatusOK || string(again) != string(body) {
			t.Fatalf("%d %s", status, again)
		}
		if e.count(`SELECT count(*) FROM core.analytics_events WHERE name = 'action.status_changed'`) != 1 {
			t.Fatal("replay changed state again")
		}
		status, conflict := e.do(http.MethodPost, "/api/approvals/"+absApproval+"/decision", maria, `{"decision":"rejected"}`, "Idempotency-Key", agenttest.NewID())
		if status != http.StatusConflict || code(t, conflict) != "conflict" {
			t.Fatalf("second decision: %d %s", status, conflict)
		}
	})

	t.Run("S4_request_without_core_action", func(t *testing.T) {
		status, body := e.do(http.MethodPost, "/api/approvals/"+accApproval+"/decision", maria, `{"decision":"rejected"}`, "Idempotency-Key", agenttest.NewID())
		if status != http.StatusOK || !strings.Contains(string(body), `"status":"rejected"`) {
			t.Fatalf("%d %s", status, body)
		}
	})
}

// stubMCP returns fixed results, for shapes the fake servers never produce.
type stubMCP struct {
	list   []mcpclient.Approval
	decide mcpclient.Approval
	err    error
}

func (s stubMCP) ListPendingApprovals(context.Context, string) ([]mcpclient.Approval, error) {
	return s.list, s.err
}

func (s stubMCP) DecideApproval(context.Context, string, string, string, string, string) (mcpclient.Approval, error) {
	return s.decide, s.err
}

func TestApprovals_EdgeCases(t *testing.T) {
	odd := mcpclient.Approval{ID: "APR-1", Kind: "absence_request", RefID: "ABS-1", RequesterID: "intern.anna", Summary: "Отгул",
		Status: "pending", CreatedAt: "2026-11-10T10:00:00Z"}
	e := newEnv(t, stubMCP{list: []mcpclient.Approval{odd}, decide: odd})
	maria := tok(t, "lead.maria", "lead", "delivery")
	status, body := e.do(http.MethodGet, "/api/approvals", maria, "")
	if status != http.StatusOK || !strings.Contains(string(body), `"requester_display_name":"Анна Котова"`) {
		t.Fatalf("name from core: %d %s", status, body)
	}
	if status, body := e.do(http.MethodPost, "/api/approvals/APR-1/decision", maria, `{"decision":"approved"}`, "Idempotency-Key", agenttest.NewID()); status != http.StatusOK {
		t.Fatalf("unexpected status from MCP is skipped: %d %s", status, body)
	}

	down := newEnv(t, stubMCP{err: errors.Join(apierr.UpstreamUnavailable(), errors.New("dial"))})
	if status, body := down.do(http.MethodGet, "/api/approvals", maria, ""); status != http.StatusBadGateway || code(t, body) != "upstream_unavailable" {
		t.Fatalf("%d %s", status, body)
	}
	if status, _ := down.do(http.MethodPost, "/api/approvals/APR-1/decision", maria, `{"decision":"approved"}`, "Idempotency-Key", agenttest.NewID()); status != http.StatusBadGateway {
		t.Fatalf("decide with MCP down: %d", status)
	}

	broken := newEnv(t, stubMCP{list: []mcpclient.Approval{odd}, decide: odd})
	broken.pool.Close()
	for _, path := range []string{"/api/approvals"} {
		if status, _ := broken.do(http.MethodGet, path, maria, ""); status != http.StatusInternalServerError {
			t.Fatalf("db down list: %d", status)
		}
	}
	if status, _ := broken.do(http.MethodPost, "/api/approvals/APR-1/decision", maria, `{"decision":"approved"}`, "Idempotency-Key", agenttest.NewID()); status != http.StatusInternalServerError {
		t.Fatalf("db down decide: %d", status)
	}

	rec := httptest.NewRecorder()
	New(e.pool, stubMCP{}, e.acts, nil, nil).list(rec, httptest.NewRequest(http.MethodGet, "/api/approvals", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no claims: %d", rec.Code)
	}
}
