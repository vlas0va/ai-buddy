package approvals

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
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aibuddy/services/internal/mocks/absence"
	"aibuddy/services/internal/mocks/access"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/confirm"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pgtest"
)

func TestListPending_S3_S4_OnlyCallerAsApprover_R71(t *testing.T) {
	e := newEnv(t)
	absID, absItem := e.seedAbsence("intern.anna", "lead.maria")
	_, accItem := e.seedAccess("intern.anna", "lead.maria", "space-delivery-incidents")
	_, ivanItem := e.seedAbsence("intern.ivan", "lead.dmitry")
	_, decided := e.seedAbsence("mentor.oleg", "lead.maria")
	e.exec(`UPDATE approvals.items SET status = 'approved' WHERE id = $1`, decided)

	got := mustCall[listResult](e, maria, "list_pending", nil)
	var ids []string
	for _, it := range got.Items {
		ids = append(ids, it.ID)
	}
	if !slices.Equal(ids, []string{absItem, accItem}) {
		t.Fatalf("maria sees %v, want %v", ids, []string{absItem, accItem})
	}
	first := got.Items[0]
	if first.Kind != "absence_request" || first.RefID != absID || first.RequesterID != "intern.anna" ||
		first.RequesterDisplayName != "Анна Котова" || first.Summary != "Отгул 10.11.2026" || first.Status != "pending" ||
		first.Replayed != nil {
		t.Fatalf("item %+v", first)
	}
	if _, err := time.Parse(time.RFC3339, first.CreatedAt); err != nil {
		t.Fatalf("created_at %q", first.CreatedAt)
	}
	if got.Items[1].Summary != "Доступ: Закрытое пространство инцидентов «Доставки»" {
		t.Fatalf("access summary %q", got.Items[1].Summary)
	}
	other := mustCall[listResult](e, dmitry, "list_pending", map[string]any{})
	if len(other.Items) != 1 || other.Items[0].ID != ivanItem {
		t.Fatalf("dmitry sees %+v", other.Items)
	}
	raw, terr := e.call(caller("lead.nobody", "lead", "delivery"), "list_pending", nil)
	if terr != nil || string(raw) != `{"items":[]}` {
		t.Fatalf("empty list %s / %v", raw, terr)
	}
	for _, c := range []mcpx.Caller{anna, oleg} {
		e.wantCode(c, "list_pending", nil, apierr.CodeForbidden)
	}
}

