package apierr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"unicode"
)

func TestConstructors_DefaultsPerCode(t *testing.T) {
	tests := []struct {
		e         *Error
		code      Code
		status    int
		retryable bool
	}{
		{Unauthorized(), CodeUnauthorized, 401, false},
		{Forbidden(), CodeForbidden, 403, false},
		{NotFound(), CodeNotFound, 404, false},
		{Validation(), CodeValidation, 400, false},
		{Conflict(), CodeConflict, 409, false},
		{Expired(), CodeExpired, 409, false},
		{RateLimited(), CodeRateLimited, 429, true},
		{UpstreamUnavailable(), CodeUpstreamUnavailable, 502, true},
		{Internal(), CodeInternal, 500, false},
		{InvalidConfirmation(), CodeInvalidConfirmation, 403, false},
		{Unavailable(), CodeUnavailable, 503, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			if tt.e.Code != tt.code || tt.e.Status != tt.status || tt.e.Retryable != tt.retryable {
				t.Fatalf("got %+v", tt.e)
			}
			if !hasCyrillic(tt.e.Message) {
				t.Fatalf("default message must be Russian: %q", tt.e.Message)
			}
		})
	}
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

func TestNew_MessageAndUnknownCode(t *testing.T) {
	e := New(CodeValidation, "Дата окончания раньше даты начала")
	if e.Message != "Дата окончания раньше даты начала" || e.Status != 400 {
		t.Fatalf("got %+v", e)
	}
	u := New(Code("weird"), "")
	if u.Status != 500 || u.Retryable || u.Message != Internal().Message {
		t.Fatalf("unknown code: %+v", u)
	}
}

func TestWithMessage_Copies(t *testing.T) {
	base := Validation()
	e := base.WithMessage("Укажите дату")
	if e == base || base.Message == "Укажите дату" || e.Message != "Укажите дату" || e.Code != CodeValidation {
		t.Fatalf("WithMessage must copy: base %+v, e %+v", base, e)
	}
}

func TestErrorIsAsFromHasCode(t *testing.T) {
	wrapped := fmt.Errorf("loading request: %w", Forbidden())
	if !errors.Is(wrapped, Forbidden()) {
		t.Fatal("errors.Is by code must match")
	}
	if errors.Is(wrapped, NotFound()) {
		t.Fatal("different code must not match")
	}
	if errors.Is(Forbidden(), errors.New("x")) {
		t.Fatal("non-apierr target must not match")
	}
	if e, ok := As(wrapped); !ok || e.Code != CodeForbidden {
		t.Fatal("As must find wrapped error")
	}
	if _, ok := As(errors.New("plain")); ok {
		t.Fatal("As on plain error")
	}
	if !HasCode(wrapped, CodeForbidden) || HasCode(wrapped, CodeConflict) || HasCode(nil, CodeForbidden) {
		t.Fatal("HasCode")
	}
	if Forbidden().Error() != "forbidden: "+Forbidden().Message {
		t.Fatalf("Error(): %s", Forbidden().Error())
	}
	tests := []struct {
		name string
		in   error
		want Code
	}{
		{"apierr", wrapped, CodeForbidden},
		{"deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), CodeUpstreamUnavailable},
		{"canceled", context.Canceled, CodeUpstreamUnavailable},
		{"plain", errors.New("db down"), CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := From(tt.in); got.Code != tt.want {
				t.Fatalf("From = %s, want %s", got.Code, tt.want)
			}
		})
	}
	if From(nil) != nil {
		t.Fatal("From(nil) must be nil")
	}
}

func TestToAPIAndToMCP(t *testing.T) {
	api := []struct{ in, want Code }{
		{CodeUnavailable, CodeUpstreamUnavailable},
		{CodeInvalidConfirmation, CodeExpired},
		{CodeForbidden, CodeForbidden},
		{CodeRateLimited, CodeRateLimited},
		{Code("weird"), CodeInternal},
	}
	for _, tt := range api {
		got := ToAPI(New(tt.in, "сообщение"))
		if got.Code != tt.want {
			t.Errorf("ToAPI(%s) = %s, want %s", tt.in, got.Code, tt.want)
		}
		if got.Status != New(tt.want, "").Status {
			t.Errorf("ToAPI(%s) status %d", tt.in, got.Status)
		}
	}
	if m := ToAPI(Unavailable()); !m.Retryable {
		t.Error("ToAPI must keep retryable")
	}
	mcp := []struct{ in, want Code }{
		{CodeForbidden, CodeForbidden},
		{CodeNotFound, CodeNotFound},
		{CodeValidation, CodeValidation},
		{CodeConflict, CodeConflict},
		{CodeInvalidConfirmation, CodeInvalidConfirmation},
		{CodeUnavailable, CodeUnavailable},
		{CodeUnauthorized, CodeForbidden},
		{CodeExpired, CodeInvalidConfirmation},
		{CodeInternal, CodeUnavailable},
		{CodeRateLimited, CodeUnavailable},
		{CodeUpstreamUnavailable, CodeUnavailable},
	}
	for _, tt := range mcp {
		in := New(tt.in, "сообщение")
		got := ToMCP(in)
		if got.Code != tt.want || got.Message != "сообщение" || got.Retryable != in.Retryable {
			t.Errorf("ToMCP(%s) = %+v, want code %s", tt.in, got, tt.want)
		}
	}
}

func TestWriteJSON_Body(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    int
		code      Code
		retryable bool
	}{
		{"forbidden", Forbidden(), 403, CodeForbidden, false},
		{"wrapped validation", fmt.Errorf("x: %w", Validation().WithMessage("Плохо")), 400, CodeValidation, false},
		{"plain error hidden", errors.New("pq: secret details"), 500, CodeInternal, false},
		{"mcp unavailable mapped", Unavailable(), 502, CodeUpstreamUnavailable, true},
		{"zero status", &Error{Code: CodeConflict, Message: "Уже решено"}, 409, CodeConflict, false},
		{"nil", nil, 500, CodeInternal, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteJSON(rec, tt.err)
			if rec.Code != tt.status {
				t.Fatalf("status %d want %d", rec.Code, tt.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Fatalf("content type %q", ct)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 3 || body["code"] != string(tt.code) || body["retryable"] != tt.retryable {
				t.Fatalf("body %v", body)
			}
			if msg, _ := body["message"].(string); msg == "" || msg == "pq: secret details" {
				t.Fatalf("message %q", msg)
			}
		})
	}
}
