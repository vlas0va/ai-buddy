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

// HeaderRequestID is the request id header, accepted from clients and forwarded to the agent.
const HeaderRequestID = "X-Request-Id"

// Middleware wraps an http.Handler.
type Middleware = func(http.Handler) http.Handler

// Chain composes middleware; the first one is the outermost: Chain(a, b)(h) == a(b(h)).
func Chain(mws ...Middleware) Middleware {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}

type requestIDKey struct{}

// RequestID keeps a well-formed incoming X-Request-Id (1–128 chars of [A-Za-z0-9._:-]) or generates a UUID v4,
// stores it in the context (RequestIDFromContext), on the request header and on the response header.
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

// WithRequestID returns ctx carrying id (RequestID does this for incoming requests).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the request id stored by RequestID, or "".
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
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails on supported platforms
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Recover turns a panic into an apierr internal response (if nothing was written yet) and logs it with the stack.
// http.ErrAbortHandler is re-panicked, as net/http expects. A nil log means slog.Default().
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

// AccessLog logs one line per request: method, path, status, bytes, duration_ms, request_id.
// GET /healthz is logged at debug level to keep container health checks out of the log. A nil log means slog.Default().
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

// statusWriter records the status and size of a response. It keeps flushing working for SSE
// (Flush, FlushError and Unwrap for http.ResponseController).
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

// FlushError flushes the underlying writer, reporting http.ErrNotSupported when it cannot flush.
func (s *statusWriter) FlushError() error {
	err := http.NewResponseController(s.ResponseWriter).Flush()
	if err == nil && !s.wroteHeader {
		s.status, s.wroteHeader = http.StatusOK, true // flushing sends an implicit 200
	}
	return err
}

// Flush implements http.Flusher.
func (s *statusWriter) Flush() { _ = s.FlushError() }

// Unwrap exposes the underlying writer to http.ResponseController.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) statusCode() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

// CORS headers sent for allowed origins.
const (
	corsMethods = "GET, POST, PUT, PATCH, DELETE"
	corsHeaders = "Authorization, Content-Type, Idempotency-Key, X-Request-Id, Last-Event-ID"
	corsMaxAge  = 600
)

// CORS allows cross-origin requests from the exact origins listed (scheme://host[:port], trailing "/" ignored;
// "*" allows any origin). Credentials are never allowed: the API uses bearer tokens, not cookies.
// Preflight requests (OPTIONS with Access-Control-Request-Method) are answered here: 204 for allowed
// origins, apierr forbidden otherwise. Requests without Origin pass through untouched.
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
