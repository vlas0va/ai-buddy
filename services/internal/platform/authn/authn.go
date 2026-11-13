// Пакет authn выпускает и проверяет JWT HS256 мок-IdP и кладёт проверенные claims в контекст запроса. Личность в любом
// месте бэкенда берётся только из этих claims (R71).
//
// Токен: base64url (без выравнивания) от заголовка {"alg":"HS256","typ":"JWT"}, payload Claims и HMAC-SHA256 от
// "<header>.<payload>" на JWT_SECRET. Проверка строгая: ровно HS256 и typ JWT (alg "none" и любые другие алгоритмы
// отклоняются, как и заголовок "crit"), сравнение MAC за постоянное время, exp обязателен, sub и role обязательны, iat
// не в будущем.
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

// DefaultLeeway — допустимое расхождение часов для exp и iat в Verify и NewVerifier.
const DefaultLeeway = 30 * time.Second

// Claims — claims JWT пользователя. Время — в секундах Unix.
type Claims struct {
	Sub         string `json:"sub"`
	Role        string `json:"role"`
	TeamID      string `json:"team_id"`
	CohortStart string `json:"cohort_start,omitempty"` // YYYY-MM-DD, пусто для пользователей без когорты
	Iat         int64  `json:"iat,omitempty"`
	Exp         int64  `json:"exp"`
}

// Ошибки проверки. Middleware на все отвечает одинаковым 401 и не говорит клиенту, какая именно.
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

// Issue подписывает c с Iat = now и Exp = now + ttl. ttl должен быть положительным.
func Issue(secret []byte, c Claims, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("authn: issue: ttl must be positive, got %s", ttl)
	}
	now := time.Now()
	c.Iat = now.Unix()
	c.Exp = now.Add(ttl).Unix()
	return Sign(secret, c)
}

// Sign подписывает c ровно как передано (тесты собирают им истёкшие токены). Sub, Role и Exp обязательны.
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

// Verify проверяет token на момент now с DefaultLeeway и возвращает его claims.
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
	// Map, а не структура: encoding/json сопоставляет поля структуры без учёта регистра ("ALG" прошёл бы).
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

// Verifier проверяет токены с фиксированными секретом, допуском и часами; безопасен для конкурентного использования.
type Verifier struct {
	Secret []byte
	Leeway time.Duration    // допустимое расхождение часов; NewVerifier ставит DefaultLeeway
	Now    func() time.Time // nil означает time.Now
}

// NewVerifier возвращает Verifier с DefaultLeeway и системными часами.
func NewVerifier(secret []byte) *Verifier {
	return &Verifier{Secret: secret, Leeway: DefaultLeeway, Now: time.Now}
}

// Verify проверяет token и возвращает его claims.
func (v *Verifier) Verify(token string) (Claims, error) {
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	return verify(v.Secret, token, now(), v.Leeway)
}

// Middleware требует корректный `Authorization: Bearer <JWT>`; при успехе кладёт claims и исходный токен в контекст
// запроса (FromContext, TokenFromContext), иначе отвечает apierr 401 с `WWW-Authenticate: Bearer`.
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

// Middleware — это NewVerifier(secret).Middleware.
func Middleware(secret []byte) func(http.Handler) http.Handler {
	return NewVerifier(secret).Middleware
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	apierr.WriteJSON(w, apierr.Unauthorized())
}

// BearerToken возвращает токен из заголовка `Authorization: Bearer <token>` (схема без учёта регистра).
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

// NewContext возвращает ctx с c (так делает Middleware; тесты обработчиков могут вызывать напрямую).
func NewContext(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// FromContext возвращает проверенные claims, сохранённые Middleware или NewContext.
func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(Claims)
	return c, ok
}

// TokenFromContext возвращает исходный bearer-токен, принятый Middleware, для передачи агенту и MCP-серверам.
func TokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenKey{}).(string)
	return t, ok && t != ""
}

// WithToken возвращает ctx с исходным bearer-токеном для путей кода, которые вызывают MCP без HTTP-запроса (например,
// тесты или ядро, действующее от имени пользователя с выпущенным им токеном).
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}
