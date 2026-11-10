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
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/canonjson"
	"aibuddy/services/internal/platform/confirm"
)

// The main scenario end to end: message → proposal stored → confirm → MCP write with core's token → action saved
// and linked → action_result streamed → visible after reload → lead approves → action and task updated, intern
// notified → a repeated confirm replays without any call.
func TestChat_S3_DayOffEndToEnd_R66(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)

	s := e.message(sid, anna, "Хочу отгул в пятницу")
	if s.names() != "status,status,token,token,sources,action_proposal,done" {
		t.Fatalf("message stream: %s", s.names())
	}
	msgTurn := e.agent.Last(agentclient.KindMessage)
	done := s.last()
	if done["message_id"] != msgTurn.MessageID || msgTurn.MessageID == "" || done["provider"] != "stub" || done["intent"] != "test" {
		t.Fatalf("done %v must be the agent's done with core's message id %q", done, msgTurn.MessageID)
	}
	if e.count(`SELECT count(*) FROM core.chat_messages WHERE id = $1 AND author = 'assistant'`, msgTurn.MessageID) != 1 {
		t.Fatal("done.message_id does not name the saved assistant message")
	}
	pid := s.find("action_proposal")["proposal_id"].(string)

	t.Run("R71_user_context_from_claims", func(t *testing.T) {
		uc := msgTurn.UserContext
		if msgTurn.Auth != "Bearer "+anna || msgTurn.Turn.Kind != "message" || msgTurn.Turn.Text != "Хочу отгул в пятницу" ||
			msgTurn.SessionID != sid || uc.UserID != "intern.anna" || uc.Role != "intern" || uc.TeamID != "delivery" ||
			uc.CurrentPeriod != "week_2" || uc.DisplayName != "Анна Котова" || !msgTurn.Flags["scenario_s3"] {
			t.Fatalf("turn %+v", msgTurn)
		}
		if _, ok := msgTurn.Raw["user_context"].(map[string]any)["flags"]; ok {
			t.Fatal("flags belong to the top level of the request")
		}
		var cl05 *agentclient.OpenTask
		for i, task := range uc.OpenTasks {
			if task.Status == "done" {
				t.Fatalf("done task in open_tasks: %+v", task)
			}
			if task.ChecklistItemID != nil && *task.ChecklistItemID == "CL-05" {
				cl05 = &uc.OpenTasks[i]
			}
		}
		if cl05 == nil || cl05.Status != "overdue" || cl05.Period != "week_1" || cl05.DueAt == nil || cl05.TaskID == "" {
			t.Fatalf("CL-05 in open_tasks: %+v", uc.OpenTasks)
		}
		if uc.Mentees != nil {
			t.Fatalf("intern got mentees: %v", uc.Mentees)
		}
		if len(msgTurn.History) != 0 {
			t.Fatalf("history of the first turn must be empty: %v", msgTurn.History)
		}
	})

	t.Run("R71_proposal_stored_with_hash", func(t *testing.T) {
		var kind, status, hash, args, user string
		var expires time.Time
		if err := e.pool.QueryRow(context.Background(), `SELECT kind, status, draft_hash, (draft->'args')::text, user_id, expires_at
			FROM core.proposals WHERE id = $1 AND session_id = $2`, pid, sid).Scan(&kind, &status, &hash, &args, &user, &expires); err != nil {
			t.Fatal(err)
		}
		want, _ := canonjson.SHA256Hex(json.RawMessage(args))
		if kind != "absence_request" || status != "pending" || hash != want || user != "intern.anna" {
			t.Fatalf("%s %s %s %s", kind, status, hash, user)
		}
		if d := time.Until(expires); d < 29*time.Minute || d > 31*time.Minute {
			t.Fatalf("proposal expires in %s, want 30 min (DefaultProposalTTL)", d)
		}
	})

	c := e.stream(e.confirmPath(sid, pid), anna, nil)
	if c.names() != "status,status,action_result,token,done" {
		t.Fatalf("confirm stream: %s", c.names())
	}
	ar := c.find("action_result")
	actionID, taskID := ar["action_id"].(string), ar["task_id"].(string)

	t.Run("R66_confirm_turn_and_mcp_write", func(t *testing.T) {
		turn := e.agent.Last(agentclient.KindConfirm)
		var d struct {
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		}
		_ = json.Unmarshal(turn.Turn.Draft, &d)
		if turn.Turn.ProposalID != pid || d.Tool != "absence.create_request" || turn.Auth != "Bearer "+anna {
			t.Fatalf("confirm turn %+v", turn.Turn)
		}
		if err := confirm.Verify(hmacSecret, turn.Turn.ConfirmationToken, "intern.anna", pid, d.Args, time.Now()); err != nil {
			t.Fatalf("confirmation token: %v", err)
		}
		if e.mcp.Calls("absence.create_request") != 1 {
			t.Fatalf("MCP write calls = %d", e.mcp.Calls("absence.create_request"))
		}
		if !strings.HasPrefix(ar["external_id"].(string), "ABS-") || ar["status"] != "pending_approval" || ar["kind"] != "absence_request" ||
			ar["summary"] != "Отгул 13.11.2026, согласующий — Мария Лебедева" || ar["approver_display_name"] != "Мария Лебедева" {
			t.Fatalf("action_result %v", ar)
		}
		if e.proposalStatus(pid) != "executed" {
			t.Fatalf("proposal %s", e.proposalStatus(pid))
		}
		if n := e.count(`SELECT count(*) FROM core.plan_tasks WHERE id = $1 AND source = 'action' AND status = 'waiting_approval'
			AND linked_action_id = $2 AND period = 'week_2'`, taskID, actionID); n != 1 {
			t.Fatal("task not linked")
		}
	})

	t.Run("R66_visible_after_reload", func(t *testing.T) {
		var list []map[string]any
		e.do(http.MethodGet, "/api/actions", anna, nil).json(t, &list)
		if len(list) != 1 || list[0]["id"] != actionID || list[0]["task_id"] != taskID || list[0]["status"] != "pending_approval" {
			t.Fatalf("actions %v", list)
		}
		var sess struct {
			SessionID string    `json:"session_id"`
			Messages  []Message `json:"messages"`
		}
		e.do(http.MethodGet, "/api/chat/sessions/"+sid, anna, nil).json(t, &sess)
		if sess.SessionID != sid || len(sess.Messages) != 3 {
			t.Fatalf("messages %+v", sess.Messages)
		}
		m := sess.Messages
		if m[0].Author != "user" || m[1].Author != "assistant" || m[1].Text != "Отгул согласует руководитель. Подготовил заявку на 13.11.2026." ||
			len(m[1].Sources) != 1 || m[2].Text != "Заявка отправлена на согласование." || m[2].ID != c.last()["message_id"] || len(m[0].Sources) != 0 {
			t.Fatalf("messages %+v", m)
		}
	})

	t.Run("S3_lead_approves_R08", func(t *testing.T) {
		maria := e.maria()
		var pending []map[string]any
		e.do(http.MethodGet, "/api/approvals", maria, nil).json(t, &pending)
		if len(pending) != 1 || pending[0]["kind"] != "absence_request" || pending[0]["requester_display_name"] != "Анна Котова" {
			t.Fatalf("approvals %v", pending)
		}
		r := e.do(http.MethodPost, "/api/approvals/"+pending[0]["id"].(string)+"/decision", maria,
			map[string]string{"decision": "approved"}, "Idempotency-Key", agenttest.NewID())
		if r.status != http.StatusOK {
			t.Fatalf("decision %d %s", r.status, r.body)
		}
		var list []map[string]any
		e.do(http.MethodGet, "/api/actions", anna, nil).json(t, &list)
		if list[0]["status"] != "approved" {
			t.Fatalf("action %v", list[0])
		}
		if e.str(`SELECT status FROM core.plan_tasks WHERE id = $1`, taskID) != "done" {
			t.Fatal("task not done")
		}
		if e.count(`SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND kind = 'status_change'`) != 1 {
			t.Fatal("no notification for the intern")
		}
	})

	t.Run("R79_replay_without_calls", func(t *testing.T) {
		confirms, writes := e.agent.Count(agentclient.KindConfirm), e.mcp.Calls("absence.create_request")
		again := e.stream(e.confirmPath(sid, pid), anna, nil)
		if again.names() != "action_result,done" || again.find("action_result")["action_id"] != actionID ||
			again.find("action_result")["status"] != "approved" || again.last()["message_id"] != c.last()["message_id"] {
			t.Fatalf("replay %s %v", again.names(), again.events)
		}
		if e.agent.Count(agentclient.KindConfirm) != confirms || e.mcp.Calls("absence.create_request") != writes {
			t.Fatal("replay called the agent or MCP")
		}
		if e.count(`SELECT count(*) FROM core.actions`) != 1 {
			t.Fatal("replay created an action")
		}
		if r := e.do(http.MethodPost, e.cancelPath(sid, pid), anna, nil); r.status != http.StatusConflict {
			t.Fatalf("cancel executed: %d", r.status)
		}
	})

	t.Run("S2_history_on_next_turn", func(t *testing.T) {
		e.message(sid, anna, "Спасибо")
		h := e.agent.Last(agentclient.KindMessage).History
		if len(h) != 3 || h[0].Author != "user" || h[2].Text != "Заявка отправлена на согласование." {
			t.Fatalf("history %+v", h)
		}
	})
}

