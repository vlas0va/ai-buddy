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

// DefaultKeepAlive — интервал комментариев `: ping` в потоке SSE (contracts/agent-stream.md).
const DefaultKeepAlive = 15 * time.Second

// ErrSSEClosed возвращает SSEWriter.Send после Close.
var ErrSSEClosed = errors.New("httpx: sse stream closed")

// SSEWriter пишет Server-Sent Events (R78): каждое событие получает возрастающий `id:` (с 1), строку `event:` и одну
// строку `data:` с компактным JSON и сразу сбрасывается. Горутина пишет комментарии `: ping` раз в интервал keep-alive
// до Close или завершения контекста. Все методы безопасны для конкурентного использования. Всегда пишите `defer
// sse.Close()` в обработчике: горутина пингов должна остановиться до возврата из обработчика.
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

// NewSSEWriter ставит заголовки SSE, отправляет статус 200, сбрасывает и запускает горутину keep-alive (keepAlive <= 0
// — DefaultKeepAlive). ctx обычно r.Context(). Падает, ничего не записав, если w не умеет сбрасывать. Дедлайн записи
// сервера для потока снимается, если writer это поддерживает.
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
	_ = rc.SetWriteDeadline(time.Time{}) // долгоживущий поток; ErrNotSupported допустим
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

// Send пишет одно событие с указанным именем и данными в виде однострочного JSON (HTML-символы не экранируются;
// json.RawMessage уплотняется, поэтому пересылаемые события агента сохраняют смысл). Возвращает первую ошибку записи
// потока, ctx.Err() после завершения контекста или ErrSSEClosed после Close.
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

// write собирает кадр под блокировкой (чтобы id назначались в порядке записи), пишет и сбрасывает его.
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

// LastID возвращает id последнего отправленного события (0 до первого).
func (s *SSEWriter) LastID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Close останавливает горутину keep-alive и ждёт её; последующие Send возвращают ErrSSEClosed. Идемпотентен и не
// закрывает нижележащее соединение (это делает возврат из обработчика).
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

// MaxSSELine — самая длинная строка, которую принимает SSEReader (1 МиБ).
const MaxSSELine = 1 << 20

// ErrSSEData отмечает событие, у которого data — некорректный JSON; читатель может продолжить со следующего события.
var ErrSSEData = errors.New("httpx: sse: event data is not valid JSON")

// ErrSSETooLarge отмечает строку длиннее MaxSSELine или событие сверх лимита читателя (NewSSEReaderLimit). Дальше поток
// читать нельзя.
var ErrSSETooLarge = errors.New("httpx: sse: event too large")

// Event — одно Server-Sent Event. Name по умолчанию "message"; ID — последний id, встреченный в потоке (SSE «last event
// ID»), поэтому он сохраняется и для событий без строки id.
type Event struct {
	ID   string
	Name string
	Data json.RawMessage
}

// SSEReader разбирает text/event-stream (правила WHATWG): концы строк LF, CR и CRLF, BOM в начале, строки-комментарии,
// `field:value` с одним необязательным пробелом, несколько строк data склеиваются через "\n", неизвестные поля и retry
// игнорируются. В отличие от браузера, последнее событие без следующей пустой строки всё равно доставляется.
type SSEReader struct {
	sc       *bufio.Scanner
	lastID   string
	started  bool
	maxEvent int // байт data на событие; 0 — без лимита, кроме MaxSSELine на строку
	failed   error
}

// NewSSEReader возвращает читатель поверх r (обычно тела HTTP-ответа).
func NewSSEReader(r io.Reader) *SSEReader {
	return NewSSEReaderLimit(r, 0)
}

// sseLineSlack — запас под имя поля ("data: ") в строке ограниченного читателя.
const sseLineSlack = 64

// NewSSEReaderLimit — это NewSSEReader с лимитом maxEvent байт на data одного события (все его строки data вместе) и на
// одну строку (плюс запас под имя поля), для потоков от менее доверенных сторон; maxEvent <= 0 — без лимита на событие.
// Большее событие заставляет Next вернуть ErrSSETooLarge и остановиться.
func NewSSEReaderLimit(r io.Reader, maxEvent int) *SSEReader {
	maxLine := MaxSSELine
	if maxEvent > 0 && maxEvent+sseLineSlack < maxLine {
		maxLine = maxEvent + sseLineSlack
	} else if maxEvent < 0 {
		maxEvent = 0
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, min(4096, maxLine)), maxLine)
	sc.Split(splitLines)
	return &SSEReader{sc: sc, maxEvent: maxEvent}
}

// Next возвращает следующее событие, io.EOF в конце потока, ошибку чтения или событие вместе с ошибкой, оборачивающей
// ErrSSEData, если его data — некорректный JSON (после этого Next можно вызывать снова).
func (r *SSEReader) Next() (Event, error) {
	if r.failed != nil {
		return Event{}, r.failed
	}
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
			if r.maxEvent > 0 && len(data)+1+len(value) > r.maxEvent {
				r.failed = fmt.Errorf("%w: more than %d bytes of data", ErrSSETooLarge, r.maxEvent)
				return Event{}, r.failed
			}
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
		if errors.Is(err, bufio.ErrTooLong) {
			r.failed = fmt.Errorf("%w: line too long", ErrSSETooLarge)
			return Event{}, r.failed
		}
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

// splitLines — bufio.SplitFunc для концов строк SSE: "\r\n", "\n" или "\r".
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
			return 0, nil, nil // "\n" может прийти в следующем чтении
		}
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
