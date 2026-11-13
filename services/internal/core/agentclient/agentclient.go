// Пакет agentclient вызывает сервис агента: POST {AGENT_URL}/v1/turn с ответом text/event-stream
// (contracts/agent-stream.md). Ядро пересылает события в браузер и по пути сохраняет то, что нужно.
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aibuddy/services/internal/platform/httpx"
)

// Имена событий по контракту потока агента.
const (
	EventStatus          = "status"
	EventToken           = "token"
	EventSources         = "sources"
	EventActionProposal  = "action_proposal"
	EventActionResult    = "action_result"
	EventEscalationDraft = "escalation_draft"
	EventNextStep        = "next_step"
	EventRefusal         = "refusal"
	EventToolResult      = "tool_result" // внутреннее событие агент → ядро в ходе подтверждения, в веб не пересылается
	EventError           = "error"
	EventDone            = "done"
)

// Виды хода.
const (
	KindMessage = "message"
	KindConfirm = "confirm"
)

// TimeoutMargin добавляется к LLM_TIMEOUT_S для общего таймаута вызова агента (contracts/agent-stream.md).
const TimeoutMargin = 10 * time.Second

// Ограничения размера потока агента (значения по умолчанию для Client.MaxEventBytes и Client.MaxStreamBytes).
const (
	DefaultMaxEventBytes  = 256 << 10
	DefaultMaxStreamBytes = 4 << 20
)

// ErrTooLarge совпадает (errors.Is) с ошибкой потока, у которого событие или общий размер превышает лимиты клиента;
// такая ошибка также *UnavailableError.
var ErrTooLarge = errors.New("agentclient: agent stream too large")

// TurnRequest — тело POST /v1/turn.
type TurnRequest struct {
	SessionID   string          `json:"session_id"`
	MessageID   string          `json:"message_id,omitempty"` // id, который ядро присвоит сообщению ассистента (необязательно)
	Turn        Turn            `json:"turn"`
	UserContext UserContext     `json:"user_context"`
	History     []HistoryItem   `json:"history"`
	Flags       map[string]bool `json:"flags,omitempty"` // runtime-флаги команды вызывающего (R55)
}

// Ход — сообщение пользователя (Kind = message, Text) или подтверждение сохранённого черновика (Kind = confirm,
// ProposalID, Draft ровно как сохранён, ConfirmationToken, подписанный ядром): тогда агент вызывает draft.tool и
// отвечает событием tool_result.
type Turn struct {
	Kind              string          `json:"kind"`
	Text              string          `json:"text,omitempty"`
	ProposalID        string          `json:"proposal_id,omitempty"`
	Draft             json.RawMessage `json:"draft,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
	Attachments       []Attachment    `json:"attachments,omitempty"`
}

// Attachment — файл, приложенный к сообщению пользователя (contracts/agent-stream.md): у текстовых файлов передаётся
// текст, у картинок и документов — только метаданные.
type Attachment struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // text | image | document
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size"`
	Text string `json:"text,omitempty"`
}

// UserContext — то, что агент знает о вызывающем; собирается из проверенных claims JWT и базы ядра (R71).
type UserContext struct {
	UserID        string     `json:"user_id"`
	Role          string     `json:"role"`
	TeamID        string     `json:"team_id"`
	CurrentPeriod string     `json:"current_period"`
	DisplayName   string     `json:"display_name,omitempty"`
	Location      string     `json:"location,omitempty"` // город офиса или «Удалённо» (миграция 0005)
	OpenTasks     []OpenTask `json:"open_tasks"`
	Mentees       any        `json:"mentees,omitempty"` // наставник и руководитель: элементы GET /api/team/interns
}

// OpenTask — открытая задача верхнего уровня из собственного плана вызывающего (статус как в GET /api/plan).
type OpenTask struct {
	TaskID          string  `json:"task_id"`
	ChecklistItemID *string `json:"checklist_item_id,omitempty"` // nil для source = action
	Title           string  `json:"title"`
	Status          string  `json:"status"`
	Period          string  `json:"period"`
	DueAt           *string `json:"due_at,omitempty"`
	HumanOnly       bool    `json:"human_only"`
}

// HistoryItem — одно из прежних сообщений сессии.
type HistoryItem struct {
	Author string `json:"author"`
	Text   string `json:"text"`
}

// Event — одно событие потока агента; Data — JSON-объект из его строки data, пересылается без изменений.
type Event struct {
	Name string
	Data json.RawMessage
}

// ErrUnavailable совпадает (errors.Is) с любой ошибкой, означающей, что агент недоступен, ответил не 200, оборвал поток
// или превысил таймаут.
var ErrUnavailable = errors.New("agentclient: agent unavailable")

// UnavailableError — типизированная ошибка недоступного или сбоящего агента.
type UnavailableError struct {
	Op     string // connect, status 503, read
	Err    error  // причина, может быть nil
	Status int    // HTTP-статус ответа не 200, иначе 0
	// Code, Message и Retryable берутся из JSON-тела ошибки агента {"error": {code, message, retryable}} в ответе не
	// 200, если агент его прислал.
	Code      string
	Message   string
	Retryable bool
}

// Error реализует error.
func (e *UnavailableError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("agentclient: %s: agent unavailable", e.Op)
	}
	return fmt.Sprintf("agentclient: %s: agent unavailable: %v", e.Op, e.Err)
}

// Unwrap возвращает причину.
func (e *UnavailableError) Unwrap() error { return e.Err }