func TestChat_S4_AccessProposalLinksChecklistTask_R66(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.message(sid, anna, "Нужен доступ к пространству инцидентов").find("action_proposal")["proposal_id"].(string)
	c := e.stream(e.confirmPath(sid, pid), anna, nil)
	ar := c.find("action_result")
	if ar == nil || ar["kind"] != "access_request" || ar["status"] != "pending_approval" {
		t.Fatalf("%s %v", c.names(), ar)
	}
	if e.str(`SELECT checklist_item_id || ':' || status FROM core.plan_tasks WHERE id = $1`, ar["task_id"]) != "CL-05:waiting_approval" {
		t.Fatal("access action not linked to CL-05")
	}
}

func TestConfirm_S3_ExpiredProposal_409(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	e.chat.Now = func() time.Time { return time.Now().Add(DefaultProposalTTL + time.Minute) }
	for i := 0; i < 2; i++ {
		r := e.do(http.MethodPost, e.confirmPath(sid, pid), anna, nil)
		if r.status != http.StatusConflict || r.code(t) != "expired" {
			t.Fatalf("attempt %d: %d %s", i, r.status, r.body)
		}
	}
	if e.proposalStatus(pid) != "expired" || e.agent.Count(agentclient.KindConfirm) != 0 {
		t.Fatal("expired proposal reached the agent")
	}
	if r := e.do(http.MethodPost, e.cancelPath(sid, pid), anna, nil); r.status != http.StatusNoContent {
		t.Fatalf("cancel expired: %d", r.status)
	}
}

