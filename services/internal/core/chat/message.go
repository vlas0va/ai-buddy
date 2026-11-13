package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/escalations"
	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/canonjson"
	"aibuddy/services/internal/platform/confirm"
	"aibuddy/services/internal/platform/httpx"
)

// kindApprovalDecision — вид черновика для решения руководителя в чате (approvals.decide).
const kindApprovalDecision = "approval_decision"

// allowedTools сопоставляет виду черновика единственный инструмент записи, который может назвать черновик чата этого
// вида (R71).
var allowedTools = map[string]string{
	"absence_request":    mcpclient.ToolCreateAbsence,
	"access_request":     mcpclient.ToolRequestAccess,
	"trip_request":       mcpclient.ToolCreateTrip,
	"consent":            mcpclient.ToolSignConsent,
	"absence_reschedule": mcpclient.ToolReschedule,
	"absence_cancel":     mcpclient.ToolCancelAbsence,
	kindApprovalDecision: mcpclient.ToolDecideApproval, // только руководитель
}

// internalEvents — события агента, которые никогда не уходят в веб: tool_result внутренний, а action_result отправляет
// только ядро (после сохранения). done агента заменяется на done ядра (те же данные, message_id ядра).
var internalEvents = map[string]bool{
	agentclient.EventToolResult: true, agentclient.EventActionResult: true,
}

// turnState собирает то, что ход сохраняет и о чём сообщает, пока пересылаются его события.
type turnState struct {
	text          strings.Builder
	sources       []json.RawMessage
	summary       string          // summary сохранённого черновика (запасной текст ассистента)
	refusal       string          // refusal.reason
	refusalText   string          // refusal.text (запасной текст ассистента)
	escalatedTo   string          // escalation_draft.to_role
	queued        string          // escalation_draft.question с queue=true: попадает в список адресата (13.11)
	done          json.RawMessage // done агента, пересылается с message_id ядра
	doneProvider  string
	doneLatencyMS *int64
	doneEscalated bool
	tooLarge      bool // текст или источники сверх ограничений на ход
	// карточки, показанные с сообщением и восстанавливаемые из истории: proposal, escalation, refusal, next_step
	cards     map[string]json.RawMessage
	proposals []json.RawMessage // все сохранённые черновики хода (у списка доступов их несколько)
}

// maxCard ограничивает одну сохраняемую карточку; большая пересылается, но не хранится.
const maxCard = 32 << 10

func (s *turnState) card(name string, data json.RawMessage) {
	if len(data) == 0 || len(data) > maxCard {
		return
	}
	if s.cards == nil {
		s.cards = map[string]json.RawMessage{}
	}
	s.cards[name] = data
}

// addText дописывает текст ассистента, если ход не превысит maxTurnText (иначе выставляется tooLarge).
func (s *turnState) addText(t string) {
	if s.text.Len()+len(t) > maxTurnText {
		s.tooLarge = true
		return
	}
	s.text.WriteString(t)
}

// collect запоминает части события, которые ядро сохраняет или о которых сообщает.
func (s *turnState) collect(ev agentclient.Event) {
	switch ev.Name {
	case agentclient.EventToken:
		var t struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Data, &t) == nil {
			s.addText(t.Text)
		}
	case agentclient.EventSources:
		var src struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(ev.Data, &src) == nil {
			if len(s.sources)+len(src.Items) > maxSources {
				s.tooLarge = true
			} else {
				s.sources = append(s.sources, src.Items...)
			}
		}
	case agentclient.EventRefusal:
		var r struct {
			Reason string `json:"reason"`
			Text   string `json:"text"`
		}
		if json.Unmarshal(ev.Data, &r) == nil {
			s.refusal, s.refusalText = r.Reason, r.Text
		}
		s.card("refusal", ev.Data)
	case agentclient.EventEscalationDraft:
		var d struct {
			ToRole   string `json:"to_role"`
			Queue    bool   `json:"queue"`
			Question string `json:"question"`
		}
		if json.Unmarshal(ev.Data, &d) == nil {
			s.escalatedTo = d.ToRole
			if d.Queue {
				s.queued = d.Question
			}
		}
		s.card("escalation", ev.Data)
	case agentclient.EventNextStep:
		s.card("next_step", ev.Data)
	case "date_picker": // отзыв 12.11: календарь под ответом про отсутствие возвращается вместе с историей
		s.card("date_picker", ev.Data)
	case agentclient.EventDone:
		s.done = ev.Data
		var d struct {
			Provider  string `json:"provider"`
			LatencyMS *int64 `json:"latency_ms"`
			Escalated bool   `json:"escalated"`
		}
		if json.Unmarshal(ev.Data, &d) == nil {
			s.doneProvider, s.doneLatencyMS, s.doneEscalated = d.Provider, d.LatencyMS, d.Escalated
		}
	}
}

