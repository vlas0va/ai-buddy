package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

// syncRecorder is a flushable ResponseWriter that can be read while the keep-alive goroutine writes.
type syncRecorder struct {
	mu       sync.Mutex
	header   http.Header
	buf      bytes.Buffer
	code     int
	flushes  int
	writeErr error
	flushErr error
}

func newSyncRecorder() *syncRecorder { return &syncRecorder{header: http.Header{}} }

func (s *syncRecorder) Header() http.Header { return s.header }
func (s *syncRecorder) WriteHeader(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		s.code = code
	}
}
func (s *syncRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.buf.Write(b)
}
func (s *syncRecorder) FlushError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		s.code = http.StatusOK
	}
	s.flushes++
	if s.flushes > 1 && s.flushErr != nil {
		return s.flushErr
	}
	return nil
}
func (s *syncRecorder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestSSEWriter_FramesAndHeaders_R78(t *testing.T) {
	rec := newSyncRecorder()
	sse, err := NewSSEWriter(context.Background(), rec, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer sse.Close()
	if rec.code != http.StatusOK || rec.header.Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		rec.header.Get("Cache-Control") != "no-cache" || rec.header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("code %d headers %v", rec.code, rec.header)
	}
	if err := sse.Send("status", map[string]string{"stage": "thinking"}); err != nil {
		t.Fatal(err)
	}
	pretty := json.RawMessage("{\n  \"text\": \"Отгул <пятница> & выходные\"\n}")
	if err := sse.Send("token", pretty); err != nil {
		t.Fatal(err)
	}
	want := "id: 1\nevent: status\ndata: {\"stage\":\"thinking\"}\n\n" +
		"id: 2\nevent: token\ndata: {\"text\":\"Отгул <пятница> & выходные\"}\n\n"
	if got := rec.String(); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if sse.LastID() != 2 {
		t.Fatalf("LastID %d", sse.LastID())
	}
}

func TestSSEWriter_KeepAlive_R78(t *testing.T) {
	rec := newSyncRecorder()
	sse, err := NewSSEWriter(context.Background(), rec, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(rec.String(), ": ping\n\n") {
		if time.Now().After(deadline) {
			t.Fatal("no keep-alive ping")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := sse.Close(); err != nil {
		t.Fatal(err)
	}
	after := rec.String()
	time.Sleep(30 * time.Millisecond)
	if rec.String() != after {
		t.Fatal("pings continued after Close")
	}
	if err := sse.Send("done", map[string]string{}); !errors.Is(err, ErrSSEClosed) {
		t.Fatalf("Send after Close: %v", err)
	}
	if err := sse.Close(); err != nil {
		t.Fatal("Close must be idempotent")
	}
}

func TestSSEWriter_DefaultKeepAliveAndContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sse, err := NewSSEWriter(ctx, newSyncRecorder(), 0)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := sse.Send("token", map[string]string{"text": "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send after cancel: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = sse.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked: keep-alive goroutine did not stop on ctx done")
	}
}

func TestSSEWriter_ConcurrentSends_R78(t *testing.T) {
	rec := newSyncRecorder()
	sse, err := NewSSEWriter(context.Background(), rec, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if err := sse.Send("token", map[string]int{"g": g, "i": i}); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	_ = sse.Close()
	r := NewSSEReader(strings.NewReader(rec.String()))
	n := 0
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		n++
		if ev.ID != strconv.Itoa(n) || ev.Name != "token" {
			t.Fatalf("event %d: %+v", n, ev)
		}
	}
	if n != 200 {
		t.Fatalf("got %d events", n)
	}
}

func TestSSEWriter_Errors(t *testing.T) {
	if _, err := NewSSEWriter(context.Background(), &plainWriter{h: http.Header{}}, 0); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("non-flusher: %v", err)
	}
	rec := newSyncRecorder()
	sse, err := NewSSEWriter(context.Background(), rec, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer sse.Close()
	for _, name := range []string{"", "a\nb", "a\rb"} {
		if err := sse.Send(name, map[string]string{}); err == nil {
			t.Errorf("event name %q accepted", name)
		}
	}
	if err := sse.Send("token", make(chan int)); err == nil {
		t.Error("unencodable data accepted")
	}
	if err := sse.Send("token", json.RawMessage(`{bad`)); err == nil {
		t.Error("invalid raw JSON accepted")
	}
	rec.mu.Lock()
	rec.writeErr = errors.New("client gone")
	rec.mu.Unlock()
	err1 := sse.Send("token", map[string]string{"text": "a"})
	err2 := sse.Send("token", map[string]string{"text": "b"})
	if err1 == nil || !errors.Is(err2, err1) {
		t.Fatalf("write errors: %v / %v", err1, err2)
	}
	if sse.LastID() != 1 {
		t.Fatalf("failed write still consumed one id, LastID=%d", sse.LastID())
	}

	frec := newSyncRecorder()
	frec.flushErr = errors.New("flush failed")
	fsse, err := NewSSEWriter(context.Background(), frec, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer fsse.Close()
	if err := fsse.Send("token", map[string]string{}); err == nil || !strings.Contains(err.Error(), "flush") {
		t.Fatalf("flush error: %v", err)
	}
}

// End to end over a real HTTP server: middleware chain keeps flushing working, the reader parses the stream.
func TestSSE_OverHTTPWithMiddleware_R78(t *testing.T) {
	h := Chain(RequestID, Recover(nil), AccessLog(nil))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse, err := NewSSEWriter(r.Context(), w, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer sse.Close()
		_ = sse.Send("status", map[string]string{"stage": "calling_service"})
		_ = sse.Send("done", map[string]string{"message_id": "m1"})
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := NewSSEReader(resp.Body)
	var names []string
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, ev.ID+":"+ev.Name+":"+string(ev.Data))
	}
	want := `1:status:{"stage":"calling_service"}|2:done:{"message_id":"m1"}`
	if strings.Join(names, "|") != want {
		t.Fatalf("got %v", names)
	}
}

func TestSSEReader_Parse_R78(t *testing.T) {
	type ev struct{ id, name, data string }
	tests := []struct {
		name   string
		stream string
		want   []ev
	}{
		{"basic LF", "id: 1\nevent: status\ndata: {\"stage\":\"thinking\"}\n\nid: 2\nevent: done\ndata: {}\n\n",
			[]ev{{"1", "status", `{"stage":"thinking"}`}, {"2", "done", `{}`}}},
		{"CRLF", "id: 1\r\nevent: token\r\ndata: {\"text\":\"a\"}\r\n\r\n", []ev{{"1", "token", `{"text":"a"}`}}},
		{"CR only", "event: token\rdata: [1]\r\r", []ev{{"", "token", `[1]`}}},
		{"multi-line data", "event: token\ndata: {\"a\":\ndata: 1}\n\n", []ev{{"", "token", "{\"a\":\n1}"}}},
		{"comments and ping", ": ping\n\n: hello\nevent: done\n: inner\ndata: {}\n\n", []ev{{"", "done", `{}`}}},
		{"final event without blank line", "event: done\ndata: {\"message_id\":\"m\"}", []ev{{"", "done", `{"message_id":"m"}`}}},
		{"final event with single newline", "event: done\ndata: {}\n", []ev{{"", "done", `{}`}}},
		{"no space after colon", "event:token\ndata:{\"x\":1}\n\n", []ev{{"", "token", `{"x":1}`}}},
		{"two spaces keep one", "data:  \"x\"\n\n", []ev{{"", "message", ` "x"`}}},
		{"id persists", "id: 5\ndata: 1\n\ndata: 2\n\n", []ev{{"5", "message", `1`}, {"5", "message", `2`}}},
		{"id with NUL ignored", "id: 1\ndata: 1\n\nid: a\x00b\ndata: 2\n\n", []ev{{"1", "message", `1`}, {"1", "message", `2`}}},
		{"empty id resets", "id: 1\ndata: 1\n\nid\ndata: 2\n\n", []ev{{"1", "message", `1`}, {"", "message", `2`}}},
		{"BOM", "\xef\xbb\xbfevent: token\ndata: true\n\n", []ev{{"", "token", `true`}}},
		{"unknown fields and retry ignored", "retry: 1000\nfoo: bar\nevent: token\ndata: null\n\n", []ev{{"", "token", `null`}}},
		{"event without data is dropped, name resets", "event: lonely\n\ndata: 1\n\n", []ev{{"", "message", `1`}}},
		{"blank lines only", "\n\n\r\n", nil},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []string{"whole", "one byte"} {
				var src io.Reader = strings.NewReader(tt.stream)
				if mode == "one byte" {
					src = iotest.OneByteReader(src) // "\r" and "\n" arrive in separate reads
				}
				r := NewSSEReader(src)
				var got []ev
				for {
					e, err := r.Next()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatalf("%s: %v", mode, err)
					}
					got = append(got, ev{e.ID, e.Name, string(e.Data)})
				}
				if len(got) != len(tt.want) {
					t.Fatalf("%s: got %v want %v", mode, got, tt.want)
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Fatalf("%s: event %d got %+v want %+v", mode, i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestSSEReader_InvalidDataAndLongLine(t *testing.T) {
	r := NewSSEReader(strings.NewReader("event: token\ndata: not json\n\ndata\n\nevent: done\ndata: {}\n\n"))
	ev, err := r.Next()
	if !errors.Is(err, ErrSSEData) || ev.Name != "token" || string(ev.Data) != "not json" {
		t.Fatalf("invalid data: %+v %v", ev, err)
	}
	if _, err := r.Next(); !errors.Is(err, ErrSSEData) {
		t.Fatalf("field without colon gives empty data: %v", err)
	}
	ev, err = r.Next()
	if err != nil || ev.Name != "done" {
		t.Fatalf("reader must continue: %+v %v", ev, err)
	}
	long := "data: \"" + strings.Repeat("a", MaxSSELine+10) + "\"\n\n"
	if _, err := NewSSEReader(strings.NewReader(long)).Next(); !errors.Is(err, ErrSSETooLarge) {
		t.Fatalf("long line: %v", err)
	}
}

func TestSSEReader_EventLimit_R76(t *testing.T) {
	small := "event: token\ndata: {\"text\":\"ok\"}\n\n"
	for _, tc := range []struct {
		name, stream string
		limit        int
		ok           bool
	}{
		{"within limit", small, 64, true},
		{"one long line", "data: \"" + strings.Repeat("a", 200) + "\"\n\n", 64, false},
		{"many short data lines", strings.Repeat("data: 1\n", 40) + "\n", 64, false},
		{"negative limit means none", "data: \"" + strings.Repeat("a", 200) + "\"\n\n", -1, true},
		{"limit above MaxSSELine", small, MaxSSELine * 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewSSEReaderLimit(strings.NewReader(small+tc.stream), tc.limit)
			if ev, err := r.Next(); err != nil || ev.Name != "token" {
				t.Fatalf("first event: %+v %v", ev, err)
			}
			_, err := r.Next()
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, ErrSSETooLarge)) {
				t.Fatalf("second event: %v", err)
			}
			if !tc.ok {
				if _, again := r.Next(); !errors.Is(again, ErrSSETooLarge) {
					t.Fatalf("the reader must stop after an oversized event: %v", again)
				}
			}
		})
	}
}