func TestCancel_S3_NothingWritten_R71(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	for i := 0; i < 2; i++ {
		if r := e.do(http.MethodPost, e.cancelPath(sid, pid), anna, nil); r.status != http.StatusNoContent {
			t.Fatalf("cancel %d: %d %s", i, r.status, r.body)
		}
	}
	r := e.do(http.MethodPost, e.confirmPath(sid, pid), anna, nil)
	if r.status != http.StatusConflict || r.code(t) != "conflict" {
		t.Fatalf("confirm cancelled: %d %s", r.status, r.body)
	}
	if e.agent.Count(agentclient.KindConfirm) != 0 || e.mcp.Requests() != 0 || e.count(`SELECT count(*) FROM core.actions`) != 0 {
		t.Fatal("cancel wrote something")
	}
}

func TestChat_ForeignSessionAndProposal_Forbidden_R71(t *testing.T) {
	e := newEnv(t)
	anna, ivan := e.anna(), e.ivan()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	ivanSID := e.session(ivan)
	turns := len(e.agent.Requests())
	for _, tc := range []struct {
		name, method, path, token string
		status                    int
	}{
		{"foreign session history", http.MethodGet, "/api/chat/sessions/" + sid, ivan, http.StatusForbidden},
		{"message into foreign session", http.MethodPost, "/api/chat/sessions/" + sid + "/messages", ivan, http.StatusForbidden},
		{"confirm in foreign session", http.MethodPost, e.confirmPath(sid, pid), ivan, http.StatusForbidden},
		{"cancel in foreign session", http.MethodPost, e.cancelPath(sid, pid), ivan, http.StatusForbidden},
		{"foreign proposal in own session", http.MethodPost, e.confirmPath(ivanSID, pid), ivan, http.StatusForbidden},
		{"unknown session", http.MethodGet, "/api/chat/sessions/" + agenttest.NewID(), anna, http.StatusForbidden},
		{"unknown proposal", http.MethodPost, e.confirmPath(sid, agenttest.NewID()), anna, http.StatusForbidden},
		{"malformed session id", http.MethodGet, "/api/chat/sessions/abc", anna, http.StatusNotFound},
		{"malformed proposal id", http.MethodPost, e.cancelPath(sid, "abc"), anna, http.StatusNotFound},
		{"no token", http.MethodPost, "/api/chat/sessions", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.do(tc.method, tc.path, tc.token, map[string]string{"text": "Покажи заявки Анны"})
			if r.status != tc.status {
				t.Fatalf("%d %s", r.status, r.body)
			}
		})
	}
	if len(e.agent.Requests()) != turns || e.proposalStatus(pid) != "pending" {
		t.Fatal("a forbidden request reached the agent or changed the proposal")
	}
}

