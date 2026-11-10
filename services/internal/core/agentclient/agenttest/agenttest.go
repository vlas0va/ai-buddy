// Package agenttest is a scriptable fake of the agent service (POST /v1/turn, contracts/agent-stream.md) for core
// tests. By default a message turn about «отгул» proposes absence.create_request, one about «доступ» proposes
// access.request_access, anything else gets a plain answer; a confirm turn calls draft.tool on the MCP servers at
// MCPBaseURL with the user's JWT, idempotency_key = proposal_id and core's confirmation token (exactly what the real
// agent does) and answers with tool_result.
package agenttest

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/confirm"
	"aibuddy/services/internal/platform/mcpx"
)

// Event is one event the fake sends.
type Event struct {
	Name string
	Data any
}

// Request is a turn received by the fake.
type Request struct {
	agentclient.TurnRequest
	Auth string         // Authorization header
	Raw  map[string]any // body as JSON
}

// Agent is the running fake. Script fields may be changed between turns (they are read under the lock).
type Agent struct {
	URL        string // base URL to use as AGENT_URL
	MCPBaseURL string // where confirm turns call the write tool

	mu        sync.Mutex
	reqs      []Request
	onMessage func(Request) []Event
	onConfirm func(Request) []Event
	status    int  // non-zero: answer every turn with this HTTP status and a JSON error
	hang      bool // send one status event, then wait for the client to go away
}

// New starts the fake; it is closed at test cleanup.
func New(t testing.TB, mcpBaseURL string) *Agent {
	t.Helper()
	a := &Agent{MCPBaseURL: mcpBaseURL}
	srv := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(srv.Close)
	a.URL = srv.URL
	return a
}

// OnMessage replaces the script of message turns (nil restores the default).
func (a *Agent) OnMessage(f func(Request) []Event) { a.mu.Lock(); a.onMessage = f; a.mu.Unlock() }

// OnConfirm replaces the script of confirm turns (nil restores the default).
func (a *Agent) OnConfirm(f func(Request) []Event) { a.mu.Lock(); a.onConfirm = f; a.mu.Unlock() }

// FailWith makes every turn answer with HTTP status and the agent's JSON error body; 0 restores normal answers.
func (a *Agent) FailWith(status int) { a.mu.Lock(); a.status = status; a.mu.Unlock() }

// Hang makes every turn send one status event and then block until the caller gives up.
func (a *Agent) Hang(on bool) { a.mu.Lock(); a.hang = on; a.mu.Unlock() }

// Requests returns the turns received so far.
func (a *Agent) Requests() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Request(nil), a.reqs...)
}

// Count returns how many turns of kind were received.
func (a *Agent) Count(kind string) int {
	n := 0
	for _, r := range a.Requests() {
		if r.Turn.Kind == kind {
			n++
		}
	}
	return n
}

// Last returns the last turn of kind (zero Request when there is none).
func (a *Agent) Last(kind string) Request {
	reqs := a.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].Turn.Kind == kind {
			return reqs[i]
		}
	}
	return Request{}
}

func (a *Agent) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/turn" {
		http.NotFound(w, r)
		return
	}
	var req Request
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.Unmarshal(raw, &req.TurnRequest)
	_ = json.Unmarshal(raw, &req.Raw)
	req.Auth = r.Header.Get("Authorization")
	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	status, hang, onMessage, onConfirm := a.status, a.hang, a.onMessage, a.onConfirm
	a.mu.Unlock()
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"code": "context_mismatch", "message": "Контекст пользователя не совпадает с токеном.", "retryable": false}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	out := &writer{w: w}
	if hang {
		out.send(Event{"status", map[string]string{"stage": "thinking"}})
		<-r.Context().Done()
		return
	}
	var events []Event
	switch {
	case req.Turn.Kind == agentclient.KindConfirm && onConfirm != nil:
		events = onConfirm(req)
	case req.Turn.Kind == agentclient.KindConfirm:
		events = a.Confirm(r.Context(), req)
	case onMessage != nil:
		events = onMessage(req)
	default:
		events = Message(req)
	}
	for _, ev := range events {
		out.send(ev)
	}
}

type writer struct {
	w  http.ResponseWriter
	id int
}

func (o *writer) send(ev Event) {
	o.id++
	var data []byte
	if raw, ok := ev.Data.(json.RawMessage); ok {
		data = raw
	} else {
		data, _ = json.Marshal(ev.Data)
	}
	fmt.Fprintf(o.w, "id: %d\nevent: %s\ndata: %s\n\n", o.id, ev.Name, data)
	if f, ok := o.w.(http.Flusher); ok {
		f.Flush()
	}
}

