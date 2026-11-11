package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/agentclient/agenttest"
)

func decisionProposal(approvalID, decision string) agenttest.Event {
	return agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(), "kind": "approval_decision",
		"summary": "Решение по заявке", "draft": map[string]any{"tool": "approvals.decide",
			"args": map[string]any{"approval_id": approvalID, "decision": decision, "comment": ""}}}}
}

// proposeWith makes the agent answer the next message with ev and returns the stored proposal id.
func (e *env) proposeWith(sid, token string, ev agenttest.Event) string {
	e.t.Helper()
	e.agent.OnMessage(func(agenttest.Request) []agenttest.Event { return []agenttest.Event{ev, agenttest.Done()} })
	defer e.agent.OnMessage(nil)
	s := e.message(sid, token, "предложение")
	p := s.find("action_proposal")
	if p == nil {
		e.t.Fatalf("proposal not stored: %s %v", s.names(), s.last())
	}
	return p["proposal_id"].(string)
}

func (e *env) actionStatus(id any) string {
	return e.str(`SELECT status FROM core.actions WHERE id = $1`, id)
}

// Finding 1: a lead's decision in chat is checked against the confirmed draft and read back from the approvals
// service; the agent's claims about which request it decided are never applied.
func TestConfirm_S3_LeadDecisionReadBackFromMCP_R71(t *testing.T) {
	e := newEnv(t)
	anna, maria := e.anna(), e.maria()
	annaSID := e.session(anna)
	first := e.stream(e.confirmPath(annaSID, e.propose(annaSID, anna)), anna, nil).find("action_result")
	second := e.stream(e.confirmPath(annaSID, e.propose(annaSID, anna)), anna, nil).find("action_result")
	var pending []map[string]any
	e.do(http.MethodGet, "/api/approvals", maria, nil).json(t, &pending)
	x1, x2 := pending[0]["id"].(string), pending[1]["id"].(string)
	sid := e.session(maria)

	t.Run("agent result differs from the confirmed decision", func(t *testing.T) {
		for _, result := range []map[string]any{
			{"id": x2, "status": "rejected"},           // decision reversed
			{"id": x1, "status": "approved"},           // another approval
			{"id": x2, "status": "approved", "ID": x1}, // case variant
		} {
			pid := e.proposeWith(sid, maria, decisionProposal(x2, "approved"))
			e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
				return []agenttest.Event{{Name: "tool_result", Data: map[string]any{"proposal_id": req.Turn.ProposalID,
					"kind": "approval_decision", "tool": "approvals.decide", "result": result}}}
			})
			s := e.stream(e.confirmPath(sid, pid), maria, nil)
			if s.last()["message"] != msgBadResult || e.proposalStatus(pid) != "failed" || e.mcp.Calls("approvals.decide") != 0 {
				t.Fatalf("%v: %s %v %s", result, s.names(), s.last(), e.proposalStatus(pid))
			}
		}
		if e.actionStatus(second["action_id"]) != "pending_approval" || e.actionStatus(first["action_id"]) != "pending_approval" {
			t.Fatal("a rejected tool_result changed an action")
		}
	})

	t.Run("agent names another request", func(t *testing.T) {
		pid := e.proposeWith(sid, maria, decisionProposal(x1, "approved"))
		// The agent never calls MCP and claims the decision was about the second request.
		e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
			return []agenttest.Event{{Name: "tool_result", Data: map[string]any{"proposal_id": req.Turn.ProposalID, "kind": "approval_decision",
				"tool": "approvals.decide", "result": map[string]any{"id": x1, "status": "approved", "kind": "absence_request",
					"ref_id": second["external_id"], "requester_id": "intern.anna"}}}, agenttest.Done()}
		})
		s := e.stream(e.confirmPath(sid, pid), maria, nil)
		if got := s.find("action_result"); got == nil || got["action_id"] != first["action_id"] {
			t.Fatalf("%s %v", s.names(), got)
		}
		if e.actionStatus(first["action_id"]) != "pending_hr" || e.actionStatus(second["action_id"]) != "pending_approval" {
			t.Fatal("decision applied to the request the agent named")
		}
		if e.mcp.Calls("approvals.decide") != 1 {
			t.Fatalf("core must read the decision back itself: %d", e.mcp.Calls("approvals.decide"))
		}
	})

	t.Run("lead who is not the approver", func(t *testing.T) {
		dmitry := agenttest.Token(t, jwtSecret, "lead.dmitry", "lead", "search", "")
		dsid := e.session(dmitry)
		pid := e.proposeWith(dsid, dmitry, decisionProposal(x2, "approved"))
		e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
			return []agenttest.Event{{Name: "tool_result", Data: map[string]any{"proposal_id": req.Turn.ProposalID, "kind": "approval_decision",
				"tool": "approvals.decide", "result": map[string]any{"id": x2, "status": "approved"}}}}
		})
		s := e.stream(e.confirmPath(dsid, pid), dmitry, nil)
		if s.last()["code"] != "internal" || e.proposalStatus(pid) != "failed" || e.actionStatus(second["action_id"]) != "pending_approval" {
			t.Fatalf("%s %v %s", s.names(), s.last(), e.proposalStatus(pid))
		}
	})
}