func TestChat_DisallowedProposalRejected_R71(t *testing.T) {
	dup := agenttest.NewID()
	for _, tc := range []struct {
		name     string
		proposal agenttest.Event
	}{
		{"tool outside the allow-list", agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(),
			"kind": "salary_change", "draft": map[string]any{"tool": "payroll.pay", "args": map[string]any{"amount": "1"}}}}},
		{"approval decision from an intern", agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(),
			"kind": "approval_decision", "draft": map[string]any{"tool": "approvals.decide", "args": map[string]any{"approval_id": "APR-1", "decision": "approved"}}}}},
		{"tool does not match kind", agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(),
			"kind": "absence_request", "draft": map[string]any{"tool": "access.request_access", "args": map[string]any{"resource_id": "jira"}}}}},
		{"args carry idempotency key", agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(),
			"kind": "access_request", "draft": map[string]any{"tool": "access.request_access", "args": map[string]any{"resource_id": "jira", "idempotency_key": "x"}}}}},
		{"args carry confirmation token", agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(),
			"kind": "access_request", "draft": map[string]any{"tool": "access.request_access", "args": map[string]any{"resource_id": "jira", "confirmation_token": "x"}}}}},
		{"args are not an object", agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(),
			"kind": "access_request", "draft": map[string]any{"tool": "access.request_access", "args": []string{"jira"}}}}},
		{"proposal id is not a uuid", agenttest.AccessProposal("p-1", "jira")},
		{"draft is not an object", agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(), "kind": "access_request", "draft": "x"}}},
		{"proposal is not an object", agenttest.Event{Name: "action_proposal", Data: []int{1}}},
		{"duplicate proposal id", agenttest.AccessProposal(dup, "jira")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			anna := e.anna()
			sid := e.session(anna)
			if tc.name == "duplicate proposal id" {
				e.agent.OnMessage(func(agenttest.Request) []agenttest.Event { return []agenttest.Event{tc.proposal, agenttest.Done()} })
				if s := e.message(sid, anna, "доступ"); s.names() != "action_proposal,done" {
					t.Fatalf("first: %s", s.names())
				}
			}
			e.agent.OnMessage(func(agenttest.Request) []agenttest.Event {
				return []agenttest.Event{{Name: "token", Data: map[string]string{"text": "Готово."}}, tc.proposal, agenttest.Done()}
			})
			s := e.message(sid, anna, "согласуй")
			last := s.last()
			if s.names() != "token,error" || last["code"] != "forbidden" || last["retryable"] != false || last["message"] != msgBadProposal {
				t.Fatalf("%s %v", s.names(), last)
			}
			want := 0
			if tc.name == "duplicate proposal id" {
				want = 1
			}
			if e.count(`SELECT count(*) FROM core.proposals`) != want {
				t.Fatal("rejected proposal stored")
			}
			if e.count(`SELECT count(*) FROM core.chat_messages WHERE author = 'assistant' AND text = 'Готово.'`) != 1 {
				t.Fatal("assistant text before the rejection not saved")
			}
		})
	}
}

func TestChat_AgentUnavailable_UpstreamUnavailable_R79(t *testing.T) {
	t.Run("agent down", func(t *testing.T) {
		e := newEnv(t, func(d *Deps, _ *Config) { d.Agent = agentclient.New("http://127.0.0.1:1", time.Second) })
		anna := e.anna()
		sid := e.session(anna)
		s := e.message(sid, anna, "Привет")
		last := s.last()
		if s.names() != "error" || last["code"] != "upstream_unavailable" || last["retryable"] != true || last["message"] != msgAgentUnavailable {
			t.Fatalf("%s %v", s.names(), last)
		}
		if e.count(`SELECT count(*) FROM core.chat_messages WHERE author = 'user'`) != 1 {
			t.Fatal("user message not saved")
		}
	})
	t.Run("agent timeout", func(t *testing.T) {
		e := newEnv(t)
		e.chat.agent = &agentclient.Client{BaseURL: e.agent.URL, Timeout: 300 * time.Millisecond}
		e.agent.Hang(true)
		anna := e.anna()
		s := e.message(e.session(anna), anna, "Привет")
		if s.names() != "status,error" || s.last()["code"] != "upstream_unavailable" {
			t.Fatalf("%s %v", s.names(), s.last())
		}
	})
	t.Run("agent rejects the request", func(t *testing.T) {
		e := newEnv(t)
		e.agent.FailWith(http.StatusForbidden)
		anna := e.anna()
		s := e.message(e.session(anna), anna, "Привет")
		if s.names() != "error" || s.last()["code"] != "context_mismatch" || s.last()["retryable"] != false {
			t.Fatalf("%s %v", s.names(), s.last())
		}
	})
	t.Run("agent error event ends the stream", func(t *testing.T) {
		e := newEnv(t)
		e.agent.OnMessage(func(agenttest.Request) []agenttest.Event {
			return []agenttest.Event{{Name: "token", Data: map[string]string{"text": "Начинаю"}},
				{Name: "error", Data: map[string]any{"code": "llm_timeout", "message": "Модель не ответила", "retryable": true}},
				{Name: "token", Data: map[string]string{"text": "после ошибки"}}}
		})
		anna := e.anna()
		s := e.message(e.session(anna), anna, "Привет")
		if s.names() != "token,error" || s.last()["code"] != "llm_timeout" {
			t.Fatalf("%s %v", s.names(), s.last())
		}
		if e.count(`SELECT count(*) FROM core.chat_messages WHERE author = 'assistant' AND text = 'Начинаю'`) != 1 {
			t.Fatal("partial answer not saved")
		}
	})
	t.Run("confirm with agent down then retry", func(t *testing.T) {
		e := newEnv(t)
		anna := e.anna()
		sid := e.session(anna)
		pid := e.propose(sid, anna)
		e.agent.FailWith(http.StatusServiceUnavailable)
		s := e.stream(e.confirmPath(sid, pid), anna, nil)
		if s.names() != "error" || s.last()["code"] != "upstream_unavailable" || s.last()["retryable"] != true || e.proposalStatus(pid) != "failed" {
			t.Fatalf("%s %v %s", s.names(), s.last(), e.proposalStatus(pid))
		}
		e.agent.FailWith(0)
		if s := e.stream(e.confirmPath(sid, pid), anna, nil); s.find("action_result") == nil || e.proposalStatus(pid) != "executed" {
			t.Fatalf("retry: %s", s.names())
		}
	})
}