func TestDecide_S3_DecisionPropagatesToAbsence_R66(t *testing.T) {
	tests := []struct {
		decision, comment, wantRequest string
	}{
		{"approved", "Хорошего отдыха", "approved"},
		{"rejected", "", "rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.decision, func(t *testing.T) {
			e := newEnv(t)
			absID, itemID := e.seedAbsence("intern.anna", "lead.maria")
			args := decision(itemID, tt.decision)
			if tt.comment != "" {
				args["comment"] = tt.comment
			}
			got := mustCall[itemOut](e, maria, "decide", confirmed(t, maria, newKey(t), args))
			if got.ID != itemID || got.Status != tt.decision || got.Comment != tt.comment || got.RefID != absID ||
				got.Replayed == nil || *got.Replayed || got.RequesterDisplayName != "Анна Котова" {
				t.Fatalf("got %+v", got)
			}
			if n := e.count(`SELECT count(*) FROM absence.requests WHERE id = $1 AND status = $2 AND decided_at IS NOT NULL`,
				absID, tt.wantRequest); n != 1 {
				t.Fatalf("absence request not %s", tt.wantRequest)
			}
			if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = $2 AND decided_at IS NOT NULL
AND coalesce(comment, '') = $3`, itemID, tt.decision, tt.comment); n != 1 {
				t.Fatal("approval item not updated")
			}
			if len(mustCall[listResult](e, maria, "list_pending", nil).Items) != 0 {
				t.Fatal("decided item still pending")
			}
		})
	}
}

func TestDecide_S4_DecisionPropagatesToAccess_R66(t *testing.T) {
	for decisionValue, want := range map[string]string{"approved": "granted", "rejected": "rejected"} {
		t.Run(decisionValue, func(t *testing.T) {
			e := newEnv(t)
			accID, itemID := e.seedAccess("intern.anna", "lead.maria", "space-delivery-incidents")
			got := mustCall[itemOut](e, maria, "decide", confirmed(t, maria, newKey(t), decision(itemID, decisionValue)))
			if got.Kind != "access_request" || got.Status != decisionValue {
				t.Fatalf("got %+v", got)
			}
			if n := e.count(`SELECT count(*) FROM access.requests WHERE id = $1 AND status = $2 AND decided_at IS NOT NULL`,
				accID, want); n != 1 {
				t.Fatalf("access request not %s", want)
			}
		})
	}
}

func TestDecide_ForeignTeam_R71(t *testing.T) {
	e := newEnv(t)
	absID, itemID := e.seedAbsence("intern.anna", "lead.maria")
	foreign := e.wantCode(dmitry, "decide", confirmed(t, dmitry, newKey(t), decision(itemID, "approved")), apierr.CodeForbidden)
	missing := e.wantCode(dmitry, "decide", confirmed(t, dmitry, newKey(t), decision("APR-00000000", "approved")), apierr.CodeForbidden)
	if foreign.Message != missing.Message {
		t.Fatalf("foreign %q and unknown %q differ: existence leaks", foreign.Message, missing.Message)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = 'pending'`, itemID); n != 1 {
		t.Fatal("foreign lead changed the item")
	}
	if n := e.count(`SELECT count(*) FROM absence.requests WHERE id = $1 AND status = 'pending_approval'`, absID); n != 1 {
		t.Fatal("foreign lead changed the request")
	}
}

func TestDecide_NonLeadRoles_R71(t *testing.T) {
	e := newEnv(t)
	_, itemID := e.seedAbsence("intern.anna", "lead.maria")
	for _, c := range []mcpx.Caller{anna, oleg, caller("hrbp.elena", "hrbp", "people")} {
		t.Run(c.Role, func(t *testing.T) {
			e.wantCode(c, "decide", confirmed(t, c, newKey(t), decision(itemID, "approved")), apierr.CodeForbidden)
		})
	}
	// A JWT claiming the approver's id with the intern role is still refused by the role allow-list.
	spoof := caller("lead.maria", "intern", "delivery")
	e.wantCode(spoof, "decide", confirmed(t, spoof, newKey(t), decision(itemID, "approved")), apierr.CodeForbidden)
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE status = 'pending'`); n != 1 {
		t.Fatal("item decided by a non-lead")
	}
}

func TestDecide_Confirmation_R71(t *testing.T) {
	e := newEnv(t)
	_, itemID := e.seedAbsence("intern.anna", "lead.maria")
	args := decision(itemID, "approved")
	key := newKey(t)
	hash, err := confirm.ArgsHash(args)
	if err != nil {
		t.Fatal(err)
	}
	expiredToken, err := confirm.IssueUntil(confSecret, maria.Sub, key, hash, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	expired := decision(itemID, "approved")
	expired["idempotency_key"], expired["confirmation_token"] = key, expiredToken
	tampered := confirmed(t, maria, key, decision(itemID, "rejected"))
	tampered["decision"] = "approved"
	extra := decision(itemID, "approved")
	extra["approver_id"] = "lead.maria"

	tests := []struct {
		name string
		args map[string]any
		want apierr.Code
	}{
		{"without token", args, apierr.CodeInvalidConfirmation},
		{"expired token", expired, apierr.CodeInvalidConfirmation},
		{"token of another lead", confirmed(t, dmitry, key, args), apierr.CodeInvalidConfirmation},
		{"decision changed after confirmation", tampered, apierr.CodeInvalidConfirmation},
		{"unknown field approver_id", confirmed(t, maria, key, extra), apierr.CodeValidation},
		{"bad decision value", confirmed(t, maria, key, decision(itemID, "maybe")), apierr.CodeValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.wantCode(maria, "decide", tt.args, tt.want)
		})
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = 'pending'`, itemID); n != 1 {
		t.Fatal("item decided without a valid confirmation")
	}
}

