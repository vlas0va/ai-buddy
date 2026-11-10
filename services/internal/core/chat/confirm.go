package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	case !h.Now().Before(p.expiresAt):
		if _, err := h.pool.Exec(ctx, `UPDATE core.proposals SET status = 'expired' WHERE id = $1 AND status IN ('pending', 'failed')`, p.id); err != nil {
			h.log.Warn("mark proposal expired failed", "proposal", p.id, "err", err)
		}
		apierr.WriteJSON(w, errExpired)
		return
	}
	attempt := 1
	if p.status != "pending" {
		attempt = 2
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
	var d draft
	hash := ""
	if json.Unmarshal(p.draft, &d) == nil {
		hash, _ = canonjson.SHA256Hex(d.Args)
	}
	if hash == "" || hash != p.draftHash {
		h.markFailed(ctx, p.id)
		apierr.WriteJSON(w, fmt.Errorf("proposal %s: stored draft does not match draft_hash: %w", p.id, apierr.Internal()))
		return
	}
	confirmation, err := confirm.Issue(h.cfg.ConfirmationSecret, c.Sub, p.id, p.draftHash, h.cfg.ConfirmationTTL)
	if err != nil {
		h.markFailed(ctx, p.id)
		apierr.WriteJSON(w, fmt.Errorf("issue confirmation token: %w", err))
		return
	}
	uc, flags, err := h.userContext(ctx, c)
	if err != nil {
		h.markFailed(ctx, p.id)
		apierr.WriteJSON(w, err)
		return
	}
	hist, err := h.history(ctx, sid, newUUID())
	if err != nil {
		h.markFailed(ctx, p.id)
		apierr.WriteJSON(w, err)
		return
	}
	sse, err := httpx.NewSSEWriter(ctx, w, h.cfg.KeepAlive)
	if err != nil {
		h.markFailed(ctx, p.id)
		apierr.WriteJSON(w, err)
		return
	}
	defer sse.Close()
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
		switch {
		case ev.Name == agentclient.EventToolResult:
			if saved {
				continue
			}
			res, err := parseToolResult(ev.Data, p.id)
			if err != nil {
				h.log.Warn("bad tool_result", "proposal", p.id, "err", err)
				h.fail(ctx, c, p, sse, streamError{Code: string(apierr.CodeUpstreamUnavailable), Message: msgNoResult, Retryable: true})
				return
			}
			save := h.saveResult
			if d.Tool == mcpclient.ToolDecideApproval {
				save = h.saveDecision
			}
			a, msgID, err := save(ctx, c, p, d, res, assistantID, attempt)
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
				st.text.WriteString(res.Followup)
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
	if stored.Tool == mcpclient.ToolDecideApproval {
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
func (h *Handler) saveResult(ctx context.Context, c authn.Claims, p proposal, d draft, res toolResult, messageID string, attempt int) (*actions.Action, string, error) {
	var out *actions.Action
	err := h.inProposalTx(ctx, p.id, func(ctx context.Context, tx pgx.Tx, sessionID string) (string, error) {
		a, err := h.actions.Save(ctx, tx, actions.SaveInput{Claims: c, ProposalID: p.id, Kind: p.kind, Tool: d.Tool, Args: d.Args,
			Result: res.Result, MessageID: messageID, ProposedAt: p.createdAt, Attempt: attempt,
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

// saveDecision records a lead's approval decision made in chat (approval_decision): the requester's action and plan
// task follow the decision and the requester is notified (actions.ApplyDecision), the assistant message is saved and
// the proposal is marked executed with a note of the action and message, which a later confirm replays. A decision
// on a request made outside the chat has no action in core: then only the message is saved.
func (h *Handler) saveDecision(ctx context.Context, c authn.Claims, p proposal, _ draft, res toolResult, messageID string, _ int) (*actions.Action, string, error) {
	var decided mcpclient.Approval
	if err := mcpclient.Decode(res.Result, &decided); err != nil || decided.RefID == "" {
		return nil, "", fmt.Errorf("approvals.decide result without ref_id: %w", apierr.Internal())
	}
	var out *actions.Action
	err := h.inProposalTx(ctx, p.id, func(ctx context.Context, tx pgx.Tx, sessionID string) (string, error) {
		a, changed, err := h.actions.ApplyDecision(ctx, tx, decided.Kind, decided.RefID, decided.Status)
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
			if err := h.emitter.Emit(ctx, tx, analytics.Event{Name: "action.status_changed", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
				Props: map[string]any{"kind": a.Kind, "action_id": a.ID, "status": a.Status}}); err != nil {
				return "", fmt.Errorf("emit action.status_changed: %w", err)
			}
		}
		if err := insertMessage(ctx, tx, messageID, sessionID, "assistant", text, nil); err != nil {
			return "", err
		}
		note, _ := json.Marshal(map[string]map[string]string{"_core": {"action_id": actionID, "message_id": messageID}})
		if _, err := tx.Exec(ctx, `UPDATE core.proposals SET draft = draft || $2::jsonb WHERE id = $1`, p.id, string(note)); err != nil {
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

// markFailed moves a confirmed proposal to failed, even when the client has gone away.
func (h *Handler) markFailed(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := h.pool.Exec(ctx, `UPDATE core.proposals SET status = 'failed' WHERE id = $1 AND status = 'confirmed'`, id); err != nil {
		h.log.Warn("mark proposal failed failed", "proposal", id, "err", err)
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
	// Nothing is sent anywhere: the proposal is only marked cancelled.
	tag, err := h.pool.Exec(r.Context(), `UPDATE core.proposals SET status = 'cancelled' WHERE id = $1 AND status IN ('pending', 'failed', 'expired', 'cancelled')`, p.id)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("cancel proposal: %w", err))
		return
	}
	if tag.RowsAffected() != 1 {
		apierr.WriteJSON(w, apierr.Conflict().WithMessage("Заявка уже отправлена, отменить её в чате нельзя"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// toolResult is what core needs from the agent's tool_result event.
type toolResult struct {
	Result          json.RawMessage // structured MCP result of draft.tool
	Followup        string          // optional text the agent wants shown after the result card
	ChecklistItemID string          // optional task_checklist_item_id: plan task to link (S4)
	ApproverName    string          // optional approver_display_name
}

// parseToolResult accepts the agent's tool_result in either shape: a wrapper {"proposal_id", "tool", "result":
// {...MCP result...}, "followup"?} (also "structuredContent"/"structured_content" instead of "result") or the bare
// MCP structured result (an object with "id"). A wrapper naming another proposal is rejected.
func parseToolResult(data json.RawMessage, proposalID string) (toolResult, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return toolResult{}, errors.New("tool_result is not a JSON object")
	}
	var pid string
	if raw, ok := obj["proposal_id"]; ok && json.Unmarshal(raw, &pid) == nil && pid != "" && pid != proposalID {
		return toolResult{}, fmt.Errorf("tool_result is for proposal %q", pid)
	}
	var tr toolResult
	for key, dst := range map[string]*string{"followup": &tr.Followup, "task_checklist_item_id": &tr.ChecklistItemID,
		"approver_display_name": &tr.ApproverName} {
		if raw, ok := obj[key]; ok {
			_ = json.Unmarshal(raw, dst) // null or a non-string leaves it empty
		}
	}
	for _, k := range []string{"result", "structuredContent", "structured_content"} {
		var inner map[string]json.RawMessage
		if raw, ok := obj[k]; ok && json.Unmarshal(raw, &inner) == nil && inner != nil {
			tr.Result = raw
			return tr, nil
		}
	}
	if _, ok := obj["id"]; ok {
		return toolResult{Result: data}, nil
	}
	return toolResult{}, errors.New("tool_result carries no MCP result")
}
