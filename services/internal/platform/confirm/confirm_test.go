package confirm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/platform/canonjson"
)

var secret = []byte("test-confirm-secret")

const proposalID = "6f1c2b9e-3d4a-4b5c-8d7e-0f1a2b3c4d5e"

func draftArgs() map[string]any {
	return map[string]any{"type": "day_off", "start_date": "2026-11-14", "end_date": "2026-11-14", "comment": "Семейные дела"}
}

// Токен посчитан на Python (json.dumps sort_keys + hmac); Go должен выдавать и принимать те же байты.
func TestIssueVerify_PythonVector_R71(t *testing.T) {
	const wantHash = "5996ac754fae79b3bd1cc70069bd91d1e99f3b4e9aef2afa817b27b473f9dbf0"
	const wantToken = "eyJhcmdzX2hhc2giOiI1OTk2YWM3NTRmYWU3OWIzYmQxY2M3MDA2OWJkOTFkMWU5OWYzYjRlOWFlZjJhZmE4MTdiMjdiNDczZjlkYmYwIiwiZXhwIjo0MTAyNDQ0ODAwLCJrZXkiOiI2ZjFjMmI5ZS0zZDRhLTRiNWMtOGQ3ZS0wZjFhMmIzYzRkNWUiLCJzdWIiOiJpbnRlcm4uYW5uYSJ9" +
		".-pOzgTmkPDkKnTatNR-Hf7PQ1qc7DqWFXkKvbRCglsM"
	py := []byte("python-confirm-secret")
	h, err := ArgsHash(draftArgs())
	if err != nil || h != wantHash {
		t.Fatalf("ArgsHash = %s, %v", h, err)
	}
	tok, err := IssueUntil(py, "intern.anna", proposalID, h, time.Unix(4102444800, 0))
	if err != nil {
		t.Fatal(err)
	}
	if tok != wantToken {
		t.Fatalf("token differs from Python\n got %s\nwant %s", tok, wantToken)
	}
	// MCP-сервер получает args плюс idempotency_key и confirmation_token.
	received := json.RawMessage(`{"comment":"Семейные дела","confirmation_token":"` + wantToken + `","end_date":"2026-11-14",` +
		`"idempotency_key":"` + proposalID + `","start_date":"2026-11-14","type":"day_off"}`)
	if err := Verify(py, wantToken, "intern.anna", proposalID, received, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestArgsHash_ExcludesTransportKeys_R79(t *testing.T) {
	base, err := ArgsHash(draftArgs())
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := canonjson.SHA256Hex(draftArgs())
	if base != direct {
		t.Fatal("ArgsHash of plain args must equal canonjson.SHA256Hex (core's draft_hash)")
	}
	withKeys := draftArgs()
	withKeys[KeyIdempotency] = proposalID
	withKeys[KeyConfirmation] = "x.y"
	type args struct {
		Type      string `json:"type"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Comment   string `json:"comment"`
		Key       string `json:"idempotency_key,omitempty"`
	}
	same := []any{
		withKeys,
		args{"day_off", "2026-11-14", "2026-11-14", "Семейные дела", "k"},
		json.RawMessage(`{ "type":"day_off","comment":"Семейные дела","end_date":"2026-11-14","start_date":"2026-11-14" }`),
	}
	for i, a := range same {
		h, err := ArgsHash(a)
		if err != nil || h != base {
			t.Errorf("case %d: hash %s err %v, want %s", i, h, err, base)
		}
	}
	nested, _ := ArgsHash(map[string]any{"x": map[string]any{KeyIdempotency: "k"}})
	plain, _ := ArgsHash(map[string]any{"x": map[string]any{}})
	if nested == plain {
		t.Error("only top-level keys are removed")
	}
	for _, bad := range []any{[]any{1}, nil, "s", json.RawMessage(`{`), make(chan int)} {
		if _, err := ArgsHash(bad); err == nil {
			t.Errorf("ArgsHash(%T) must fail", bad)
		}
	}
}

func signedPayload(key []byte, body string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(body))
	return p + "." + base64.RawURLEncoding.EncodeToString(sign(key, p))
}

func TestVerify_Failures_R71(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h, _ := ArgsHash(draftArgs())
	valid, err := IssueUntil(secret, "intern.anna", proposalID, h, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	p, s, _ := strings.Cut(valid, ".")
	expired, _ := IssueUntil(secret, "intern.anna", proposalID, h, now.Add(-time.Second))
	atExp, _ := IssueUntil(secret, "intern.anna", proposalID, h, now)
	foreign, _ := IssueUntil([]byte("other"), "intern.anna", proposalID, h, now.Add(time.Minute))
	changed := draftArgs()
	changed["end_date"] = "2026-11-20"
	tests := []struct {
		name  string
		token string
		sub   string
		key   string
		args  any
		want  error
	}{
		{"valid", valid, "intern.anna", proposalID, draftArgs(), nil},
		{"foreign secret", foreign, "intern.anna", proposalID, draftArgs(), ErrSignature},
		{"tampered payload", base64.RawURLEncoding.EncodeToString([]byte(`{"args_hash":"`+h+`","exp":4102444800,"key":"`+proposalID+`","sub":"intern.anna"}`)) + "." + s, "intern.anna", proposalID, draftArgs(), ErrSignature},
		{"expired", expired, "intern.anna", proposalID, draftArgs(), ErrExpired},
		{"at exp", atExp, "intern.anna", proposalID, draftArgs(), ErrExpired},
		{"other user", valid, "intern.boris", proposalID, draftArgs(), ErrSubject},
		{"other key", valid, "intern.anna", "another-key", draftArgs(), ErrKey},
		{"changed args", valid, "intern.anna", proposalID, changed, ErrArgsHash},
		{"args not object", valid, "intern.anna", proposalID, []any{}, ErrArgsHash},
		{"empty", "", "intern.anna", proposalID, draftArgs(), ErrMalformed},
		{"no dot", p, "intern.anna", proposalID, draftArgs(), ErrMalformed},
		{"three parts", valid + ".x", "intern.anna", proposalID, draftArgs(), ErrMalformed},
		{"empty signature", p + ".", "intern.anna", proposalID, draftArgs(), ErrMalformed},
		{"padded signature", valid + "=", "intern.anna", proposalID, draftArgs(), ErrMalformed},
		{"signed garbage payload", signedPayload(secret, "nope"), "intern.anna", proposalID, draftArgs(), ErrMalformed},
		{"signed payload unknown field", signedPayload(secret, `{"args_hash":"`+h+`","exp":4102444800,"key":"k","sub":"s","x":1}`), "s", "k", draftArgs(), ErrMalformed},
		{"signed payload missing exp", signedPayload(secret, `{"args_hash":"`+h+`","key":"k","sub":"s"}`), "s", "k", draftArgs(), ErrMalformed},
		{"payload not base64", "@@@." + base64.RawURLEncoding.EncodeToString(sign(secret, "@@@")), "s", "k", draftArgs(), ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verify(secret, tt.token, tt.sub, tt.key, tt.args, now)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("unexpected %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) || !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v want %v (wrapping ErrInvalid)", err, tt.want)
			}
		})
	}
	if err := Verify(nil, valid, "intern.anna", proposalID, draftArgs(), now); !errors.Is(err, ErrNoSecret) || errors.Is(err, ErrInvalid) {
		t.Fatalf("empty secret: %v", err)
	}
}

func TestIssue_Errors(t *testing.T) {
	h, _ := ArgsHash(draftArgs())
	tests := []struct {
		name               string
		secret             []byte
		sub, key, argsHash string
		ttl                time.Duration
	}{
		{"zero ttl", secret, "a", "k", h, 0},
		{"no secret", nil, "a", "k", h, time.Minute},
		{"no sub", secret, "", "k", h, time.Minute},
		{"no key", secret, "a", "", h, time.Minute},
		{"raw args instead of hash", secret, "a", "k", `{"type":"day_off"}`, time.Minute},
		{"uppercase hash", secret, "a", "k", strings.ToUpper(h), time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Issue(tt.secret, tt.sub, tt.key, tt.argsHash, tt.ttl); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	tok, err := Issue(secret, "lead.maria", "idem-1", h, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(secret, tok, "lead.maria", "idem-1", draftArgs(), time.Now()); err != nil {
		t.Fatalf("fresh token: %v", err)
	}
	if err := Verify(secret, tok, "lead.maria", "idem-1", draftArgs(), time.Now().Add(5*time.Minute+time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("after ttl: %v", err)
	}
}