func TestConfirm_S3_ToolErrorThenRetry_R79(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	e.mcp.FailNext("absence.create_request", apierr.Unavailable())
	s := e.stream(e.confirmPath(sid, pid), anna, nil)
	if s.names() != "error" || s.last()["code"] != "unavailable" || s.last()["retryable"] != true {
		t.Fatalf("%s %v", s.names(), s.last())
	}
	if e.proposalStatus(pid) != "failed" || e.count(`SELECT count(*) FROM core.analytics_events WHERE name = 'action.failed'`) != 1 {
		t.Fatal("tool error not recorded")
	}
	s = e.stream(e.confirmPath(sid, pid), anna, nil)
	if s.find("action_result") == nil || e.proposalStatus(pid) != "executed" || e.count(`SELECT count(*) FROM core.actions`) != 1 {
		t.Fatalf("retry %s", s.names())
	}
	if e.count(`SELECT count(*) FROM core.analytics_events WHERE name = 'action.created' AND (props->>'attempt')::int = 2`) != 1 {
		t.Fatal("second attempt not counted")
	}
}

func TestConfirm_S3_AgentWithoutUsableResult_Failed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []agenttest.Event
		code   string
	}{
		{"no tool_result", []agenttest.Event{agenttest.Done()}, "upstream_unavailable"},
		{"tool_result for another proposal", []agenttest.Event{{Name: "tool_result", Data: map[string]any{"proposal_id": agenttest.NewID(), "result": map[string]any{"id": "ABS-1"}}}}, "upstream_unavailable"},
		{"tool_result without binding", []agenttest.Event{{Name: "tool_result", Data: map[string]any{"result": map[string]any{"id": "ABS-1", "status": "pending_approval"}}}}, "upstream_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			anna := e.anna()
			sid := e.session(anna)
			pid := e.propose(sid, anna)
			e.agent.OnConfirm(func(agenttest.Request) []agenttest.Event { return tc.events })
			s := e.stream(e.confirmPath(sid, pid), anna, nil)
			if s.last()["code"] != tc.code || s.last()["retryable"] != true || e.proposalStatus(pid) != "failed" {
				t.Fatalf("%s %v %s", s.names(), s.last(), e.proposalStatus(pid))
			}
			if e.count(`SELECT count(*) FROM core.actions`) != 0 {
				t.Fatal("action saved")
			}
		})
	}
}

func TestConfirm_S3_LateEventsAfterSave(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	e.agent.OnConfirm(func(req agenttest.Request) []agenttest.Event {
		result := e.agent.Confirm(context.Background(), req)[2]
		result.Data.(map[string]any)["followup"] = ""
		return []agenttest.Event{
			agenttest.AccessProposal(agenttest.NewID(), "jira"), // ignored in a confirm turn
			result,
			result, // duplicate, ignored
			{Name: "token", Data: map[string]string{"text": "Готово"}},
			{Name: "error", Data: map[string]any{"code": "llm_timeout", "message": "x", "retryable": true}}, // after the save
		}
	})
	s := e.stream(e.confirmPath(sid, pid), anna, nil)
	if s.names() != "action_result,token,done" || e.proposalStatus(pid) != "executed" {
		t.Fatalf("%s", s.names())
	}
	if e.str(`SELECT text FROM core.chat_messages WHERE id = $1`, s.last()["message_id"]) != "Готово" {
		t.Fatal("follow-up text not stored")
	}
	if e.count(`SELECT count(*) FROM core.proposals`) != 1 {
		t.Fatal("proposal from a confirm turn stored")
	}
}

func TestConfirm_TamperedDraftHash_Internal(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	pid := e.propose(sid, anna)
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.proposals SET draft_hash = repeat('0', 64) WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	r := e.do(http.MethodPost, e.confirmPath(sid, pid), anna, nil)
	// Rejected before the agent was called: the proposal stays pending and can still be cancelled.
	if r.status != http.StatusInternalServerError || e.proposalStatus(pid) != "pending" || e.agent.Count(agentclient.KindConfirm) != 0 {
		t.Fatalf("%d %s", r.status, e.proposalStatus(pid))
	}
	if r := e.do(http.MethodPost, e.cancelPath(sid, pid), anna, nil); r.status != http.StatusNoContent {
		t.Fatalf("cancel: %d", r.status)
	}
}