// Finding 2: the tool_result must describe the confirmed proposal; the retry with an honest result saves it.
func TestConfirm_S3_ToolResultMustMatchProposal_R71(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	for name, lie := range map[string]func(map[string]any, map[string]any){
		"other dates":          func(_, r map[string]any) { r["start_date"] = "2026-11-12" },
		"approved at creation": func(_, r map[string]any) { r["status"] = "approved" },
		"missing proposal id":  func(w, _ map[string]any) { delete(w, "proposal_id") },
		"other kind":           func(w, _ map[string]any) { w["kind"] = "access_request" },
		"other tool":           func(w, _ map[string]any) { w["tool"] = "access.request_access" },
	} {
		e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
			ev := e.agent.Confirm(context.Background(), req)[2] // the real write, then a lie about it
			wrapper := ev.Data.(map[string]any)
			var result map[string]any
			if err := json.Unmarshal(wrapper["result"].(json.RawMessage), &result); err != nil {
				t.Error(err)
			}
			wrapper["result"] = result
			lie(wrapper, result)
			return []agenttest.Event{ev}
		})
		s := e.stream(e.confirmPath(sid, pid), anna, nil)
		if s.last()["message"] != msgBadResult || e.proposalStatus(pid) != "failed" || e.count(`SELECT count(*) FROM core.actions`) != 0 {
			t.Fatalf("%s: %s %v", name, s.names(), s.last())
		}
	}
	// An honest agent replays the same write; the absence ignores the agent's checklist hint (S3 task, not CL-05).
	e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
		evs := e.agent.Confirm(context.Background(), req)
		evs[2].Data.(map[string]any)["task_checklist_item_id"] = "CL-05"
		return evs
	})
	ar := e.stream(e.confirmPath(sid, pid), anna, nil).find("action_result")
	if ar == nil || e.proposalStatus(pid) != "executed" || e.count(`SELECT count(*) FROM core.actions`) != 1 {
		t.Fatalf("honest retry: %v", ar)
	}
	if e.str(`SELECT source FROM core.plan_tasks WHERE id = $1`, ar["task_id"]) != "action" {
		t.Fatal("absence linked to a checklist task")
	}
}

