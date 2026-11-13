// Пакет agenttest — управляемая сценарием подделка сервиса агента (POST /v1/turn, contracts/agent-stream.md) для тестов
// ядра. По умолчанию ход-сообщение про «отгул» предлагает absence.create_request, про «доступ» — access.request_access,
// на остальное приходит простой ответ; ход подтверждения вызывает draft.tool на MCP-серверах по MCPBaseURL с JWT
// пользователя, idempotency_key = proposal_id и токеном подтверждения ядра (ровно как настоящий агент) и отвечает
// tool_result.
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

// Event — одно событие, которое отправляет подделка.
type Event struct {
	Name string
	Data any
}

// Request — ход, полученный подделкой.
type Request struct {
	agentclient.TurnRequest
	Auth string         // заголовок Authorization
	Raw  map[string]any // тело как JSON
}

// Agent — запущенная подделка. Поля сценария можно менять между ходами (читаются под блокировкой).
type Agent struct {
	URL        string // базовый URL для AGENT_URL
	MCPBaseURL string // куда ходы подтверждения отправляют вызов инструмента записи

	mu        sync.Mutex
	reqs      []Request
	onMessage func(Request) []Event
	onConfirm func(Request) []Event
	status    int  // не ноль: отвечать на каждый ход этим HTTP-статусом и JSON-ошибкой
	hang      bool // отправить одно событие status и ждать, пока клиент уйдёт
}

// New запускает подделку; она закрывается при очистке теста.
func New(t testing.TB, mcpBaseURL string) *Agent {
	t.Helper()
	a := &Agent{MCPBaseURL: mcpBaseURL}
	srv := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(srv.Close)
	a.URL = srv.URL
	return a
}

// OnMessage заменяет сценарий ходов-сообщений (nil возвращает сценарий по умолчанию).
func (a *Agent) OnMessage(f func(Request) []Event) { a.mu.Lock(); a.onMessage = f; a.mu.Unlock() }

// OnConfirm заменяет сценарий ходов подтверждения (nil возвращает сценарий по умолчанию).
func (a *Agent) OnConfirm(f func(Request) []Event) { a.mu.Lock(); a.onConfirm = f; a.mu.Unlock() }

// FailWith заставляет каждый ход отвечать HTTP-статусом и JSON-телом ошибки агента; 0 возвращает обычные ответы.
func (a *Agent) FailWith(status int) { a.mu.Lock(); a.status = status; a.mu.Unlock() }

// Hang заставляет каждый ход отправить одно событие status и затем ждать, пока вызывающий не сдастся.
func (a *Agent) Hang(on bool) { a.mu.Lock(); a.hang = on; a.mu.Unlock() }

// Requests возвращает полученные на текущий момент ходы.
func (a *Agent) Requests() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Request(nil), a.reqs...)
}

// Count возвращает, сколько ходов вида kind получено.
func (a *Agent) Count(kind string) int {
	n := 0
	for _, r := range a.Requests() {
		if r.Turn.Kind == kind {
			n++
		}
	}
	return n
}

// Last возвращает последний ход вида kind (пустой Request, если таких нет).
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
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/kb/") {
		// тестовые документы базы знаний: «mentor-only» недоступен (404), «broken» — сбой агента (500)
		switch id := strings.TrimPrefix(r.URL.Path, "/v1/kb/"); {
		case r.Header.Get("Authorization") == "":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case id == "mentor-only":
			http.NotFound(w, r)
		case id == "broken":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"doc_id": id, "title": "Документ " + id, "body": "# Текст",
				"source_type": "test", "badge": "Тестовая инструкция прототипа"})
		}
		return
	}
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

// NewID возвращает случайный UUID v4 (id черновиков).
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// AbsenceProposal — action_proposal на отгул с start по end (YYYY-MM-DD).
func AbsenceProposal(id, start, end string) Event {
	return Event{"action_proposal", map[string]any{"proposal_id": id, "kind": "absence_request", "summary": "Отгул " + start,
		"draft": map[string]any{"tool": "absence.create_request",
			"args":    map[string]any{"type": "day_off", "start_date": start, "end_date": end, "comment": ""},
			"display": map[string]any{"approver_display_name": "Мария Лебедева", "dates": start}}}}
}

// AccessProposal — action_proposal на доступ к resource.
func AccessProposal(id, resource string) Event {
	return Event{"action_proposal", map[string]any{"proposal_id": id, "kind": "access_request", "summary": "Доступ " + resource,
		"draft": map[string]any{"tool": "access.request_access", "args": map[string]any{"resource_id": resource, "reason": "Для работы"},
			"display": map[string]any{"resource_name": resource, "elevated": false}}}}
}

// Done — собственное событие done агента (ядро его поглощает).
func Done() Event {
	return Event{"done", map[string]any{"message_id": "", "provider": "stub", "intent": "test"}}
}

// Message — сценарий хода-сообщения по умолчанию.
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

// Confirm — сценарий хода подтверждения по умолчанию: вызывает draft.tool через MCP, как настоящий агент.
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
	kind := map[string]string{"absence.create_request": "absence_request", "absence.reschedule_request": "absence_reschedule", "access.request_access": "access_request",
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

// Token возвращает JWT демо-пользователя (для тестов).
func Token(t testing.TB, secret []byte, sub, role, team, cohort string) string {
	t.Helper()
	tok, err := authn.Issue(secret, authn.Claims{Sub: sub, Role: role, TeamID: team, CohortStart: cohort}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
