package absence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/confirm"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pgtest"
)

func (e *env) addDoc(id, roles, teams, url string) {
	e.t.Helper()
	e.exec(`INSERT INTO kb.documents (doc_id, title, body, acl_roles, acl_teams, url)
VALUES ($1, 'Тестовая инструкция: отгул', 'Текст '||$1, $2::text[], $3::text[], nullif($4, ''))`, id, roles, teams, url)
}

func TestGetPolicy_S3_TeamDocFirst_R69_R71(t *testing.T) {
	e := newEnv(t)
	e.wantCode(anna, "get_policy", nil, apierr.CodeNotFound)

	e.addDoc("absence-policy", "{intern,mentor,lead}", "{*}", "")
	e.addDoc("absence-policy-delivery", "{intern,mentor,lead}", "{delivery}", "https://wiki.example.test/absence")
	e.addDoc("absence-policy-search", "{mentor}", "{search}", "") // not readable by interns
	tests := []struct {
		name    string
		c       mcpx.Caller
		wantDoc string
		wantURL string
	}{
		{"team document wins", anna, "absence-policy-delivery", "https://wiki.example.test/absence"},
		{"lead of the same team", maria, "absence-policy-delivery", "https://wiki.example.test/absence"},
		{"team document hidden by ACL falls back to common", ivan, "absence-policy", ""},
		{"mentor of search reads the team document", caller("mentor.sergey", "mentor", "search"), "absence-policy-search", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustCall[policy](e, tt.c, "get_policy", map[string]any{})
			if p.DocID != tt.wantDoc || p.URL != tt.wantURL || p.SourceType != "test" || p.Text != "Текст "+tt.wantDoc || p.Title == "" {
				t.Fatalf("got %+v", p)
			}
		})
	}
}