// doneEvent — done агента (если был) с message_id = messageID; больше ядро ничего не добавляет.
func (s *turnState) doneEvent(messageID string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(s.done, &m) != nil || m == nil {
		m = map[string]json.RawMessage{}
	}
	id, _ := json.Marshal(messageID)
	m["message_id"] = id
	out, _ := json.Marshal(m) // значения — корректный JSON: ошибки быть не может
	return out
}

func (h *Handler) postMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, token, ok := caller(w, r)
	if !ok {
		return
	}
	sid := r.PathValue("session_id")
	if err := h.ownSession(ctx, c.Sub, sid); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	var body struct {
		Text        string                   `json:"text"`
		Attachments []agentclient.Attachment `json:"attachments"`
	}
	if err := httpx.ReadJSONLimit(w, r, &body, maxMessageBodyBytes); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	atts, err := validateAttachments(body.Attachments)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	text := strings.TrimSpace(body.Text)
	if (text == "" && len(atts) == 0) || utf8.RuneCountInString(text) > maxTextRunes {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Сообщение: от 1 до 4000 символов"))
		return
	}
	if h.limiter != nil && h.cfg.RatePerMin > 0 {
		allowed, err := h.limiter.Allow(ctx, "chat:"+c.Sub, h.cfg.RatePerMin, time.Minute)
		if err != nil {
			h.log.Warn("rate limiter unavailable, allowing", "err", err)
		}
		if !allowed {
			apierr.WriteJSON(w, apierr.RateLimited().WithMessage("Слишком много сообщений, подождите минуту"))
			return
		}
	}
	userMsgID := newUUID()
	if err := insertMessage(ctx, h.pool, userMsgID, sid, "user", text, nil, nil); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if err := saveAttachments(ctx, h.pool, userMsgID, atts); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	h.emit(ctx, c, "chat.message_sent", map[string]any{"session_id": sid, "attachments": len(atts)})
	uc, flags, err := h.userContext(ctx, c)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	hist, err := h.history(ctx, sid, userMsgID)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	sse, err := httpx.NewSSEWriter(ctx, w, h.cfg.KeepAlive)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	defer sse.Close()
	started := h.Now()
	assistantID := newUUID()
	stream, err := h.agent.Turn(ctx, token, agentclient.TurnRequest{SessionID: sid, MessageID: assistantID,
		Turn: agentclient.Turn{Kind: agentclient.KindMessage, Text: text, Attachments: atts}, UserContext: uc, History: hist, Flags: flags})
	if err != nil {
		h.log.Warn("agent turn failed", "session", sid, "err", err)
		_ = sse.Send(agentclient.EventError, agentError(err))
		return
	}
	defer stream.Close()

	var st turnState
loop:
	for {
		ev, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.log.Warn("agent stream failed", "session", sid, "err", err)
			h.saveAssistant(ctx, assistantID, sid, &st)
			_ = sse.Send(agentclient.EventError, agentError(err))
			return
		}
		st.collect(ev)
		if st.tooLarge {
			h.log.Warn("agent turn over the size bounds", "session", sid, "event", ev.Name)
			h.saveAssistant(ctx, assistantID, sid, &st)
			_ = sse.Send(agentclient.EventError, errTooLarge)
			return
		}
		switch {
		case internalEvents[ev.Name]:
			h.log.Warn("agent event dropped", "event", ev.Name, "session", sid)
		case ev.Name == agentclient.EventActionProposal:
			summary, err := h.storeProposal(ctx, c, sid, ev.Data)
			if err != nil {
				h.log.Warn("action proposal rejected", "session", sid, "err", err)
				h.saveAssistant(ctx, assistantID, sid, &st)
				e := apierr.From(err)
				_ = sse.Send(agentclient.EventError, streamError{Code: string(e.Code), Message: e.Message, Retryable: e.Retryable})
				return
			}
			st.summary = summary
			if len(ev.Data) <= maxCard && len(st.proposals) < 10 {
				st.proposals = append(st.proposals, ev.Data) // восстановить и подтвердить можно только черновик, сохранённый ядром
				if raw, err := json.Marshal(st.proposals); err == nil {
					st.card("proposals", raw)
				}
			}
			if sse.Send(ev.Name, ev.Data) != nil {
				return
			}
		case ev.Name == "quiz_passed":
			// организаторы 12.13: учебная задача подтверждается мини-проверкой; ядро отмечает выполненной собственную
			// задачу вызывающего (пользователь берётся из JWT, агент только называет пункт чек-листа) и просит веб
			// обновить план
			if err := h.quizPassed(ctx, c, ev.Data); err != nil {
				h.log.Warn("quiz_passed not applied", "session", sid, "err", err)
				continue
			}
			if sse.Send(ev.Name, ev.Data) != nil {
				return
			}
		case ev.Name == agentclient.EventError:
			h.saveAssistant(ctx, assistantID, sid, &st)
			_ = sse.Send(ev.Name, ev.Data)
			return
		case ev.Name == agentclient.EventDone:
			break loop
		default: // status, token, sources, escalation_draft, next_step, refusal и события, добавленные позже
			if sse.Send(ev.Name, ev.Data) != nil {
				return // клиент ушёл; ctx отменяет вызов агента
			}
		}
	}
	if !h.saveAssistant(ctx, assistantID, sid, &st) {
		_ = sse.Send(agentclient.EventError, streamError{Code: string(apierr.CodeInternal), Message: apierr.Internal().Message, Retryable: true})
		return
	}
	h.reportAnswer(ctx, c, sid, assistantID, started, &st)
	h.enqueue(ctx, c, sid, assistantID, &st)
	_ = sse.Send(agentclient.EventDone, st.doneEvent(assistantID))
}

