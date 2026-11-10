// Package agentclient calls the agent service: POST {AGENT_URL}/v1/turn answering text/event-stream
// (contracts/agent-stream.md). Core relays the events to the browser and persists what it must on the way.
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aibuddy/services/internal/platform/httpx"
)

// Event names of the agent stream contract.
const (
	EventStatus          = "status"
	EventToken           = "token"
	EventSources         = "sources"
	EventActionProposal  = "action_proposal"
	EventActionResult    = "action_result"
	EventEscalationDraft = "escalation_draft"
	EventNextStep        = "next_step"
	EventRefusal         = "refusal"
	EventToolResult      = "tool_result" // internal agent → core event of a confirm turn, never relayed to web
	EventError           = "error"
	EventDone            = "done"
)

// Turn kinds.
const (
	KindMessage = "message"
	KindConfirm = "confirm"
)

// TimeoutMargin is added to LLM_TIMEOUT_S for the overall agent call timeout (contracts/agent-stream.md).
const TimeoutMargin = 10 * time.Second

// Size limits of an agent stream (defaults of Client.MaxEventBytes and Client.MaxStreamBytes).
const (
	DefaultMaxEventBytes  = 256 << 10
	DefaultMaxStreamBytes = 4 << 20
)

// ErrTooLarge is matched (errors.Is) by the error of a stream whose event or total size exceeds the client's
// limits; such an error is also an *UnavailableError.
var ErrTooLarge = errors.New("agentclient: agent stream too large")

// TurnRequest is the body of POST /v1/turn.
type TurnRequest struct {
	SessionID   string          `json:"session_id"`
	MessageID   string          `json:"message_id,omitempty"` // id core will give the assistant message (optional)
	Turn        Turn            `json:"turn"`
	UserContext UserContext     `json:"user_context"`
	History     []HistoryItem   `json:"history"`
	Flags       map[string]bool `json:"flags,omitempty"` // runtime flags of the caller's team (R55)
}

// Turn is a user message (Kind = message, Text) or the confirmation of a stored proposal (Kind = confirm,
// ProposalID, Draft exactly as stored, ConfirmationToken signed by core): the agent then calls draft.tool and
// answers with a tool_result event.
type Turn struct {
	Kind              string          `json:"kind"`
	Text              string          `json:"text,omitempty"`
	ProposalID        string          `json:"proposal_id,omitempty"`
	Draft             json.RawMessage `json:"draft,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

// UserContext is what the agent knows about the caller; it is built from verified JWT claims and core's DB (R71).
type UserContext struct {
	UserID        string     `json:"user_id"`
	Role          string     `json:"role"`
	TeamID        string     `json:"team_id"`
	CurrentPeriod string     `json:"current_period"`
	DisplayName   string     `json:"display_name,omitempty"`
	OpenTasks     []OpenTask `json:"open_tasks"`
	Mentees       any        `json:"mentees,omitempty"` // mentor and lead: GET /api/team/interns items
}

// OpenTask is an open top-level task of the caller's own plan (status as GET /api/plan reports it).
type OpenTask struct {
	TaskID          string  `json:"task_id"`
	ChecklistItemID *string `json:"checklist_item_id,omitempty"` // nil for source = action
	Title           string  `json:"title"`
	Status          string  `json:"status"`
	Period          string  `json:"period"`
	DueAt           *string `json:"due_at,omitempty"`
	HumanOnly       bool    `json:"human_only"`
}

// HistoryItem is one earlier message of the session.
type HistoryItem struct {
	Author string `json:"author"`
	Text   string `json:"text"`
}

// Event is one event of the agent stream; Data is the JSON object of its data line, relayed unchanged.
type Event struct {
	Name string
	Data json.RawMessage
}

// ErrUnavailable is matched (errors.Is) by every error meaning the agent could not be reached, answered with a
// non-200 status, broke the stream or ran past the timeout.
var ErrUnavailable = errors.New("agentclient: agent unavailable")

// UnavailableError is the typed error for an unreachable or failing agent.
type UnavailableError struct {
	Op     string // connect, status 503, read
	Err    error  // cause, may be nil
	Status int    // HTTP status of a non-200 answer, 0 otherwise
	// Code, Message and Retryable come from the agent's JSON error body {"error": {code, message, retryable}}
	// of a non-200 answer, when it sent one.
	Code      string
	Message   string
	Retryable bool
}

// Error implements error.
func (e *UnavailableError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("agentclient: %s: agent unavailable", e.Op)
	}
	return fmt.Sprintf("agentclient: %s: agent unavailable: %v", e.Op, e.Err)
}

// Unwrap returns the cause.
func (e *UnavailableError) Unwrap() error { return e.Err }

// Is makes errors.Is(err, ErrUnavailable) true.
func (e *UnavailableError) Is(target error) bool { return target == ErrUnavailable }

// Client calls the agent. Fields may be changed before first use.
type Client struct {
	BaseURL      string        // AGENT_URL, e.g. http://agent:8090
	HTTPClient   *http.Client  // nil means http.DefaultClient (no client timeout: Timeout bounds the call)
	Timeout      time.Duration // whole call including the stream; <= 0 means none
	ServiceToken string        // AGENT_SERVICE_TOKEN, sent as X-Service-Token when set
	// MaxEventBytes bounds one event, MaxStreamBytes the whole answer; <= 0 means the defaults.
	MaxEventBytes  int
	MaxStreamBytes int64
}

// New returns a client for baseURL whose calls are cancelled after llmTimeout + TimeoutMargin.
func New(baseURL string, llmTimeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Timeout: llmTimeout + TimeoutMargin}
}

// Turn posts req with `Authorization: Bearer userToken` and X-Request-Id (from ctx) and returns the open event
// stream. Cancelling ctx (client disconnect) or reaching Timeout aborts the call. Connection failures and
// non-200 answers are *UnavailableError. The caller must Close the stream.
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

// limitedBody fails with ErrTooLarge once more than left bytes were read.
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

// Stream is an open agent event stream. It is not safe for concurrent use.
type Stream struct {
	body   io.ReadCloser
	r      *httpx.SSEReader
	cancel context.CancelFunc
}

// Next returns the next event, io.EOF when the agent closed the stream normally, or an *UnavailableError when the
// stream broke or timed out. Events whose data is not JSON are skipped.
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

// Close aborts the call and releases the connection; it is safe to call more than once.
func (s *Stream) Close() error {
	s.cancel()
	return s.body.Close()
}
