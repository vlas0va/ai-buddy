package httpx

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"aibuddy/services/internal/platform/apierr"
)

// HeaderRequestID — заголовок id запроса; принимается от клиентов и передаётся агенту.
const HeaderRequestID = "X-Request-Id"

// Middleware оборачивает http.Handler.
type Middleware = func(http.Handler) http.Handler

// Chain составляет middleware; первый — самый внешний: Chain(a, b)(h) == a(b(h)).
func Chain(mws ...Middleware) Middleware {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}

type requestIDKey struct{}

// RequestID сохраняет корректный входящий X-Request-Id (1–128 символов из [A-Za-z0-9._:-]) или генерирует UUID v4,
// кладёт его в контекст (RequestIDFromContext), в заголовок запроса и в заголовок ответа.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = newUUID()
		}
		r.Header.Set(HeaderRequestID, id)
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// WithRequestID возвращает ctx с id (RequestID делает это для входящих запросов).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext возвращает id запроса, сохранённый RequestID, или "".
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("._:-", c) >= 0
		if !ok {
			return false
		}
	}
	return true
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read никогда не падает на поддерживаемых платформах
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Recover превращает панику во внутреннюю ошибку apierr (если ещё ничего не записано) и логирует её со стеком.
// http.ErrAbortHandler пробрасывается паникой дальше, как ожидает net/http. Nil log означает slog.Default().
func Recover(log *slog.Logger) Middleware {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := wrap(w)
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.ErrorContext(r.Context(), "panic in handler", "panic", fmt.Sprint(v),
					"request_id", RequestIDFromContext(r.Context()), "stack", string(debug.Stack()))
				if !sw.wroteHeader {
					apierr.WriteJSON(sw, apierr.Internal())
				}
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// AccessLog пишет одну строку на запрос: method, path, status, bytes, duration_ms, request_id. GET /healthz логируется
// на уровне debug, чтобы проверки здоровья контейнера не засоряли журнал. Nil log означает slog.Default().
func AccessLog(log *slog.Logger) Middleware {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := wrap(w)
			next.ServeHTTP(sw, r)
			level := slog.LevelInfo
			if r.URL.Path == "/healthz" {
				level = slog.LevelDebug
			}
			log.Log(r.Context(), level, "http request",
				"method", r.Method, "path", r.URL.Path, "status", sw.statusCode(), "bytes", sw.bytes,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", RequestIDFromContext(r.Context()))
		})
	}
}

// statusWriter запоминает статус и размер ответа. Сохраняет работу flush для SSE (Flush, FlushError и Unwrap для
// http.ResponseController).
type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func wrap(w http.ResponseWriter) *statusWriter {
	if sw, ok := w.(*statusWriter); ok {
		return sw
	}
	return &statusWriter{ResponseWriter: w}
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader && code >= 200 {
		s.status, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status, s.wroteHeader = http.StatusOK, true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// FlushError сбрасывает нижележащий writer и сообщает http.ErrNotSupported, если сброс невозможен.
func (s *statusWriter) FlushError() error {
	err := http.NewResponseController(s.ResponseWriter).Flush()
	if err == nil && !s.wroteHeader {
		s.status, s.wroteHeader = http.StatusOK, true // сброс неявно отправляет 200
	}
	return err
}

// Flush реализует http.Flusher.
func (s *statusWriter) Flush() { _ = s.FlushError() }

// Unwrap открывает нижележащий writer для http.ResponseController.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) statusCode() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

// Заголовки CORS, отправляемые для разрешённых origin.
const (
	corsMethods = "GET, POST, PUT, PATCH, DELETE"
	corsHeaders = "Authorization, Content-Type, Idempotency-Key, X-Request-Id, Last-Event-ID"
	corsMaxAge  = 600
)

// CORS разрешает кросс-доменные запросы с перечисленных origin в точности (scheme://host[:port], завершающий "/"
// игнорируется; "*" разрешает любой origin). Credentials никогда не разрешаются: API использует bearer-токены, а не
// cookies. Preflight-запросы (OPTIONS с Access-Control-Request-Method) отвечаются здесь: 204 для разрешённых origin,
// иначе apierr forbidden. Запросы без Origin проходят без изменений.
func CORS(origins []string) Middleware {
	allowed := make(map[string]bool, len(origins))
	anyOrigin := false
	for _, o := range origins {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		switch o {
		case "":
		case "*":
			anyOrigin = true
		default:
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			ok := anyOrigin || allowed[origin]
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				if !ok {
					apierr.WriteJSON(w, apierr.Forbidden())
					return
				}
				setAllowOrigin(h, origin, anyOrigin)
				h.Set("Access-Control-Allow-Methods", corsMethods)
				h.Set("Access-Control-Allow-Headers", corsHeaders)
				h.Set("Access-Control-Max-Age", strconv.Itoa(corsMaxAge))
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if ok {
				setAllowOrigin(h, origin, anyOrigin)
				h.Set("Access-Control-Expose-Headers", HeaderRequestID)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func setAllowOrigin(h http.Header, origin string, anyOrigin bool) {
	if anyOrigin {
		h.Set("Access-Control-Allow-Origin", "*")
		return
	}
	h.Set("Access-Control-Allow-Origin", origin)
}
