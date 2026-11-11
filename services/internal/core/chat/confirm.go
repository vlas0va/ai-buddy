package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aibuddy/services/internal/core/actions"
	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/canonjson"
	"aibuddy/services/internal/platform/confirm"
	"aibuddy/services/internal/platform/httpx"
)

// Proposal lifecycle (core.proposals.status):
//
//	pending   → stored, never sent to the agent: can be confirmed, cancelled or expire.
//	confirmed → a confirm attempt is in flight.
//	failed    → an attempt reached the agent and did not complete. The write may have happened, so the proposal can
//	            no longer be cancelled or expire: a later confirm retries with the same idempotency key and replays.
//	executed  → saved (core.actions); a confirm replays the stored result (R79).
//	cancelled, expired → final; only reachable from pending.
//
// A failure before the agent is called puts the proposal back to the status it had.

// proposal is a row of core.proposals.
type proposal struct {
	id, kind, status, draftHash string
	draft                       json.RawMessage
	createdAt, expiresAt        time.Time
}

// loadProposal returns the caller's proposal in the session; a malformed id is not_found, a missing or foreign
// one forbidden.
func (h *Handler) loadProposal(ctx context.Context, q pgx.Row, id string) (proposal, error) {
	p := proposal{id: id}
	var draftText string
	err := q.Scan(&p.kind, &p.status, &p.draftHash, &draftText, &p.createdAt, &p.expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, apierr.Forbidden()
	}
	if err != nil {
		return p, fmt.Errorf("load proposal: %w", err)
	}
	p.draft = json.RawMessage(draftText)
	return p, nil
}

const proposalSelect = `SELECT kind, status, draft_hash, draft::text, created_at, expires_at FROM core.proposals
	WHERE id = $1 AND session_id = $2 AND user_id = $3`

// target checks the session and proposal ids of a confirm or cancel request and loads the proposal.
func (h *Handler) target(r *http.Request, c authn.Claims) (proposal, error) {
	sid, pid := r.PathValue("session_id"), r.PathValue("proposal_id")
	if err := h.ownSession(r.Context(), c.Sub, sid); err != nil {
		return proposal{}, err
	}
	if !uuidRE.MatchString(pid) {
		return proposal{}, apierr.NotFound()
	}
	return h.loadProposal(r.Context(), h.pool.QueryRow(r.Context(), proposalSelect, pid, sid, c.Sub), pid)
}

var (
	errCancelled = apierr.Conflict().WithMessage("Заявка отменена. Попросите ассистента подготовить новую")
	errExpired   = apierr.Expired().WithMessage("Черновик заявки устарел. Попросите ассистента подготовить его заново")
	errInFlight  = apierr.Conflict().WithMessage("Заявка уже отправлена")
)