func TestValidateRequest_S3_Rules(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO absence.requests (id, employee_id, type, start_date, end_date, status, approver_id, idempotency_key) VALUES
('ABS-000001', 'intern.anna', 'day_off', '2026-11-17', '2026-11-18', 'pending_approval', 'lead.maria', gen_random_uuid()),
('ABS-000002', 'intern.anna', 'day_off', '2026-11-20', '2026-11-20', 'approved', 'lead.maria', gen_random_uuid()),
('ABS-000003', 'intern.anna', 'day_off', '2026-11-24', '2026-11-24', 'rejected', 'lead.maria', gen_random_uuid()),
('ABS-000004', 'intern.ivan', 'day_off', '2026-11-25', '2026-11-25', 'pending_approval', 'lead.dmitry', gen_random_uuid())`)
	tests := []struct {
		name  string
		start string
		end   string
		want  []string // substrings, one per expected problem, in order
	}{
		{"tomorrow, one weekday", "2026-11-10", "2026-11-10", nil},
		{"three weekdays", "2026-11-10", "2026-11-12", nil},
		{"today is too early", "2026-11-09", "2026-11-09", []string{"не раньше завтрашнего дня (10.11.2026)"}},
		{"past date", "2026-11-02", "2026-11-02", []string{"не раньше завтрашнего дня"}},
		{"saturday", "2026-11-14", "2026-11-14", []string{"только на рабочие дни, выходные: 14.11.2026 (суббота)"}},
		{"friday to monday", "2026-11-13", "2026-11-16", []string{
			"14.11.2026 (суббота), 15.11.2026 (воскресенье)", "не больше чем на 3 дня подряд, выбрано 4 дня"}},
		{"five weekdays", "2026-12-07", "2026-12-11", []string{"выбрано 5 дней"}},
		{"end before start", "2026-11-12", "2026-11-11", []string{"Дата окончания не может быть раньше даты начала"}},
		{"overlaps pending", "2026-11-18", "2026-11-19", []string{"пересекаются с заявкой ABS-000001 на 17.11.2026–18.11.2026 (на согласовании)"}},
		{"overlaps approved", "2026-11-19", "2026-11-20", []string{"ABS-000002 на 20.11.2026 (согласована)"}},
		{"rejected and foreign requests do not overlap", "2026-11-24", "2026-11-25", nil},
		{"long range lists four weekends and the rest", "2026-11-10", "2026-12-31", []string{
			"14.11.2026 (суббота), 15.11.2026 (воскресенье), 21.11.2026 (суббота), 22.11.2026 (воскресенье), и ещё",
			"выбрано 52 дня", "ABS-000001", "ABS-000002"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustCall[validateResult](e, anna, "validate_request", dayOff(tt.start, tt.end))
			if got.Valid != (len(tt.want) == 0) || len(got.Problems) != len(tt.want) {
				t.Fatalf("got %+v, want problems %q", got, tt.want)
			}
			for i, w := range tt.want {
				if !strings.Contains(got.Problems[i], w) {
					t.Fatalf("problem %d = %q, want it to contain %q", i, got.Problems[i], w)
				}
			}
			if got.ApproverID != "lead.maria" || got.ApproverDisplayName != "Мария Лебедева" {
				t.Fatalf("approver %+v", got)
			}
		})
	}

	t.Run("lead without a lead has no approver", func(t *testing.T) {
		got := mustCall[validateResult](e, maria, "validate_request", dayOff("2026-11-10", "2026-11-10"))
		if got.Valid || len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "согласующего") || got.ApproverID != "" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("mentor gets his lead", func(t *testing.T) {
		got := mustCall[validateResult](e, oleg, "validate_request", dayOff("2026-11-10", "2026-11-10"))
		if !got.Valid || got.ApproverID != "lead.maria" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("tomorrow is computed in Moscow time", func(t *testing.T) {
		e.clock.Set(time.Date(2026, 11, 9, 22, 30, 0, 0, time.UTC)) // 01:30 on 10 Nov in Moscow
		defer e.clock.Set(monday)
		got := mustCall[validateResult](e, anna, "validate_request", dayOff("2026-11-10", "2026-11-10"))
		if got.Valid || !strings.Contains(got.Problems[0], "(11.11.2026)") {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("employee missing from the directory", func(t *testing.T) {
		e.wantCode(caller("intern.ghost", "intern", "delivery"), "validate_request", dayOff("2026-11-10", "2026-11-10"),
			apierr.CodeForbidden)
	})
	t.Run("bad arguments are rejected by the schema", func(t *testing.T) {
		e.wantCode(anna, "validate_request", dayOff("10.11.2026", "2026-11-10"), apierr.CodeValidation)
		e.wantCode(anna, "validate_request", map[string]any{"type": "vacation", "start_date": "2026-11-10", "end_date": "2026-11-10"},
			apierr.CodeValidation)
	})
	t.Run("validate itself rejects unparsable dates", func(t *testing.T) {
		s := &service{pool: e.pool, now: e.clock.Now}
		_, err := s.validate(context.Background(), e.pool, anna, dates{Type: "day_off", StartDate: "x", EndDate: "y"})
		if !apierr.HasCode(err, apierr.CodeValidation) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCreateRequest_S3_CreatesRequestAndApproval_R66(t *testing.T) {
	e := newEnv(t)
	key := newKey(t)
	args := dayOff("2026-11-10", "2026-11-11")
	args["comment"] = "Экзамен в вузе"
	got := mustCall[requestOut](e, anna, "create_request", confirmed(t, anna, key, args))
	if !strings.HasPrefix(got.ID, "ABS-") || got.Status != "pending_approval" || got.ApproverID != "lead.maria" ||
		got.ApproverDisplayName != "Мария Лебедева" || got.Comment != "Экзамен в вузе" || got.StartDate != "2026-11-10" ||
		got.EndDate != "2026-11-11" || got.Replayed == nil || *got.Replayed {
		t.Fatalf("got %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.CreatedAt); err != nil {
		t.Fatalf("created_at %q: %v", got.CreatedAt, err)
	}
	if n := e.count(`SELECT count(*) FROM absence.requests WHERE id = $1 AND employee_id = 'intern.anna'
AND idempotency_key = $2::uuid AND status = 'pending_approval'`, got.ID, key); n != 1 {
		t.Fatalf("requests = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE kind = 'absence_request' AND ref_id = $1
AND requester_id = 'intern.anna' AND approver_id = 'lead.maria' AND status = 'pending'
AND summary = 'Отгул 10.11.2026–11.11.2026'`, got.ID); n != 1 {
		t.Fatalf("approval items = %d", n)
	}

	t.Run("single day summary and mentor as requester", func(t *testing.T) {
		got := mustCall[requestOut](e, oleg, "create_request", confirmed(t, oleg, newKey(t), dayOff("2026-11-12", "2026-11-12")))
		if n := e.count(`SELECT count(*) FROM approvals.items WHERE ref_id = $1 AND summary = 'Отгул 12.11.2026'
AND approver_id = 'lead.maria'`, got.ID); n != 1 || got.Comment != "" {
			t.Fatalf("items = %d, got %+v", n, got)
		}
	})
	t.Run("visible after reload", func(t *testing.T) {
		again := mustCall[requestOut](e, anna, "get_request", map[string]any{"id": got.ID})
		if again.ID != got.ID || again.Status != "pending_approval" || again.Replayed != nil {
			t.Fatalf("got %+v", again)
		}
	})
}

