package access

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

func ids(l statusList) []string {
	var out []string
	for _, it := range l.Items {
		out = append(out, it.ResourceID)
	}
	return out
}

func TestListRequired_S4_TeamScope_R71(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		c    mcpx.Caller
		want []string
	}{
		{"intern of delivery", anna, []string{"bitbucket", "confluence", "jira", "jira-dlv", "bb-delivery-api", "space-delivery",
			"space-delivery-incidents", "db-delivery-orders"}},
		{"mentor of delivery", oleg, []string{"bitbucket", "confluence", "jira", "jira-dlv", "bb-delivery-api", "space-delivery",
			"space-delivery-incidents", "db-delivery-orders"}},
		{"intern of search", ivan, []string{"bitbucket", "confluence", "jira", "jira-srch", "bb-search-indexer", "space-search",
			"db-search-catalog"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustCall[statusList](e, tt.c, "list_required", nil)
			if !slices.Equal(ids(got), tt.want) {
				t.Fatalf("got %v, want %v", ids(got), tt.want)
			}
			for _, it := range got.Items {
				if it.Status != "not_requested" || it.Kind == "channel" || (it.ChecklistItemID == "") != it.Optional || it.Name == "" {
					t.Fatalf("item %+v", it)
				}
			}
		})
	}
	m := mustCall[statusList](e, anna, "list_required", map[string]any{}).byID()
	if !m["space-delivery-incidents"].Elevated || m["confluence"].Elevated || m["space-delivery"].ChecklistItemID != "CL-05" ||
		m["jira"].Kind != "tool" || m["space-delivery"].Kind != "space" {
		t.Fatalf("items %+v", m)
	}
	e.wantCode(maria, "list_required", nil, apierr.CodeForbidden) // белый список ролей: стажёр, наставник
}

func TestListRequired_S4_LatestStatusWins(t *testing.T) {
	e := newEnv(t)
	e.addRequest("intern.anna", "confluence", "rejected", 10)
	e.addRequest("intern.anna", "confluence", "granted", 1)
	e.addRequest("intern.anna", "jira", "granted", 10)
	e.addRequest("intern.anna", "jira", "rejected", 1)
	e.addRequest("intern.anna", "space-delivery-incidents", "pending_approval", 1)
	e.addRequest("intern.ivan", "bitbucket", "granted", 1) // чужие заявки не считаются
	m := mustCall[statusList](e, anna, "list_required", nil).byID()
	want := map[string]string{"confluence": "granted", "jira": "rejected", "space-delivery-incidents": "pending_approval",
		"bitbucket": "not_requested", "space-delivery": "not_requested"}
	for id, status := range want {
		if m[id].Status != status {
			t.Errorf("%s: %q, want %q", id, m[id].Status, status)
		}
	}
}

func TestListMyAccess_S4_OwnRequestsOnly_R71(t *testing.T) {
	e := newEnv(t)
	e.addRequest("intern.anna", "confluence", "granted", 5)
	e.addRequest("intern.anna", "ch-delivery-oncall", "rejected", 5)
	e.addRequest("intern.anna", "ch-delivery-oncall", "pending_approval", 1)
	e.addRequest("intern.ivan", "space-search", "granted", 1)

	got := mustCall[statusList](e, anna, "list_my_access", nil)
	if !slices.Equal(ids(got), []string{"confluence", "ch-delivery-oncall"}) {
		t.Fatalf("anna %v", ids(got))
	}
	if m := got.byID(); m["ch-delivery-oncall"].Status != "pending_approval" || m["ch-delivery-oncall"].Kind != "channel" ||
		!m["ch-delivery-oncall"].Elevated || m["confluence"].Status != "granted" {
		t.Fatalf("items %+v", got.Items)
	}
	if got := mustCall[statusList](e, ivan, "list_my_access", nil); !slices.Equal(ids(got), []string{"space-search"}) {
		t.Fatalf("ivan %v", ids(got))
	}
	raw, terr := e.call(oleg, "list_my_access", nil)
	if terr != nil || string(raw) != `{"items":[]}` {
		t.Fatalf("oleg %s / %v", raw, terr)
	}
}

