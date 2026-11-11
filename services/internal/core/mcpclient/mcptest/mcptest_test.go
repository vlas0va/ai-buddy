package mcptest_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/core/mcpclient/mcptest"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/mcpx"
)

var (
	jwtSecret  = []byte("test-jwt-secret-0123456789abcdef")
	hmacSecret = []byte("test-hmac-secret-0123456789abcdef")
)

type caller struct {
	sub, role, team string
}

func call(t *testing.T, fake *mcptest.Server, who caller, tool string, args map[string]any, key string) (json.RawMessage, *apierr.Error) {
	t.Helper()
	tok, err := authn.Issue(jwtSecret, authn.Claims{Sub: who.sub, Role: who.role, TeamID: who.team}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		if args, err = mcpx.WithConfirmation(hmacSecret, who.sub, key, args, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	server, name, _ := strings.Cut(tool, ".")
	out, toolErr, err := (&mcpx.Client{BaseURL: fake.URL}).Call(context.Background(), tok, server, name, args)
	if err != nil {
		t.Fatalf("%s: transport: %v", tool, err)
	}
	return out, toolErr
}

func ok(t *testing.T) func(json.RawMessage, *apierr.Error) map[string]any {
	return func(raw json.RawMessage, e *apierr.Error) map[string]any {
		t.Helper()
		if e != nil {
			t.Fatalf("tool error: %v", e)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
}

func TestFakeServers_ReadAndWriteTools(t *testing.T) {
	fake := mcptest.New(t, jwtSecret, hmacSecret)
	anna := caller{"intern.anna", "intern", "delivery"}
	ivan := caller{"intern.ivan", "intern", "search"}
	maria := caller{"lead.maria", "lead", "delivery"}
	oleg := caller{"mentor.oleg", "mentor", "delivery"}

	ok(t)(call(t, fake, anna, "absence.get_policy", nil, ""))
	ok(t)(call(t, fake, anna, "absence.validate_request", map[string]any{"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13"}, ""))
	if _, e := call(t, fake, anna, "absence.create_request",
		map[string]any{"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-12"}, "11111111-1111-4111-8111-111111111110"); e == nil || e.Code != apierr.CodeValidation {
		t.Fatalf("end before start: %v", e)
	}
	created := ok(t)(call(t, fake, anna, "absence.create_request",
		map[string]any{"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13"}, "11111111-1111-4111-8111-111111111111"))
	id := created["id"].(string)
	if fake.AbsenceStatus(id) != "pending_approval" || fake.AbsenceStatus("ABS-999999") != "" {
		t.Fatal("absence status")
	}
	mine := ok(t)(call(t, fake, anna, "absence.list_my_requests", nil, ""))
	if len(mine["items"].([]any)) != 1 {
		t.Fatalf("list_my_requests: %v", mine)
	}
	ok(t)(call(t, fake, anna, "absence.get_request", map[string]any{"id": id}, ""))
	if _, e := call(t, fake, ivan, "absence.get_request", map[string]any{"id": id}, ""); e == nil || e.Code != apierr.CodeForbidden {
		t.Fatalf("foreign get_request: %v", e)
	}

	req := ok(t)(call(t, fake, anna, "access.list_required", nil, ""))
	if len(req["items"].([]any)) != 4 {
		t.Fatalf("list_required: %v", req)
	}
	ok(t)(call(t, fake, anna, "access.list_team_channels", nil, ""))
	if _, e := call(t, fake, anna, "access.request_access", map[string]any{"resource_id": "space-search"}, "11111111-1111-4111-8111-111111111112"); e == nil || e.Code != apierr.CodeForbidden {
		t.Fatalf("foreign team resource: %v", e)
	}
	granted := ok(t)(call(t, fake, anna, "access.request_access", map[string]any{"resource_id": "space-delivery"}, "11111111-1111-4111-8111-111111111113"))
	if granted["status"] != "granted" {
		t.Fatalf("granted: %v", granted)
	}
	replay := ok(t)(call(t, fake, anna, "access.request_access", map[string]any{"resource_id": "space-delivery"}, "11111111-1111-4111-8111-111111111113"))
	if replay["replayed"] != true {
		t.Fatalf("replay: %v", replay)
	}
	elevated := ok(t)(call(t, fake, anna, "access.request_access", map[string]any{"resource_id": "space-delivery-incidents"}, "11111111-1111-4111-8111-111111111114"))
	if elevated["status"] != "pending_approval" {
		t.Fatalf("elevated: %v", elevated)
	}
	my := ok(t)(call(t, fake, anna, "access.list_my_access", nil, ""))
	if len(my["items"].([]any)) != 2 {
		t.Fatalf("list_my_access: %v", my)
	}

	pending := ok(t)(call(t, fake, maria, "approvals.list_pending", nil, ""))
	items := pending["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("pending: %v", pending)
	}
	accessApproval := items[1].(map[string]any)["id"].(string)
	if _, e := call(t, fake, caller{"lead.dmitry", "lead", "search"}, "approvals.decide",
		map[string]any{"approval_id": accessApproval, "decision": "approved"}, "11111111-1111-4111-8111-111111111115"); e == nil || e.Code != apierr.CodeForbidden {
		t.Fatalf("foreign lead decide: %v", e)
	}
	ok(t)(call(t, fake, maria, "approvals.decide", map[string]any{"approval_id": accessApproval, "decision": "approved"}, "11111111-1111-4111-8111-111111111116"))
	again := ok(t)(call(t, fake, maria, "approvals.decide", map[string]any{"approval_id": accessApproval, "decision": "approved"}, "11111111-1111-4111-8111-111111111116"))
	if again["replayed"] != true {
		t.Fatalf("decide replay: %v", again)
	}
	if _, e := call(t, fake, maria, "approvals.decide", map[string]any{"approval_id": accessApproval, "decision": "rejected"}, "11111111-1111-4111-8111-111111111117"); e == nil || e.Code != apierr.CodeConflict {
		t.Fatalf("decide twice: %v", e)
	}

	ok(t)(call(t, fake, oleg, "kb.search", map[string]any{"query": "доступ"}, ""))
	ok(t)(call(t, fake, oleg, "kb.add_faq", map[string]any{"title": "Вопрос", "body": "Ответ"}, "11111111-1111-4111-8111-111111111118"))
	if fake.FAQs() != 1 {
		t.Fatal("faq not stored")
	}

	fake.FailNext("absence.get_policy", apierr.Unavailable())
	if _, e := call(t, fake, anna, "absence.get_policy", nil, ""); e == nil || e.Code != apierr.CodeUnavailable {
		t.Fatalf("queued failure: %v", e)
	}
	if fake.Calls("absence.get_policy") != 2 || fake.Requests() == 0 {
		t.Fatalf("counters: %d %d", fake.Calls("absence.get_policy"), fake.Requests())
	}
}