// Is делает errors.Is(err, ErrUnavailable) истинным.
func (e *UnavailableError) Is(target error) bool { return target == ErrUnavailable }

// Client вызывает агента. Поля можно менять до первого использования.
type Client struct {
	BaseURL      string        // AGENT_URL, e.g. http://agent:8090
	HTTPClient   *http.Client  // nil означает http.DefaultClient (без таймаута клиента: вызов ограничивает Timeout)
	Timeout      time.Duration // весь вызов вместе с потоком; <= 0 — без ограничения
	ServiceToken string        // AGENT_SERVICE_TOKEN, передаётся как X-Service-Token, если задан
	// MaxEventBytes ограничивает одно событие, MaxStreamBytes — весь ответ; <= 0 — значения по умолчанию.
	MaxEventBytes  int
	MaxStreamBytes int64
}

// New возвращает клиента для baseURL, вызовы которого отменяются через llmTimeout + TimeoutMargin.
func New(baseURL string, llmTimeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Timeout: llmTimeout + TimeoutMargin}
}

// Turn отправляет req с `Authorization: Bearer userToken` и X-Request-Id (из ctx) и возвращает открытый поток событий.
// Отмена ctx (клиент отключился) или достижение Timeout прерывает вызов. Ошибки соединения и ответы не 200 —
// *UnavailableError. Вызывающий должен закрыть поток (Close).
func (c *Client) Turn(ctx context.Context, userToken string, req TurnRequest) (*Stream, error) {
	if req.History == nil {
		req.History = []HistoryItem{}
	}
	if req.UserContext.OpenTasks == nil {
		req.UserContext.OpenTasks = []OpenTask{}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agentclient: encode turn: %w", err)
	}
	cancel := context.CancelFunc(func() {})
	if c.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/turn", bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, &UnavailableError{Op: "build request", Err: err}
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	hreq.Header.Set("Authorization", "Bearer "+userToken)
	if id := httpx.RequestIDFromContext(ctx); id != "" {
		hreq.Header.Set(httpx.HeaderRequestID, id)
	}
	if c.ServiceToken != "" {
		hreq.Header.Set("X-Service-Token", c.ServiceToken)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		cancel()
		return nil, &UnavailableError{Op: "connect", Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		ue := &UnavailableError{Op: fmt.Sprintf("status %d", resp.StatusCode), Status: resp.StatusCode}
		var body struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) == nil {
			ue.Code, ue.Message, ue.Retryable = body.Error.Code, body.Error.Message, body.Error.Retryable
		}
		_ = resp.Body.Close()
		cancel()
		return nil, ue
	}
	maxEvent, maxStream := c.MaxEventBytes, c.MaxStreamBytes
	if maxEvent <= 0 {
		maxEvent = DefaultMaxEventBytes
	}
	if maxStream <= 0 {
		maxStream = DefaultMaxStreamBytes
	}
	limited := &limitedBody{r: resp.Body, left: maxStream}
	return &Stream{body: resp.Body, r: httpx.NewSSEReaderLimit(limited, maxEvent), cancel: cancel}, nil
}

// limitedBody падает с ErrTooLarge, как только прочитано больше left байт.
type limitedBody struct {
	r    io.Reader
	left int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}

// Stream — открытый поток событий агента. Не безопасен для конкурентного использования.
type Stream struct {
	body   io.ReadCloser
	r      *httpx.SSEReader
	cancel context.CancelFunc
}

// Next возвращает следующее событие, io.EOF, если агент штатно закрыл поток, или *UnavailableError, если поток
// оборвался или истёк таймаут. События, у которых data не JSON, пропускаются.
func (s *Stream) Next() (Event, error) {
	for {
		ev, err := s.r.Next()
		switch {
		case err == nil:
			return Event{Name: ev.Name, Data: ev.Data}, nil
		case errors.Is(err, httpx.ErrSSEData):
			continue
		case errors.Is(err, io.EOF):
			return Event{}, io.EOF
		case errors.Is(err, httpx.ErrSSETooLarge) && !errors.Is(err, ErrTooLarge):
			return Event{}, &UnavailableError{Op: "read", Err: fmt.Errorf("%w: %w", ErrTooLarge, err)}
		default:
			return Event{}, &UnavailableError{Op: "read", Err: err}
		}
	}
}

// Close прерывает вызов и освобождает соединение; вызывать можно больше одного раза.
func (s *Stream) Close() error {
	s.cancel()
	return s.body.Close()
}

// KBDoc возвращает тестовый документ базы знаний, который пользователю можно читать (GET /v1/kb/{doc_id}: агент
// проверяет JWT и ACL). found = false для отсутствующего или недоступного документа; остальные сбои —
// *UnavailableError.
func (c *Client) KBDoc(ctx context.Context, userToken, docID string) (doc json.RawMessage, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v1/kb/"+url.PathEscape(docID), nil)
	if err != nil {
		return nil, false, &UnavailableError{Op: "build request", Err: err}
	}
	hreq.Header.Set("Authorization", "Bearer "+userToken)
	if c.ServiceToken != "" {
		hreq.Header.Set("X-Service-Token", c.ServiceToken)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return nil, false, &UnavailableError{Op: "connect", Err: err}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		if err != nil || !json.Valid(body) {
			return nil, false, &UnavailableError{Op: "read document", Err: err}
		}
		return body, true, nil
	case http.StatusNotFound, http.StatusForbidden:
		return nil, false, nil
	}
	return nil, false, &UnavailableError{Op: fmt.Sprintf("status %d", resp.StatusCode), Status: resp.StatusCode}
}