func TestCreateRequest_S3_Idempotent_R79(t *testing.T) {
	e := newEnv(t)
	key := newKey(t)
	args := confirmed(t, anna, key, dayOff("2026-11-10", "2026-11-10"))
	first := mustCall[requestOut](e, anna, "create_request", args)
	e.clock.Set(monday.AddDate(0, 0, 3)) // the stored result is returned even when the dates are no longer valid
	second := mustCall[requestOut](e, anna, "create_request", args)
	if second.ID != first.ID || second.Replayed == nil || !*second.Replayed || second.CreatedAt != first.CreatedAt {
		t.Fatalf("first %+v, second %+v", first, second)
	}
	if n := e.count(`SELECT count(*) FROM absence.requests`); n != 1 {
		t.Fatalf("requests = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items`); n != 1 {
		t.Fatalf("approval items = %d", n)
	}
}

func TestCreateRequest_S3_SameKeyDifferentArgs_R79(t *testing.T) {
	e := newEnv(t)
	key := newKey(t)
	mustCall[requestOut](e, anna, "create_request", confirmed(t, anna, key, dayOff("2026-11-10", "2026-11-10")))
	other := dayOff("2026-11-11", "2026-11-11")
	terr := e.wantCode(anna, "create_request", confirmed(t, anna, key, other), apierr.CodeConflict)
	if !strings.Contains(terr.Message, "Ключ идемпотентности") {
		t.Fatalf("message %q", terr.Message)
	}
	withComment := dayOff("2026-11-10", "2026-11-10")
	withComment["comment"] = "другое"
	e.wantCode(anna, "create_request", confirmed(t, anna, key, withComment), apierr.CodeConflict)
	// Another employee reusing the key learns nothing about the stored request.
	terr = e.wantCode(ivan, "create_request", confirmed(t, ivan, key, dayOff("2026-11-10", "2026-11-10")), apierr.CodeConflict)
	if strings.Contains(terr.Message, "ABS-") || strings.Contains(terr.Message, "Анна") {
		t.Fatalf("message leaks data: %q", terr.Message)
	}
	if n := e.count(`SELECT count(*) FROM absence.requests`); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}

func TestCreateRequest_S3_RevalidatesDates_R79(t *testing.T) {
	e := newEnv(t)
	mustCall[requestOut](e, anna, "create_request", confirmed(t, anna, newKey(t), dayOff("2026-11-10", "2026-11-11")))
	tests := []struct {
		name, start, end, want string
	}{
		{"weekend", "2026-11-14", "2026-11-14", "Заявку нельзя создать: Отгул оформляется только на рабочие дни"},
		{"overlap with own pending request", "2026-11-11", "2026-11-12", "пересекаются с заявкой"},
		{"past and too long", "2026-11-02", "2026-11-06", "не раньше завтрашнего дня (10.11.2026); Отгул можно оформить не больше"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terr := e.wantCode(anna, "create_request", confirmed(t, anna, newKey(t), dayOff(tt.start, tt.end)), apierr.CodeValidation)
			if !strings.Contains(terr.Message, tt.want) {
				t.Fatalf("message %q, want %q", terr.Message, tt.want)
			}
		})
	}
	t.Run("lead has no approver", func(t *testing.T) {
		e.wantCode(maria, "create_request", confirmed(t, maria, newKey(t), dayOff("2026-11-10", "2026-11-10")), apierr.CodeValidation)
	})
	if n := e.count(`SELECT count(*) FROM absence.requests`); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}

