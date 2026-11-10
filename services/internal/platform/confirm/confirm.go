// Package confirm issues and verifies confirmation tokens for MCP write tools (contracts/mcp-tools.md, R71, R79).
//
// Token = P + "." + S, where
//
//	P = base64url(payload JSON), S = base64url(HMAC-SHA256(CONFIRMATION_HMAC_SECRET, P))
//
// base64url is RFC 4648 §5 WITHOUT padding; the MAC is computed over the ASCII bytes of P (not the decoded JSON).
// The payload is the canonical JSON (canonjson, keys sorted) of {"args_hash","exp","key","sub"}: exp is Unix
// seconds, args_hash is the lowercase hex SHA-256 of the canonical arguments (ArgsHash). Python produces the same P
// with json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=False).
package confirm

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"aibuddy/services/internal/platform/canonjson"
)

// Argument keys excluded from the hash: they are not part of what the user confirmed.
const (
	KeyIdempotency  = "idempotency_key"
	KeyConfirmation = "confirmation_token"
)

// ErrInvalid is wrapped by every verification failure; MCP servers answer all of them with invalid_confirmation.
var ErrInvalid = errors.New("confirm: invalid confirmation token")

// Specific verification failures; each satisfies errors.Is(err, ErrInvalid).
var (
	ErrMalformed = fmt.Errorf("%w: malformed", ErrInvalid)
	ErrSignature = fmt.Errorf("%w: bad signature", ErrInvalid)
	ErrExpired   = fmt.Errorf("%w: expired", ErrInvalid)
	ErrSubject   = fmt.Errorf("%w: subject mismatch", ErrInvalid)
	ErrKey       = fmt.Errorf("%w: idempotency key mismatch", ErrInvalid)
	ErrArgsHash  = fmt.Errorf("%w: arguments mismatch", ErrInvalid)
)

// ErrNoSecret is a configuration error (empty secret), deliberately not wrapping ErrInvalid.
var ErrNoSecret = errors.New("confirm: empty secret")

type payload struct {
	ArgsHash string `json:"args_hash"`
	Exp      int64  `json:"exp"`
	Key      string `json:"key"`
	Sub      string `json:"sub"`
}

var b64 = base64.RawURLEncoding.Strict()

// Issue returns a token for sub (JWT subject), key (idempotency_key) and argsHash (from ArgsHash) that expires
// ttl from now. ttl must be positive.
func Issue(secret []byte, sub, key, argsHash string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("confirm: issue: ttl must be positive, got %s", ttl)
	}
	return IssueUntil(secret, sub, key, argsHash, time.Now().Add(ttl))
}

// IssueUntil is Issue with an absolute expiry (truncated to seconds); tests use it for expired tokens.
func IssueUntil(secret []byte, sub, key, argsHash string, exp time.Time) (string, error) {
	if len(secret) == 0 {
		return "", ErrNoSecret
	}
	if sub == "" || key == "" {
		return "", errors.New("confirm: issue: sub and key are required")
	}
	if !isHexSHA256(argsHash) {
		return "", fmt.Errorf("confirm: issue: args hash must be 64 lowercase hex chars, got %q", argsHash)
	}
	body, err := canonjson.Marshal(payload{ArgsHash: argsHash, Exp: exp.Unix(), Key: key, Sub: sub})
	if err != nil {
		return "", fmt.Errorf("confirm: issue: %w", err)
	}
	p := base64.RawURLEncoding.EncodeToString(body)
	return p + "." + base64.RawURLEncoding.EncodeToString(sign(secret, p)), nil
}

// Verify checks token against the caller's JWT subject, the idempotency_key of the call and the arguments the
// server actually received (idempotency_key and confirmation_token are ignored, see ArgsHash), at time now.
// Checks run in order signature, exp, sub, key, args; the first failure is returned.
func Verify(secret []byte, token, sub, key string, args any, now time.Time) error {
	if len(secret) == 0 {
		return ErrNoSecret
	}
	p, s, ok := strings.Cut(token, ".")
	if !ok || p == "" || s == "" || strings.Contains(s, ".") {
		return ErrMalformed
	}
	sig, err := b64.DecodeString(s)
	if err != nil {
		return ErrMalformed
	}
	if !hmac.Equal(sig, sign(secret, p)) {
		return ErrSignature
	}
	raw, err := b64.DecodeString(p)
	if err != nil {
		return ErrMalformed
	}
	var pl payload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pl); err != nil || pl.Sub == "" || pl.Key == "" || pl.Exp == 0 || pl.ArgsHash == "" {
		return ErrMalformed
	}
	if !now.Before(time.Unix(pl.Exp, 0)) {
		return ErrExpired
	}
	if pl.Sub != sub {
		return ErrSubject
	}
	if pl.Key != key {
		return ErrKey
	}
	h, err := ArgsHash(args)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrArgsHash, err)
	}
	if !hmac.Equal([]byte(h), []byte(pl.ArgsHash)) {
		return ErrArgsHash
	}
	return nil
}

// ArgsHash returns the lowercase hex SHA-256 of the canonical JSON of args (a JSON object: map, struct or
// json.RawMessage) after removing the top-level keys idempotency_key and confirmation_token.
// It equals canonjson.SHA256Hex(draft.args) for the draft the user confirmed.
func ArgsHash(args any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("confirm: args: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return "", errors.New("confirm: args must be a JSON object")
	}
	delete(obj, KeyIdempotency)
	delete(obj, KeyConfirmation)
	return canonjson.SHA256Hex(obj)
}

func sign(secret []byte, p string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(p))
	return m.Sum(nil)
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