// Finding 2: drafts and proposals are read with exact keys, and the stored kind and tool are re-checked at confirm.
func TestChat_DraftKeysAndStoredKind_R71(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	for name, ev := range map[string]map[string]any{
		"tool and TOOL": {"proposal_id": agenttest.NewID(), "kind": "access_request",
			"draft": map[string]any{"tool": "access.request_access", "TOOL": "approvals.decide", "args": map[string]any{"resource_id": "jira"}}},
		"kind and KIND": {"proposal_id": agenttest.NewID(), "kind": "access_request", "KIND": "approval_decision",
			"draft": map[string]any{"tool": "access.request_access", "args": map[string]any{"resource_id": "jira"}}},
		"case variant of idempotency_key": {"proposal_id": agenttest.NewID(), "kind": "access_request",
			"draft": map[string]any{"tool": "access.request_access", "args": map[string]any{"resource_id": "jira", "Idempotency_Key": "x"}}},
		"draft without tool": {"proposal_id": agenttest.NewID(), "kind": "access_request", "draft": map[string]any{"args": map[string]any{}}},
	} {
		e.agent.OnMessage(func(agenttest.Request) []agenttest.Event {
			return []agenttest.Event{{Name: "action_proposal", Data: ev}}
		})
		if s := e.message(sid, anna, "доступ"); s.last()["code"] != "forbidden" {
			t.Fatalf("%s: %s %v", name, s.names(), s.last())
		}
	}
	e.agent.OnMessage(nil)
	if e.count(`SELECT count(*) FROM core.proposals`) != 0 {
		t.Fatal("case-variant proposal stored")
	}

	for name, tamper := range map[string]string{
		"kind changed":     `UPDATE core.proposals SET kind = 'absence_request' WHERE id = $1`,
		"case variant key": `UPDATE core.proposals SET draft = draft || '{"Tool":"approvals.decide"}'::jsonb WHERE id = $1`,
	} {
		pid := e.message(sid, anna, "нужен доступ").find("action_proposal")["proposal_id"].(string)
		e.exec(tamper, pid)
		r := e.do(http.MethodPost, e.confirmPath(sid, pid), anna, nil)
		if r.status < 400 || e.proposalStatus(pid) != "pending" || e.agent.Count(agentclient.KindConfirm) != 0 {
			t.Fatalf("%s: %d %s %s", name, r.status, r.body, e.proposalStatus(pid))
		}
	}
}

// Finding 3: once an attempt reached the agent the write may have happened: cancel is refused and expiry no longer
// blocks the retry, which replays the write and saves it.
func TestConfirm_S3_NoCancelOrExpiryAfterAgentReached_R66(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
		if evs := e.agent.Confirm(context.Background(), req); evs[0].Name == "error" {
			return evs
		}
		return []agenttest.Event{{Name: "error", Data: map[string]any{"code": "service_timeout", "message": "Сервис не ответил", "retryable": true}}}
	})
	if s := e.stream(e.confirmPath(sid, pid), anna, nil); s.last()["code"] != "service_timeout" || e.proposalStatus(pid) != "failed" {
		t.Fatalf("%s %v", s.names(), s.last())
	}
	if r := e.do(http.MethodPost, e.cancelPath(sid, pid), anna, nil); r.status != http.StatusConflict || e.proposalStatus(pid) != "failed" {
		t.Fatalf("cancel after the write: %d %s", r.status, r.body)
	}
	e.chat.Now = func() time.Time { return time.Now().Add(DefaultProposalTTL + time.Minute) }
	e.agent.OnConfirm(nil)
	s := e.stream(e.confirmPath(sid, pid), anna, nil)
	if s.find("action_result") == nil || e.proposalStatus(pid) != "executed" || e.count(`SELECT count(*) FROM core.actions`) != 1 {
		t.Fatalf("retry after expiry: %s %v", s.names(), s.last())
	}
	var pending []map[string]any
	e.do(http.MethodGet, "/api/approvals", e.maria(), nil).json(t, &pending)
	if len(pending) != 1 {
		t.Fatalf("the retry wrote a second request: %v", pending)
	}
}