// attempt is one confirmation of a proposal.
type attempt struct {
	c     authn.Claims
	token string
	p     proposal
	d     draft
	n     int // 1 for the first confirmation, 2 for a retry (analytics)
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, token, ok := caller(w, r)
	if !ok {
		return
	}
	p, err := h.target(r, c)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	sid := r.PathValue("session_id")
	switch {
	case p.status == "executed":
		h.replay(w, r, c, p)
		return
	case p.status == "cancelled":
		apierr.WriteJSON(w, errCancelled)
		return
	case p.status == "expired":
		apierr.WriteJSON(w, errExpired)
		return
	case p.status == "pending" && !h.Now().Before(p.expiresAt):
		// Only a proposal that never reached the agent expires; after an attempt the retry must stay possible (R66).
		if _, err := h.pool.Exec(ctx, `UPDATE core.proposals SET status = 'expired' WHERE id = $1 AND status = 'pending'`, p.id); err != nil {
			h.log.Warn("mark proposal expired failed", "proposal", p.id, "err", err)
		}
		apierr.WriteJSON(w, errExpired)
		return
	}
	at := attempt{c: c, token: token, p: p, n: 1}
	if p.status != "pending" {
		at.n = 2
	}
	// A repeated confirm while one is in flight is allowed: the write is idempotent by proposal_id (R79).
	tag, err := h.pool.Exec(ctx, `UPDATE core.proposals SET status = 'confirmed' WHERE id = $1 AND status IN ('pending', 'failed', 'confirmed')`, p.id)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("claim proposal: %w", err))
		return
	}
	if tag.RowsAffected() != 1 {
		apierr.WriteJSON(w, errInFlight) // executed or cancelled in between
		return
	}
	if at.d, err = h.checkStored(c, p); err != nil {
		h.release(ctx, p)
		apierr.WriteJSON(w, err)
		return
	}
	confirmation, err := confirm.Issue(h.cfg.ConfirmationSecret, c.Sub, p.id, p.draftHash, h.cfg.ConfirmationTTL)
	if err != nil {
		h.release(ctx, p)
		apierr.WriteJSON(w, fmt.Errorf("issue confirmation token: %w", err))
		return
	}
	uc, flags, err := h.userContext(ctx, c)
	if err != nil {
		h.release(ctx, p)
		apierr.WriteJSON(w, err)
		return
	}
	hist, err := h.history(ctx, sid, newUUID())
	if err != nil {
		h.release(ctx, p)
		apierr.WriteJSON(w, err)
		return
	}
	sse, err := httpx.NewSSEWriter(ctx, w, h.cfg.KeepAlive)
	if err != nil {
		h.release(ctx, p)
		apierr.WriteJSON(w, err)
		return
	}
	defer sse.Close()
	// From here on the agent may have called the write tool: failures leave the proposal failed (retryable).
	assistantID := newUUID()
	stream, err := h.agent.Turn(ctx, token, agentclient.TurnRequest{SessionID: sid, MessageID: assistantID,
		Turn:        agentclient.Turn{Kind: agentclient.KindConfirm, ProposalID: p.id, Draft: p.draft, ConfirmationToken: confirmation},
		UserContext: uc, History: hist, Flags: flags})
	if err != nil {
		h.log.Warn("agent confirm turn failed", "proposal", p.id, "err", err)
		h.fail(ctx, c, p, sse, agentError(err))
		return
	}
	defer stream.Close()

	var (
		st    turnState
		saved bool
	)
loop:
	for {
		ev, err := stream.Next()
		switch {
		case errors.Is(err, io.EOF):
			break loop
		case err != nil:
			h.log.Warn("agent confirm stream failed", "proposal", p.id, "err", err)
			if saved {
				break loop // the write is saved; a late agent failure only loses the follow-up text
			}
			h.fail(ctx, c, p, sse, agentError(err))
			return
		}
		st.collect(ev)
		if st.tooLarge {
			h.log.Warn("agent confirm turn over the size bounds", "proposal", p.id, "event", ev.Name)
			if !saved {
				h.markFailed(ctx, p.id)
			}
			_ = sse.Send(agentclient.EventError, errTooLarge)
			return
		}
		switch {
		case ev.Name == agentclient.EventToolResult:
			if saved {
				continue
			}
			res, err := parseToolResult(ev.Data, p, at.d)
			if err != nil {
				h.log.Warn("tool_result rejected", "proposal", p.id, "err", err)
				h.fail(ctx, c, p, sse, streamError{Code: string(apierr.CodeUpstreamUnavailable), Message: msgBadResult, Retryable: true})
				return
			}
			save := h.saveResult
			if at.d.Tool == mcpclient.ToolDecideApproval {
				save = h.saveDecision
			}
			a, msgID, err := save(ctx, at, res, assistantID)
			if err != nil {
				h.log.Error("save action failed", "proposal", p.id, "err", err)
				h.fail(ctx, c, p, sse, streamError{Code: string(apierr.CodeInternal), Message: msgSaveFailed, Retryable: true})
				return
			}
			saved, assistantID = true, msgID
			if a != nil && sse.Send(agentclient.EventActionResult, a.Result()) != nil {
				return
			}
			if res.Followup != "" {
				if st.addText(res.Followup); st.tooLarge {
					_ = sse.Send(agentclient.EventError, errTooLarge)
					return
				}
				if sse.Send(agentclient.EventToken, map[string]string{"text": res.Followup}) != nil {
					return
				}
			}
		case ev.Name == agentclient.EventError:
			if saved {
				break loop
			}
			// The proposal stays confirmable: a retry is safe because the write is idempotent by proposal_id (R79).
			var e streamError
			_ = json.Unmarshal(ev.Data, &e)
			h.markFailed(ctx, p.id)
			h.emit(ctx, c, "action.failed", map[string]any{"kind": p.kind, "code": e.Code, "retryable": e.Retryable})
			_ = sse.Send(ev.Name, ev.Data)
			return
		case ev.Name == agentclient.EventDone:
			break loop
		case internalEvents[ev.Name] || ev.Name == agentclient.EventActionProposal:
			h.log.Warn("agent event dropped", "event", ev.Name, "proposal", p.id)
		default: // status, token, sources, next_step and events added later
			if sse.Send(ev.Name, ev.Data) != nil {
				if !saved {
					h.markFailed(ctx, p.id)
				}
				return
			}
		}
	}
	if !saved {
		h.fail(ctx, c, p, sse, streamError{Code: string(apierr.CodeUpstreamUnavailable), Message: msgNoResult, Retryable: true})
		return
	}
	if text := st.text.String(); text != "" {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if _, err := h.pool.Exec(uctx, `UPDATE core.chat_messages SET text = $2 WHERE id = $1`, assistantID, text); err != nil {
			h.log.Warn("update assistant message failed", "message", assistantID, "err", err)
		}
		cancel()
	}
	_ = sse.Send(agentclient.EventDone, st.doneEvent(assistantID))
}