func TestCreateRequest_RightsMatrix_R71(t *testing.T) {
	e := newEnv(t)
	args := dayOff("2026-11-10", "2026-11-10")
	hash, err := confirm.ArgsHash(args)
	if err != nil {
		t.Fatal(err)
	}
	key := newKey(t)
	expired, err := confirm.IssueUntil(confSecret, anna.Sub, key, hash, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	withToken := func(token string) map[string]any {
		out := dayOff("2026-11-10", "2026-11-10")
		out["idempotency_key"], out["confirmation_token"] = key, token
		return out
	}
	foreign := confirmed(t, ivan, key, args)
	tampered := confirmed(t, anna, key, args)
	tampered["end_date"] = "2026-11-11"
	wrongKey := confirmed(t, anna, key, args)
	wrongKey["idempotency_key"] = newKey(t)
	otherSecret, err := mcpx.WithConfirmation([]byte("other-secret"), anna.Sub, key, args, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	spoofed := dayOff("2026-11-10", "2026-11-10")
	spoofed["employee_id"] = "intern.ivan"

	tests := []struct {
		name string
		c    mcpx.Caller
		args map[string]any
		want apierr.Code
	}{
		{"write without token", anna, args, apierr.CodeInvalidConfirmation},
		{"garbage token", anna, withToken("not-a-token"), apierr.CodeInvalidConfirmation},
		{"expired token", anna, withToken(expired), apierr.CodeInvalidConfirmation},
		{"token of another user", anna, foreign, apierr.CodeInvalidConfirmation},
		{"arguments changed after confirmation", anna, tampered, apierr.CodeInvalidConfirmation},
		{"token for another key", anna, wrongKey, apierr.CodeInvalidConfirmation},
		{"token signed with another secret", anna, otherSecret, apierr.CodeInvalidConfirmation},
		{"employee_id in args is an unknown field", anna, confirmed(t, anna, key, spoofed), apierr.CodeValidation},
		{"role outside the allow-list", caller("alumni.petr", "alumni", "delivery"), confirmed(t, caller("alumni.petr", "alumni", "delivery"), key, args), apierr.CodeForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.wantCode(tt.c, "create_request", tt.args, tt.want)
		})
	}
	if n := e.count(`SELECT count(*) FROM absence.requests`); n != 0 {
		t.Fatalf("requests = %d", n)
	}
	t.Run("valid token creates for the JWT subject", func(t *testing.T) {
		got := mustCall[requestOut](e, anna, "create_request", confirmed(t, anna, key, args))
		if n := e.count(`SELECT count(*) FROM absence.requests WHERE id = $1 AND employee_id = 'intern.anna'`, got.ID); n != 1 {
			t.Fatalf("requests = %d", n)
		}
	})
}

func TestGetRequest_S3_OwnOnly_R71(t *testing.T) {
	e := newEnv(t)
	own := mustCall[requestOut](e, anna, "create_request", confirmed(t, anna, newKey(t), dayOff("2026-11-10", "2026-11-10")))
	got := mustCall[requestOut](e, anna, "get_request", map[string]any{"id": own.ID})
	if got != (requestOut{ID: own.ID, Type: "day_off", StartDate: "2026-11-10", EndDate: "2026-11-10", Status: "pending_approval",
		ApproverID: "lead.maria", ApproverDisplayName: "Мария Лебедева", CreatedAt: own.CreatedAt}) {
		t.Fatalf("got %+v", got)
	}
	foreign := e.wantCode(ivan, "get_request", map[string]any{"id": own.ID}, apierr.CodeForbidden)
	missing := e.wantCode(ivan, "get_request", map[string]any{"id": "ABS-999999"}, apierr.CodeForbidden)
	if foreign.Message != missing.Message {
		t.Fatalf("foreign %q and missing %q differ", foreign.Message, missing.Message)
	}
	e.wantCode(maria, "get_request", map[string]any{"id": own.ID}, apierr.CodeForbidden) // not even the approver
	e.wantCode(anna, "get_request", map[string]any{"id": "42"}, apierr.CodeValidation)
}

func TestListMyRequests_S3_OwnWithStatusFilter_R71(t *testing.T) {
	e := newEnv(t)
	a := mustCall[requestOut](e, anna, "create_request", confirmed(t, anna, newKey(t), dayOff("2026-11-10", "2026-11-10")))
	b := mustCall[requestOut](e, anna, "create_request", confirmed(t, anna, newKey(t), dayOff("2026-11-12", "2026-11-13")))
	mustCall[requestOut](e, ivan, "create_request", confirmed(t, ivan, newKey(t), dayOff("2026-11-10", "2026-11-10")))
	e.exec(`UPDATE absence.requests SET status = 'approved' WHERE id = $1`, a.ID)

	type list struct {
		Items []requestOut `json:"items"`
	}
	ids := func(l list) []string {
		var out []string
		for _, it := range l.Items {
			out = append(out, it.ID)
		}
		return out
	}
	if got := ids(mustCall[list](e, anna, "list_my_requests", nil)); !slices.Equal(got, []string{b.ID, a.ID}) {
		t.Fatalf("all = %v", got)
	}
	if got := ids(mustCall[list](e, anna, "list_my_requests", map[string]any{"status": "approved"})); !slices.Equal(got, []string{a.ID}) {
		t.Fatalf("approved = %v", got)
	}
	raw, terr := e.call(maria, "list_my_requests", map[string]any{})
	if terr != nil || string(raw) != `{"items":[]}` {
		t.Fatalf("lead list = %s / %v", raw, terr)
	}
	e.wantCode(anna, "list_my_requests", map[string]any{"status": "lost"}, apierr.CodeValidation)
}

func TestCreateRequest_S3_OneTransaction_R66(t *testing.T) {
	e := newEnv(t)
	e.exec(`CREATE FUNCTION approvals.fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'boom'; END $$;
CREATE TRIGGER fail BEFORE INSERT ON approvals.items FOR EACH ROW EXECUTE FUNCTION approvals.fail()`)
	terr := e.wantCode(anna, "create_request", confirmed(t, anna, newKey(t), dayOff("2026-11-10", "2026-11-10")), apierr.CodeUnavailable)
	if strings.Contains(terr.Message, "boom") {
		t.Fatalf("internal detail leaked: %q", terr.Message)
	}
	if n := e.count(`SELECT count(*) FROM absence.requests`); n != 0 {
		t.Fatalf("request left without approval item: %d", n)
	}
}

func TestHandlers_DatabaseDown_R79(t *testing.T) {
	pool := pgtest.New(t)
	closed, err := pgxpool.New(context.Background(), pgtest.URL(t, pool))
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	e := &env{t: t, pool: pool, clock: &clock{now: monday}}
	e.srv = newServer(t, closed, e.clock.Now)
	calls := map[string]any{
		"get_policy":       nil,
		"list_my_requests": nil,
		"validate_request": dayOff("2026-11-10", "2026-11-10"),
		"create_request":   confirmed(t, anna, newKey(t), dayOff("2026-11-10", "2026-11-10")),
		"get_request":      map[string]any{"id": "ABS-000100"},
	}
	for tool, args := range calls {
		t.Run(tool, func(t *testing.T) {
			terr := e.wantCode(anna, tool, args, apierr.CodeUnavailable)
			if strings.Contains(terr.Message, "pool") {
				t.Fatalf("internal detail leaked: %q", terr.Message)
			}
		})
	}
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, terr := newServer(t, pool, e.clock.Now).Invoke(ctx, anna, "get_policy", nil); terr == nil || !terr.Retryable {
			t.Fatalf("got %v", terr)
		}
	})
}