func TestDecide_S3_Idempotent_R79(t *testing.T) {
	e := newEnv(t)
	absID, itemID := e.seedAbsence("intern.anna", "lead.maria")
	key := newKey(t)
	args := decision(itemID, "approved")
	args["comment"] = "Ок"
	first := mustCall[itemOut](e, maria, "decide", confirmed(t, maria, key, args))
	decidedAt := e.str(`SELECT decided_at::text FROM approvals.items WHERE id = $1`, itemID)
	second := mustCall[itemOut](e, maria, "decide", confirmed(t, maria, key, args))
	if second.ID != first.ID || second.Status != "approved" || second.Comment != "Ок" || second.Replayed == nil || !*second.Replayed {
		t.Fatalf("first %+v, second %+v", first, second)
	}
	if again := e.str(`SELECT decided_at::text FROM approvals.items WHERE id = $1`, itemID); again != decidedAt {
		t.Fatalf("replay wrote again: %s → %s", decidedAt, again)
	}
	if n := e.count(`SELECT count(*) FROM absence.requests WHERE id = $1 AND status = 'approved'`, absID); n != 1 {
		t.Fatal("request not approved")
	}
}

func TestDecide_S3_SameKeyDifferentArgs_R79(t *testing.T) {
	e := newEnv(t)
	_, itemID := e.seedAbsence("intern.anna", "lead.maria")
	_, otherItem := e.seedAbsence("mentor.oleg", "lead.maria")
	key := newKey(t)
	mustCall[itemOut](e, maria, "decide", confirmed(t, maria, key, decision(itemID, "approved")))

	t.Run("other decision", func(t *testing.T) {
		terr := e.wantCode(maria, "decide", confirmed(t, maria, key, decision(itemID, "rejected")), apierr.CodeConflict)
		if !strings.Contains(terr.Message, "Ключ идемпотентности") {
			t.Fatalf("message %q", terr.Message)
		}
	})
	t.Run("other comment", func(t *testing.T) {
		args := decision(itemID, "approved")
		args["comment"] = "передумала"
		e.wantCode(maria, "decide", confirmed(t, maria, key, args), apierr.CodeConflict)
	})
	t.Run("other item", func(t *testing.T) {
		e.wantCode(maria, "decide", confirmed(t, maria, key, decision(otherItem, "approved")), apierr.CodeConflict)
		if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = 'pending' AND idempotency_key IS NULL`,
			otherItem); n != 1 {
			t.Fatal("other item changed")
		}
	})
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = 'approved'`, itemID); n != 1 {
		t.Fatal("first decision lost")
	}
}

func TestDecide_S3_AlreadyDecided_Conflict(t *testing.T) {
	e := newEnv(t)
	_, itemID := e.seedAbsence("intern.anna", "lead.maria")
	mustCall[itemOut](e, maria, "decide", confirmed(t, maria, newKey(t), decision(itemID, "rejected")))
	terr := e.wantCode(maria, "decide", confirmed(t, maria, newKey(t), decision(itemID, "approved")), apierr.CodeConflict)
	if !strings.Contains(terr.Message, "уже рассмотрен") {
		t.Fatalf("message %q", terr.Message)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = 'rejected'`, itemID); n != 1 {
		t.Fatal("decision overwritten")
	}
}

func TestDecide_S3_RequestNoLongerPending_RollsBack(t *testing.T) {
	e := newEnv(t)
	absID, itemID := e.seedAbsence("intern.anna", "lead.maria")
	e.exec(`UPDATE absence.requests SET status = 'cancelled' WHERE id = $1`, absID)
	terr := e.wantCode(maria, "decide", confirmed(t, maria, newKey(t), decision(itemID, "approved")), apierr.CodeConflict)
	if !strings.Contains(terr.Message, "не ожидает согласования") {
		t.Fatalf("message %q", terr.Message)
	}
	if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = 'pending' AND idempotency_key IS NULL`,
		itemID); n != 1 {
		t.Fatal("item update not rolled back")
	}
}

func TestHandlers_DatabaseDown_R79(t *testing.T) {
	e := newEnv(t)
	_, itemID := e.seedAbsence("intern.anna", "lead.maria")
	closed, err := pgxpool.New(context.Background(), pgtest.URL(t, e.pool))
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	down := &env{t: t, pool: e.pool, srv: newServer(t, "approvals", closed, Register)}
	down.wantCode(maria, "list_pending", nil, apierr.CodeUnavailable)
	down.wantCode(maria, "decide", confirmed(t, maria, newKey(t), decision(itemID, "approved")), apierr.CodeUnavailable)

	t.Run("failing request update keeps the item pending", func(t *testing.T) {
		e.exec(`CREATE FUNCTION absence.fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'boom'; END $$;
CREATE TRIGGER fail BEFORE UPDATE ON absence.requests FOR EACH ROW EXECUTE FUNCTION absence.fail()`)
		terr := e.wantCode(maria, "decide", confirmed(t, maria, newKey(t), decision(itemID, "approved")), apierr.CodeUnavailable)
		if strings.Contains(terr.Message, "boom") {
			t.Fatalf("internal detail leaked: %q", terr.Message)
		}
		if n := e.count(`SELECT count(*) FROM approvals.items WHERE id = $1 AND status = 'pending'`, itemID); n != 1 {
			t.Fatal("item update not rolled back")
		}
	})
	t.Run("failing item update", func(t *testing.T) {
		e.exec(`CREATE TRIGGER fail BEFORE UPDATE ON approvals.items FOR EACH ROW EXECUTE FUNCTION absence.fail()`)
		e.wantCode(maria, "decide", confirmed(t, maria, newKey(t), decision(itemID, "approved")), apierr.CodeUnavailable)
	})
}

func TestRegister_Errors(t *testing.T) {
	pool := pgtest.New(t)
	other, err := mcpx.NewServer("absence", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	if Register(nil, pool) == nil {
		t.Fatal("nil server accepted")
	}
	srv, err := mcpx.NewServer("approvals", mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	if Register(srv, nil) == nil {
		t.Fatal("nil pool accepted")
	}
	if Register(other, pool) == nil {
		t.Fatal("foreign server accepted")
	}
}

// bearer adds a JWT to every request of a raw go-sdk client session.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// nextWorkday is a weekday at least two days ahead in Moscow time (the absence server runs on the real clock here).
func nextWorkday() string {
	y, m, d := time.Now().In(time.FixedZone("MSK", 3*60*60)).Date()
	day := time.Date(y, m, d+2, 0, 0, 0, 0, time.UTC)
	for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		day = day.AddDate(0, 0, 1)
	}
	return day.Format(time.DateOnly)
}

// TestRoundTrip_S3_S4_HTTP_R66_R71 runs S3 step 8 and S4 over HTTP: an intern creates a day-off request and an
// elevated access request, the lead lists and decides them with confirmation tokens, the intern sees the result.
func TestRoundTrip_S3_S4_HTTP_R66_R71(t *testing.T) {
	pool := pgtest.New(t)
	mux := http.NewServeMux()
	if err := mcpx.Mount(mux,
		newServer(t, "approvals", pool, Register),
		newServer(t, "absence", pool, absence.Register),
		newServer(t, "access", pool, access.Register)); err != nil {
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
	call := func(c mcpx.Caller, server, tool string, args any) string {
		t.Helper()
		raw, terr, err := client.Call(ctx, jwt(c), server, tool, args)
		if err != nil || terr != nil {
			t.Fatalf("%s.%s as %s: %v / %v", server, tool, c.Sub, terr, err)
		}
		return string(raw)
	}

	day := nextWorkday()
	created := call(anna, "absence", "create_request", confirmed(t, anna, newKey(t),
		map[string]any{"type": "day_off", "start_date": day, "end_date": day}))
	accessOut := call(anna, "access", "request_access", confirmed(t, anna, newKey(t),
		map[string]any{"resource_id": "space-delivery-incidents"}))
	if !strings.Contains(created, `"status":"pending_approval"`) || !strings.Contains(accessOut, `"status":"pending_approval"`) {
		t.Fatalf("created %s, access %s", created, accessOut)
	}

	var pending listResult
	raw, terr, err := client.Call(ctx, jwt(maria), "approvals", "list_pending", nil)
	if err != nil || terr != nil {
		t.Fatalf("list_pending: %v / %v", terr, err)
	}
	if err := json.Unmarshal(raw, &pending); err != nil || len(pending.Items) != 2 {
		t.Fatalf("pending %s", raw)
	}
	for _, it := range pending.Items {
		// Dmitry (another team) cannot decide, and the token minted for Maria does not work for him.
		args := confirmed(t, maria, newKey(t), decision(it.ID, "approved"))
		if _, terr, err := client.Call(ctx, jwt(dmitry), "approvals", "decide", args); err != nil || terr == nil ||
			terr.Code != apierr.CodeInvalidConfirmation {
			t.Fatalf("foreign lead with maria's token: %v / %v", terr, err)
		}
		if _, terr, err := client.Call(ctx, jwt(dmitry), "approvals", "decide",
			confirmed(t, dmitry, newKey(t), decision(it.ID, "approved"))); err != nil || terr == nil || terr.Code != apierr.CodeForbidden {
			t.Fatalf("foreign lead: %v / %v", terr, err)
		}
		if _, terr, err := client.Call(ctx, jwt(anna), "approvals", "decide", args); err != nil || terr == nil ||
			terr.Code != apierr.CodeForbidden {
			t.Fatalf("intern decide: %v / %v", terr, err)
		}
		out := call(maria, "approvals", "decide", args)
		if !strings.Contains(out, `"status":"approved"`) || !strings.Contains(out, `"replayed":false`) {
			t.Fatalf("decide %s", out)
		}
		if again := call(maria, "approvals", "decide", args); !strings.Contains(again, `"replayed":true`) {
			t.Fatalf("replay %s", again)
		}
	}
	absID := pending.Items[0].RefID
	if pending.Items[0].Kind != "absence_request" {
		absID = pending.Items[1].RefID
	}
	if got := call(anna, "absence", "get_request", map[string]any{"id": absID}); !strings.Contains(got, `"status":"approved"`) {
		t.Fatalf("absence after decision %s", got)
	}
	if got := call(anna, "access", "list_my_access", nil); !strings.Contains(got, `"resource_id":"space-delivery-incidents"`) ||
		!strings.Contains(got, `"status":"granted"`) {
		t.Fatalf("access after decision %s", got)
	}

	t.Run("tools/list hides decide from an intern", func(t *testing.T) {
		names := func(c mcpx.Caller) []string {
			hc := &http.Client{Transport: bearer{token: jwt(c)}}
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx,
				&mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp/approvals", HTTPClient: hc, MaxRetries: -1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			res, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, tool := range res.Tools {
				out = append(out, tool.Name)
			}
			slices.Sort(out)
			return out
		}
		if got := names(anna); len(got) != 0 {
			t.Fatalf("intern sees %v", got)
		}
		if got := names(maria); !slices.Equal(got, []string{"decide", "list_pending"}) {
			t.Fatalf("lead sees %v", got)
		}
	})
}