// checkStored re-checks a stored proposal before it is signed: exact-key draft, the tool allowed for the stored kind
// (approval decisions only for leads) and draft_hash equal to the hash of draft.args.
func (h *Handler) checkStored(c authn.Claims, p proposal) (draft, error) {
	d, err := decodeDraft(p.draft)
	if err != nil {
		return draft{}, fmt.Errorf("proposal %s: %w: %w", p.id, apierr.Internal(), err)
	}
	if tool, ok := allowedTools[p.kind]; !ok || tool != d.Tool || (p.kind == kindApprovalDecision && (c.Role != "lead" || h.mcp == nil)) {
		return draft{}, fmt.Errorf("proposal %s: tool %q not allowed for kind %q: %w", p.id, d.Tool, p.kind, apierr.Forbidden().WithMessage(msgBadProposal))
	}
	if hash, err := canonjson.SHA256Hex(d.Args); err != nil || hash != p.draftHash {
		return draft{}, fmt.Errorf("proposal %s: stored draft does not match draft_hash: %w", p.id, apierr.Internal())
	}
	return d, nil
}

// replay answers a confirm of an executed proposal with the stored action_result and done, calling nobody (R79).
func (h *Handler) replay(w http.ResponseWriter, r *http.Request, c authn.Claims, p proposal) {
	var a *actions.Action
	var messageID string
	var stored struct {
		Tool string `json:"tool"`
		Core struct {
			ActionID  string `json:"action_id"`
			MessageID string `json:"message_id"`
		} `json:"_core"`
	}
	_ = json.Unmarshal(p.draft, &stored)
	if p.kind == kindApprovalDecision {
		messageID = stored.Core.MessageID
		if stored.Core.ActionID != "" {
			got, err := h.actions.ByID(r.Context(), h.pool, stored.Core.ActionID)
			if err != nil {
				apierr.WriteJSON(w, fmt.Errorf("replay decision %s: %w", p.id, err))
				return
			}
			a = &got
		}
	} else {
		got, err := h.actions.ByProposal(r.Context(), h.pool, c.Sub, p.id)
		if err != nil {
			apierr.WriteJSON(w, fmt.Errorf("replay proposal %s: %w", p.id, err))
			return
		}
		a, messageID = &got, got.MessageID
	}
	sse, err := httpx.NewSSEWriter(r.Context(), w, h.cfg.KeepAlive)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	defer sse.Close()
	if a != nil && sse.Send(agentclient.EventActionResult, a.Result()) != nil {
		return
	}
	_ = sse.Send(agentclient.EventDone, map[string]string{"message_id": messageID})
}

