// Пакет confirm выпускает и проверяет токены подтверждения для инструментов записи MCP (contracts/mcp-tools.md, R71,
// R79).
//
// Токен = P + "." + S, где
//
//	P = base64url(payload JSON), S = base64url(HMAC-SHA256(CONFIRMATION_HMAC_SECRET, P))
//
// base64url — RFC 4648 §5 БЕЗ выравнивания; MAC считается по ASCII-байтам P (а не по декодированному JSON). Payload —
// канонический JSON (canonjson, ключи отсортированы) от {"args_hash","exp","key","sub"}: exp — секунды Unix, args_hash
// — SHA-256 канонических аргументов строчным hex (ArgsHash). Python выдаёт тот же P через json.dumps(payload,
// sort_keys=True, separators=(",", ":"), ensure_ascii=False).
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

// Ключи аргументов, исключённые из хеша: они не входят в то, что подтвердил пользователь.
const (
	KeyIdempotency  = "idempotency_key"
	KeyConfirmation = "confirmation_token"
)

// ErrInvalid оборачивается каждой ошибкой проверки; MCP-серверы на все отвечают invalid_confirmation.
var ErrInvalid = errors.New("confirm: invalid confirmation token")

// Конкретные ошибки проверки; каждая удовлетворяет errors.Is(err, ErrInvalid).
var (
	ErrMalformed = fmt.Errorf("%w: malformed", ErrInvalid)
	ErrSignature = fmt.Errorf("%w: bad signature", ErrInvalid)
	ErrExpired   = fmt.Errorf("%w: expired", ErrInvalid)
	ErrSubject   = fmt.Errorf("%w: subject mismatch", ErrInvalid)
	ErrKey       = fmt.Errorf("%w: idempotency key mismatch", ErrInvalid)
	ErrArgsHash  = fmt.Errorf("%w: arguments mismatch", ErrInvalid)
)

// ErrNoSecret — ошибка конфигурации (пустой секрет), намеренно не оборачивает ErrInvalid.
var ErrNoSecret = errors.New("confirm: empty secret")

type payload struct {
	ArgsHash string `json:"args_hash"`
	Exp      int64  `json:"exp"`
	Key      string `json:"key"`
	Sub      string `json:"sub"`
}

var b64 = base64.RawURLEncoding.Strict()

// Issue возвращает токен для sub (субъект JWT), key (idempotency_key) и argsHash (из ArgsHash), который истекает через
// ttl. ttl должен быть положительным.
func Issue(secret []byte, sub, key, argsHash string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("confirm: issue: ttl must be positive, got %s", ttl)
	}
	return IssueUntil(secret, sub, key, argsHash, time.Now().Add(ttl))
}

// IssueUntil — это Issue с абсолютным сроком (обрезается до секунд); тесты используют его для истёкших токенов.
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

// Verify проверяет token по субъекту JWT вызывающего, idempotency_key вызова и аргументам, которые сервер действительно
// получил (idempotency_key и confirmation_token игнорируются, см. ArgsHash), на момент now. Проверки идут по порядку:
// подпись, exp, sub, key, args; возвращается первая неудача.
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

// ArgsHash возвращает SHA-256 канонического JSON от args (JSON-объект: map, структура или json.RawMessage) строчным hex
// после удаления ключей верхнего уровня idempotency_key и confirmation_token. Равен canonjson.SHA256Hex(draft.args) для
// черновика, который подтвердил пользователь.
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