func TestChat_RateLimited_429_R76(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	e.limiter.allow = false
	r := e.do(http.MethodPost, "/api/chat/sessions/"+sid+"/messages", anna, map[string]string{"text": "Привет"})
	if r.status != http.StatusTooManyRequests || r.code(t) != "rate_limited" || len(e.agent.Requests()) != 0 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if e.count(`SELECT count(*) FROM core.chat_messages`) != 0 || e.limiter.keys[0] != "chat:intern.anna" {
		t.Fatal("limited message stored")
	}
	e.limiter.allow, e.limiter.err = true, context.DeadlineExceeded // Redis down: fail open
	if s := e.message(sid, anna, "Привет"); s.names() != "status,token,done" {
		t.Fatal(s.names())
	}
}

func TestChat_MessageValidation_R76(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	for name, body := range map[string]any{
		"empty":         map[string]string{"text": ""},
		"whitespace":    map[string]string{"text": "   "},
		"too long":      map[string]string{"text": strings.Repeat("я", 4001)},
		"unknown field": map[string]string{"text": "a", "role": "lead"},
		"not json":      "{",
	} {
		t.Run(name, func(t *testing.T) {
			r := e.do(http.MethodPost, "/api/chat/sessions/"+sid+"/messages", anna, body)
			if r.status != http.StatusBadRequest || r.code(t) != "validation_error" {
				t.Fatalf("%d %s", r.status, r.body)
			}
		})
	}
	if len(e.agent.Requests()) != 0 {
		t.Fatal("invalid message reached the agent")
	}
	if s := e.message(sid, anna, strings.Repeat("я", 4000)); s.last()["message_id"] == nil {
		t.Fatal("4000 characters must be accepted")
	}
}

func TestChat_S2_UserContextForMentorAndLead(t *testing.T) {
	e := newEnv(t, func(d *Deps, _ *Config) { d.Flags = stubFlags{err: context.Canceled} })
	anna := e.anna()
	annaSID := e.session(anna)
	e.stream(e.confirmPath(annaSID, e.message(annaSID, anna, "нужен доступ").find("action_proposal")["proposal_id"].(string)), anna, nil)
	for _, tc := range []struct {
		token, period string
		tasks         bool
	}{{e.oleg(), "week_2", true}, {e.maria(), "week_1", false}} {
		e.message(e.session(tc.token), tc.token, "Как дела у стажёра?")
		uc := e.agent.Last(agentclient.KindMessage).UserContext
		mentees, _ := json.Marshal(uc.Mentees)
		turn := e.agent.Last(agentclient.KindMessage)
		if uc.CurrentPeriod != tc.period || !strings.Contains(string(mentees), `"user_id":"intern.anna"`) || turn.Flags != nil ||
			!strings.Contains(string(mentees), `"accesses":[{"name":"Закрытое пространство инцидентов «Доставки»","status":"pending_approval"}]`) ||
			!strings.Contains(string(mentees), `"open_tasks":[{`) {
			t.Fatalf("%s: %+v %s", uc.UserID, uc, mentees)
		}
		if (len(uc.OpenTasks) > 0) != tc.tasks {
			t.Fatalf("%s open tasks %v", uc.UserID, uc.OpenTasks)
		}
	}
}

func TestChat_S2_HistoryIsLastTenMessages(t *testing.T) {
	e := newEnv(t)
	anna := e.anna()
	sid := e.session(anna)
	for i := 0; i < 12; i++ {
		author := []string{"user", "assistant"}[i%2]
		if err := insertMessage(context.Background(), e.pool, agenttest.NewID(), sid, author, "m"+itoa(i), nil); err != nil {
			t.Fatal(err)
		}
	}
	e.message(sid, anna, "Новый вопрос")
	h := e.agent.Last(agentclient.KindMessage).History
	if len(h) != historyLimit || h[0].Text != "m2" || h[9].Text != "m11" {
		t.Fatalf("history %+v", h)
	}
}

func TestChat_RefusalAndUnknownEvents_R72(t *testing.T) {
	e := newEnv(t)
	e.agent.OnMessage(func(agenttest.Request) []agenttest.Event {
		return []agenttest.Event{
			{Name: "refusal", Data: map[string]any{"reason": "out_of_scope", "redirect_role": "hrbp", "text": "Это вопрос к HR BP."}},
			{Name: "action_result", Data: map[string]any{"action_id": "forged"}}, // only core emits action_result
			{Name: "tool_result", Data: map[string]any{"id": "ABS-1"}},           // internal, never relayed
			{Name: "escalation_draft", Data: map[string]any{"to_role": "hrbp", "text": "Вопрос о зарплате"}},
			{Name: "next_step", Data: map[string]any{"checklist_item_id": "CL-05", "title": "Запроси доступ"}},
			{Name: "future_event", Data: map[string]any{"x": 1}}, // unknown events are relayed unchanged
			{Name: "done", Data: map[string]any{"message_id": "", "escalated": true, "latency_ms": 42, "provider": "stub"}},
		}
	})
	anna := e.anna()
	s := e.message(e.session(anna), anna, "Какая у меня зарплата?")
	if s.names() != "refusal,escalation_draft,next_step,future_event,done" || s.last()["escalated"] != true {
		t.Fatalf("%s %v", s.names(), s.last())
	}
	for _, q := range []string{
		`SELECT count(*) FROM core.analytics_events WHERE name = 'chat.refused' AND props->>'reason' = 'out_of_scope'`,
		`SELECT count(*) FROM core.analytics_events WHERE name = 'chat.escalated' AND props->>'to_role' = 'hrbp'`,
		`SELECT count(*) FROM core.analytics_events WHERE name = 'chat.answered' AND (props->>'latency_ms')::int = 42 AND props->>'provider' = 'stub'`,
		`SELECT count(*) FROM core.chat_messages WHERE author = 'assistant' AND text = 'Это вопрос к HR BP.'`,
	} {
		if e.count(q) != 1 {
			t.Fatalf("missing: %s", q)
		}
	}
}