// saveResult saves the action, the assistant message announcing it and marks the proposal executed in one
// transaction. It survives a client disconnect: the write already happened and must be visible after reload (R66).
func (h *Handler) saveResult(ctx context.Context, at attempt, res toolResult, messageID string) (*actions.Action, string, error) {
	var out *actions.Action
	err := h.inProposalTx(ctx, at.p.id, func(ctx context.Context, tx pgx.Tx, sessionID string) (string, error) {
		a, err := h.actions.Save(ctx, tx, actions.SaveInput{Claims: at.c, ProposalID: at.p.id, Kind: at.p.kind, Tool: at.d.Tool,
			Args: at.d.Args, Result: res.Result, MessageID: messageID, ProposedAt: at.p.createdAt, Attempt: at.n,
			ChecklistItemID: res.ChecklistItemID, ApproverName: res.ApproverName})
		if err != nil {
			return "", err
		}
		if a.MessageID != "" {
			messageID = a.MessageID // a concurrent confirm saved it first
		}
		a.MessageID, out = messageID, &a
		if err := insertMessage(ctx, tx, messageID, sessionID, "assistant", a.Summary, nil); err != nil {
			return "", err
		}
		return `UPDATE core.proposals SET status = 'executed' WHERE id = $1`, nil
	})
	return out, messageID, err
}

// saveDecision records a lead's approval decision made in chat (approval_decision). The agent's tool_result was
// already checked against the confirmed draft (approval id and decision); core then reads the decision back from the
// approvals service itself (approvals.decide with the same idempotency key replays the stored result and writes
// nothing new) and applies it only to the requester's action whose request names this lead as approver (R71):
// action and plan task follow the decision, the requester is notified, the assistant message is saved and the
// proposal is marked executed with a note of the action and message, which a later confirm replays. A decision on
// a request made outside the chat has no action in core: then only the message is saved.
func (h *Handler) saveDecision(ctx context.Context, at attempt, _ toolResult, messageID string) (*actions.Action, string, error) {
	var args map[string]any
	if err := json.Unmarshal(at.d.Args, &args); err != nil {
		return nil, "", fmt.Errorf("decode decision args: %w", err)
	}
	vctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	raw, err := h.mcp.Execute(vctx, at.token, mcpclient.ToolDecideApproval, args, at.p.id)
	cancel()
	if err != nil {
		return nil, "", fmt.Errorf("read back approvals.decide: %w", err)
	}
	var decided mcpclient.Approval
	if err := mcpclient.Decode(raw, &decided); err != nil {
		return nil, "", err
	}
	if decided.ID != args["approval_id"] || decided.Status != args["decision"] || decided.RefID == "" || decided.RequesterID == "" {
		return nil, "", fmt.Errorf("approvals.decide answered %s/%s for %v/%v: %w", decided.ID, decided.Status,
			args["approval_id"], args["decision"], apierr.Internal())
	}
	var out *actions.Action
	err = h.inProposalTx(ctx, at.p.id, func(ctx context.Context, tx pgx.Tx, sessionID string) (string, error) {
		a, changed, err := h.actions.ApplyDecision(ctx, tx, decided.Kind, decided.RefID, decided.Status,
			actions.Scope{RequesterID: decided.RequesterID, ApproverID: at.c.Sub})
		switch {
		case errors.Is(err, actions.ErrNotFound):
		case err != nil:
			return "", err
		default:
			out = &a
		}
		text := decided.Summary
		actionID := ""
		if out != nil {
			text, actionID = a.Summary, a.ID
		}
		if changed && h.emitter != nil {
			if err := h.emitter.Emit(ctx, tx, analytics.Event{Name: "action.status_changed", UserID: at.c.Sub, Role: at.c.Role,
				TeamID: at.c.TeamID, Props: map[string]any{"kind": a.Kind, "action_id": a.ID, "status": a.Status}}); err != nil {
				return "", fmt.Errorf("emit action.status_changed: %w", err)
			}
		}
		if err := insertMessage(ctx, tx, messageID, sessionID, "assistant", text, nil); err != nil {
			return "", err
		}
		note, _ := json.Marshal(map[string]map[string]string{"_core": {"action_id": actionID, "message_id": messageID}})
		if _, err := tx.Exec(ctx, `UPDATE core.proposals SET draft = draft || $2::jsonb WHERE id = $1`, at.p.id, string(note)); err != nil {
			return "", fmt.Errorf("note decision: %w", err)
		}
		return `UPDATE core.proposals SET status = 'executed' WHERE id = $1`, nil
	})
	return out, messageID, err
}