// reportAnswer записывает chat.answered, chat.refused и chat.escalated по итогам хода (contracts/events.md).
func (h *Handler) reportAnswer(ctx context.Context, c authn.Claims, sid, messageID string, started time.Time, st *turnState) {
	latency := h.Now().Sub(started).Milliseconds()
	if st.doneLatencyMS != nil {
		latency = *st.doneLatencyMS
	}
	props := map[string]any{"session_id": sid, "message_id": messageID, "source_count": len(st.sources), "latency_ms": latency}
	if st.doneProvider != "" {
		props["provider"] = st.doneProvider
	}
	h.emit(ctx, c, "chat.answered", props)
	if st.refusal != "" {
		h.emit(ctx, c, "chat.refused", map[string]any{"reason": st.refusal})
	}
	if st.doneEscalated {
		esc := map[string]any{}
		if st.escalatedTo != "" {
			esc["to_role"] = st.escalatedTo
		}
		h.emit(ctx, c, "chat.escalated", esc)
	}
}

// saveAssistant сохраняет сообщение ассистента за ход (токены, иначе summary черновика или текст отказа), чтобы
// done.message_id всегда указывал на сохранённое сообщение, и сообщает об успехе.
func (h *Handler) saveAssistant(ctx context.Context, id, sessionID string, st *turnState) bool {
	text := st.text.String()
	for _, alt := range []string{st.summary, st.refusalText} {
		if text == "" {
			text = alt
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := insertMessage(ctx, h.pool, id, sessionID, "assistant", text, st.sources, st.cards); err != nil {
		h.log.Error("save assistant message failed", "session", sessionID, "err", err)
		return false
	}
	return true
}

// draft — часть черновика, которую проверяет ядро; черновик целиком сохраняется и возвращается без изменений.
type draft struct {
	Tool string
	Args json.RawMessage
}

// decodeDraft читает tool и args с точными ключами (без вариантов регистра tool, args или display); args должен быть
// объектом без idempotency_key и confirmation_token.
func decodeDraft(raw json.RawMessage) (draft, error) {
	m, err := exactObject(raw, "tool", "args", "display")
	if err != nil {
		return draft{}, fmt.Errorf("draft: %w", err)
	}
	d := draft{Tool: str(m, "tool"), Args: m["args"]}
	if d.Tool == "" {
		return draft{}, errors.New("draft without tool")
	}
	args, err := exactObject(d.Args, confirm.KeyIdempotency, confirm.KeyConfirmation)
	if err != nil {
		return draft{}, fmt.Errorf("draft args: %w", err)
	}
	for _, k := range []string{confirm.KeyIdempotency, confirm.KeyConfirmation} {
		if _, ok := args[k]; ok {
			return draft{}, fmt.Errorf("draft args carry %s", k)
		}
	}
	return d, nil
}

// storeProposal проверяет action_proposal и сохраняет его в core.proposals для вызывающего и сессии с draft_hash =
// канонический SHA-256 от draft.args и сроком ProposalTTL. Принимаются только разрешённые инструменты записи; решения
// по согласованию — только от руководителей.
func (h *Handler) storeProposal(ctx context.Context, c authn.Claims, sessionID string, data json.RawMessage) (string, error) {
	bad := apierr.Forbidden().WithMessage(msgBadProposal)
	ev, err := exactObject(data, "proposal_id", "kind", "draft", "summary")
	if err != nil {
		return "", fmt.Errorf("decode proposal: %w: %w", bad, err)
	}
	p := struct{ ProposalID, Kind, Summary string }{str(ev, "proposal_id"), str(ev, "kind"), str(ev, "summary")}
	rawDraft := ev["draft"]
	if len(rawDraft) > maxDraftBytes {
		return "", fmt.Errorf("draft of %d bytes: %w", len(rawDraft), apierr.UpstreamUnavailable().WithMessage(msgTooLarge))
	}
	d, err := decodeDraft(rawDraft)
	if err != nil {
		return "", fmt.Errorf("%w: %w", bad, err)
	}
	if !uuidRE.MatchString(p.ProposalID) {
		return "", fmt.Errorf("proposal id %q is not a uuid: %w", p.ProposalID, bad)
	}
	if tool, ok := allowedTools[p.Kind]; !ok || tool != d.Tool {
		return "", fmt.Errorf("tool %q of kind %q is not allowed: %w", d.Tool, p.Kind, bad)
	}
	if p.Kind == kindApprovalDecision && (c.Role != "lead" || h.mcp == nil) {
		return "", fmt.Errorf("approval decision proposed to role %q: %w", c.Role, bad)
	}
	hash, err := canonjson.SHA256Hex(d.Args)
	if err != nil {
		return "", fmt.Errorf("hash draft args: %w: %w", bad, err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, rawDraft); err != nil {
		return "", fmt.Errorf("compact draft: %w: %w", bad, err)
	}
	tag, err := h.pool.Exec(ctx, `INSERT INTO core.proposals (id, session_id, user_id, kind, draft, draft_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (id) DO NOTHING`,
		p.ProposalID, sessionID, c.Sub, p.Kind, compact.Bytes(), hash, h.Now().Add(h.cfg.ProposalTTL))
	if err != nil {
		return "", fmt.Errorf("insert proposal: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return "", fmt.Errorf("proposal %s already exists: %w", p.ProposalID, bad)
	}
	h.emit(ctx, c, "action.proposed", map[string]any{"kind": p.Kind, "proposal_id": p.ProposalID})
	return p.Summary, nil
}

// quizPassed отмечает выполненной задачу чек-листа вызывающего, подтверждаемую проверкой (вместе с подзадачами).
// Затрагиваются только задачи с подтверждением через проверку; остальное игнорируется.
func (h *Handler) quizPassed(ctx context.Context, c authn.Claims, data json.RawMessage) error {
	var in struct {
		ChecklistItemID string `json:"checklist_item_id"`
	}
	if err := json.Unmarshal(data, &in); err != nil || in.ChecklistItemID == "" {
		return fmt.Errorf("bad quiz_passed: %s", data)
	}
	if !quizItems[in.ChecklistItemID] {
		return fmt.Errorf("item %s is not confirmed by a quiz", in.ChecklistItemID)
	}
	_, err := h.pool.Exec(ctx, `UPDATE core.plan_tasks t SET status = 'done', updated_at = now()
		FROM core.plans p WHERE p.id = t.plan_id AND p.user_id = $1 AND p.plan_role = 'intern'
		  AND t.checklist_item_id = $2 AND t.source = 'checklist' AND t.status <> 'done'`, c.Sub, in.ChecklistItemID)
	return err
}

// quizItems повторяют plan.confirmModes («quiz»): учебные задачи без шагов.
var quizItems = map[string]bool{"CL-07": true, "CL-09": true}

// enqueue кладёт вопрос стажёра, на который buddy не ответил, в список адресата (организаторы 13.11). Пользователь
// берётся из JWT, сообщение — только что сохранённое; сбой логируется и никогда не ломает ход.
func (h *Handler) enqueue(ctx context.Context, c authn.Claims, sid, messageID string, st *turnState) {
	if st.queued == "" {
		return
	}
	added, err := escalations.Enqueue(ctx, h.pool, c, st.escalatedTo, st.queued, sid, messageID)
	if err != nil {
		h.log.Warn("question not queued", "session", sid, "err", err)
		return
	}
	if added && h.emitter != nil {
		if err := h.emitter.Emit(ctx, h.pool, analytics.Event{Name: "escalation.sent", UserID: c.Sub, Role: c.Role, TeamID: c.TeamID,
			Props: map[string]any{"to_role": st.escalatedTo}}); err != nil {
			h.log.Warn("escalation.sent event failed", "err", err)
		}
	}
}
