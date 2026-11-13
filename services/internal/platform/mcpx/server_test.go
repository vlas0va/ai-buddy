package mcpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/confirm"
)

func createArgs() map[string]any {
	return map[string]any{"type": "day_off", "start_date": "2026-11-14", "end_date": "2026-11-14", "comment": "Семейные дела"}
}

func confirmed(t *testing.T, sub string, args map[string]any) map[string]any {
	t.Helper()
	out, err := WithConfirmation(confSecret, sub, proposalID, args, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCall_SuccessCarriesCallerFromJWT_R71(t *testing.T) {
	_, client := stack(t, newAbsence(t, nil, &recorder{}))
	tok := jwtFor(t, "intern.anna", "intern", "delivery")
	out, terr, err := client.Call(context.Background(), tok, "absence", "get_policy", nil)
	if err != nil || terr != nil {
		t.Fatalf("err %v tool err %v", err, terr)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	// Claims приходят в обработчик через req.Extra.TokenInfo (Caller) и через контекст запроса.
	if got["text"] != "intern.anna|intern|delivery|2026-11-02|token=true|ctx=intern.anna" || got["doc_id"] != "absence-policy-delivery" {
		t.Fatalf("got %v", got)
	}
}

func TestCall_RoleAllowList_R71(t *testing.T) {
	ts, client := stack(t, newApprovals(t))
	intern := jwtFor(t, "intern.anna", "intern", "delivery")
	lead := jwtFor(t, "lead.maria", "lead", "delivery")
	args := confirmed(t, "intern.anna", map[string]any{"approval_id": "a1", "decision": "approved"})

	_, terr, err := client.Call(context.Background(), intern, "approvals", "decide", args)
	if err != nil || terr == nil || terr.Code != apierr.CodeForbidden {
		t.Fatalf("intern decide: err %v tool err %v", err, terr)
	}
	// Для стажёра не показывается, для руководителя показывается.
	list, err := session(t, ts.URL+"/mcp/approvals", intern).ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 0 {
		t.Fatalf("intern tools/list: %v %v", list, err)
	}
	list, err = session(t, ts.URL+"/mcp/approvals", lead).ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 2 {
		t.Fatalf("lead tools/list: %v %v", list, err)
	}
	leadArgs := confirmed(t, "lead.maria", map[string]any{"approval_id": "a1", "decision": "approved"})
	out, terr, err := client.Call(context.Background(), lead, "approvals", "decide", leadArgs)
	if err != nil || terr != nil || !strings.Contains(string(out), `"status":"approved"`) {
		t.Fatalf("lead decide: %s %v %v", out, terr, err)
	}
}

func TestCall_InputValidation_R71(t *testing.T) {
	_, client := stack(t, newAbsence(t, nil, &recorder{}))
	tok := jwtFor(t, "intern.anna", "intern", "delivery")
	foreign := createArgs()
	foreign["employee_id"] = "intern.boris"
	tests := []struct {
		name string
		tool string
		args any
		want string // подстрока сообщения
	}{
		{"unknown field employee_id", "create_request", confirmed(t, "intern.anna", foreign), "employee_id"},
		{"enum", "validate_request", map[string]any{"type": "sick_leave", "start_date": "2026-11-14", "end_date": "2026-11-14"}, "Некорректные аргументы"},
		{"required", "validate_request", map[string]any{"type": "day_off"}, "Некорректные аргументы"},
		{"wrong type", "get_request", map[string]any{"id": 5}, "Некорректные аргументы"},
		{"date format", "validate_request", map[string]any{"type": "day_off", "start_date": "14.11.2026", "end_date": "2026-11-14"}, "ГГГГ-ММ-ДД"},
		{"uuid format", "create_request", func() map[string]any {
			a := confirmed(t, "intern.anna", createArgs())
			a["idempotency_key"] = "not-a-uuid"
			return a
		}(), "UUID"},
		{"args not an object", "get_request", json.RawMessage(`[1]`), "JSON-объектом"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, terr, err := client.Call(context.Background(), tok, "absence", tt.tool, tt.args)
			if err != nil {
				t.Fatalf("transport: %v", err)
			}
			if terr == nil || terr.Code != apierr.CodeValidation || terr.Retryable || !strings.Contains(terr.Message, tt.want) {
				t.Fatalf("got %+v, want validation_error containing %q", terr, tt.want)
			}
		})
	}
}

func TestCall_WriteConfirmation_R71_R79(t *testing.T) {
	writes := &recorder{}
	_, client := stack(t, newAbsence(t, nil, writes))
	anna := jwtFor(t, "intern.anna", "intern", "delivery")
	hash, _ := confirm.ArgsHash(createArgs())
	with := func(token, key string, args map[string]any) map[string]any {
		out := map[string]any{"confirmation_token": token, "idempotency_key": key}
		for k, v := range args {
			out[k] = v
		}
		return out
	}
	expired, _ := confirm.IssueUntil(confSecret, "intern.anna", proposalID, hash, time.Now().Add(-time.Second))
	foreignSecret, _ := confirm.Issue([]byte("someone-else"), "intern.anna", proposalID, hash, time.Minute)
	borisToken, _ := confirm.Issue(confSecret, "intern.boris", proposalID, hash, time.Minute)
	valid, _ := confirm.Issue(confSecret, "intern.anna", proposalID, hash, time.Minute)
	changed := createArgs()
	changed["end_date"] = "2026-11-20"
	tests := []struct {
		name string
		args map[string]any
	}{
		{"no token", createArgs()},
		{"empty token", with("", proposalID, createArgs())},
		{"no idempotency key", func() map[string]any {
			a := with(valid, proposalID, createArgs())
			delete(a, "idempotency_key")
			return a
		}()},
		{"expired", with(expired, proposalID, createArgs())},
		{"foreign secret", with(foreignSecret, proposalID, createArgs())},
		{"token of another user", with(borisToken, proposalID, createArgs())},
		{"other idempotency key", with(valid, "0b9a3f6e-2c1d-4e5f-8a7b-9c0d1e2f3a4b", createArgs())},
		{"arguments changed after confirmation", with(valid, proposalID, changed)},
		{"garbage token", with("abc.def", proposalID, createArgs())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, terr, err := client.Call(context.Background(), anna, "absence", "create_request", tt.args)
			if err != nil {
				t.Fatalf("transport: %v", err)
			}
			if terr == nil || terr.Code != apierr.CodeInvalidConfirmation {
				t.Fatalf("got %+v, want invalid_confirmation", terr)
			}
		})
	}
	if writes.n() != 0 {
		t.Fatalf("handler ran %d times for rejected writes", writes.n())
	}
	// Корректный токен проходит, и повтор с тем же ключом и токеном тоже проходит: мок воспроизводит результат (R79).
	for i := 0; i < 2; i++ {
		out, terr, err := client.Call(context.Background(), anna, "absence", "create_request", with(valid, proposalID, createArgs()))
		if err != nil || terr != nil || !strings.Contains(string(out), `"id":"ABS-000001"`) {
			t.Fatalf("attempt %d: %s %v %v", i, out, terr, err)
		}
	}
	if writes.n() != 2 {
		t.Fatalf("handler calls %d", writes.n())
	}
	var got map[string]any
	_ = json.Unmarshal(writes.calls[0], &got)
	if got["idempotency_key"] != proposalID || got["comment"] != "Семейные дела" {
		t.Fatalf("handler args %v", got)
	}
}

func TestCall_HandlerErrors(t *testing.T) {
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	_, client := stack(t, newAbsence(t, log, &recorder{}))
	tok := jwtFor(t, "intern.anna", "intern", "delivery")
	day := func(d string) map[string]any {
		return map[string]any{"type": "day_off", "start_date": d, "end_date": d}
	}
	tests := []struct {
		name      string
		tool      string
		args      any
		code      apierr.Code
		retryable bool
		message   string
		logged    string
	}{
		{"apierr forbidden", "get_request", map[string]any{"id": "ABS-999999"}, apierr.CodeForbidden, false, apierr.Forbidden().Message, ""},
		{"apierr with message", "get_request", map[string]any{"id": "ABS-000409"}, apierr.CodeConflict, false, "Заявка уже рассмотрена", ""},
		{"api-only code mapped", "get_request", map[string]any{"id": "ABS-000429"}, apierr.CodeUnavailable, true, apierr.RateLimited().Message, ""},
		{"wrapped apierr", "get_request", map[string]any{"id": "ABS-000404"}, apierr.CodeNotFound, false, apierr.NotFound().Message, ""},
		{"context canceled", "get_request", map[string]any{"id": "ABS-000499"}, apierr.CodeUnavailable, false, apierr.Internal().Message, "context canceled"},
		{"plain error is hidden", "list_my_requests", map[string]any{"status": "cancelled"}, apierr.CodeUnavailable, false, apierr.Internal().Message, secretErr},
		{"panic", "validate_request", day("2026-01-01"), apierr.CodeUnavailable, false, apierr.Internal().Message, "boom"},
		{"output violates schema", "validate_request", day("2026-01-02"), apierr.CodeUnavailable, false, apierr.Internal().Message, "outputSchema"},
		{"output not an object", "validate_request", day("2026-01-03"), apierr.CodeUnavailable, false, apierr.Internal().Message, "not a JSON object"},
		{"output not encodable", "validate_request", day("2026-01-04"), apierr.CodeUnavailable, false, apierr.Internal().Message, "encode result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, terr, err := client.Call(context.Background(), tok, "absence", tt.tool, tt.args)
			if err != nil {
				t.Fatalf("transport: %v", err)
			}
			if terr == nil || terr.Code != tt.code || terr.Retryable != tt.retryable || terr.Message != tt.message {
				t.Fatalf("got %+v", terr)
			}
			if strings.Contains(terr.Message, secretErr) {
				t.Fatal("internal detail leaked")
			}
			if tt.logged != "" && !strings.Contains(logs.String(), tt.logged) {
				t.Fatalf("log lacks %q: %s", tt.logged, logs.String())
			}
		})
	}
	out, terr, err := client.Call(context.Background(), tok, "absence", "validate_request", day("2026-11-16"))
	if err != nil || terr != nil || string(out) != `{"problems":[],"valid":true}` && string(out) != `{"valid":true,"problems":[]}` {
		t.Fatalf("valid output: %s %v %v", out, terr, err)
	}
}

