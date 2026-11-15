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

// Жизненный цикл черновика (core.proposals.status):
//
//	pending   → сохранён, агенту не отправлялся: можно подтвердить, отменить, может истечь.
//	confirmed → идёт попытка подтверждения.
//	failed    → попытка дошла до агента и не завершилась. Запись могла произойти, поэтому черновик
//	            больше нельзя отменить и он не истекает: следующее подтверждение повторяет с тем же ключом идемпотентности и воспроизводит.
//	executed  → сохранён (core.actions); подтверждение воспроизводит сохранённый результат (R79).
//	cancelled, expired → финальные; достижимы только из pending.
//
// Сбой до вызова агента возвращает черновику прежний статус.

// proposal — строка core.proposals.
type proposal struct {
	id, kind, status, draftHash string
	draft                       json.RawMessage
	createdAt, expiresAt        time.Time
}

// loadProposal возвращает черновик вызывающего в сессии; некорректный id — not_found, отсутствующий или чужой —
// forbidden.
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

// target проверяет id сессии и черновика в запросе подтверждения или отмены и загружает черновик.
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

// attempt — одно подтверждение черновика.
type attempt struct {
	c     authn.Claims
	token string
	p     proposal
	d     draft
	n     int // 1 для первого подтверждения, 2 для повтора (аналитика)
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
		// Истекает только черновик, который ни разу не дошёл до агента; после попытки повтор должен оставаться
		// возможным (R66).
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
	// Повторное подтверждение, пока идёт предыдущее, разрешено: запись идемпотентна по proposal_id (R79).
	tag, err := h.pool.Exec(ctx, `UPDATE core.proposals SET status = 'confirmed' WHERE id = $1 AND status IN ('pending', 'failed', 'confirmed')`, p.id)
	if err != nil {
		apierr.WriteJSON(w, fmt.Errorf("claim proposal: %w", err))
		return
	}
	if tag.RowsAffected() != 1 {
		apierr.WriteJSON(w, errInFlight) // тем временем выполнен или отменён
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
	// Дальше агент мог уже вызвать инструмент записи: при сбоях черновик остаётся failed (можно повторить).
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
				break loop // запись сохранена; поздний сбой агента теряет только текст дополнения
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
			// Черновик можно подтверждать снова: повтор безопасен, потому что запись идемпотентна по proposal_id (R79).
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
		default: // status, token, sources, next_step и события, добавленные позже
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

// checkStored перепроверяет сохранённый черновик перед подписью: draft с точными ключами, инструмент разрешён для
// сохранённого вида (решения по согласованию — только руководителям) и draft_hash равен хешу draft.args.
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

// replay отвечает на подтверждение выполненного черновика сохранёнными action_result и done, никого не вызывая (R79).
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

// saveResult в одной транзакции сохраняет действие, объявляющее его сообщение ассистента и помечает черновик
// выполненным. Переживает отключение клиента: запись уже произошла и должна быть видна после перезагрузки (R66).
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
			messageID = a.MessageID // параллельное подтверждение сохранило его раньше
		}
		a.MessageID, out = messageID, &a
		if err := insertMessage(ctx, tx, messageID, sessionID, "assistant", a.Summary, nil, nil); err != nil {
			return "", err
		}
		return `UPDATE core.proposals SET status = 'executed' WHERE id = $1`, nil
	})
	return out, messageID, err
}