// NewID returns a random UUID v4 (proposal ids).
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// AbsenceProposal is an action_proposal for a day off from start to end (YYYY-MM-DD).
func AbsenceProposal(id, start, end string) Event {
	return Event{"action_proposal", map[string]any{"proposal_id": id, "kind": "absence_request", "summary": "Отгул " + start,
		"draft": map[string]any{"tool": "absence.create_request",
			"args":    map[string]any{"type": "day_off", "start_date": start, "end_date": end, "comment": ""},
			"display": map[string]any{"approver_display_name": "Мария Лебедева", "dates": start}}}}
}

// AccessProposal is an action_proposal for access to resource.
func AccessProposal(id, resource string) Event {
	return Event{"action_proposal", map[string]any{"proposal_id": id, "kind": "access_request", "summary": "Доступ " + resource,
		"draft": map[string]any{"tool": "access.request_access", "args": map[string]any{"resource_id": resource, "reason": "Для работы"},
			"display": map[string]any{"resource_name": resource, "elevated": false}}}}
}

// Done is the agent's own done event (core swallows it).
func Done() Event {
	return Event{"done", map[string]any{"message_id": "", "provider": "stub", "intent": "test"}}
}

// Message is the default script of a message turn.
func Message(req Request) []Event {
	text := strings.ToLower(req.Turn.Text)
	events := []Event{{"status", map[string]string{"stage": "thinking"}}}
	switch {
	case strings.Contains(text, "отгул"):
		events = append(events,
			Event{"status", map[string]string{"stage": "searching_kb"}},
			Event{"token", map[string]string{"text": "Отгул согласует руководитель. "}},
			Event{"token", map[string]string{"text": "Подготовил заявку на 13.11.2026."}},
			Event{"sources", map[string]any{"items": []map[string]string{{"doc_id": "absence-policy-delivery",
				"title": "Тестовая инструкция: отгул", "source_type": "test", "url": "/kb/absence-policy-delivery"}}}},
			AbsenceProposal(NewID(), "2026-11-13", "2026-11-13"))
	case strings.Contains(text, "доступ"):
		events = append(events, Event{"token", map[string]string{"text": "Подготовил заявку на доступ."}},
			AccessProposal(NewID(), "space-delivery-incidents"))
	default:
		events = append(events, Event{"token", map[string]string{"text": "Вот что я нашёл."}})
	}
	return append(events, Done())
}

// Confirm is the default script of a confirm turn: it calls draft.tool via MCP as the real agent does.
func (a *Agent) Confirm(ctx context.Context, req Request) []Event {
	var d struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(req.Turn.Draft, &d); err != nil || d.Args == nil {
		return []Event{{"error", map[string]any{"code": "bad_proposal", "message": "Некорректный черновик", "retryable": false}}}
	}
	server, tool, _ := strings.Cut(d.Tool, ".")
	args := map[string]any{}
	for k, v := range d.Args {
		args[k] = v
	}
	args[confirm.KeyIdempotency] = req.Turn.ProposalID
	args[confirm.KeyConfirmation] = req.Turn.ConfirmationToken
	token, _ := strings.CutPrefix(req.Auth, "Bearer ")
	out, toolErr, err := (&mcpx.Client{BaseURL: a.MCPBaseURL}).Call(ctx, token, server, tool, args)
	switch {
	case err != nil:
		return []Event{{"error", map[string]any{"code": "service_unavailable", "message": "Сервис недоступен", "retryable": true}}}
	case toolErr != nil:
		return []Event{{"error", map[string]any{"code": string(toolErr.Code), "message": toolErr.Message, "retryable": toolErr.Retryable}}}
	}
	kind := map[string]string{"absence.create_request": "absence_request", "access.request_access": "access_request",
		"approvals.decide": "approval_decision"}[d.Tool]
	var res struct {
		ID              string  `json:"id"`
		Status          string  `json:"status"`
		ChecklistItemID *string `json:"checklist_item_id"`
		Approver        string  `json:"approver_display_name"`
	}
	_ = json.Unmarshal(out, &res)
	return []Event{
		{"status", map[string]string{"stage": "calling_service"}},
		{"status", map[string]string{"stage": "saving"}},
		{"tool_result", map[string]any{"proposal_id": req.Turn.ProposalID, "kind": kind, "tool": d.Tool,
			"external_id": res.ID, "status": res.Status, "approver_display_name": res.Approver,
			"task_checklist_item_id": res.ChecklistItemID, "replayed": false,
			"followup": "Заявка отправлена на согласование.", "result": json.RawMessage(out)}},
		Done(),
	}
}

// Token returns a JWT for a demo user (tests).
func Token(t testing.TB, secret []byte, sub, role, team, cohort string) string {
	t.Helper()
	tok, err := authn.Issue(secret, authn.Claims{Sub: sub, Role: role, TeamID: team, CohortStart: cohort}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