func TestRegister_Errors(t *testing.T) {
	pool := pgtest.New(t)
	other, err := mcpx.NewServer("approvals", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := mcpx.NewServer("absence", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(nil, pool); err == nil {
		t.Fatal("nil server accepted")
	}
	if err := Register(srv, nil); err == nil {
		t.Fatal("nil pool accepted")
	}
	if err := Register(other, pool); err == nil {
		t.Fatal("foreign server accepted")
	}
	if err := Register(srv, pool); err != nil {
		t.Fatal(err)
	}
	if err := srv.Validate(); err != nil {
		t.Fatalf("not every contract tool is bound: %v", err)
	}
}

func TestFormatting(t *testing.T) {
	for n, want := range map[int]string{1: "день", 2: "дня", 4: "дня", 5: "дней", 11: "дней", 12: "дней", 21: "день", 22: "дня", 52: "дня", 111: "дней"} {
		if got := plural(n, "день", "дня", "дней"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
	if deref(nil) != "" {
		t.Error("deref(nil)")
	}
}

// TestRoundTrip_S3_HTTP_R71 runs the main scenario over the real transport: JWT, MCP client, confirmation token.
func TestRoundTrip_S3_HTTP_R71(t *testing.T) {
	e := newEnv(t)
	e.addDoc("absence-policy", "{intern,mentor,lead}", "{*}", "")
	mux := http.NewServeMux()
	if err := mcpx.Mount(mux, e.srv); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := &mcpx.Client{BaseURL: ts.URL + "/mcp"}
	ctx := context.Background()
	token := func(c mcpx.Caller) string {
		tok, err := authn.Issue(jwtSecret, c.Claims, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	annaJWT, ivanJWT := token(anna), token(ivan)
	call := func(jwt, tool string, args any) (json.RawMessage, *apierr.Error) {
		t.Helper()
		raw, terr, err := client.Call(ctx, jwt, "absence", tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return raw, terr
	}

	if raw, terr := call(annaJWT, "get_policy", nil); terr != nil || !strings.Contains(string(raw), `"source_type":"test"`) {
		t.Fatalf("get_policy %s / %v", raw, terr)
	}
	raw, terr := call(annaJWT, "validate_request", dayOff("2026-11-10", "2026-11-10"))
	if terr != nil || !strings.Contains(string(raw), `"valid":true`) {
		t.Fatalf("validate %s / %v", raw, terr)
	}
	args := confirmed(t, anna, newKey(t), dayOff("2026-11-10", "2026-11-10"))
	raw, terr = call(annaJWT, "create_request", args)
	var created requestOut
	if terr != nil || json.Unmarshal(raw, &created) != nil || created.Status != "pending_approval" || *created.Replayed {
		t.Fatalf("create %s / %v", raw, terr)
	}
	raw, terr = call(annaJWT, "create_request", args)
	if terr != nil || !strings.Contains(string(raw), `"replayed":true`) || !strings.Contains(string(raw), created.ID) {
		t.Fatalf("replay %s / %v", raw, terr)
	}
	if raw, terr := call(annaJWT, "get_request", map[string]any{"id": created.ID}); terr != nil || !strings.Contains(string(raw), created.ID) {
		t.Fatalf("get %s / %v", raw, terr)
	}
	if raw, terr := call(annaJWT, "list_my_requests", nil); terr != nil || !strings.Contains(string(raw), created.ID) {
		t.Fatalf("list %s / %v", raw, terr)
	}
	if _, terr := call(ivanJWT, "get_request", map[string]any{"id": created.ID}); terr == nil || terr.Code != apierr.CodeForbidden {
		t.Fatalf("foreign get: %v", terr)
	}
	// Ivan replays Anna's confirmed arguments with his own JWT: the token is bound to Anna.
	if _, terr := call(ivanJWT, "create_request", args); terr == nil || terr.Code != apierr.CodeInvalidConfirmation {
		t.Fatalf("foreign confirmation: %v", terr)
	}
	if _, _, err := client.Call(ctx, "forged", "absence", "get_policy", nil); !apierr.HasCode(err, apierr.CodeUnauthorized) {
		t.Fatalf("forged JWT: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM absence.requests`); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}
