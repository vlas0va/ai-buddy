package actions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/pgtest"
)

var byMaria = Scope{RequesterID: "intern.anna", ApproverID: "lead.maria"}
var byOlga = Scope{RequesterID: "intern.anna", ApproverID: "hradmin.olga", Stage: "hr_admin"}

var (
	anna  = authn.Claims{Sub: "intern.anna", Role: "intern", TeamID: "delivery"}
	ivan  = authn.Claims{Sub: "intern.ivan", Role: "intern", TeamID: "search"}
	oleg  = authn.Claims{Sub: "mentor.oleg", Role: "mentor", TeamID: "delivery"}
	maria = authn.Claims{Sub: "lead.maria", Role: "lead", TeamID: "delivery"}
)

func newSvc(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.New(t)
	return NewService(analytics.NewEmitter(false), notify.Store{}, nil), pool
}

func absenceResult(id, start, end, approverName string) json.RawMessage {
	m := map[string]any{"id": id, "type": "day_off", "start_date": start, "end_date": end, "status": "pending_approval",
		"approver_id": "lead.maria", "created_at": "2026-11-09T10:00:00Z"}
	if approverName != "" {
		m["approver_display_name"] = approverName
	}
	b, _ := json.Marshal(m)
	return b
}

func accessResult(id, resource, name, status, item string, approver *string) json.RawMessage {
	m := map[string]any{"id": id, "resource_id": resource, "status": status, "created_at": "2026-11-09T10:00:00Z",
		"checklist_item_id": item, "approver_id": approver}
	if name != "" {
		m["resource_name"] = name
	}
	if item == "" {
		m["checklist_item_id"] = nil
	}
	b, _ := json.Marshal(m)
	return b
}

type taskRow struct {
	source, period, title, status string
	linked                        *string
	due                           *string
	checklist                     *string
}

func loadTask(t *testing.T, pool *pgxpool.Pool, id string) taskRow {
	t.Helper()
	var r taskRow
	err := pool.QueryRow(context.Background(), `SELECT source, period, title, status, linked_action_id::text, due_at::text, checklist_item_id
		FROM core.plan_tasks WHERE id = $1`, id).Scan(&r.source, &r.period, &r.title, &r.status, &r.linked, &r.due, &r.checklist)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSave_S3_AbsenceCreatesActionTask_R66(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	pid := "aaaaaaaa-0000-4000-8000-000000000001"
	in := SaveInput{Claims: anna, ProposalID: pid, Kind: KindAbsence, Tool: "absence.create_request",
		Args:   json.RawMessage(`{"type":"day_off","start_date":"2026-11-13","end_date":"2026-11-13","comment":""}`),
		Result: absenceResult("ABS-000101", "2026-11-13", "2026-11-13", "Мария Лебедева"), MessageID: "bbbbbbbb-0000-4000-8000-000000000001",
		ProposedAt: time.Now().Add(-time.Second), Attempt: 1}
	a, err := svc.Save(ctx, pool, in)
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary != "Отгул 13.11.2026, согласующий — Мария Лебедева" || a.Status != StatusPendingApproval ||
		*a.ExternalID != "ABS-000101" || *a.ApproverDisplayName != "Мария Лебедева" || a.TaskID == nil ||
		a.ProposalID != pid || a.MessageID != in.MessageID || a.CreatedAt == "" || a.UserID != "intern.anna" {
		t.Fatalf("action %+v", a)
	}
	task := loadTask(t, pool, *a.TaskID)
	if task.source != "action" || task.period != "week_2" || task.title != "Отгул 13.11.2026" ||
		task.status != "waiting_approval" || *task.linked != a.ID || *task.due != "2026-11-13" || task.checklist != nil {
		t.Fatalf("task %+v", task)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.analytics_events WHERE name = 'action.created' AND props->>'action_id' = $1
		AND (props->>'attempt')::int = 1 AND props ? 'duration_ms_since_proposal'`, a.ID); n != 1 {
		t.Fatalf("action.created events = %d", n)
	}
	again, err := svc.Save(ctx, pool, in)
	if err != nil || again.ID != a.ID {
		t.Fatalf("idempotent save: %+v %v", again, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.plan_tasks WHERE source = 'action'`); n != 1 {
		t.Fatalf("action tasks = %d", n)
	}
	if _, err := svc.Save(ctx, pool, SaveInput{Claims: ivan, ProposalID: pid, Kind: KindAbsence, Result: in.Result}); !apierr.HasCode(err, apierr.CodeConflict) {
		t.Fatalf("foreign proposal id: %v", err)
	}
}

func TestSave_S3_ApproverLookupAndRange(t *testing.T) {
	svc, pool := newSvc(t)
	a, err := svc.Save(context.Background(), pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000002",
		Kind: KindAbsence, Result: absenceResult("ABS-000102", "2026-11-12", "2026-11-13", "")})
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary != "Отгул 12.11.2026–13.11.2026, согласующий — Мария Лебедева" {
		t.Fatalf("summary %q", a.Summary)
	}
}