// A lead approves in chat (approval_decision): core signs approvals.decide, the agent calls it, core applies the
// decision to the intern's action and plan task, notifies the intern and streams the updated action.
func TestChat_S3_LeadDecidesInChat_R66(t *testing.T) {
	e := newEnv(t)
	anna, maria := e.anna(), e.maria()
	annaSID := e.session(anna)
	ar := e.stream(e.confirmPath(annaSID, e.propose(annaSID, anna)), anna, nil).find("action_result")
	var pending []map[string]any
	e.do(http.MethodGet, "/api/approvals", maria, nil).json(t, &pending)
	approvalID := pending[0]["id"].(string)

	decision := func(approval, ref string) agenttest.Event {
		return agenttest.Event{Name: "action_proposal", Data: map[string]any{"proposal_id": agenttest.NewID(), "kind": "approval_decision",
			"summary": "Согласовать отгул", "draft": map[string]any{"tool": "approvals.decide",
				"args":    map[string]any{"approval_id": approval, "decision": "approved", "comment": ""},
				"display": map[string]any{"ref_id": ref, "requester_display_name": "Анна Котова"}}}}
	}
	sid := e.session(maria)
	e.agent.OnMessage(func(agenttest.Request) []agenttest.Event {
		return []agenttest.Event{decision(approvalID, ar["external_id"].(string)), agenttest.Done()}
	})
	pid := e.message(sid, maria, "Согласуй отгул Анны").find("action_proposal")["proposal_id"].(string)
	if e.str(`SELECT kind FROM core.proposals WHERE id = $1`, pid) != "approval_decision" {
		t.Fatal("kind not stored")
	}
	c := e.stream(e.confirmPath(sid, pid), maria, nil)
	got := c.find("action_result")
	// Two decide calls: the agent's write and core's read-back with the same idempotency key (a replay).
	if got == nil || got["action_id"] != ar["action_id"] || got["status"] != "approved" || e.mcp.Calls("approvals.decide") != 2 {
		t.Fatalf("%s %v", c.names(), got)
	}
	if e.str(`SELECT status FROM core.plan_tasks WHERE id = $1`, ar["task_id"]) != "done" ||
		e.count(`SELECT count(*) FROM core.notifications WHERE user_id = 'intern.anna' AND kind = 'status_change'`) != 1 ||
		e.count(`SELECT count(*) FROM core.analytics_events WHERE name = 'action.status_changed'`) != 1 {
		t.Fatal("decision not applied")
	}
	again := e.stream(e.confirmPath(sid, pid), maria, nil)
	if again.names() != "action_result,done" || again.find("action_result")["status"] != "approved" ||
		again.last()["message_id"] != c.last()["message_id"] || e.mcp.Calls("approvals.decide") != 2 {
		t.Fatalf("replay %s %v", again.names(), again.last())
	}

	t.Run("request made outside the chat", func(t *testing.T) {
		// The access request is created directly in MCP, as if it came from another channel: core has no action.
		direct := agenttest.Request{Auth: "Bearer " + anna}
		direct.Turn = agentclient.Turn{Kind: "confirm", ProposalID: agenttest.NewID(),
			Draft: json.RawMessage(`{"tool":"access.request_access","args":{"resource_id":"space-delivery-incidents"}}`)}
		hash, _ := canonjson.SHA256Hex(json.RawMessage(`{"resource_id":"space-delivery-incidents"}`))
		direct.Turn.ConfirmationToken, _ = confirm.Issue(hmacSecret, "intern.anna", direct.Turn.ProposalID, hash, time.Minute)
		if evs := e.agent.Confirm(context.Background(), direct); evs[0].Name == "error" {
			t.Fatalf("direct request: %v", evs[0].Data)
		}
		e.do(http.MethodGet, "/api/approvals", maria, nil).json(t, &pending)
		e.agent.OnMessage(func(agenttest.Request) []agenttest.Event {
			return []agenttest.Event{decision(pending[0]["id"].(string), "ACC-000041"), agenttest.Done()}
		})
		pid := e.message(sid, maria, "Согласуй доступ").find("action_proposal")["proposal_id"].(string)
		c := e.stream(e.confirmPath(sid, pid), maria, nil)
		if c.names() != "status,status,token,done" || e.proposalStatus(pid) != "executed" {
			t.Fatalf("%s", c.names())
		}
		if again := e.stream(e.confirmPath(sid, pid), maria, nil); again.names() != "done" {
			t.Fatalf("replay %s", again.names())
		}
	})
}

