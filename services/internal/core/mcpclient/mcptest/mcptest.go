// Пакет mcptest запускает в памяти подделки MCP-серверов absence, access, approvals и kb для тестов ядра.
//
// Подделки — настоящие серверы mcpx, собранные из contracts/schemas, поэтому тест проходит через настоящий mcpx.Client,
// проверку JWT, белый список ролей, входные и выходные схемы и проверку токена подтверждения; подделано только
// хранилище — это map. Люди и ресурсы повторяют демо-данные (migrations/0003_seed_demo.sql).
package mcptest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/mcpx"
)

type person struct {
	name, team, lead string
}

var people = map[string]person{
	"intern.anna":   {"Анна Котова", "delivery", "lead.maria"},
	"intern.ivan":   {"Иван Белов", "search", "lead.dmitry"},
	"mentor.oleg":   {"Олег Мартынов", "delivery", "lead.maria"},
	"mentor.sergey": {"Сергей Орлов", "search", "lead.dmitry"},
	"lead.maria":    {"Мария Лебедева", "delivery", ""},
	"lead.dmitry":   {"Дмитрий Волков", "search", ""},
}

type resource struct {
	name, kind, team, item string
	elevated               bool
}

var resources = map[string]resource{
	"confluence":               {"Confluence", "tool", "", "CL-01", false},
	"space-delivery":           {"Пространство кластера «Доставка»", "space", "delivery", "CL-05", false},
	"space-delivery-incidents": {"Закрытое пространство инцидентов «Доставки»", "space", "delivery", "CL-05", true},
	"space-search":             {"Пространство кластера «Поиск»", "space", "search", "CL-05", false},
	"db-delivery-orders":       {"БД delivery_orders (PostgreSQL)", "database", "delivery", "", false},
}

type absenceRec struct {
	owner string
	res   mcpclient.AbsenceRequest
}

type accessRec struct {
	owner string
	res   mcpclient.AccessRequest
}

type approvalRec struct {
	approver string
	key      string
	res      mcpclient.Approval
}

// Server — запущенный набор поддельных MCP-серверов; URL используйте как MCP_BASE_URL.
type Server struct {
	URL string // базовый URL, например http://127.0.0.1:1234/mcp

	mu        sync.Mutex
	requests  int
	calls     map[string]int
	lastArgs  map[string]json.RawMessage
	fail      map[string][]*apierr.Error
	absence   map[string]*absenceRec // по ключу идемпотентности
	access    map[string]*accessRec  // по ключу идемпотентности
	approvals []*approvalRec
	faqs      map[string]mcpclient.FAQ
	seqABS    int
	seqACC    int
}

