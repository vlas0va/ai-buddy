// Package authn issues and verifies the HS256 JWTs of the mock IdP and puts verified claims into the request
// context. Identity used anywhere in the backend comes only from these claims (R71).
//
// Token: base64url (no padding) of header {"alg":"HS256","typ":"JWT"}, payload Claims and the HMAC-SHA256 of
// "<header>.<payload>" with JWT_SECRET. Verification is strict: exactly HS256 and typ JWT (alg "none" and every
// other algorithm are rejected, as is a "crit" header), constant-time MAC comparison, exp required,
// sub and role required, iat not in the future.
package authn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"aibuddy/services/internal/platform/apierr"
)

// DefaultLeeway is the clock skew tolerated for exp and iat by Verify and NewVerifier.
const DefaultLeeway = 30 * time.Second

// Claims are the JWT claims of a user. Times are Unix seconds.
type Claims struct {
	Sub         string `json:"sub"`
	Role        string `json:"role"`
	TeamID      string `json:"team_id"`
	CohortStart string `json:"cohort_start,omitempty"` // YYYY-MM-DD, empty for users without a cohort
	Iat         int64  `json:"iat,omitempty"`
	Exp         int64  `json:"exp"`
}

// Verification errors. Middleware answers all of them with the same 401 and does not tell the client which one.
var (
	ErrNoSecret      = errors.New("authn: empty secret")
	ErrMalformed     = errors.New("authn: malformed token")
	ErrAlgorithm     = errors.New("authn: unsupported token header")
	ErrSignature     = errors.New("authn: invalid signature")
	ErrExpired       = errors.New("authn: token expired")
	ErrNotYetValid   = errors.New("authn: token issued in the future")
	ErrMissingClaims = errors.New("authn: required claim missing")
)

var (
	b64        = base64.RawURLEncoding
	b64Strict  = base64.RawURLEncoding.Strict()
	headerPart = b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
)

// Issue signs c with Iat = now and Exp = now + ttl. ttl must be positive.
func Issue(secret []byte, c Claims, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("authn: issue: ttl must be positive, got %s", ttl)
	}
	now := time.Now()
	c.Iat = now.Unix()
	c.Exp = now.Add(ttl).Unix()
	return Sign(secret, c)
}

// Sign signs c exactly as given (tests use it to build expired tokens). Sub, Role and Exp are required.
func Sign(secret []byte, c Claims) (string, error) {
	if len(secret) == 0 {
		return "", ErrNoSecret
	}
	if c.Sub == "" || c.Role == "" || c.Exp == 0 {
		return "", ErrMissingClaims
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("authn: marshal claims: %w", err)
	}
	signingInput := headerPart + "." + b64.EncodeToString(payload)
	return signingInput + "." + b64.EncodeToString(mac(secret, signingInput)), nil
}

// Verify checks token at time now with DefaultLeeway and returns its claims.
func Verify(secret []byte, token string, now time.Time) (Claims, error) {
	return verify(secret, token, now, DefaultLeeway)
}

func verify(secret []byte, token string, now time.Time, leeway time.Duration) (Claims, error) {
	if len(secret) == 0 {
		return Claims{}, ErrNoSecret
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Claims{}, ErrMalformed
	}
	// A map, not a struct: encoding/json matches struct fields case-insensitively ("ALG" would pass).
	var h map[string]any
	if err := decodePart(parts[0], &h); err != nil {
		return Claims{}, err
	}
	if _, crit := h["crit"]; h["alg"] != "HS256" || h["typ"] != "JWT" || crit {
		return Claims{}, ErrAlgorithm
	}
	sig, err := b64Strict.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	if !hmac.Equal(sig, mac(secret, parts[0]+"."+parts[1])) {
		return Claims{}, ErrSignature
	}
	var c Claims
	if err := decodePart(parts[1], &c); err != nil {
		return Claims{}, err
	}
	if c.Sub == "" || c.Role == "" || c.Exp == 0 {
		return Claims{}, ErrMissingClaims
	}
	if !now.Before(time.Unix(c.Exp, 0).Add(leeway)) {
		return Claims{}, ErrExpired
	}
	if c.Iat != 0 && time.Unix(c.Iat, 0).After(now.Add(leeway)) {
		return Claims{}, ErrNotYetValid
	}
	return c, nil
}

func decodePart(part string, dst any) error {
	raw, err := b64Strict.DecodeString(part)
	if err != nil {
		return ErrMalformed
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return ErrMalformed
	}
	return nil
}

func mac(secret []byte, signingInput string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(signingInput))
	return m.Sum(nil)
}

// Verifier verifies tokens with a fixed secret, leeway and clock; it is safe for concurrent use.
type Verifier struct {
	Secret []byte
	Leeway time.Duration    // tolerated clock skew; NewVerifier sets DefaultLeeway
	Now    func() time.Time // nil means time.Now
}

// NewVerifier returns a Verifier with DefaultLeeway and the system clock.
func NewVerifier(secret []byte) *Verifier {
	return &Verifier{Secret: secret, Leeway: DefaultLeeway, Now: time.Now}
}

// Verify checks token and returns its claims.
func (v *Verifier) Verify(token string) (Claims, error) {
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	return verify(v.Secret, token, now(), v.Leeway)
}

// Middleware requires a valid `Authorization: Bearer <JWT>`; on success it stores the claims and the raw token
// in the request context (FromContext, TokenFromContext), otherwise it answers apierr 401 with
// `WWW-Authenticate: Bearer`.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := BearerToken(r)
		if !ok {
			unauthorized(w)
			return
		}
		c, err := v.Verify(token)
		if err != nil {
			slog.Default().DebugContext(r.Context(), "jwt rejected", "err", err)
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithToken(NewContext(r.Context(), c), token)))
	})
}

// Middleware is NewVerifier(secret).Middleware.
func Middleware(secret []byte) func(http.Handler) http.Handler {
	return NewVerifier(secret).Middleware
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	apierr.WriteJSON(w, apierr.Unauthorized())
}

// BearerToken returns the token of an `Authorization: Bearer <token>` header (scheme case-insensitive).
func BearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

type claimsKey struct{}

type tokenKey struct{}

// NewContext returns ctx carrying c (Middleware does this; tests of handlers may call it directly).
func NewContext(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// FromContext returns the verified claims stored by Middleware or NewContext.
func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(Claims)
	return c, ok
}

// TokenFromContext returns the raw bearer token accepted by Middleware, for forwarding to the agent and MCP servers.
func TokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenKey{}).(string)
	return t, ok && t != ""
}

// WithToken returns ctx carrying a raw bearer token, for code paths that call MCP without an HTTP request
// (e.g. tests or core acting on behalf of a user with a token it issued).
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}
