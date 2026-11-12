import type { z } from 'zod'
import type { ChatHistory } from '@/api/chat'
import type { AttachmentMeta, sourceSchema } from '@/api/schemas'
import type {
  ActionResult,
  EscalationDraftData,
  NextStep,
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
  /** files attached to a user message (metadata only) */
  attachments?: AttachmentMeta[]
  proposal: (Proposal & { state: ProposalState }) | null
  /** several drafts of one message (the missing accesses with checkboxes); then `proposal` is null */
  bundle?: BundleItem[]
  result: ActionResult | null
  escalation: EscalationDraftData | null
  /** the draft was already sent (restored from the history) */
  escalationSent?: boolean
  refusal: Refusal | null
  error: StreamError | null
  nextStep?: NextStep | null
  /** server id of a saved assistant message (done.message_id or history), used for «Полезно / Не полезно» */
  serverId?: string
  /** user text that produced this assistant message, used by "Повторить" */
  retryText?: string
  /** attachments of that user message, sent again by "Повторить" */
  retryAttachments?: unknown[]
}

export type BundleItem = Proposal & { state: ProposalState; result?: ActionResult; errorText?: string }

export interface ChatState {
  messages: ChatMessage[]
  streaming: boolean
  stage: Stage | null
  /** a draft of a list is being sent: its result goes into the list, no new chat message (feedback 12.11) */
  quiet?: boolean
}

const inBundle = (messages: ChatMessage[], proposalId: string) =>
  messages.some((m) => m.bundle?.some((p) => p.proposal_id === proposalId))

function patchBundle(messages: ChatMessage[], pick: (p: BundleItem) => boolean, patch: (p: BundleItem) => BundleItem): ChatMessage[] {
  return messages.map((m) => (m.bundle?.some(pick) ? { ...m, bundle: m.bundle.map((p) => (pick(p) ? patch(p) : p)) } : m))
}

export const initialState: ChatState = { messages: [], streaming: false, stage: null }

/** Rebuilds the message list from GET /api/chat/sessions/{id}, cards included. */
export function fromHistory(history: ChatHistory): ChatState {
  const messages = history.messages.map((m): ChatMessage => {
    const proposalState: ProposalState = m.proposal_state === 'confirmed' ? 'confirmed' : m.proposal_state === 'cancelled' ? 'cancelled' : 'pending'
    return {
      id: m.id,
      author: m.author,
      text: m.text,
      sources: m.sources ?? [],
      attachments: m.attachments ?? [],
      proposal: m.proposal ? { ...m.proposal, state: proposalState } : null,
      bundle: m.proposals?.length ? m.proposals.map((p) => ({ ...p.proposal, state: p.state })) : undefined,
      result: m.result ?? null,
      escalation: m.escalation ?? null,
      escalationSent: m.escalation_sent ?? false,
      refusal: m.refusal ?? null,
      nextStep: m.next_step ?? null,
      error: null,
      serverId: m.author === 'assistant' ? m.id : undefined,
    }
  })
  // results of a list's drafts go back into the list instead of separate messages (the live chat shows them so too)
  const listed = new Map<string, number>()
  messages.forEach((m, i) => m.bundle?.forEach((p) => listed.set(p.proposal_id, i)))
  const kept: ChatMessage[] = []
  for (const m of messages) {
    const res = m.result
    const owner = res?.proposal_id !== undefined ? listed.get(res.proposal_id) : undefined
    const list = owner === undefined ? undefined : messages[owner]
    if (!res || !list?.bundle) {
      kept.push(m)
      continue
    }
    list.bundle = list.bundle.map((p) => (p.proposal_id === res.proposal_id ? { ...p, state: 'confirmed', result: res } : p))
  }
  return { messages: kept, streaming: false, stage: null }
}

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
export function startTurn(state: ChatState, text: string, attachments: (AttachmentMeta & { text?: string })[] = []): ChatState {
  const assistant = { ...blank('assistant'), retryText: text, retryAttachments: attachments.length ? attachments : undefined }
  const meta = attachments.map(({ name, kind, mime, size }) => ({ name, kind, mime, size }))
  const user = { ...blank('user', text), attachments: meta }
  return {
    messages: [...state.messages, user, assistant],
    streaming: true,
    stage: 'thinking',
  }
}

/** Retry: reuse the user bubble, drop the failed assistant message and start a fresh one. */
export function restartTurn(state: ChatState, assistantId: string): ChatState {
  const i = state.messages.findIndex((m) => m.id === assistantId)
  if (i === -1) return state
  const retryText = state.messages[i]?.retryText
  const assistant = { ...blank('assistant'), retryText, retryAttachments: state.messages[i]?.retryAttachments }
  return {
    messages: [...state.messages.slice(0, i), assistant, ...state.messages.slice(i + 1)],
    streaming: true,
    stage: 'thinking',
  }
}

