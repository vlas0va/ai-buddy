package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestHandler(t *testing.T) {
	h := NewHandler(quiet)
	tests := []struct {
		name, method, path string
		status             int
		contains           string
	}{
		{"healthz", "GET", "/healthz", 200, `"service":"mcp-absence"`},
		{"not found", "GET", "/nope", 404, ""},
		{"wrong method", "POST", "/healthz", 405, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status %d want %d", rec.Code, tc.status)
			}
			if !strings.Contains(rec.Body.String(), tc.contains) {
				t.Fatalf("body %q lacks %q", rec.Body.String(), tc.contains)
			}
		})
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(NewHandler(quiet))
	defer ok.Close()
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	if err := Healthcheck(ok.URL + "/healthz"); err != nil {
		t.Fatal(err)
	}
	if err := Healthcheck(bad.URL + "/healthz"); err == nil {
		t.Fatal("want error on 404")
	}
	if err := Healthcheck("http://127.0.0.1:1/healthz"); err == nil {
		t.Fatal("want error on refused connection")
	}
}

func TestRunGracefulShutdown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, addr, NewHandler(quiet), quiet) }()
	deadline := time.Now().Add(3 * time.Second)
	for Healthcheck("http://"+addr+"/healthz") != nil {
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

func TestRunListenError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := Run(context.Background(), l.Addr().String(), NewHandler(quiet), quiet); err == nil {
		t.Fatal("want error for busy port")
	}
}