// inProposalTx runs fn in a transaction holding the proposal row lock and then executes the statement fn returns
// (with the proposal id). It survives a client disconnect: the write already happened and must be visible after
// reload (R66).
func (h *Handler) inProposalTx(ctx context.Context, id string, fn func(ctx context.Context, tx pgx.Tx, sessionID string) (string, error)) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, sessionID string
	if err := tx.QueryRow(ctx, `SELECT status, session_id::text FROM core.proposals WHERE id = $1 FOR UPDATE`, id).Scan(&status, &sessionID); err != nil {
		return fmt.Errorf("lock proposal: %w", err)
	}
	if status == "cancelled" || status == "expired" {
		return fmt.Errorf("proposal %s is %s: %w", id, status, apierr.Conflict())
	}
	final, err := fn(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, final, id); err != nil {
		return fmt.Errorf("mark proposal executed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// fail marks the proposal failed (it can be confirmed again), records action.failed and emits the error event.
func (h *Handler) fail(ctx context.Context, c authn.Claims, p proposal, sse *httpx.SSEWriter, e streamError) {
	h.markFailed(ctx, p.id)
	h.emit(context.WithoutCancel(ctx), c, "action.failed", map[string]any{"kind": p.kind, "code": e.Code, "retryable": e.Retryable})
	_ = sse.Send(agentclient.EventError, e)
}

// markFailed moves a confirmed proposal to failed after an attempt reached the agent, even when the client has
// gone away.
func (h *Handler) markFailed(ctx context.Context, id string) {
	h.setStatus(ctx, id, "failed")
}

// release undoes the claim of an attempt that failed before the agent was called: the proposal gets back the
// status it had (pending stays cancellable and can expire; failed stays failed).
func (h *Handler) release(ctx context.Context, p proposal) {
	status := "failed"
	if p.status == "pending" {
		status = "pending"
	}
	h.setStatus(ctx, p.id, status)
}

func (h *Handler) setStatus(ctx context.Context, id, status string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := h.pool.Exec(ctx, `UPDATE core.proposals SET status = $2 WHERE id = $1 AND status = 'confirmed'`, id, status); err != nil {
		h.log.Warn("update proposal status failed", "proposal", id, "status", status, "err", err)
	}
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	c, _, ok := caller(w, r)
	if !ok {
		return
	}
	p, err := h.target(r, c)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	// Nothing is sent anywhere: the proposal is only marked cancelled. Once an attempt reached the agent the write
	// may have happened, so cancelling is refused and the user can confirm again to see the result (R66).
	tag, err := h.pool.Exec(r.Context(), `UPDATE core.proposals SET status = 'cancelled' WHERE id = $1 AND status IN ('pending', 'expired', 'cancelled')`, p.id)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("cancel proposal: %w", err))
		return
	}
	if tag.RowsAffected() != 1 {
		msg := "Заявка уже отправлена, отменить её в чате нельзя"
		if p.status != "executed" {
			msg = "Заявка уже могла быть отправлена, отменить её нельзя. Подтвердите ещё раз, чтобы увидеть результат"
		}
		apierr.WriteJSON(w, apierr.Conflict().WithMessage(msg))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// toolResult is what core takes from the agent's tool_result event.
type toolResult struct {
	Result          json.RawMessage // structured MCP result of draft.tool
	Followup        string          // optional text the agent wants shown after the result card
	ChecklistItemID string          // task_checklist_item_id: plan task to link (access only)
	ApproverName    string          // optional approver_display_name
}

// parseToolResult binds the agent's tool_result to the confirmed proposal (R71): proposal_id, kind and tool must be
// those of the proposal and the stored draft, the MCP result ("result") must describe the confirmed draft.args.
func parseToolResult(data json.RawMessage, p proposal, d draft) (toolResult, error) {
	obj, err := exactObject(data, "proposal_id", "kind", "tool", "result")
	if err != nil {
		return toolResult{}, fmt.Errorf("tool_result: %w", err)
	}
	for key, want := range map[string]string{"proposal_id": p.id, "kind": p.kind, "tool": d.Tool} {
		if got := str(obj, key); got != want {
			return toolResult{}, fmt.Errorf("tool_result %s %q, want %q", key, got, want)
		}
	}
	result, err := exactObject(obj["result"], "id", "status")
	if err != nil {
		return toolResult{}, fmt.Errorf("tool_result result: %w", err)
	}
	if err := checkResult(d, result); err != nil {
		return toolResult{}, err
	}
	tr := toolResult{Result: obj["result"], Followup: str(obj, "followup"), ApproverName: str(obj, "approver_display_name")}
	if d.Tool == mcpclient.ToolRequestAccess {
		tr.ChecklistItemID = str(obj, "task_checklist_item_id")
	}
	return tr, nil
}

// checkResult verifies that an MCP result is the result of the confirmed draft: the same dates or resource, the
// approval and decision that were signed, and a status a new request can have.
func checkResult(d draft, result map[string]json.RawMessage) error {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(d.Args, &args) // decodeDraft checked it is an object
	same := func(keys ...string) error {
		for _, k := range keys {
			if str(result, k) != str(args, k) {
				return fmt.Errorf("result %s %q differs from the confirmed %q", k, str(result, k), str(args, k))
			}
		}
		return nil
	}
	id, status := str(result, "id"), str(result, "status")
	switch d.Tool {
	case mcpclient.ToolCreateAbsence:
		if err := same("type", "start_date", "end_date"); err != nil {
			return err
		}
		if !strings.HasPrefix(id, "ABS-") || status != actions.StatusPendingApproval {
			return fmt.Errorf("new absence request %q has status %q", id, status)
		}
	case mcpclient.ToolRequestAccess:
		if err := same("resource_id"); err != nil {
			return err
		}
		if !strings.HasPrefix(id, "ACC-") || (status != actions.StatusPendingApproval && status != actions.StatusGranted) {
			return fmt.Errorf("new access request %q has status %q", id, status)
		}
	case mcpclient.ToolCreateTrip:
		if err := same("city", "start_date", "end_date"); err != nil {
			return err
		}
		if !strings.HasPrefix(id, "TRP-") || status != actions.StatusPendingApproval {
			return fmt.Errorf("new trip request %q has status %q", id, status)
		}
	case mcpclient.ToolSignConsent:
		if err := same("consent_id"); err != nil {
			return err
		}
		if !strings.HasPrefix(id, "CNS-") || status != actions.StatusSigned {
			return fmt.Errorf("new consent signature %q has status %q", id, status)
		}
	case mcpclient.ToolDecideApproval:
		if id == "" || id != str(args, "approval_id") || status != str(args, "decision") || (status != "approved" && status != "rejected") {
			return fmt.Errorf("decision %q/%q differs from the confirmed %q/%q", id, status, str(args, "approval_id"), str(args, "decision"))
		}
	default:
		return fmt.Errorf("tool %q cannot be confirmed in chat", d.Tool)
	}
	return nil
}