func TestListTeamChannels_S4_CL04_R71(t *testing.T) {
	e := newEnv(t)
	type channels struct {
		Items []channel `json:"items"`
	}
	tests := []struct {
		c    mcpx.Caller
		want []channel
	}{
		{anna, []channel{
			{Name: "#interns-2026", Purpose: "Комьюнити стажёров и объявления куратора"},
			{Name: "#delivery-team", Purpose: "Рабочий чат команды"},
			{Name: "#delivery-oncall", Purpose: "Закрытый канал дежурств", Closed: true},
		}},
		{ivan, []channel{
			{Name: "#interns-2026", Purpose: "Комьюнити стажёров и объявления куратора"},
			{Name: "#search-team", Purpose: "Рабочий чат команды"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.c.Sub, func(t *testing.T) {
			got := mustCall[channels](e, tt.c, "list_team_channels", nil)
			if !slices.Equal(got.Items, tt.want) {
				t.Fatalf("got %+v, want %+v", got.Items, tt.want)
			}
		})
	}
	raw, _ := e.call(anna, "list_team_channels", nil)
	for _, leak := range []string{"resource_id", "team", "employee", "member"} {
		if strings.Contains(string(raw), `"`+leak) {
			t.Fatalf("channel list exposes %q: %s", leak, raw)
		}
	}
	e.exec(`DELETE FROM access.resources WHERE kind = 'channel'`)
	if raw, terr := e.call(anna, "list_team_channels", nil); terr != nil || string(raw) != `{"items":[]}` {
		t.Fatalf("empty %s / %v", raw, terr)
	}
}

func TestRequestAccess_S4_PlainResourceGrantedAtOnce_R66(t *testing.T) {
	e := newEnv(t)
	key := newKey(t)
	args := resource("confluence")
	args["reason"] = "Документация команды"
	got := mustCall[result](e, anna, "request_access", confirmed(t, anna, key, args))
	if !strings.HasPrefix(got.ID, "ACC-") || got.ResourceID != "confluence" || got.ResourceName != "Confluence" ||
		got.Status != "granted" || got.ApproverID != nil || got.ChecklistItemID == nil || *got.ChecklistItemID != "CL-01" ||
		got.Replayed {
		t.Fatalf("got %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.CreatedAt); err != nil {
		t.Fatalf("created_at %q", got.CreatedAt)
	}
	if n := e.count(`SELECT count(*) FROM access.requests WHERE id = $1 AND employee_id = 'intern.anna' AND status = 'granted'
AND decided_at IS NOT NULL AND reason = 'Документация команды' AND idempotency_key = $2::uuid`, got.ID, key); n != 1 {
		t.Fatal("request not stored")
	}
	if n := e.count(`SELECT count(*) FROM approvals.items`); n != 0 {
		t.Fatalf("plain access created %d approval items", n)
	}
	if m := mustCall[statusList](e, anna, "list_required", nil).byID(); m["confluence"].Status != "granted" {
		t.Fatalf("list_required %+v", m["confluence"])
	}
	t.Run("mentor", func(t *testing.T) {
		got := mustCall[result](e, oleg, "request_access", confirmed(t, oleg, newKey(t), resource("space-delivery")))
		if got.Status != "granted" || *got.ChecklistItemID != "CL-05" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("resource without checklist item", func(t *testing.T) {
		e.exec(`INSERT INTO access.resources (id, name, kind) VALUES ('miro', 'Miro', 'tool')`)
		raw, terr := e.call(anna, "request_access", confirmed(t, anna, newKey(t), resource("miro")))
		if terr != nil || !strings.Contains(string(raw), `"checklist_item_id":null`) || !strings.Contains(string(raw), `"approver_id":null`) {
			t.Fatalf("got %s / %v", raw, terr)
		}
	})
}

func TestRequestAccess_S4_ElevatedGoesToLead_R71(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		resource, name, checklist string
	}{
		{"space-delivery-incidents", "Закрытое пространство инцидентов «Доставки»", "CL-05"},
		{"ch-delivery-oncall", "#delivery-oncall", "CL-01"},
	}
	for _, tt := range tests {
		t.Run(tt.resource, func(t *testing.T) {
			got := mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), resource(tt.resource)))
			if got.Status != "pending_approval" || got.ApproverID == nil || *got.ApproverID != "lead.maria" ||
				got.ResourceName != tt.name || *got.ChecklistItemID != tt.checklist {
				t.Fatalf("got %+v", got)
			}
			if n := e.count(`SELECT count(*) FROM approvals.items WHERE kind = 'access_request' AND ref_id = $1
AND requester_id = 'intern.anna' AND approver_id = 'lead.maria' AND status = 'pending' AND summary = $2`,
				got.ID, "Доступ: "+tt.name); n != 1 {
				t.Fatal("approval item for the lead missing")
			}
			if n := e.count(`SELECT count(*) FROM access.requests WHERE id = $1 AND decided_at IS NULL`, got.ID); n != 1 {
				t.Fatal("pending request has decided_at")
			}
		})
	}
}

func TestRequestAccess_S4_OtherTeamForbidden_R71(t *testing.T) {
	e := newEnv(t)
	foreign := e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), resource("space-search")), apierr.CodeForbidden)
	unknown := e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), resource("payroll")), apierr.CodeForbidden)
	if foreign.Message != unknown.Message {
		t.Fatalf("messages differ: %q vs %q", foreign.Message, unknown.Message)
	}
	e.wantCode(ivan, "request_access", confirmed(t, ivan, newKey(t), resource("ch-delivery-oncall")), apierr.CodeForbidden)
	e.wantCode(maria, "request_access", confirmed(t, maria, newKey(t), resource("confluence")), apierr.CodeForbidden) // роль
	if n := e.count(`SELECT count(*) FROM access.requests`); n != 0 {
		t.Fatalf("requests = %d", n)
	}
}

