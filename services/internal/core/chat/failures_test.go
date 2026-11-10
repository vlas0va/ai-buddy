package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/agentclient/agenttest"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/platform/authn"
)

type failingEmitter struct{}

func (failingEmitter) Emit(context.Context, analytics.DBTX, analytics.Event) error {
	return errors.New("boom")
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

// Database failures answer internal (500) before streaming and never leave a proposal confirmed.
func TestChat_DatabaseFailures_Internal(t *testing.T) {
	e := newEnv(t, func(d *Deps, _ *Config) { d.Emitter = failingEmitter{} }) // analytics failures are only logged
	anna := e.anna()
	sid := e.session(anna)
	executed, pending := e.propose(sid, anna), e.propose(sid, anna)
	e.exec(`UPDATE core.proposals SET status = 'executed' WHERE id = $1`, executed)
	if r := e.do(http.MethodPost, e.confirmPath(sid, executed), anna, nil); r.status != http.StatusInternalServerError {
		t.Fatalf("replay without an action: %d", r.status)
	}

	e.exec(`ALTER TABLE core.plan_tasks RENAME TO plan_tasks_broken`)
	if r := e.do(http.MethodPost, "/api/chat/sessions/"+sid+"/messages", anna, map[string]string{"text": "Привет"}); r.status != http.StatusInternalServerError {
		t.Fatalf("message without plan tasks: %d", r.status)
	}
	if r := e.do(http.MethodPost, e.confirmPath(sid, pending), anna, nil); r.status != http.StatusInternalServerError || e.proposalStatus(pending) != "pending" {
		t.Fatalf("confirm without plan tasks: %d %s", r.status, e.proposalStatus(pending))
	}

	e.exec(`ALTER TABLE core.chat_messages RENAME TO chat_messages_broken`)
	for _, r := range []response{
		e.do(http.MethodGet, "/api/chat/sessions/"+sid, anna, nil),
		e.do(http.MethodPost, "/api/chat/sessions/"+sid+"/messages", anna, map[string]string{"text": "Привет"}),
	} {
		if r.status != http.StatusInternalServerError {
			t.Fatalf("without messages: %d %s", r.status, r.body)
		}
	}

	e.exec(`ALTER TABLE core.proposals RENAME TO proposals_broken`)
	for _, path := range []string{e.confirmPath(sid, pending), e.cancelPath(sid, pending)} {
		if r := e.do(http.MethodPost, path, anna, nil); r.status != http.StatusInternalServerError {
			t.Fatalf("%s without proposals: %d", path, r.status)
		}
	}

	e.pool.Close()
	for _, r := range []response{
		e.do(http.MethodPost, "/api/chat/sessions", anna, nil),
		e.do(http.MethodGet, "/api/chat/sessions/"+sid, anna, nil),
	} {
		if r.status != http.StatusInternalServerError {
			t.Fatalf("pool closed: %d %s", r.status, r.body)
		}
	}
}

func TestChat_S2_MenteesBestEffort(t *testing.T) {
	e := newEnv(t, func(d *Deps, _ *Config) { d.Emitter = nil; d.Limiter = nil; d.Flags = nil })
	e.exec(`ALTER TABLE core.actions RENAME TO actions_broken`)
	oleg := e.oleg()
	if s := e.message(e.session(oleg), oleg, "Как дела у Анны?"); s.last()["message_id"] == nil {
		t.Fatal(s.names())
	}
	if uc := e.agent.Last(agentclient.KindMessage).UserContext; uc.Mentees != nil || uc.CurrentPeriod != "week_2" {
		t.Fatalf("%+v", uc)
	}
}

func TestChat_CallerWithoutToken_Unauthorized(t *testing.T) {
	e := newEnv(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat/sessions", nil)
	e.chat.createSession(rec, req.WithContext(authn.NewContext(req.Context(), authn.Claims{Sub: "intern.anna"})))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
}

func TestConfirm_S3_DecisionResultWithoutRef_Failed(t *testing.T) {
	e := newEnv(t)
	maria := e.maria()
	sid := e.session(maria)
	e.agent.OnMessage(func(agenttest.Request) []agenttest.Event {
		return []agenttest.Event{{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(), "kind": "approval_decision",
			"draft": map[string]any{"tool": "approvals.decide", "args": map[string]any{"approval_id": "APR-1", "decision": "approved"}}}}}
	})
	pid := e.message(sid, maria, "Согласуй").find("action_proposal")["proposal_id"].(string)
	for _, result := range []map[string]any{{"id": "APR-1"}, {"id": "APR-1", "kind": "absence_request", "ref_id": "ABS-1", "status": "pending"}} {
		e.agent.OnConfirm(func(agenttest.Request) []agenttest.Event {
			return []agenttest.Event{{Name: "tool_result", Data: map[string]any{"result": result}}}
		})
		s := e.stream(e.confirmPath(sid, pid), maria, nil)
		if s.last()["code"] != "upstream_unavailable" || s.last()["message"] != msgBadResult || e.proposalStatus(pid) != "failed" {
			t.Fatalf("%v: %s %v", result, s.names(), s.last())
		}
	}
}

func TestConfirm_S3_CancelledWhileInFlight_Conflict(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
		e.exec(`UPDATE core.proposals SET status = 'cancelled' WHERE id = $1`, pid) // the user cancelled in another tab
		return e.agent.Confirm(context.Background(), req)
	})
	s := e.stream(e.confirmPath(sid, pid), anna, nil)
	if s.last()["code"] != "internal" || e.count(`SELECT count(*) FROM core.actions`) != 0 || e.proposalStatus(pid) != "cancelled" {
		t.Fatalf("%s %v %s", s.names(), s.last(), e.proposalStatus(pid))
	}
}