func TestParseToolResult_BoundToProposal_R71(t *testing.T) {
	p := proposal{id: "11111111-1111-4111-8111-111111111111", kind: "access_request"}
	d := draft{Tool: "access.request_access", Args: json.RawMessage(`{"resource_id":"jira","reason":""}`)}
	wrap := func(extra string) string {
		return `{"proposal_id":"` + p.id + `","kind":"access_request","tool":"access.request_access",` + extra + `}`
	}
	for _, tc := range []struct {
		name, data string
		ok         bool
	}{
		{"agent wrapper", wrap(`"result":{"id":"ACC-1","resource_id":"jira","status":"granted"},"followup":"ok","task_checklist_item_id":"CL-01"`), true},
		{"bare MCP result", `{"id":"ACC-1","resource_id":"jira","status":"granted"}`, false},
		{"other proposal", strings.Replace(wrap(`"result":{"id":"ACC-1","resource_id":"jira","status":"granted"}`), p.id, "22222222-2222-4222-8222-222222222222", 1), false},
		{"other kind", strings.Replace(wrap(`"result":{"id":"ACC-1","resource_id":"jira","status":"granted"}`), `"kind":"access_request"`, `"kind":"absence_request"`, 1), false},
		{"other tool", strings.Replace(wrap(`"result":{"id":"ACC-1","resource_id":"jira","status":"granted"}`), `"tool":"access.request_access"`, `"tool":"absence.create_request"`, 1), false},
		{"other resource", wrap(`"result":{"id":"ACC-1","resource_id":"gitlab","status":"granted"}`), false},
		{"rejected at creation", wrap(`"result":{"id":"ACC-1","resource_id":"jira","status":"rejected"}`), false},
		{"not an access id", wrap(`"result":{"id":"ABS-1","resource_id":"jira","status":"granted"}`), false},
		{"case variant of result", wrap(`"result":{"id":"ACC-1","resource_id":"jira","status":"granted"},"Result":{}`), false},
		{"case variant inside result", wrap(`"result":{"id":"ACC-1","ID":"ACC-2","resource_id":"jira","status":"granted"}`), false},
		{"result not an object", wrap(`"result":"ok"`), false},
		{"not an object", `[1]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseToolResult(json.RawMessage(tc.data), p, d)
			if (err == nil) != tc.ok {
				t.Fatalf("%+v %v", got, err)
			}
			if tc.ok && (got.Followup != "ok" || got.ChecklistItemID != "CL-01" || !strings.Contains(string(got.Result), "ACC-1")) {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestCheckResult_S3_MatchesConfirmedDraft_R71(t *testing.T) {
	absence := draft{Tool: "absence.create_request", Args: json.RawMessage(`{"type":"day_off","start_date":"2026-11-13","end_date":"2026-11-13","comment":""}`)}
	decision := draft{Tool: "approvals.decide", Args: json.RawMessage(`{"approval_id":"APR-1","decision":"approved","comment":""}`)}
	for _, tc := range []struct {
		name   string
		d      draft
		result string
		ok     bool
	}{
		{"absence as confirmed", absence, `{"id":"ABS-000101","type":"day_off","start_date":"2026-11-13","end_date":"2026-11-13","status":"pending_approval"}`, true},
		{"absence other dates", absence, `{"id":"ABS-000101","type":"day_off","start_date":"2026-11-12","end_date":"2026-11-13","status":"pending_approval"}`, false},
		{"absence approved at creation", absence, `{"id":"ABS-000101","type":"day_off","start_date":"2026-11-13","end_date":"2026-11-13","status":"approved"}`, false},
		{"decision as confirmed", decision, `{"id":"APR-1","status":"approved"}`, true},
		{"decision on another approval", decision, `{"id":"APR-2","status":"approved"}`, false},
		{"decision reversed", decision, `{"id":"APR-1","status":"rejected"}`, false},
		{"decision still pending", draft{Tool: "approvals.decide", Args: json.RawMessage(`{"approval_id":"APR-1","decision":"pending"}`)}, `{"id":"APR-1","status":"pending"}`, false},
		{"unknown tool", draft{Tool: "kb.add_faq", Args: json.RawMessage(`{}`)}, `{"id":"x"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var res map[string]json.RawMessage
			_ = json.Unmarshal([]byte(tc.result), &res)
			if err := checkResult(tc.d, res); (err == nil) != tc.ok {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentError(t *testing.T) {
	if e := agentError(&agentclient.UnavailableError{Status: 400, Code: "bad_request"}); e.Code != "bad_request" || e.Message != msgAgentUnavailable {
		t.Fatalf("%+v", e)
	}
	if e := agentError(&agentclient.UnavailableError{Status: 502, Code: "x"}); e.Code != "upstream_unavailable" || !e.Retryable {
		t.Fatalf("%+v", e)
	}
}