func TestRequestAccess_S4_DuplicateConflict(t *testing.T) {
	e := newEnv(t)
	mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), resource("confluence")))
	terr := e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), resource("confluence")), apierr.CodeConflict)
	if !strings.Contains(terr.Message, "Доступ к ресурсу «Confluence» уже выдан") {
		t.Fatalf("message %q", terr.Message)
	}
	pending := mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), resource("space-delivery-incidents")))
	terr = e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), resource("space-delivery-incidents")), apierr.CodeConflict)
	if !strings.Contains(terr.Message, "уже на согласовании ("+pending.ID+")") {
		t.Fatalf("message %q", terr.Message)
	}
	e.exec(`UPDATE access.requests SET status = 'rejected' WHERE id = $1`, pending.ID)
	again := mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), resource("space-delivery-incidents")))
	if again.ID == pending.ID || again.Status != "pending_approval" {
		t.Fatalf("re-request after rejection %+v", again)
	}
	if m := mustCall[statusList](e, anna, "list_required", nil).byID(); m["space-delivery-incidents"].Status != "pending_approval" {
		t.Fatalf("latest status %+v", m["space-delivery-incidents"])
	}
	// Другой сотрудник может запросить тот же ресурс.
	mustCall[result](e, oleg, "request_access", confirmed(t, oleg, newKey(t), resource("confluence")))
}

func TestRequestAccess_S4_Idempotent_R79(t *testing.T) {
	e := newEnv(t)
	key := newKey(t)
	args := confirmed(t, anna, key, resource("space-delivery-incidents"))
	first := mustCall[result](e, anna, "request_access", args)
	second := mustCall[result](e, anna, "request_access", args)
	if second.ID != first.ID || !second.Replayed || first.Replayed || second.Status != "pending_approval" ||
		second.ResourceName != first.ResourceName || second.CreatedAt != first.CreatedAt {
		t.Fatalf("first %+v, second %+v", first, second)
	}
	if n := e.count(`SELECT count(*) FROM access.requests`); n != 1 {
		t.Fatalf("requests = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items`); n != 1 {
		t.Fatalf("approval items = %d", n)
	}

	withReason := resource("space-delivery-incidents")
	withReason["reason"] = "Дежурства"
	tests := []struct {
		name string
		c    mcpx.Caller
		args map[string]any
	}{
		{"same key, other resource", anna, resource("confluence")},
		{"same key, other reason", anna, withReason},
		{"same key, other employee", oleg, resource("space-delivery-incidents")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terr := e.wantCode(tt.c, "request_access", confirmed(t, tt.c, key, tt.args), apierr.CodeConflict)
			if strings.Contains(terr.Message, "ACC-") {
				t.Fatalf("message leaks the stored request: %q", terr.Message)
			}
		})
	}
	if n := e.count(`SELECT count(*) FROM access.requests`); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}

func TestRequestAccess_RightsMatrix_R71(t *testing.T) {
	e := newEnv(t)
	args := resource("confluence")
	key := newKey(t)
	hash, err := confirm.ArgsHash(args)
	if err != nil {
		t.Fatal(err)
	}
	expiredToken, err := confirm.IssueUntil(confSecret, anna.Sub, key, hash, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	expired := resource("confluence")
	expired["idempotency_key"], expired["confirmation_token"] = key, expiredToken
	tampered := confirmed(t, anna, key, resource("confluence"))
	tampered["resource_id"] = "space-delivery-incidents"
	spoofed := resource("confluence")
	spoofed["employee_id"] = "intern.ivan"

	tests := []struct {
		name string
		args map[string]any
		want apierr.Code
	}{
		{"without token", args, apierr.CodeInvalidConfirmation},
		{"expired token", expired, apierr.CodeInvalidConfirmation},
		{"token of another user", confirmed(t, ivan, key, args), apierr.CodeInvalidConfirmation},
		{"resource changed after confirmation", tampered, apierr.CodeInvalidConfirmation},
		{"employee_id is an unknown field", confirmed(t, anna, key, spoofed), apierr.CodeValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.wantCode(anna, "request_access", tt.args, tt.want)
		})
	}
	if n := e.count(`SELECT count(*) FROM access.requests`); n != 0 {
		t.Fatalf("requests = %d", n)
	}
}

