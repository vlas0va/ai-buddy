package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var secret = []byte("test-jwt-secret")

var anna = Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery", CohortStart: "2026-11-02"}

// craft signs arbitrary header and payload JSON, to build tokens Issue would never produce.
func craft(key []byte, headerJSON, payloadJSON string) string {
	si := b64.EncodeToString([]byte(headerJSON)) + "." + b64.EncodeToString([]byte(payloadJSON))
	return si + "." + b64.EncodeToString(mac(key, si))
}

func TestIssueVerify_RoundTrip_R71(t *testing.T) {
	tok, err := Issue(secret, anna, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.") || strings.Contains(tok, "=") {
		t.Fatalf("unexpected token shape %s", tok)
	}
	c, err := Verify(secret, tok, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != anna.Sub || c.Role != anna.Role || c.TeamID != anna.TeamID || c.CohortStart != anna.CohortStart {
		t.Fatalf("claims %+v", c)
	}
	if c.Exp-c.Iat != 3600 {
		t.Fatalf("exp-iat = %d", c.Exp-c.Iat)
	}
}

// Token built by Python (hmac + json.dumps), so the agent's test tokens verify in Go.
func TestVerify_PythonVector_R71(t *testing.T) {
	const tok = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJjb2hvcnRfc3RhcnQiOiIyMDI2LTExLTAyIiwiZXhwIjo0MTAyNDQ0ODAwLCJpYXQiOjE3NjI2NDY0MDAsInJvbGUiOiJpbnRlcm4iLCJzdWIiOiJpbnRlcm4uYW5uYSIsInRlYW1faWQiOiJkZWxpdmVyeSJ9." +
		"a-rlzufqBpyakVpAUMRdOI1qe0xAGnpMUx8C8qZfQ4U"
	c, err := Verify([]byte("python-jwt-secret"), tok, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery", CohortStart: "2026-11-02", Iat: 1762646400, Exp: 4102444800}
	if c != want {
		t.Fatalf("got %+v want %+v", c, want)
	}
}

func TestVerify_Rejects_R71(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	exp := now.Add(time.Hour).Unix()
	good := `{"sub":"intern.anna","role":"intern","team_id":"delivery","exp":` + itoa(exp) + `}`
	valid := craft(secret, `{"alg":"HS256","typ":"JWT"}`, good)
	parts := strings.Split(valid, ".")
	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"alg none unsigned", b64.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + parts[1] + ".", ErrMalformed},
		{"alg none with signature", craft(secret, `{"alg":"none","typ":"JWT"}`, good), ErrAlgorithm},
		{"alg HS512", craft(secret, `{"alg":"HS512","typ":"JWT"}`, good), ErrAlgorithm},
		{"alg RS256", craft(secret, `{"alg":"RS256","typ":"JWT"}`, good), ErrAlgorithm},
		{"alg lowercase", craft(secret, `{"alg":"hs256","typ":"JWT"}`, good), ErrAlgorithm},
		{"alg key uppercase", craft(secret, `{"ALG":"HS256","typ":"JWT"}`, good), ErrAlgorithm},
		{"typ missing", craft(secret, `{"alg":"HS256"}`, good), ErrAlgorithm},
		{"typ other", craft(secret, `{"alg":"HS256","typ":"at+jwt"}`, good), ErrAlgorithm},
		{"crit header", craft(secret, `{"alg":"HS256","typ":"JWT","crit":["exp"]}`, good), ErrAlgorithm},
		{"wrong secret", craft([]byte("other"), `{"alg":"HS256","typ":"JWT"}`, good), ErrSignature},
		{"tampered payload", parts[0] + "." + b64.EncodeToString([]byte(strings.Replace(good, "intern", "lead", 1))) + "." + parts[2], ErrSignature},
		{"two parts", parts[0] + "." + parts[1], ErrMalformed},
		{"four parts", valid + ".x", ErrMalformed},
		{"empty", "", ErrMalformed},
		{"padded signature", valid + "=", ErrMalformed},
		{"header not base64", "@@@." + parts[1] + "." + parts[2], ErrMalformed},
		{"header not json", b64.EncodeToString([]byte("nope")) + "." + parts[1] + "." + parts[2], ErrMalformed},
		{"payload not json", craft(secret, `{"alg":"HS256","typ":"JWT"}`, `[1`), ErrMalformed},
		{"exp as string", craft(secret, `{"alg":"HS256","typ":"JWT"}`, `{"sub":"a","role":"intern","exp":"1"}`), ErrMalformed},
		{"no exp", craft(secret, `{"alg":"HS256","typ":"JWT"}`, `{"sub":"a","role":"intern"}`), ErrMissingClaims},
		{"no sub", craft(secret, `{"alg":"HS256","typ":"JWT"}`, `{"role":"intern","exp":`+itoa(exp)+`}`), ErrMissingClaims},
		{"no role", craft(secret, `{"alg":"HS256","typ":"JWT"}`, `{"sub":"a","exp":`+itoa(exp)+`}`), ErrMissingClaims},
		{"expired beyond leeway", craft(secret, `{"alg":"HS256","typ":"JWT"}`, `{"sub":"a","role":"intern","exp":`+itoa(now.Add(-DefaultLeeway).Unix())+`}`), ErrExpired},
		{"iat in future", craft(secret, `{"alg":"HS256","typ":"JWT"}`, `{"sub":"a","role":"intern","iat":`+itoa(now.Add(2*DefaultLeeway).Unix())+`,"exp":`+itoa(exp)+`}`), ErrNotYetValid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Verify(secret, tt.token, now); !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
	if _, err := Verify(nil, valid, now); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("empty secret: %v", err)
	}
	if _, err := Verify(secret, valid, now); err != nil {
		t.Fatalf("control token must verify: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestVerify_Leeway(t *testing.T) {
	exp := time.Unix(1_800_000_000, 0)
	tok, err := Sign(secret, Claims{Sub: "a", Role: "lead", Exp: exp.Unix(), Iat: exp.Add(-time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(secret, tok, exp.Add(10*time.Second)); err != nil {
		t.Fatalf("within default leeway: %v", err)
	}
	strict := &Verifier{Secret: secret, Now: func() time.Time { return exp }}
	if _, err := strict.Verify(tok); !errors.Is(err, ErrExpired) {
		t.Fatalf("zero leeway at exp: %v", err)
	}
	strict.Now = func() time.Time { return exp.Add(-time.Second) }
	if _, err := strict.Verify(tok); err != nil {
		t.Fatalf("one second before exp: %v", err)
	}
	old, _ := Sign(secret, Claims{Sub: "a", Role: "lead", Exp: 1_000_000_000})
	if _, err := (&Verifier{Secret: secret}).Verify(old); !errors.Is(err, ErrExpired) {
		t.Fatalf("nil Now must use the system clock: %v", err)
	}
	fresh, _ := Issue(secret, anna, time.Minute)
	if _, err := NewVerifier(secret).Verify(fresh); err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
}

func TestSignIssue_Errors(t *testing.T) {
	if _, err := Sign(nil, Claims{Sub: "a", Role: "b", Exp: 1}); !errors.Is(err, ErrNoSecret) {
		t.Fatal(err)
	}
	if _, err := Sign(secret, Claims{Sub: "a", Role: "b"}); !errors.Is(err, ErrMissingClaims) {
		t.Fatal(err)
	}
	if _, err := Issue(secret, anna, 0); err == nil {
		t.Fatal("ttl 0 must fail")
	}
	if _, err := Issue(secret, Claims{Role: "intern"}, time.Hour); !errors.Is(err, ErrMissingClaims) {
		t.Fatal(err)
	}
}

func TestMiddleware_R71(t *testing.T) {
	tok, err := Issue(secret, anna, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var gotClaims Claims
	var gotToken string
	h := Middleware(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims, _ = FromContext(r.Context())
		gotToken, _ = TokenFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name   string
		header string
		status int
	}{
		{"valid", "Bearer " + tok, http.StatusNoContent},
		{"scheme case-insensitive", "bearer " + tok, http.StatusNoContent},
		{"missing", "", http.StatusUnauthorized},
		{"basic", "Basic dXNlcjpwYXNz", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"no space", "Bearer" + tok, http.StatusUnauthorized},
		{"bad token", "Bearer " + tok + "x", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotClaims, gotToken = Claims{}, ""
			req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status %d want %d", rec.Code, tt.status)
			}
			if tt.status == http.StatusUnauthorized {
				if rec.Header().Get("WWW-Authenticate") != "Bearer" {
					t.Fatal("missing WWW-Authenticate")
				}
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != "unauthorized" {
					t.Fatalf("body %s", rec.Body.String())
				}
				if gotClaims.Sub != "" {
					t.Fatal("handler must not run")
				}
				return
			}
			if gotClaims.Sub != "intern.anna" || gotToken != tok {
				t.Fatalf("ctx claims %+v token %q", gotClaims, gotToken)
			}
		})
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	if _, ok := FromContext(ctx); ok {
		t.Fatal("empty ctx has no claims")
	}
	if _, ok := TokenFromContext(ctx); ok {
		t.Fatal("empty ctx has no token")
	}
	if _, ok := TokenFromContext(WithToken(ctx, "")); ok {
		t.Fatal("empty token must not count")
	}
	ctx = WithToken(NewContext(ctx, anna), "t")
	if c, ok := FromContext(ctx); !ok || c.Sub != anna.Sub {
		t.Fatal("NewContext")
	}
	if tok, ok := TokenFromContext(ctx); !ok || tok != "t" {
		t.Fatal("WithToken")
	}
}
