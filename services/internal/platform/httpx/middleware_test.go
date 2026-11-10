package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestChain_Order(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(mw("a"), mw("b"), mw("c"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "h")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Join(order, "") != "abch" {
		t.Fatalf("order %v", order)
	}
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		keep     bool
	}{
		{"kept", "req-123_abc.def:9", true},
		{"missing", "", false},
		{"bad chars", "abc def", false},
		{"header injection", "a\r\nX-Evil: 1", false},
		{"too long", strings.Repeat("a", 129), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ctxID, hdrID string
			h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctxID = RequestIDFromContext(r.Context())
				hdrID = r.Header.Get(HeaderRequestID)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.incoming != "" {
				req.Header[HeaderRequestID] = []string{tt.incoming}
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			got := rec.Header().Get(HeaderRequestID)
			if got != ctxID || got != hdrID {
				t.Fatalf("response %q ctx %q header %q", got, ctxID, hdrID)
			}
			if tt.keep && got != tt.incoming {
				t.Fatalf("incoming id not kept: %q", got)
			}
			if !tt.keep && !uuidRE.MatchString(got) {
				t.Fatalf("generated id is not a uuid v4: %q", got)
			}
		})
	}
	if RequestIDFromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context()) != "" {
		t.Fatal("empty context must give empty id")
	}
}

func TestRecover(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logBuf, nil))
	t.Run("panic before write", func(t *testing.T) {
		h := Recover(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"internal"`) {
			t.Fatalf("got %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(logBuf.String(), "boom") || !strings.Contains(logBuf.String(), "stack") {
			t.Fatalf("log %s", logBuf.String())
		}
		if strings.Contains(rec.Body.String(), "boom") {
			t.Fatal("panic value leaked to client")
		}
	})
	t.Run("panic after write", func(t *testing.T) {
		h := Recover(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			panic("late")
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
			t.Fatalf("got %d %q", rec.Code, rec.Body.String())
		}
	})
	t.Run("abort handler re-panics", func(t *testing.T) {
		h := Recover(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
		defer func() {
			if v := recover(); v != http.ErrAbortHandler {
				t.Fatalf("recovered %v", v)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
	t.Run("no panic", func(t *testing.T) {
		h := Recover(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("got %d", rec.Code)
		}
	})
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := Chain(RequestID, AccessLog(log))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/x?secret=1", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines %v", lines)
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if first["status"] != float64(418) || first["bytes"] != float64(5) || first["path"] != "/api/x" ||
		first["method"] != "POST" || first["level"] != "INFO" || first["request_id"] == "" {
		t.Fatalf("first %v", first)
	}
	if second["status"] != float64(200) || second["level"] != "DEBUG" {
		t.Fatalf("second %v", second)
	}
	// Implicit 200 without any write, and the default logger.
	rec := httptest.NewRecorder()
	AccessLog(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
}

type plainWriter struct{ h http.Header }

func (p *plainWriter) Header() http.Header         { return p.h }
func (p *plainWriter) Write(b []byte) (int, error) { return len(b), nil }
func (p *plainWriter) WriteHeader(int)             {}

func TestStatusWriter_FlushAndUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := wrap(rec)
	if wrap(sw) != sw {
		t.Fatal("wrap must not double-wrap")
	}
	sw.WriteHeader(http.StatusContinue) // informational codes are not the final status
	sw.Flush()
	if !rec.Flushed || sw.statusCode() != http.StatusOK || sw.Unwrap() != rec {
		t.Fatalf("flushed %v status %d", rec.Flushed, sw.statusCode())
	}
	nf := wrap(&plainWriter{h: http.Header{}})
	if err := nf.FlushError(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("non-flusher: %v", err)
	}
	if nf.wroteHeader {
		t.Fatal("a failed flush must not mark the header as written")
	}
}

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"http://localhost:5173/", " ", "https://buddy.example"})(next)
	tests := []struct {
		name        string
		method      string
		origin      string
		preflight   bool
		status      int
		allowOrigin string
	}{
		{"no origin", http.MethodGet, "", false, 200, ""},
		{"allowed simple", http.MethodGet, "http://localhost:5173", false, 200, "http://localhost:5173"},
		{"second allowed", http.MethodPost, "https://buddy.example", false, 200, "https://buddy.example"},
		{"other origin passes without headers", http.MethodGet, "http://evil.example", false, 200, ""},
		{"preflight allowed", http.MethodOptions, "http://localhost:5173", true, 204, "http://localhost:5173"},
		{"preflight denied", http.MethodOptions, "http://evil.example", true, 403, ""},
		{"options without preflight header", http.MethodOptions, "http://localhost:5173", false, 200, "http://localhost:5173"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/me", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.preflight {
				req.Header.Set("Access-Control-Request-Method", "POST")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status %d want %d", rec.Code, tt.status)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.allowOrigin {
				t.Fatalf("allow-origin %q want %q", got, tt.allowOrigin)
			}
			if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("credentials must never be allowed")
			}
			if tt.preflight && tt.status == 204 {
				if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key") ||
					!strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "PATCH") ||
					rec.Header().Get("Access-Control-Max-Age") != "600" {
					t.Fatalf("preflight headers %v", rec.Header())
				}
			}
			if tt.origin != "" && rec.Header().Get("Vary") == "" {
				t.Fatal("Vary: Origin missing")
			}
		})
	}
	star := CORS([]string{"*"})(next)
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://any.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	star.ServeHTTP(rec, req)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard: %d %v", rec.Code, rec.Header())
	}
}
