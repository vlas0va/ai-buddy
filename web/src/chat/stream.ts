import type { z } from 'zod'
import type { sourceSchema } from '@/api/schemas'
import type {
  ActionResult,
  EscalationDraftData,
  Proposal,
  Refusal,
  Stage,
  StreamError,
  StreamEvent,
} from './events'

export type Source = z.infer<typeof sourceSchema>
export type ProposalState = 'pending' | 'confirming' | 'confirmed' | 'cancelled'

export interface ChatMessage {
  id: string
  author: 'user' | 'assistant'
  text: string
  sources: Source[]
  proposal: (Proposal & { state: ProposalState }) | null
  result: ActionResult | null
  escalation: EscalationDraftData | null
  refusal: Refusal | null
  error: StreamError | null
  /** user text that produced this assistant message, used by "Повторить" */
  retryText?: string
}

export interface ChatState {
  messages: ChatMessage[]
  streaming: boolean
  stage: Stage | null
}

export const initialState: ChatState = { messages: [], streaming: false, stage: null }

let counter = 0
const nextId = () => `m${++counter}`

function blank(author: ChatMessage['author'], text = ''): ChatMessage {
  return {
    id: nextId(),
    author,
    text,
    sources: [],
    proposal: null,
    result: null,
    escalation: null,
    refusal: null,
    error: null,
  }
}

/** Starts a turn: appends the user bubble (unless retrying) and an empty assistant message. */
export function startTurn(state: ChatState, text: string): ChatState {
  const assistant = { ...blank('assistant'), retryText: text }
  return {
    messages: [...state.messages, blank('user', text), assistant],
    streaming: true,
    stage: 'thinking',
  }
}

/** Retry: reuse the user bubble, drop the failed assistant message and start a fresh one. */
export function restartTurn(state: ChatState, assistantId: string): ChatState {
  const i = state.messages.findIndex((m) => m.id === assistantId)
  if (i === -1) return state
  const retryText = state.messages[i]?.retryText
  const assistant = { ...blank('assistant'), retryText }
  return {
    messages: [...state.messages.slice(0, i), assistant, ...state.messages.slice(i + 1)],
    streaming: true,
    stage: 'thinking',
  }
}

/** Confirm turn streams into a fresh assistant message so the proposal card stays above it. */
export function startConfirm(state: ChatState, proposalId: string): ChatState {
  return {
    messages: [...updateProposal(state.messages, proposalId, 'confirming'), blank('assistant')],
    streaming: true,
    stage: 'calling_service',
  }
}

export function updateProposal(messages: ChatMessage[], proposalId: string, next: ProposalState): ChatMessage[] {
  return messages.map((m) => (m.proposal?.proposal_id === proposalId ? { ...m, proposal: { ...m.proposal, state: next } } : m))
}

export function setProposalState(state: ChatState, proposalId: string, next: ProposalState): ChatState {
  return { ...state, messages: updateProposal(state.messages, proposalId, next) }
}

function patchLast(state: ChatState, patch: (m: ChatMessage) => ChatMessage): ChatState {
  const i = state.messages.length - 1
  const last = state.messages[i]
  if (!last || last.author !== 'assistant') return state
  const messages = state.messages.slice()
  messages[i] = patch(last)
  return { ...state, messages }
}

export const handlers = {
  status: (s: ChatState, d: { stage: Stage }): ChatState => ({ ...s, stage: d.stage }),
  token: (s: ChatState, d: { text: string }): ChatState => patchLast(s, (m) => ({ ...m, text: m.text + d.text })),
  sources: (s: ChatState, d: { items: Source[] }): ChatState =>
    patchLast(s, (m) => ({ ...m, sources: [...m.sources, ...d.items] })),
  action_proposal: (s: ChatState, d: Proposal): ChatState =>
    patchLast(s, (m) => ({ ...m, proposal: { ...d, state: 'pending' } })),
  action_result: (s: ChatState, d: ActionResult): ChatState => {
    const next = patchLast(s, (m) => ({ ...m, result: d }))
    return { ...next, messages: next.messages.map((m) => (m.proposal?.state === 'confirming' ? { ...m, proposal: { ...m.proposal, state: 'confirmed' as const } } : m)) }
  },
  escalation_draft: (s: ChatState, d: EscalationDraftData): ChatState => patchLast(s, (m) => ({ ...m, escalation: d })),
  refusal: (s: ChatState, d: Refusal): ChatState => patchLast(s, (m) => ({ ...m, refusal: d })),
  error: (s: ChatState, d: StreamError): ChatState => {
    const next = patchLast(s, (m) => ({ ...m, error: d }))
    return {
      messages: next.messages.map((m) => (m.proposal?.state === 'confirming' ? { ...m, proposal: { ...m.proposal, state: 'pending' as const } } : m)),
      streaming: false,
      stage: null,
    }
  },
  done: (s: ChatState): ChatState => ({ ...s, streaming: false, stage: null }),
}

export function reduce(state: ChatState, event: StreamEvent): ChatState {
  switch (event.type) {
    case 'status':
      return handlers.status(state, event.data)
    case 'token':
      return handlers.token(state, event.data)
    case 'sources':
      return handlers.sources(state, event.data)
    case 'action_proposal':
      return handlers.action_proposal(state, event.data)
    case 'action_result':
      return handlers.action_result(state, event.data)
    case 'escalation_draft':
      return handlers.escalation_draft(state, event.data)
    case 'refusal':
      return handlers.refusal(state, event.data)
    case 'error':
      return handlers.error(state, event.data)
    case 'done':
      return handlers.done(state)
  }
}