// saveDecision записывает решение руководителя по согласованию, принятое в чате (approval_decision). tool_result агента
// уже сверен с подтверждённым черновиком (id согласования и решение); затем ядро само перечитывает решение у сервиса
// согласований (approvals.decide с тем же ключом идемпотентности воспроизводит сохранённый результат и ничего нового не
// пишет) и применяет его только к действию заявителя, в заявке которого этот руководитель указан согласующим (R71):
// действие и задача плана следуют решению, заявитель уведомляется, сообщение ассистента сохраняется, а черновик
// помечается выполненным с отметкой о действии и сообщении, которую воспроизводит следующее подтверждение. У решения по
// заявке, созданной вне чата, действия в ядре нет: тогда сохраняется только сообщение.
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
			actions.Scope{RequesterID: decided.RequesterID, ApproverID: at.c.Sub, Stage: decided.Stage, Comment: decided.Comment})
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
		if err := insertMessage(ctx, tx, messageID, sessionID, "assistant", text, nil, nil); err != nil {
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

// inProposalTx выполняет fn в транзакции, удерживающей блокировку строки черновика, а затем выполняет запрос, который
// вернула fn (с id черновика). Переживает отключение клиента: запись уже произошла и должна быть видна после
// перезагрузки (R66).
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

// fail помечает черновик failed (его можно подтвердить снова), записывает action.failed и отправляет событие error.
func (h *Handler) fail(ctx context.Context, c authn.Claims, p proposal, sse *httpx.SSEWriter, e streamError) {
	h.markFailed(ctx, p.id)
	h.emit(context.WithoutCancel(ctx), c, "action.failed", map[string]any{"kind": p.kind, "code": e.Code, "retryable": e.Retryable})
	_ = sse.Send(agentclient.EventError, e)
}

// markFailed переводит подтверждаемый черновик в failed после того, как попытка дошла до агента, даже если клиент уже
// ушёл.
func (h *Handler) markFailed(ctx context.Context, id string) {
	h.setStatus(ctx, id, "failed")
}

// release отменяет захват попытки, которая сорвалась до вызова агента: черновик получает прежний статус (pending
// остаётся отменяемым и может истечь; failed остаётся failed).
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
	// Никуда ничего не отправляется: черновик только помечается отменённым. Если попытка уже дошла до агента, запись
	// могла произойти, поэтому отмена отклоняется, и пользователь может подтвердить снова, чтобы увидеть результат
	// (R66).
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

// toolResult — то, что ядро берёт из события tool_result агента.
type toolResult struct {
	Result          json.RawMessage // структурированный результат MCP для draft.tool
	Followup        string          // необязательный текст, который агент хочет показать после карточки результата
	ChecklistItemID string          // task_checklist_item_id: задача плана для связи (только доступ)
	ApproverName    string          // необязательный approver_display_name
}

// parseToolResult привязывает tool_result агента к подтверждённому черновику (R71): proposal_id, kind и tool должны
// совпадать с черновиком и сохранённым draft, а результат MCP ("result") должен описывать подтверждённые draft.args.
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

func orRead(level string) string {
	if level == "" {
		return "read"
	}
	return level
}

// checkResult проверяет, что результат MCP — результат подтверждённого черновика: те же даты или ресурс, подписанные
// согласование и решение и статус, который может быть у новой заявки.
func checkResult(d draft, result map[string]json.RawMessage) error {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(d.Args, &args) // decodeDraft проверил, что это объект
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
		// подписанные окружение и уровень (чекпоинт 11.11); черновик без них означает чтение без окружения
		if str(result, "environment") != str(args, "environment") || orRead(str(result, "level")) != orRead(str(args, "level")) {
			return fmt.Errorf("access scope %q/%q differs from the confirmed %q/%q", str(result, "environment"), str(result, "level"),
				str(args, "environment"), str(args, "level"))
		}
		if !strings.HasPrefix(id, "ACC-") || (status != actions.StatusPendingApproval && status != actions.StatusGranted) {
			return fmt.Errorf("new access request %q has status %q", id, status)
		}
	case mcpclient.ToolReschedule:
		if err := same("start_date", "end_date"); err != nil {
			return err
		}
		if id != str(args, "id") || !strings.HasPrefix(id, "ABS-") || status != actions.StatusPendingApproval {
			return fmt.Errorf("moved request %q/%q differs from the confirmed %q", id, status, str(args, "id"))
		}
	case mcpclient.ToolCancelAbsence:
		if id != str(args, "id") || !strings.HasPrefix(id, "ABS-") || status != "cancelled" {
			return fmt.Errorf("cancelled request %q/%q differs from the confirmed %q", id, status, str(args, "id"))
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
