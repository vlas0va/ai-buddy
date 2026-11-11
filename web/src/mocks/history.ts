import { saveState, state, uuid, type StoredMessage } from './db'
import type { MockEvent } from './sse'

// Stores chat turns the way GET /api/chat/sessions/{id} returns them. proposal/result are optional extensions of ChatMessage.

function assistantFrom(events: MockEvent[]): StoredMessage {
  const msg: StoredMessage = { id: uuid(), author: 'assistant', text: '', sources: [], created_at: new Date().toISOString() }
  for (const ev of events) {
    const d = ev.data as Record<string, unknown>
    if (ev.event === 'token') msg.text += String(d.text)
    else if (ev.event === 'sources') msg.sources.push(...(d.items as unknown[]))
    else if (ev.event === 'action_proposal') {
      msg.proposal = d
      msg.proposal_state = 'pending'
    } else if (ev.event === 'action_result') msg.result = d
  }
  return msg
}

function push(sessionId: string, ...messages: StoredMessage[]): void {
  const list = state.sessions.get(sessionId)
  if (!list) return
  list.push(...messages)
  saveState()
}

export function recordTurn(
  sessionId: string,
  userText: string,
  events: MockEvent[],
  files: { name: string; kind: string; size: number }[] = [],
): void {
  // a failed turn is not stored: "Повторить" sends the same text again
  if (events.some((e) => e.event === 'error')) return
  const attachments = files.map(({ name, kind, size }) => ({ name, kind, size })) // metadata only, never the text
  const user: StoredMessage = { id: uuid(), author: 'user', text: userText, sources: [], attachments, created_at: new Date().toISOString() }
  push(sessionId, user, assistantFrom(events))
}

export function setStoredProposalState(sessionId: string, proposalId: string, next: 'confirmed' | 'cancelled'): void {
  const hit = state.sessions.get(sessionId)?.find((m) => (m.proposal as { proposal_id?: string } | undefined)?.proposal_id === proposalId)
  if (!hit) return
  hit.proposal_state = next
  saveState()
}

export function recordConfirm(sessionId: string, proposalId: string, events: MockEvent[]): void {
  setStoredProposalState(sessionId, proposalId, 'confirmed')
  const assistant = assistantFrom(events)
  const actionId = (assistant.result as { action_id?: string } | undefined)?.action_id
  // a replay of an executed proposal is already stored (R79)
  if (state.sessions.get(sessionId)?.some((m) => actionId && (m.result as { action_id?: string } | undefined)?.action_id === actionId)) return
  push(sessionId, assistant)
}