func TestSave_S3_NonInterns(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	lead, err := svc.Save(ctx, pool, SaveInput{Claims: maria, ProposalID: "aaaaaaaa-0000-4000-8000-000000000003",
		Kind: KindAbsence, Result: absenceResult("ABS-000103", "2026-11-13", "2026-11-13", "")})
	if err != nil || lead.TaskID != nil {
		t.Fatalf("lead without plan: %+v %v", lead, err)
	}
	if lead.Summary != "Отгул 13.11.2026, согласующий — Мария Лебедева" {
		t.Fatalf("summary %q", lead.Summary)
	}
	mentor, err := svc.Save(ctx, pool, SaveInput{Claims: oleg, ProposalID: "aaaaaaaa-0000-4000-8000-000000000004",
		Kind: KindAbsence, Result: absenceResult("ABS-000104", "2026-11-13", "2026-11-13", "Мария Лебедева")})
	if err != nil || mentor.TaskID == nil {
		t.Fatalf("mentor: %+v %v", mentor, err)
	}
	if task := loadTask(t, pool, *mentor.TaskID); task.period != "week_2" || task.source != "action" {
		t.Fatalf("mentor task %+v", task)
	}
}

func TestSave_S4_AccessLinksChecklistTask_R66(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	lead := "lead.maria"
	pending, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000005", Kind: KindAccess,
		Result: accessResult("ACC-000041", "space-delivery-incidents", "Закрытое пространство инцидентов «Доставки»", "pending_approval", "CL-05", &lead)})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Summary != "Доступ: Закрытое пространство инцидентов «Доставки», согласующий — Мария Лебедева" || pending.TaskID == nil {
		t.Fatalf("pending %+v", pending)
	}
	task := loadTask(t, pool, *pending.TaskID)
	if task.source != "checklist" || *task.checklist != "CL-05" || task.status != "waiting_approval" || *task.linked != pending.ID {
		t.Fatalf("task %+v", task)
	}

	got, changed, err := svc.ApplyDecision(ctx, pool, KindAccess, "ACC-000041", "approved", byMaria)
	if err != nil || !changed || got.Status != StatusGranted {
		t.Fatalf("decision: %+v %v %v", got, changed, err)
	}
	if task := loadTask(t, pool, *pending.TaskID); task.status != "done" {
		t.Fatalf("task after grant %+v", task)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND kind = 'status_change' AND title = 'Доступ выдан'`); n != 1 {
		t.Fatalf("notifications = %d", n)
	}
	if _, changed, err := svc.ApplyDecision(ctx, pool, KindAccess, "ACC-000041", "approved", byMaria); err != nil || changed {
		t.Fatalf("repeat decision: %v %v", changed, err)
	}

	// A second request for the same checklist item does not reopen the done task.
	second, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000006", Kind: KindAccess,
		Result: accessResult("ACC-000042", "space-delivery", "", "pending_approval", "CL-05", nil)})
	if err != nil {
		t.Fatal(err)
	}
	if second.Summary != "Доступ: space-delivery — на согласовании" || *second.TaskID != *pending.TaskID {
		t.Fatalf("second %+v", second)
	}
	if task := loadTask(t, pool, *pending.TaskID); task.status != "done" || *task.linked != pending.ID {
		t.Fatalf("task after second request %+v", task)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAccess, "ACC-000042", "rejected", byMaria); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a request without this approver must not be touched: %v", err)
	}
	if task := loadTask(t, pool, *pending.TaskID); task.status != "done" {
		t.Fatalf("rejecting an unlinked action changed the task: %+v", task)
	}
}

func TestSave_S4_AccessWithoutInternTask(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	granted, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000007", Kind: KindAccess,
		Result: accessResult("ACC-000043", "confluence", "Confluence", "granted", "CL-01", nil)})
	if err != nil {
		t.Fatal(err)
	}
	task := loadTask(t, pool, *granted.TaskID)
	if granted.Summary != "Доступ: Confluence — выдан" || task.source != "action" || task.title != "Доступ: Confluence" || task.status != "done" || task.due != nil {
		t.Fatalf("granted %+v task %+v", granted, task)
	}
	rejected, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000008", Kind: KindAccess,
		Result: accessResult("ACC-000044", "jira", "Jira", "rejected", "", nil)})
	if err != nil || rejected.Summary != "Доступ: Jira — отклонён" {
		t.Fatalf("rejected %+v %v", rejected, err)
	}
	odd, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000009", Kind: KindAccess,
		Result: accessResult("ACC-000045", "bitbucket", "Bitbucket", "weird", "", nil)})
	if err != nil || odd.Status != StatusCreated || loadTask(t, pool, *odd.TaskID).status != "in_progress" {
		t.Fatalf("unknown status %+v %v", odd, err)
	}
}

func TestApplyDecision_S3_AbsenceRejectedAndApproved(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	a, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000010", Kind: KindAbsence,
		Result: absenceResult("ABS-000110", "2026-11-13", "2026-11-13", "Мария Лебедева")})
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000110", "rejected", byMaria)
	if err != nil || !changed || got.Status != StatusRejected {
		t.Fatalf("%+v %v %v", got, changed, err)
	}
	if task := loadTask(t, pool, *a.TaskID); task.status != "not_started" {
		t.Fatalf("task %+v", task)
	}
	lead, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000110", "approved", byMaria)
	if err != nil || lead.Status != StatusPendingHR {
		t.Fatalf("%+v %v", lead, err)
	}
	if task := loadTask(t, pool, *a.TaskID); task.status != "waiting_approval" {
		t.Fatalf("task %+v", task)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND title = 'Отгул: руководитель согласовал, ждёт HR-администратора'`); n != 1 {
		t.Fatalf("lead stage notifications = %d", n)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000110", "approved", byOlga); err != nil {
		t.Fatal(err)
	}
	if task := loadTask(t, pool, *a.TaskID); task.status != "done" {
		t.Fatalf("task %+v", task)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND title IN ('Отгул отклонён', 'Отгул согласован')`); n != 2 {
		t.Fatalf("notifications = %d", n)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-999999", "approved", byMaria); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	for _, tc := range [][2]string{{KindAbsence, "granted"}, {"other", "approved"}, {KindAccess, "maybe"}} {
		if _, _, err := svc.ApplyDecision(ctx, pool, tc[0], "X", tc[1], byMaria); !apierr.HasCode(err, apierr.CodeValidation) {
			t.Fatalf("%v: %v", tc, err)
		}
	}
}

func TestSave_InvalidInput(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	for _, in := range []SaveInput{
		{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000011", Kind: "salary_change", Result: json.RawMessage(`{}`)},
		{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000011", Kind: KindAbsence, Result: json.RawMessage(`{}`)},
		{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000011", Kind: KindAccess, Result: json.RawMessage(`[]`)},
	} {
		if _, err := svc.Save(ctx, pool, in); err == nil {
			t.Fatalf("%s: want error", in.Kind)
		}
	}
	if _, err := svc.ByProposal(ctx, pool, "intern.anna", "aaaaaaaa-0000-4000-8000-000000000011"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("by proposal: %v", err)
	}
}

type failingEmitter struct{}

func (failingEmitter) Emit(context.Context, analytics.DBTX, analytics.Event) error {
	return errors.New("boom")
}

type failingNotifier struct{}

func (failingNotifier) Create(context.Context, notify.DBTX, notify.Notification) error {
	return errors.New("boom")
}

func TestService_DependencyFailures(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	bad := NewService(failingEmitter{}, failingNotifier{}, nil)
	if _, err := bad.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000012", Kind: KindAbsence,
		Result: absenceResult("ABS-000112", "2026-11-13", "2026-11-13", "")}); err == nil {
		t.Fatal("emitter failure must fail Save")
	}
	quiet := NewService(nil, nil, nil)
	if _, err := quiet.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000013", Kind: KindAbsence,
		Result: absenceResult("ABS-000113", "2026-11-13", "2026-11-13", "")}); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := quiet.ApplyDecision(ctx, pool, KindAbsence, "ABS-000113", "approved", byMaria); err != nil || !changed {
		t.Fatalf("no notifier: %v %v", changed, err)
	}
	if _, err := bad.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000014", Kind: KindAbsence,
		Result: absenceResult("ABS-000114", "2026-11-13", "2026-11-13", "")}); err == nil {
		t.Fatal("want error")
	}
	if _, err := quiet.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000015", Kind: KindAbsence,
		Result: absenceResult("ABS-000115", "2026-11-13", "2026-11-13", "")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bad.ApplyDecision(ctx, pool, KindAbsence, "ABS-000115", "approved", byMaria); err == nil {
		t.Fatal("notifier failure must fail ApplyDecision")
	}
}

func TestList_S3_OwnNewestFirst_R66(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	for i, c := range []authn.Claims{anna, ivan, anna} {
		pid := []string{"aaaaaaaa-0000-4000-8000-000000000021", "aaaaaaaa-0000-4000-8000-000000000022", "aaaaaaaa-0000-4000-8000-000000000023"}[i]
		ext := []string{"ABS-000121", "ABS-000122", "ABS-000123"}[i]
		if _, err := svc.Save(ctx, pool, SaveInput{Claims: c, ProposalID: pid, Kind: KindAbsence,
			Result: absenceResult(ext, "2026-11-13", "2026-11-13", "")}); err != nil {
			t.Fatal(err)
		}
	}
	h := New(pool, svc, nil)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/actions", nil)
	mux.ServeHTTP(rec, req.WithContext(authn.NewContext(req.Context(), anna)))
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(items) != 2 || items[0]["external_id"] != "ABS-000123" || items[1]["external_id"] != "ABS-000121" {
		t.Fatalf("items %v", items)
	}
	for _, k := range []string{"id", "kind", "external_id", "status", "summary", "approver_display_name", "task_id", "created_at", "updated_at"} {
		if _, ok := items[0][k]; !ok {
			t.Fatalf("missing %s in %v", k, items[0])
		}
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/actions", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no claims: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/actions", nil)
	mux.ServeHTTP(rec, req.WithContext(authn.NewContext(req.Context(), maria)))
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" && rec.Body.String() != "[]" {
		t.Fatalf("empty list: %d %q", rec.Code, rec.Body)
	}
}

func TestSave_S4_HintsFromToolResult(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	a, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000031", Kind: KindAccess,
		Result:          accessResult("ACC-000051", "space-delivery", "Пространство кластера «Доставка»", "pending_approval", "", nil),
		ChecklistItemID: "CL-05", ApproverName: "Мария Лебедева"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary != "Доступ: Пространство кластера «Доставка», согласующий — Мария Лебедева" || *loadTask(t, pool, *a.TaskID).checklist != "CL-05" {
		t.Fatalf("%+v", a)
	}
	b, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000032", Kind: KindAbsence,
		Result: absenceResult("ABS-000151", "2026-11-13", "2026-11-13", ""), ApproverName: "М. Лебедева"})
	if err != nil || *b.ApproverDisplayName != "М. Лебедева" {
		t.Fatalf("%+v %v", b, err)
	}
	got, err := svc.ByID(ctx, pool, b.ID)
	if err != nil || got.ID != b.ID {
		t.Fatalf("by id: %v", err)
	}
	if _, err := svc.ByID(ctx, pool, "aaaaaaaa-0000-4000-8000-000000000099"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestHelpers(t *testing.T) {
	if RuDate("bad") != "bad" || RuDate("2026-11-13") != "13.11.2026" {
		t.Fatal("ruDate")
	}
	if absenceStatus("cancelled") != StatusCreated || taskStatus(StatusFailed) != "not_started" {
		t.Fatal("status mapping")
	}
	r := Action{ID: "1", Kind: KindAbsence, Status: StatusApproved}.Result()
	if r.ActionID != "1" || r.Status != StatusApproved {
		t.Fatalf("%+v", r)
	}
}

func TestApplyDecision_S3_ScopedToRequesterAndApprover_R71(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	a, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000041", Kind: KindAbsence,
		Result: absenceResult("ABS-000141", "2026-11-13", "2026-11-13", "")})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{
		{RequesterID: "intern.ivan", ApproverID: "lead.maria"},  // another intern
		{RequesterID: "intern.anna", ApproverID: "lead.dmitry"}, // a lead who is not the approver
		{RequesterID: "intern.anna"},                            // no approver
		{},                                                      // no scope at all
	} {
		if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000141", "approved", scope); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%+v: %v", scope, err)
		}
	}
	if got, _ := svc.ByID(ctx, pool, a.ID); got.Status != StatusPendingApproval {
		t.Fatalf("out-of-scope decision applied: %+v", got)
	}
	if _, changed, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000141", "approved", byMaria); err != nil || !changed {
		t.Fatalf("in scope: %v %v", changed, err)
	}
}

func TestSave_S4_NeverLinksHumanOnlyTask_R05(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE core.plan_tasks t SET human_only = true FROM core.plans p
		WHERE p.id = t.plan_id AND p.user_id = 'intern.anna' AND t.checklist_item_id = 'CL-05'`); err != nil {
		t.Fatal(err)
	}
	lead := "lead.maria"
	a, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000042", Kind: KindAccess,
		Result: accessResult("ACC-000061", "space-delivery-incidents", "Инциденты", "pending_approval", "CL-05", &lead)})
	if err != nil {
		t.Fatal(err)
	}
	task := loadTask(t, pool, *a.TaskID)
	if task.source != "action" || task.checklist != nil {
		t.Fatalf("human-only checklist task linked: %+v", task)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAccess, "ACC-000061", "approved", byMaria); err != nil {
		t.Fatal(err)
	}
	var open int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.plan_tasks t JOIN core.plans p ON p.id = t.plan_id
		WHERE p.user_id = 'intern.anna' AND t.checklist_item_id = 'CL-05' AND t.parent_task_id IS NULL AND t.status = 'done'`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("human-only task completed by an action: %d %v", open, err)
	}
}

func TestSave_S3_AbsenceIgnoresChecklistHint_R66(t *testing.T) {
	svc, pool := newSvc(t)
	a, err := svc.Save(context.Background(), pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000043", Kind: KindAbsence,
		Result: absenceResult("ABS-000143", "2026-11-13", "2026-11-13", ""), ChecklistItemID: "CL-05"})
	if err != nil {
		t.Fatal(err)
	}
	if task := loadTask(t, pool, *a.TaskID); task.source != "action" || task.checklist != nil {
		t.Fatalf("absence linked to a checklist task: %+v", task)
	}
}

func tripResult(id, city, start, end, approverName string) json.RawMessage {
	m := map[string]any{"id": id, "city": city, "start_date": start, "end_date": end, "purpose": "Встреча с командой",
		"status": "pending_approval", "approver_id": "lead.maria"}
	if approverName != "" {
		m["approver_display_name"] = approverName
	}
	b, _ := json.Marshal(m)
	return b
}

func TestSave_R23_TripCreatesActionAndTask_R66(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	a, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000201", Kind: KindTrip,
		Result: tripResult("TRP-000200", "Казань", "2026-11-23", "2026-11-25", "")})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != StatusPendingApproval || a.Summary != "Командировка: Казань, 23.11.2026–25.11.2026, согласующий — Мария Лебедева" || a.TaskID == nil {
		t.Fatalf("%+v", a)
	}
	if task := loadTask(t, pool, *a.TaskID); task.title != "Командировка: Казань" || task.status != "waiting_approval" {
		t.Fatalf("task %+v", task)
	}
	got, changed, err := svc.ApplyDecision(ctx, pool, KindTrip, "TRP-000200", "approved", byMaria)
	if err != nil || !changed || got.Status != StatusApproved {
		t.Fatalf("%+v %v %v", got, changed, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND title = 'Командировка согласована'`); n != 1 {
		t.Fatalf("notifications = %d", n)
	}
	b, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000202", Kind: KindTrip,
		Result: tripResult("TRP-000201", "Казань", "2026-12-01", "2026-12-01", "М. Лебедева")})
	if err != nil || !strings.Contains(b.Summary, "01.12.2026, согласующий — М. Лебедева") {
		t.Fatalf("%+v %v", b, err)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindTrip, "TRP-000201", "rejected", byMaria); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND title = 'Командировка отклонена'`); n != 1 {
		t.Fatalf("rejected notifications = %d", n)
	}
	if _, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000203", Kind: KindTrip,
		Result: json.RawMessage(`{"city":"Казань"}`)}); err == nil {
		t.Fatal("trip without id accepted")
	}
}

func TestSave_R23_ConsentSignedWithoutTask_R66(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	a, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000211", Kind: KindConsent,
		Result: json.RawMessage(`{"id":"CNS-000300","consent_id":"photo","title":"Фото и видео во внутренних материалах","status":"signed","signed_at":"2026-11-11T10:00:00Z"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != StatusSigned || a.TaskID != nil || a.Summary != "Согласие: Фото и видео во внутренних материалах — подписано" {
		t.Fatalf("%+v", a)
	}
	if _, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000212", Kind: KindConsent,
		Result: json.RawMessage(`{"consent_id":"photo"}`)}); err == nil {
		t.Fatal("consent without id accepted")
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindConsent, "CNS-000300", "approved", byMaria); !apierr.HasCode(err, apierr.CodeValidation) {
		t.Fatalf("consent decision: %v", err)
	}
}