// Finding 4: one turn may not make core relay or store unbounded content.
func TestChat_TurnSizeBounds_R76(t *testing.T) {
	big := strings.Repeat("я", 500) // 1000 bytes
	tokens := func(n int) []agenttest.Event {
		evs := make([]agenttest.Event, n)
		for i := range evs {
			evs[i] = agenttest.Event{Name: "token", Data: map[string]string{"text": big}}
		}
		return evs
	}
	items := make([]map[string]string, maxSources+1)
	for i := range items {
		items[i] = map[string]string{"doc_id": "d", "title": "t", "source_type": "test"}
	}
	for _, tc := range []struct {
		name   string
		events []agenttest.Event
	}{
		{"assistant text over the bound", tokens(70)},
		{"too many sources", []agenttest.Event{{Name: "sources", Data: map[string]any{"items": items}}}},
		{"one event over 256 KiB", []agenttest.Event{{Name: "token", Data: map[string]string{"text": strings.Repeat("a", 300<<10)}}}},
		{"draft over the bound", []agenttest.Event{agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(),
			"kind": "access_request", "draft": map[string]any{"tool": "access.request_access",
				"args": map[string]any{"resource_id": "jira", "reason": strings.Repeat("a", maxDraftBytes)}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			anna := e.anna()
			e.agent.OnMessage(func(agenttest.Request) []agenttest.Event { return append(tc.events, agenttest.Done()) })
			s := e.message(e.session(anna), anna, "Привет")
			if last := s.last(); last["code"] != "upstream_unavailable" || last["retryable"] != true || s.events[len(s.events)-1].Name != "error" {
				t.Fatalf("%s %v", s.names(), last)
			}
			if e.count(`SELECT count(*) FROM core.proposals`) != 0 ||
				e.count(`SELECT count(*) FROM core.chat_messages WHERE octet_length(text) > $1`, maxTurnText) != 0 {
				t.Fatal("over-bound content stored")
			}
		})
	}
	t.Run("confirm text over the bound", func(t *testing.T) {
		e := newEnv(t)
		anna := e.anna()
		sid := e.session(anna)
		pid := e.propose(sid, anna)
		e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
			return append(tokens(70), e.agent.Confirm(context.Background(), req)...)
		})
		s := e.stream(e.confirmPath(sid, pid), anna, nil)
		if s.last()["message"] != msgTooLarge || e.proposalStatus(pid) != "failed" || e.count(`SELECT count(*) FROM core.actions`) != 0 {
			t.Fatalf("%s %v", s.last(), e.proposalStatus(pid))
		}
	})
}

// Checkpoint 11.11: the intern moves a request until the HR admin approves; core signs absence.reschedule_request,
// checks the result is the same request with the confirmed dates and updates the existing action.
func TestConfirm_Checkpoint_RescheduleAbsence_R71(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	ar := e.stream(e.confirmPath(sid, e.propose(sid, anna)), anna, nil).find("action_result")
	absID := ar["external_id"].(string)
	move := func(id, date string) agenttest.Event {
		return agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(), "kind": "absence_reschedule",
			"summary": "Перенести заявку", "draft": map[string]any{"tool": "absence.reschedule_request",
				"args": map[string]any{"id": id, "start_date": date, "end_date": date}}}}
	}

	pid := e.proposeWith(sid, anna, move(absID, "2026-11-20"))
	got := e.stream(e.confirmPath(sid, pid), anna, nil).find("action_result")
	if got == nil || got["action_id"] != ar["action_id"] || got["status"] != "pending_approval" || e.mcp.Calls("absence.reschedule_request") != 1 {
		t.Fatalf("moved %v calls=%d", got, e.mcp.Calls("absence.reschedule_request"))
	}
	if !strings.Contains(e.str(`SELECT summary FROM core.actions WHERE id = $1`, ar["action_id"]), "20.11.2026") ||
		e.count(`SELECT count(*) FROM core.actions WHERE external_id = $1`, absID) != 1 {
		t.Fatal("action not moved")
	}

	t.Run("agent reports other dates", func(t *testing.T) {
		pid := e.proposeWith(sid, anna, move(absID, "2026-11-27"))
		e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
			return []agenttest.Event{{Name: "tool_result", Data: map[string]any{"proposal_id": req.Turn.ProposalID, "kind": "absence_reschedule",
				"tool": "absence.reschedule_request", "result": map[string]any{"id": absID, "type": "day_off", "status": "pending_approval",
					"start_date": "2026-12-01", "end_date": "2026-12-01"}}}, agenttest.Done()}
		})
		defer e.agent.OnConfirm(nil)
		s := e.stream(e.confirmPath(sid, pid), anna, nil)
		if s.find("action_result") != nil || strings.Contains(e.str(`SELECT summary FROM core.actions WHERE id = $1`, ar["action_id"]), "01.12.2026") {
			t.Fatalf("%s %v", s.names(), s.last())
		}
	})
}