func TestDefaultsApplied(t *testing.T) {
	s, err := NewServer("kb", Config{JWTSecret: jwtSecret, ConfirmationSecret: confSecret})
	if err != nil {
		t.Fatal(err)
	}
	var gotK float64
	s.Handle("search", func(_ context.Context, _ Caller, args json.RawMessage) (any, error) {
		var in struct {
			K float64 `json:"k"`
		}
		_ = DecodeArgs(args, &in)
		gotK = in.K
		return map[string]any{"items": []any{}}, nil
	})
	s.Handle("add_faq", func(context.Context, Caller, json.RawMessage) (any, error) { return map[string]any{}, nil })
	c := Caller{}
	c.Sub, c.Role, c.TeamID = "intern.anna", "intern", "delivery"
	if _, terr := s.Invoke(context.Background(), c, "search", map[string]any{"query": "отгул"}); terr != nil {
		t.Fatal(terr)
	}
	if gotK != 5 {
		t.Fatalf("default k not applied: %v", gotK)
	}
}

func TestInvoke(t *testing.T) {
	s := newAbsence(t, nil, &recorder{})
	c := Caller{}
	c.Sub, c.Role, c.TeamID = "intern.anna", "intern", "delivery"
	if out, terr := s.Invoke(context.Background(), c, "get_request", map[string]any{"id": "ABS-000001"}); terr != nil || !strings.Contains(string(out), "ABS-000001") {
		t.Fatalf("invoke: %s %v", out, terr)
	}
	if _, terr := s.Invoke(context.Background(), c, "nope", nil); terr == nil || terr.Code != apierr.CodeNotFound {
		t.Fatalf("unknown tool: %v", terr)
	}
	if _, terr := s.Invoke(context.Background(), c, "get_request", map[string]any{"x": make(chan int)}); terr == nil || terr.Code != apierr.CodeValidation {
		t.Fatalf("unencodable args: %v", terr)
	}
	if _, terr := s.Invoke(context.Background(), c, "get_policy", nil); terr != nil {
		t.Fatalf("nil args: %v", terr)
	}
	if _, terr := s.Invoke(context.Background(), c, "get_policy", json.RawMessage(`{"x":`)); terr == nil {
		t.Fatal("broken JSON args accepted")
	}
	c.Role = "hrbp"
	if _, terr := s.Invoke(context.Background(), c, "get_policy", nil); terr == nil || terr.Code != apierr.CodeForbidden {
		t.Fatalf("hrbp: %v", terr)
	}
	if got := s.Name(); got != "absence" {
		t.Fatal(got)
	}
	names := []string{}
	for _, d := range s.Tools() {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"get_policy", "list_my_requests", "validate_request", "create_request", "get_request", "reschedule_request", "cancel_request"}) {
		t.Fatalf("tools %v", names)
	}
}

func TestDecodeArgsAndWithConfirmation(t *testing.T) {
	var dst struct {
		N int `json:"n"`
	}
	if err := DecodeArgs(json.RawMessage(`{"n":"x"}`), &dst); !apierr.HasCode(err, apierr.CodeValidation) {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if _, err := WithConfirmation(nil, "a", proposalID, map[string]any{}, time.Minute); err == nil {
		t.Fatal("empty secret accepted")
	}
	if _, err := WithConfirmation(confSecret, "a", proposalID, map[string]any{"x": make(chan int)}, time.Minute); err == nil {
		t.Fatal("unencodable args accepted")
	}
	in := map[string]any{"approval_id": "a1"}
	out, err := WithConfirmation(confSecret, "lead.maria", proposalID, in, time.Minute)
	if err != nil || len(in) != 1 || out["idempotency_key"] != proposalID {
		t.Fatalf("WithConfirmation must copy: %v %v", out, err)
	}
	if err := confirm.Verify(confSecret, out["confirmation_token"].(string), "lead.maria", proposalID, out, time.Now()); err != nil {
		t.Fatalf("token does not verify: %v", err)
	}
}
