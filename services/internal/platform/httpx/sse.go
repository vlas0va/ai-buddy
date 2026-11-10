package httpx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultKeepAlive is the interval of `: ping` comments on an SSE stream (contracts/agent-stream.md).
const DefaultKeepAlive = 15 * time.Second

// ErrSSEClosed is returned by SSEWriter.Send after Close.
var ErrSSEClosed = errors.New("httpx: sse stream closed")

// SSEWriter writes Server-Sent Events (R78): every event gets an incrementing `id:` (from 1), an `event:` line and
// one `data:` line of compact JSON, and is flushed immediately. A goroutine writes `: ping` comments every
// keep-alive interval until Close or until the context is done. All methods are safe for concurrent use.
// Always `defer sse.Close()` in the handler: the ping goroutine must stop before the handler returns.
type SSEWriter struct {
	ctx    context.Context
	w      http.ResponseWriter
	rc     *http.ResponseController
	mu     sync.Mutex
	id     int64
	err    error
	closed bool
	stop   chan struct{}
	wg     sync.WaitGroup
}

// NewSSEWriter sets the SSE headers, sends the 200 status, flushes and starts the keep-alive goroutine
// (keepAlive <= 0 means DefaultKeepAlive). ctx is normally r.Context(). It fails, without writing anything,
// when w cannot flush. The server's write deadline is cleared for the stream when the writer supports it.
func NewSSEWriter(ctx context.Context, w http.ResponseWriter, keepAlive time.Duration) (*SSEWriter, error) {
	if keepAlive <= 0 {
		keepAlive = DefaultKeepAlive
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("httpx: sse: response writer cannot flush: %w", err)
	}
	_ = rc.SetWriteDeadline(time.Time{}) // long-lived stream; ErrNotSupported is fine
	s := &SSEWriter{ctx: ctx, w: w, rc: rc, stop: make(chan struct{})}
	s.wg.Add(1)
	go s.keepAlive(keepAlive)
	return s, nil
}

func (s *SSEWriter) keepAlive(every time.Duration) {
	defer s.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			if err := s.write(func(b *bytes.Buffer) { b.WriteString(": ping\n\n") }); err != nil {
				return
			}
		}
	}
}

// Send writes one event with the given name and data encoded as single-line JSON (HTML characters are not
// escaped; json.RawMessage is compacted, so relayed agent events keep their meaning). It returns the first
// write error of the stream, ctx.Err() once the context is done, or ErrSSEClosed after Close.
func (s *SSEWriter) Send(event string, data any) error {
	if event == "" || strings.ContainsAny(event, "\r\n") {
		return fmt.Errorf("httpx: sse: invalid event name %q", event)
	}
	var payload bytes.Buffer
	enc := json.NewEncoder(&payload)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("httpx: sse: encode %s: %w", event, err)
	}
	line := bytes.TrimRight(payload.Bytes(), "\n")
	return s.write(func(b *bytes.Buffer) {
		s.id++
		b.WriteString("id: ")
		b.WriteString(strconv.FormatInt(s.id, 10))
		b.WriteString("\nevent: ")
		b.WriteString(event)
		b.WriteString("\ndata: ")
		b.Write(line)
		b.WriteString("\n\n")
	})
}

// write builds a frame under the lock (so ids are assigned in write order), writes and flushes it.
func (s *SSEWriter) write(frame func(*bytes.Buffer)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return ErrSSEClosed
	case s.err != nil:
		return s.err
	case s.ctx.Err() != nil:
		return s.ctx.Err()
	}
	var b bytes.Buffer
	frame(&b)
	if _, err := s.w.Write(b.Bytes()); err != nil {
		s.err = fmt.Errorf("httpx: sse: write: %w", err)
		return s.err
	}
	if err := s.rc.Flush(); err != nil {
		s.err = fmt.Errorf("httpx: sse: flush: %w", err)
		return s.err
	}
	return nil
}

// LastID returns the id of the last event sent (0 before the first one).
func (s *SSEWriter) LastID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Close stops the keep-alive goroutine and waits for it; later Sends return ErrSSEClosed. It is idempotent and
// does not close the underlying connection (the handler returning does).
func (s *SSEWriter) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.stop)
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

// MaxSSELine is the longest line SSEReader accepts (1 MiB).
const MaxSSELine = 1 << 20

// ErrSSEData marks an event whose data is not valid JSON; the reader can continue with the next event.
var ErrSSEData = errors.New("httpx: sse: event data is not valid JSON")

// Event is one Server-Sent Event. Name defaults to "message"; ID is the last id seen on the stream (the SSE
// "last event ID"), so it persists across events that carry no id line.
type Event struct {
	ID   string
	Name string
	Data json.RawMessage
}

// SSEReader parses a text/event-stream (WHATWG rules): LF, CR and CRLF line ends, a leading BOM, comment lines,
// `field:value` with one optional space, several data lines joined with "\n", unknown fields and retry ignored.
// Unlike the browser, a final event that is not followed by a blank line is still delivered.
type SSEReader struct {
	sc      *bufio.Scanner
	lastID  string
	started bool
}

// NewSSEReader returns a reader over r (typically an HTTP response body).
func NewSSEReader(r io.Reader) *SSEReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), MaxSSELine)
	sc.Split(splitLines)
	return &SSEReader{sc: sc}
}

// Next returns the next event, io.EOF at the end of the stream, a read error, or the event together with an
// error wrapping ErrSSEData when its data is not valid JSON (Next may be called again after that).
func (r *SSEReader) Next() (Event, error) {
	var (
		name    string
		data    []byte
		hasData bool
	)
	for r.sc.Scan() {
		line := r.sc.Bytes()
		if !r.started {
			r.started = true
			line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
		}
		if len(line) == 0 {
			if !hasData {
				name = ""
				continue
			}
			return r.dispatch(name, data)
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			name = string(value)
		case "data":
			if hasData {
				data = append(data, '\n')
			}
			data = append(data, value...)
			hasData = true
		case "id":
			if bytes.IndexByte(value, 0) < 0 {
				r.lastID = string(value)
			}
		}
	}
	if err := r.sc.Err(); err != nil {
		return Event{}, fmt.Errorf("httpx: sse: read: %w", err)
	}
	if hasData {
		return r.dispatch(name, data)
	}
	return Event{}, io.EOF
}

func (r *SSEReader) dispatch(name string, data []byte) (Event, error) {
	if name == "" {
		name = "message"
	}
	ev := Event{ID: r.lastID, Name: name, Data: json.RawMessage(data)}
	if !json.Valid(data) {
		return ev, fmt.Errorf("%w: event %q", ErrSSEData, name)
	}
	return ev, nil
}

// splitLines is a bufio.SplitFunc for SSE line ends: "\r\n", "\n" or "\r".
func splitLines(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
		if i+1 < len(data) {
			if data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		}
		if !atEOF {
			return 0, nil, nil // a "\n" may follow in the next read
		}
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