// New запускает подделки на httptest-сервере, который закрывается при очистке теста.
func New(t testing.TB, jwtSecret, confirmationSecret []byte) *Server {
	t.Helper()
	s := &Server{
		calls: map[string]int{}, lastArgs: map[string]json.RawMessage{}, fail: map[string][]*apierr.Error{},
		absence: map[string]*absenceRec{}, access: map[string]*accessRec{}, faqs: map[string]mcpclient.FAQ{},
		seqABS: 100, seqACC: 40,
	}
	cfg := mcpx.Config{JWTSecret: jwtSecret, ConfirmationSecret: confirmationSecret}
	mux := http.NewServeMux()
	var servers []*mcpx.Server
	for name, tools := range map[string]map[string]mcpx.HandlerFunc{
		"absence": {
			"get_policy": s.getPolicy, "list_my_requests": s.listMyRequests, "validate_request": s.validateRequest,
			"create_request": s.createRequest, "get_request": s.getRequest, "reschedule_request": s.rescheduleRequest, "cancel_request": s.cancelRequest,
		},
		"access": {
			"list_required": s.listRequired, "list_my_access": s.listMyAccess, "list_team_channels": s.listTeamChannels,
			"request_access": s.requestAccess,
		},
		"approvals": {"list_pending": s.listPending, "decide": s.decide},
		"kb":        {"search": s.search, "get": s.getDoc, "add_faq": s.addFAQ},
	} {
		srv, err := mcpx.NewServer(name, cfg)
		if err != nil {
			t.Fatalf("mcptest: %v", err)
		}
		for tool, h := range tools {
			srv.Handle(tool, s.wrap(name+"."+tool, h))
		}
		servers = append(servers, srv)
	}
	if err := mcpx.Mount(mux, servers...); err != nil {
		t.Fatalf("mcptest: mount: %v", err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests++
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	s.URL = ts.URL + "/mcp"
	return s
}

// wrap считает вызовы обработчика, запоминает аргументы и возвращает поставленные в очередь сбои.
func (s *Server) wrap(tool string, h mcpx.HandlerFunc) mcpx.HandlerFunc {
	return func(ctx context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
		s.mu.Lock()
		s.calls[tool]++
		s.lastArgs[tool] = append(json.RawMessage(nil), args...)
		var queued *apierr.Error
		if q := s.fail[tool]; len(q) > 0 {
			queued, s.fail[tool] = q[0], q[1:]
		}
		s.mu.Unlock()
		if queued != nil {
			return nil, queued
		}
		return h(ctx, c, args)
	}
}

// Requests возвращает число HTTP-запросов, полученных всеми серверами.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Calls возвращает, сколько раз выполнился обработчик инструмента ("<server>.<tool>") (после того как mcpx принял
// вызов).
func (s *Server) Calls(tool string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[tool]
}

// LastArgs возвращает аргументы последнего принятого вызова инструмента (с idempotency_key и confirmation_token).
func (s *Server) LastArgs(tool string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastArgs[tool]
}

// FailNext заставляет следующие len(errs) принятых вызовов инструмента вернуть эти ошибки по порядку.
func (s *Server) FailNext(tool string, errs ...*apierr.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[tool] = append(s.fail[tool], errs...)
}

// AbsenceStatus возвращает статус заявки на отсутствие по id ("", если неизвестна).
func (s *Server) AbsenceStatus(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.absence {
		if r.res.ID == id {
			return r.res.Status
		}
	}
	return ""
}

// FAQs возвращает число сохранённых документов FAQ.
func (s *Server) FAQs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.faqs)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func approverOf(sub string) (string, string) {
	lead := people[sub].lead
	return lead, people[lead].name
}

func ruDate(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format("02.01.2006")
}

// addApproval нужно вызывать, удерживая s.mu.
func (s *Server) addApproval(kind, ref, requester, approver, summary string) {
	s.approvals = append(s.approvals, &approvalRec{approver: approver, res: mcpclient.Approval{
		ID: fmt.Sprintf("APR-%08x", len(s.approvals)+1), Kind: kind, RefID: ref, RequesterID: requester,
		RequesterDisplayName: people[requester].name, Summary: summary, Status: "pending", Stage: "lead", CreatedAt: now(),
	}})
}

// HRAdmin — HR-администратор, который решает второй этап заявки на отсутствие.
const HRAdmin = "hradmin.olga"

// cancelRequest отзывает заявку вызывающего, пока HR-администратор её не согласовал.
func (s *Server) cancelRequest(_ context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.absence {
		if r.res.ID != in.ID || r.owner != c.Sub {
			continue
		}
		if r.res.Status != "pending_approval" && r.res.Status != "pending_hr" {
			return nil, apierr.Conflict().WithMessage("Отменить можно только заявку, которую HR-администратор ещё не согласовал")
		}
		r.res.Status = "cancelled"
		for _, a := range s.approvals {
			if a.res.RefID == in.ID && a.res.Status == "pending" {
				a.res.Status = "cancelled"
			}
		}
		return r.res, nil
	}
	return nil, apierr.Forbidden()
}

// rescheduleRequest переносит заявку вызывающего, пока HR-администратор её не согласовал; согласование начинается
// заново.
func (s *Server) rescheduleRequest(_ context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ID        string `json:"id"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Key       string `json:"idempotency_key"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.absence {
		if r.res.ID != in.ID || r.owner != c.Sub {
			continue
		}
		if r.res.Status != "pending_approval" && r.res.Status != "pending_hr" {
			return nil, apierr.Conflict().WithMessage("Перенести можно только заявку, которую HR-администратор ещё не согласовал")
		}
		r.res.StartDate, r.res.EndDate, r.res.Status = in.StartDate, in.EndDate, "pending_approval"
		for _, a := range s.approvals {
			if a.res.RefID == in.ID && a.res.Status == "pending" {
				a.res.Status = "cancelled"
			}
		}
		s.addApproval("absence_request", r.res.ID, c.Sub, r.res.ApproverID, "Отгул "+ruDate(in.StartDate)+" (перенос)")
		return r.res, nil
	}
	return nil, apierr.Forbidden()
}

// ---- absence (отсутствия) ----

func (s *Server) getPolicy(_ context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return map[string]any{"doc_id": "absence-policy-" + c.TeamID, "title": "Тестовая инструкция: отгул",
		"text": "Отгул согласует руководитель команды.", "source_type": "test"}, nil
}

func (s *Server) listMyRequests(_ context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []mcpclient.AbsenceRequest{}
	for _, r := range s.absence {
		if r.owner == c.Sub {
			items = append(items, r.res)
		}
	}
	return map[string]any{"items": items}, nil
}

func (s *Server) validateRequest(_ context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	id, name := approverOf(c.Sub)
	return map[string]any{"valid": true, "problems": []string{}, "approver_id": id, "approver_display_name": name}, nil
}

func (s *Server) createRequest(_ context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Type      string `json:"type"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Comment   string `json:"comment"`
		Key       string `json:"idempotency_key"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	if in.EndDate < in.StartDate {
		return nil, apierr.Validation().WithMessage("Дата окончания раньше даты начала")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.absence[in.Key]; r != nil {
		out := r.res
		out.Replayed = true
		return out, nil
	}
	s.seqABS++
	approver, name := approverOf(c.Sub)
	res := mcpclient.AbsenceRequest{ID: fmt.Sprintf("ABS-%06d", s.seqABS), Type: in.Type, StartDate: in.StartDate,
		EndDate: in.EndDate, Comment: in.Comment, Status: "pending_approval", ApproverID: approver,
		ApproverDisplayName: name, CreatedAt: now()}
	s.absence[in.Key] = &absenceRec{owner: c.Sub, res: res}
	s.addApproval("absence_request", res.ID, c.Sub, approver, "Отгул "+ruDate(in.StartDate))
	return res, nil
}

func (s *Server) getRequest(_ context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.absence {
		if r.res.ID == in.ID && r.owner == c.Sub {
			return r.res, nil
		}
	}
	return nil, apierr.Forbidden()
}

// ---- access (доступы) ----

type accessItem struct {
	ResourceID      string `json:"resource_id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Elevated        bool   `json:"elevated"`
	Status          string `json:"status"`
	ChecklistItemID string `json:"checklist_item_id,omitempty"`
}

func (s *Server) accessItems(c mcpx.Caller, onlyMine bool) []accessItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []accessItem{}
	for id, r := range resources {
		if r.team != "" && r.team != c.TeamID {
			continue
		}
		status := "not_requested"
		for _, a := range s.access {
			if a.owner == c.Sub && a.res.ResourceID == id {
				status = a.res.Status
			}
		}
		if onlyMine && status == "not_requested" {
			continue
		}
		items = append(items, accessItem{id, r.name, r.kind, r.elevated, status, r.item})
	}
	return items
}

func (s *Server) listRequired(_ context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return map[string]any{"items": s.accessItems(c, false)}, nil
}

func (s *Server) listMyAccess(_ context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return map[string]any{"items": s.accessItems(c, true)}, nil
}

func (s *Server) listTeamChannels(_ context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	return map[string]any{"items": []map[string]any{{"name": "#" + c.TeamID + "-team", "purpose": "Рабочий чат команды", "closed": false}}}, nil
}

func (s *Server) requestAccess(_ context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ResourceID  string `json:"resource_id"`
		Environment string `json:"environment"`
		Level       string `json:"level"`
		Key         string `json:"idempotency_key"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	if in.Level == "" {
		in.Level = "read"
	}
	r, ok := resources[in.ResourceID]
	if !ok || (r.team != "" && r.team != c.TeamID) {
		return nil, apierr.Forbidden()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec := s.access[in.Key]; rec != nil {
		out := rec.res
		out.Replayed = true
		return out, nil
	}
	s.seqACC++
	item := r.item
	res := mcpclient.AccessRequest{ID: fmt.Sprintf("ACC-%06d", s.seqACC), ResourceID: in.ResourceID, ResourceName: r.name,
		Status: "granted", ChecklistItemID: &item, Level: in.Level, CreatedAt: now()}
	if in.Environment != "" {
		env := in.Environment
		res.Environment = &env
	}
	if r.elevated || in.Level != "read" || in.Environment == "prod" { // чекпоинт 11.11: запись, администрирование и prod согласуются
		approver, _ := approverOf(c.Sub)
		res.Status, res.ApproverID = "pending_approval", &approver
		s.addApproval("access_request", res.ID, c.Sub, approver, "Доступ: "+r.name)
	}
	s.access[in.Key] = &accessRec{owner: c.Sub, res: res}
	return res, nil
}

// ---- approvals (согласования) ----

func (s *Server) listPending(_ context.Context, c mcpx.Caller, _ json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []mcpclient.Approval{}
	for _, a := range s.approvals {
		if a.approver == c.Sub && a.res.Status == "pending" {
			items = append(items, a.res)
		}
	}
	return map[string]any{"items": items}, nil
}

func (s *Server) decide(_ context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		ApprovalID string `json:"approval_id"`
		Decision   string `json:"decision"`
		Comment    string `json:"comment"`
		Key        string `json:"idempotency_key"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.approvals {
		if a.res.ID != in.ApprovalID || a.approver != c.Sub {
			continue
		}
		if a.key == in.Key {
			out := a.res
			out.Replayed = true
			return out, nil
		}
		if a.res.Status != "pending" {
			return nil, apierr.Conflict().WithMessage("Запрос уже рассмотрен")
		}
		a.key, a.res.Status, a.res.Comment = in.Key, in.Decision, in.Comment
		s.applyDecision(a.res)
		return a.res, nil
	}
	return nil, apierr.Forbidden()
}

// applyDecision нужно вызывать, удерживая s.mu.
func (s *Server) applyDecision(a mcpclient.Approval) {
	for _, r := range s.absence {
		if a.Kind != "absence_request" || r.res.ID != a.RefID {
			continue
		}
		switch {
		case a.Stage == "lead" && a.Status == "approved":
			r.res.Status = "pending_hr"
			s.addApproval("absence_request", r.res.ID, a.RequesterID, HRAdmin, a.Summary)
			s.approvals[len(s.approvals)-1].res.Stage = "hr_admin"
		default:
			r.res.Status = a.Status
		}
	}
	for _, r := range s.access {
		if a.Kind == "access_request" && r.res.ID == a.RefID {
			r.res.Status = map[string]string{"approved": "granted", "rejected": "rejected"}[a.Status]
		}
	}
}

// ---- kb ----

func (s *Server) search(context.Context, mcpx.Caller, json.RawMessage) (any, error) {
	return map[string]any{"items": []any{}}, nil
}

func (s *Server) getDoc(context.Context, mcpx.Caller, json.RawMessage) (any, error) {
	return nil, apierr.NotFound()
}

func (s *Server) addFAQ(_ context.Context, c mcpx.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Title string `json:"title"`
		Key   string `json:"idempotency_key"`
	}
	if err := mcpx.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.faqs[in.Key]; ok {
		f.Replayed = true
		return f, nil
	}
	f := mcpclient.FAQ{DocID: fmt.Sprintf("faq-%s-%d", c.TeamID, len(s.faqs)+1), Title: in.Title, SourceType: "test"}
	s.faqs[in.Key] = f
	return f, nil
}
