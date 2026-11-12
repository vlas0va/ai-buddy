package mcpclient_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/core/mcpclient/mcptest"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
)

var (
	jwtSecret  = []byte("test-jwt-secret-0123456789abcdef")
	hmacSecret = []byte("test-hmac-secret-0123456789abcdef")
)

func token(t *testing.T, sub, role, team string) string {
	t.Helper()
	tok, err := authn.Issue(jwtSecret, authn.Claims{Sub: sub, Role: role, TeamID: team}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func createAbsence(ctx context.Context, c *mcpclient.Client, tok string, args map[string]any, key string) (mcpclient.AbsenceRequest, error) {
	var out mcpclient.AbsenceRequest
	raw, err := c.Execute(ctx, tok, mcpclient.ToolCreateAbsence, args, key)
	if err != nil {
		return out, err
	}
	return out, mcpclient.Decode(raw, &out)
}

func newClient(t *testing.T) (*mcpclient.Client, *mcptest.Server) {
	t.Helper()
	fake := mcptest.New(t, jwtSecret, hmacSecret)
	c, err := mcpclient.New(mcpclient.Config{BaseURL: fake.URL, JWTSecret: jwtSecret, ConfirmationSecret: hmacSecret})
	if err != nil {
		t.Fatal(err)
	}
	return c, fake
}

func TestNew_RequiresSecrets(t *testing.T) {
	if _, err := mcpclient.New(mcpclient.Config{JWTSecret: jwtSecret}); err == nil {
		t.Fatal("want error without confirmation secret")
	}
	if _, err := mcpclient.New(mcpclient.Config{ConfirmationSecret: hmacSecret}); err == nil {
		t.Fatal("want error without JWT secret")
	}
	if _, err := mcpclient.New(mcpclient.Config{JWTSecret: jwtSecret, ConfirmationSecret: hmacSecret, ConfirmationTTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
}

func TestSplitTool(t *testing.T) {
	for _, tc := range []struct {
		in           string
		server, name string
		ok           bool
	}{
		{"absence.create_request", "absence", "create_request", true},
		{"absence", "", "", false},
		{".x", "", "", false},
		{"a.", "", "", false},
		{"a.b.c", "", "", false},
		{"a/b.c", "", "", false},
	} {
		s, n, ok := mcpclient.SplitTool(tc.in)
		if s != tc.server || n != tc.name || ok != tc.ok {
			t.Errorf("SplitTool(%q) = %q %q %v", tc.in, s, n, ok)
		}
	}
}

func TestCreateAbsence_S3_SignsAndReplays_R79(t *testing.T) {
	c, fake := newClient(t)
	ctx := context.Background()
	anna := token(t, "intern.anna", "intern", "delivery")
	args := map[string]any{"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13", "comment": ""}
	key := "6f1c2a52-1d3e-4a6b-9a65-2b8f3c1d0e11"
	first, err := createAbsence(ctx, c, anna, args, key)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.ID == "" || first.Status != "pending_approval" || first.ApproverDisplayName != "Мария Лебедева" || first.Replayed {
		t.Fatalf("unexpected result %+v", first)
	}
	var sent map[string]any
	if err := json.Unmarshal(fake.LastArgs(mcpclient.ToolCreateAbsence), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["idempotency_key"] != key || sent["confirmation_token"] == "" {
		t.Fatalf("missing confirmation fields: %v", sent)
	}
	second, err := createAbsence(ctx, c, anna, args, key)
	if err != nil || !second.Replayed || second.ID != first.ID {
		t.Fatalf("replay: %+v %v", second, err)
	}
}

func TestExecute_Errors_R71(t *testing.T) {
	c, fake := newClient(t)
	ctx := context.Background()
	anna := token(t, "intern.anna", "intern", "delivery")
	key := "6f1c2a52-1d3e-4a6b-9a65-2b8f3c1d0e12"

	t.Run("invalid user token is unauthorized", func(t *testing.T) {
		_, err := c.Execute(ctx, "garbage", mcpclient.ToolCreateAbsence, nil, key)
		if !apierr.HasCode(err, apierr.CodeUnauthorized) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid tool name", func(t *testing.T) {
		_, err := c.Execute(ctx, anna, "nodot", map[string]any{}, key)
		if !apierr.HasCode(err, apierr.CodeInternal) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unsignable args", func(t *testing.T) {
		_, err := c.Execute(ctx, anna, mcpclient.ToolCreateAbsence, map[string]any{"x": func() {}}, key)
		if !apierr.HasCode(err, apierr.CodeInternal) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("tool error is returned as is", func(t *testing.T) {
		fake.FailNext(mcpclient.ToolRequestAccess, apierr.Validation().WithMessage("Неверные даты"))
		_, err := c.Execute(ctx, anna, mcpclient.ToolRequestAccess, map[string]any{"resource_id": "space-delivery"}, key)
		e, ok := apierr.As(err)
		if !ok || e.Code != apierr.CodeValidation || e.Message != "Неверные даты" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("role not allowed is forbidden", func(t *testing.T) {
		lead := token(t, "lead.maria", "lead", "delivery")
		_, err := c.Execute(ctx, lead, mcpclient.ToolRequestAccess, map[string]any{"resource_id": "space-delivery"}, key)
		if !apierr.HasCode(err, apierr.CodeForbidden) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("server down is upstream_unavailable", func(t *testing.T) {
		down, err := mcpclient.New(mcpclient.Config{BaseURL: "http://127.0.0.1:1/mcp", JWTSecret: jwtSecret, ConfirmationSecret: hmacSecret})
		if err != nil {
			t.Fatal(err)
		}
		_, err = down.Execute(ctx, anna, mcpclient.ToolCreateAbsence, map[string]any{}, key)
		if !apierr.HasCode(err, apierr.CodeUpstreamUnavailable) {
			t.Fatalf("got %v", err)
		}
		if _, err := down.ListPendingApprovals(ctx, anna); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestApprovals_S3_ListAndDecide(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()
	anna := token(t, "intern.anna", "intern", "delivery")
	maria := token(t, "lead.maria", "lead", "delivery")
	if _, err := createAbsence(ctx, c, anna, map[string]any{"type": "day_off", "start_date": "2026-11-13", "end_date": "2026-11-13"},
		"6f1c2a52-1d3e-4a6b-9a65-2b8f3c1d0e13"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(ctx, anna, mcpclient.ToolRequestAccess, map[string]any{"resource_id": "space-delivery-incidents", "reason": "дежурства"},
		"6f1c2a52-1d3e-4a6b-9a65-2b8f3c1d0e14"); err != nil {
		t.Fatal(err)
	}
	items, err := c.ListPendingApprovals(ctx, maria)
	if err != nil || len(items) != 2 {
		t.Fatalf("list: %v %v", items, err)
	}
	got, err := c.DecideApproval(ctx, maria, items[0].ID, "approved", "", "6f1c2a52-1d3e-4a6b-9a65-2b8f3c1d0e15")
	if err != nil || got.Status != "approved" {
		t.Fatalf("decide: %+v %v", got, err)
	}
	got, err = c.DecideApproval(ctx, maria, items[1].ID, "rejected", "нет оснований", "6f1c2a52-1d3e-4a6b-9a65-2b8f3c1d0e16")
	if err != nil || got.Status != "rejected" || got.Comment != "нет оснований" {
		t.Fatalf("decide with comment: %+v %v", got, err)
	}
	if _, err := c.ListPendingApprovals(ctx, anna); !apierr.HasCode(err, apierr.CodeForbidden) {
		t.Fatalf("intern list: %v", err)
	}
	empty, err := c.ListPendingApprovals(ctx, maria)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty list: %v %v", empty, err)
	}
}

func TestAddFAQ_S2_MentorOnly(t *testing.T) {
	c, fake := newClient(t)
	ctx := context.Background()
	oleg := token(t, "mentor.oleg", "mentor", "delivery")
	key := "6f1c2a52-1d3e-4a6b-9a65-2b8f3c1d0e17"
	if err := c.AddFAQ(ctx, oleg, "Как получить доступ", "Через сервис доступов", "team", key); err != nil {
		t.Fatal(err)
	}
	if err := c.AddFAQ(ctx, oleg, "Как получить доступ", "Через сервис доступов", "team", key); err != nil {
		t.Fatal(err)
	}
	if fake.FAQs() != 1 {
		t.Fatalf("faqs = %d", fake.FAQs())
	}
	anna := token(t, "intern.anna", "intern", "delivery")
	if err := c.AddFAQ(ctx, anna, "Заголовок", "Текст", "all", key); !apierr.HasCode(err, apierr.CodeForbidden) {
		t.Fatalf("intern add_faq: %v", err)
	}
}

func TestCall_ReadTool(t *testing.T) {
	c, _ := newClient(t)
	anna := token(t, "intern.anna", "intern", "delivery")
	raw, err := c.Call(context.Background(), anna, "absence.get_policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		SourceType string `json:"source_type"`
	}
	if err := mcpclient.Decode(raw, &out); err != nil || out.SourceType != "test" {
		t.Fatalf("%s %v", raw, err)
	}
	if _, err := c.Call(context.Background(), anna, "bad", nil); !apierr.HasCode(err, apierr.CodeInternal) {
		t.Fatalf("got %v", err)
	}
	if err := mcpclient.Decode(json.RawMessage(`{`), &out); !apierr.HasCode(err, apierr.CodeInternal) {
		t.Fatalf("decode: %v", err)
	}
}