func TestApplyDecision_S3_ApprovedDayOffNotifiesHRBP(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000221", Kind: KindAbsence,
		Result: absenceResult("ABS-000221", "2026-11-13", "2026-11-13", "Мария Лебедева")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000221", "approved", byMaria); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.notifications n JOIN core.users u ON u.id = n.user_id WHERE u.role = 'hrbp'`); n != 0 {
		t.Fatalf("HR BP notified before the HR admin: %d", n)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000221", "approved", byOlga); err != nil {
		t.Fatal(err)
	}
	var title, body string
	if err := pool.QueryRow(ctx, `SELECT title, body FROM core.notifications n JOIN core.users u ON u.id = n.user_id
		WHERE u.role = 'hrbp' ORDER BY n.created_at DESC LIMIT 1`).Scan(&title, &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(title, "Отгул оформлен: ") || strings.Contains(body, "согласующий") || !strings.Contains(body, "HR-администратор") {
		t.Fatalf("%q %q", title, body)
	}
	if withoutApprover("abc") != "abc" {
		t.Fatal("withoutApprover")
	}
}

// Checkpoint 11.11: until the HR admin approves, the intern moves the request; the same action and plan task follow
// the new dates and the request goes back to the lead.
func TestSave_Checkpoint_RescheduleMovesActionAndTask(t *testing.T) {
	svc, pool := newSvc(t)
	ctx := context.Background()
	a, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000301", Kind: KindAbsence,
		Result: absenceResult("ABS-000301", "2026-11-20", "2026-11-20", "Мария Лебедева")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000301", "approved", byMaria); err != nil {
		t.Fatal(err)
	}
	moved, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000302", Kind: KindReschedule,
		Result: absenceResult("ABS-000301", "2026-11-27", "2026-11-27", "Мария Лебедева")})
	if err != nil || moved.ID != a.ID || moved.Status != StatusPendingApproval || !strings.Contains(moved.Summary, "27.11.2026") {
		t.Fatalf("%+v %v", moved, err)
	}
	if task := loadTask(t, pool, *a.TaskID); task.status != "waiting_approval" || task.due == nil || !strings.HasPrefix(*task.due, "2026-11-27") {
		t.Fatalf("task %+v", task)
	}
	if n := count(t, pool, `SELECT count(*) FROM core.actions WHERE external_id = 'ABS-000301'`); n != 1 {
		t.Fatalf("actions = %d", n)
	}
	if _, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000303", Kind: KindReschedule,
		Result: absenceResult("ABS-999998", "2026-11-27", "2026-11-27", "")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown request: %v", err)
	}
	if _, err := svc.Save(ctx, pool, SaveInput{Claims: anna, ProposalID: "aaaaaaaa-0000-4000-8000-000000000304", Kind: KindReschedule,
		Result: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("result without id")
	}
	// A replayed lead decision after the HR admin approved never undoes the approval.
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000301", "approved", byMaria); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000301", "approved", byOlga); err != nil {
		t.Fatal(err)
	}
	if got, changed, err := svc.ApplyDecision(ctx, pool, KindAbsence, "ABS-000301", "approved", byMaria); err != nil || changed || got.Status != StatusApproved {
		t.Fatalf("replay %+v %v %v", got, changed, err)
	}
}