/** Confirm turn streams into a fresh assistant message so the proposal card stays above it. */
export function startConfirm(state: ChatState, proposalId: string): ChatState {
  if (inBundle(state.messages, proposalId)) {
    return { messages: updateProposal(state.messages, proposalId, 'confirming'), streaming: true, stage: 'calling_service', quiet: true }
  }
  return {
    messages: [...updateProposal(state.messages, proposalId, 'confirming'), blank('assistant')],
    streaming: true,
    stage: 'calling_service',
  }
}

export function updateProposal(messages: ChatMessage[], proposalId: string, next: ProposalState): ChatMessage[] {
  return messages.map((m) => {
    if (m.proposal?.proposal_id === proposalId) return { ...m, proposal: { ...m.proposal, state: next } }
    if (m.bundle?.some((p) => p.proposal_id === proposalId)) {
      return { ...m, bundle: m.bundle.map((p) => (p.proposal_id === proposalId ? { ...p, state: next } : p)) }
    }
    return m
  })
}

/** Moves every draft in `from` state to `to` (single cards and bundles). */
function moveStates(messages: ChatMessage[], from: ProposalState, to: ProposalState): ChatMessage[] {
  return messages.map((m) => ({
    ...m,
    proposal: m.proposal?.state === from ? { ...m.proposal, state: to } : m.proposal,
    bundle: m.bundle?.map((p) => (p.state === from ? { ...p, state: to } : p)),
  }))
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
  token: (s: ChatState, d: { text: string }): ChatState => (s.quiet ? s : patchLast(s, (m) => ({ ...m, text: m.text + d.text }))),
  sources: (s: ChatState, d: { items: Source[] }): ChatState =>
    s.quiet ? s : patchLast(s, (m) => ({ ...m, sources: [...m.sources, ...d.items] })),
  action_proposal: (s: ChatState, d: Proposal): ChatState =>
    patchLast(s, (m) => {
      const item = { ...d, state: 'pending' as const }
      // a second access draft in the same answer turns the cards into one list with checkboxes (feedback 12.11)
      if (d.kind === 'access_request' && (m.bundle || m.proposal?.kind === 'access_request')) {
        return { ...m, proposal: null, bundle: [...(m.bundle ?? (m.proposal ? [m.proposal] : [])), item] }
      }
      return { ...m, proposal: item }
    }),
  action_result: (s: ChatState, d: ActionResult): ChatState => {
    if (s.quiet) {
      const pick = (p: BundleItem) => (d.proposal_id ? p.proposal_id === d.proposal_id : p.state === 'confirming')
      return { ...s, messages: patchBundle(s.messages, pick, (p) => ({ ...p, state: 'confirmed', result: d, errorText: undefined })) }
    }
    const next = patchLast(s, (m) => ({ ...m, result: d }))
    return { ...next, messages: moveStates(next.messages, 'confirming', 'confirmed') }
  },
  escalation_draft: (s: ChatState, d: EscalationDraftData): ChatState => (s.quiet ? s : patchLast(s, (m) => ({ ...m, escalation: d }))),
  refusal: (s: ChatState, d: Refusal): ChatState => (s.quiet ? s : patchLast(s, (m) => ({ ...m, refusal: d }))),
  error: (s: ChatState, d: StreamError): ChatState => {
    if (s.quiet) {
      const messages = patchBundle(s.messages, (p) => p.state === 'confirming', (p) => ({ ...p, state: 'pending', errorText: d.message }))
      return { messages, streaming: false, stage: null }
    }
    const next = patchLast(s, (m) => ({ ...m, error: d }))
    return {
      messages: moveStates(next.messages, 'confirming', 'pending'),
      streaming: false,
      stage: null,
    }
  },
  next_step: (s: ChatState, d: NextStep): ChatState => (s.quiet ? s : patchLast(s, (m) => ({ ...m, nextStep: d }))),
  done: (s: ChatState, d: { message_id?: string } = {}): ChatState => {
    if (s.quiet) return { messages: s.messages, streaming: false, stage: null }
    const next = d.message_id ? patchLast(s, (m) => ({ ...m, serverId: d.message_id })) : s
    return { ...next, streaming: false, stage: null }
  },
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
    case 'next_step':
      return handlers.next_step(state, event.data)
    case 'done':
      return handlers.done(state, event.data)
    case 'ignored':
      return state
  }
}