func TestRequestAccess_S4_DirectoryEdgeCases(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO directory.employees (id, display_name, role, team_id) VALUES ('mentor.nolead', 'Тестовый наставник', 'mentor', 'delivery')`)
	nolead := caller("mentor.nolead", "mentor", "delivery")
	terr := e.wantCode(nolead, "request_access", confirmed(t, nolead, newKey(t), resource("space-delivery-incidents")), apierr.CodeValidation)
	if !strings.Contains(terr.Message, "согласующего") {
		t.Fatalf("message %q", terr.Message)
	}
	if got := mustCall[result](e, nolead, "request_access", confirmed(t, nolead, newKey(t), resource("jira"))); got.Status != "granted" {
		t.Fatalf("plain resource %+v", got)
	}
	ghost := caller("intern.ghost", "intern", "delivery")
	e.wantCode(ghost, "request_access", confirmed(t, ghost, newKey(t), resource("jira")), apierr.CodeForbidden)
}

func TestRequestAccess_S4_OneTransaction_R66(t *testing.T) {
	e := newEnv(t)
	e.exec(`CREATE FUNCTION approvals.fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'boom'; END $$;
CREATE TRIGGER fail BEFORE INSERT ON approvals.items FOR EACH ROW EXECUTE FUNCTION approvals.fail()`)
	terr := e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), resource("space-delivery-incidents")), apierr.CodeUnavailable)
	if strings.Contains(terr.Message, "boom") {
		t.Fatalf("internal detail leaked: %q", terr.Message)
	}
	if n := e.count(`SELECT count(*) FROM access.requests`); n != 0 {
		t.Fatalf("request left without approval item: %d", n)
	}
	e.exec(`CREATE TRIGGER fail BEFORE INSERT ON access.requests FOR EACH ROW EXECUTE FUNCTION approvals.fail()`)
	e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), resource("confluence")), apierr.CodeUnavailable)
	e.exec(`ALTER TABLE directory.employees RENAME TO employees_gone`) // поиск в справочнике падает посреди транзакции
	e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), resource("confluence")), apierr.CodeUnavailable)
}

func TestHandlers_DatabaseDown_R79(t *testing.T) {
	pool := pgtest.New(t)
	closed, err := pgxpool.New(context.Background(), pgtest.URL(t, pool))
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	e := &env{t: t, pool: pool, srv: newServer(t, closed)}
	calls := map[string]any{
		"list_required":      nil,
		"list_my_access":     nil,
		"list_team_channels": nil,
		"request_access":     confirmed(t, anna, newKey(t), resource("confluence")),
	}
	for tool, args := range calls {
		t.Run(tool, func(t *testing.T) {
			e.wantCode(anna, tool, args, apierr.CodeUnavailable)
		})
	}
}

func TestRegister_Errors(t *testing.T) {
	pool := pgtest.New(t)
	other, err := mcpx.NewServer("absence", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := mcpx.NewServer("access", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	if Register(nil, pool) == nil || Register(srv, nil) == nil || Register(other, pool) == nil {
		t.Fatal("invalid registration accepted")
	}
}

// TestRoundTrip_S4_HTTP_R71 прогоняет шаги 1-4 S4 через настоящий транспорт с JWT и токенами подтверждения.
func TestRoundTrip_S4_HTTP_R71(t *testing.T) {
	e := newEnv(t)
	mux := http.NewServeMux()
	if err := mcpx.Mount(mux, e.srv); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := &mcpx.Client{BaseURL: ts.URL + "/mcp"}
	ctx := context.Background()
	jwt := func(c mcpx.Caller) string {
		tok, err := authn.Issue(jwtSecret, c.Claims, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	call := func(c mcpx.Caller, tool string, args any) (json.RawMessage, *apierr.Error) {
		t.Helper()
		raw, terr, err := client.Call(ctx, jwt(c), "access", tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return raw, terr
	}

	raw, terr := call(anna, "list_required", nil)
	if terr != nil || !strings.Contains(string(raw), `"resource_id":"space-delivery"`) || strings.Contains(string(raw), "space-search") {
		t.Fatalf("list_required %s / %v", raw, terr)
	}
	plain := confirmed(t, anna, newKey(t), resource("confluence"))
	if raw, terr := call(anna, "request_access", plain); terr != nil || !strings.Contains(string(raw), `"status":"granted"`) {
		t.Fatalf("plain %s / %v", raw, terr)
	}
	if raw, terr := call(anna, "request_access", plain); terr != nil || !strings.Contains(string(raw), `"replayed":true`) {
		t.Fatalf("replay %s / %v", raw, terr)
	}
	elevated := confirmed(t, anna, newKey(t), resource("space-delivery-incidents"))
	if raw, terr := call(anna, "request_access", elevated); terr != nil || !strings.Contains(string(raw), `"approver_id":"lead.maria"`) {
		t.Fatalf("elevated %s / %v", raw, terr)
	}
	if _, terr := call(oleg, "request_access", elevated); terr == nil || terr.Code != apierr.CodeInvalidConfirmation {
		t.Fatalf("foreign confirmation %v", terr)
	}
	if _, terr := call(ivan, "request_access", confirmed(t, ivan, newKey(t), resource("space-delivery"))); terr == nil ||
		terr.Code != apierr.CodeForbidden {
		t.Fatalf("other team %v", terr)
	}
	if raw, terr := call(anna, "list_my_access", nil); terr != nil || strings.Count(string(raw), `"resource_id"`) != 2 {
		t.Fatalf("list_my_access %s / %v", raw, terr)
	}
	if raw, terr := call(anna, "list_team_channels", nil); terr != nil || !strings.Contains(string(raw), `"closed":true`) {
		t.Fatalf("channels %s / %v", raw, terr)
	}
	if n := e.count(`SELECT count(*) FROM access.requests WHERE employee_id = 'intern.anna'`); n != 2 {
		t.Fatalf("requests = %d", n)
	}
}

// Чекпоинт 11.11: заявка называет конкретный ресурс, окружение и права; чтение dev/stage выдаётся сразу, права на
// запись и администрирование и prod уходят руководителю; в таблице видно, кто согласует.
func TestRequestAccess_Checkpoint_EnvironmentAndLevel_R71(t *testing.T) {
	e := newEnv(t)
	with := func(id, env, level string) map[string]any {
		m := resource(id)
		if env != "" {
			m["environment"] = env
		}
		if level != "" {
			m["level"] = level
		}
		return m
	}
	read := mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), with("db-delivery-orders", "stage", "read")))
	if read.Status != "granted" || read.Environment == nil || *read.Environment != "stage" || read.Level != "read" {
		t.Fatalf("read on stage %+v", read)
	}
	write := mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), with("bb-delivery-api", "", "write")))
	if write.Status != "pending_approval" || write.Level != "write" || write.Environment != nil {
		t.Fatalf("write to the repo %+v", write)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE ref_id = $1 AND summary = 'Доступ: Bitbucket: репозиторий delivery-api (редактирование)'`,
		write.ID); n != 1 {
		t.Fatal("approval summary without the level")
	}
	prod := mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), with("db-delivery-orders", "prod", "read")))
	if prod.Status != "pending_approval" {
		t.Fatalf("prod %+v", prod)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE ref_id = $1 AND summary LIKE '%(prod, чтение)'`, prod.ID); n != 1 {
		t.Fatal("approval summary without the environment")
	}
	// то же чтение на stage ещё раз — дубликат; другое окружение — новая заявка
	e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), with("db-delivery-orders", "stage", "read")), apierr.CodeConflict)
	if got := mustCall[result](e, anna, "request_access", confirmed(t, anna, newKey(t), with("db-delivery-orders", "dev", ""))); got.Status != "granted" {
		t.Fatalf("dev %+v", got)
	}
	for _, bad := range []map[string]any{
		with("db-delivery-orders", "", "read"), // базе данных нужно окружение
		with("jira-dlv", "dev", "read"),        // у проекта его нет
		with("confluence", "", "admin"),        // уровень, которого у ресурса нет
	} {
		e.wantCode(anna, "request_access", confirmed(t, anna, newKey(t), bad), apierr.CodeValidation)
	}
	m := mustCall[statusList](e, anna, "list_required", nil).byID()
	db := m["db-delivery-orders"]
	if !db.Optional || !slices.Equal(db.Environments, []string{"dev", "stage", "prod"}) || db.Environment != "dev" ||
		!strings.Contains(db.ApproverLabel, "DevOps") || !slices.Equal(m["jira-dlv"].Levels, []string{"read", "write", "admin"}) {
		t.Fatalf("table rows %+v %+v", db, m["jira-dlv"])
	}
	if scopeLabel(requestIn{Level: "read"}) != "" || deref(nil) != "" {
		t.Fatal("helpers")
	}
}
