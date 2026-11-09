import { describe, expect, it } from 'vitest'
import type { Proposal, StreamEvent } from './events'
import { initialState, reduce, restartTurn, setProposalState, startConfirm, startTurn, type ChatState } from './stream'

const proposal: Proposal = {
  proposal_id: 'p1',
  kind: 'absence_request',
  draft: { tool: 'absence.create_request', args: { type: 'day_off', start_date: '2026-11-13', end_date: '2026-11-13' }, display: {} },
}

const apply = (s: ChatState, ...events: StreamEvent[]) => events.reduce(reduce, s)
const started = () => startTurn(initialState, 'хочу отгул')
const last = (s: ChatState) => s.messages.at(-1)!

describe('chat reducer, one test per event of contracts/agent-stream.md', () => {
  it('startTurn adds the user bubble and an empty assistant message', () => {
    const s = started()
    expect(s.messages.map((m) => m.author)).toEqual(['user', 'assistant'])
    expect(s.streaming).toBe(true)
    expect(last(s).retryText).toBe('хочу отгул')
  })

  it('status sets the waiting stage', () => {
    expect(apply(started(), { type: 'status', data: { stage: 'searching_kb' } }).stage).toBe('searching_kb')
  })

  it('token appends to the current answer', () => {
    const s = apply(started(), { type: 'token', data: { text: 'При' } }, { type: 'token', data: { text: 'вет' } })
    expect(last(s).text).toBe('Привет')
  })

  it('sources are collected with test source_type (R69)', () => {
    const s = apply(started(), { type: 'sources', data: { items: [{ doc_id: 'd1', title: 'Отгул', source_type: 'test' }] } })
    expect(last(s).sources).toHaveLength(1)
    expect(last(s).sources[0]?.source_type).toBe('test')
  })

  it('action_proposal stores a pending proposal', () => {
    const s = apply(started(), { type: 'action_proposal', data: proposal })
    expect(last(s).proposal?.state).toBe('pending')
  })

  it('action_result attaches the result and marks the proposal confirmed (R66)', () => {
    let s = apply(started(), { type: 'action_proposal', data: proposal }, { type: 'done', data: {} })
    s = startConfirm(s, 'p1')
    expect(s.messages.find((m) => m.proposal)?.proposal?.state).toBe('confirming')
    s = apply(s, { type: 'action_result', data: { action_id: 'a1', kind: 'absence_request', external_id: 'ABS-1042', status: 'pending_approval' } })
    expect(last(s).result?.external_id).toBe('ABS-1042')
    expect(s.messages.find((m) => m.proposal)?.proposal?.state).toBe('confirmed')
  })

  it('escalation_draft is stored', () => {
    const s = apply(started(), { type: 'escalation_draft', data: { to_role: 'mentor', text: 'Здравствуйте' } })
    expect(last(s).escalation?.to_role).toBe('mentor')
  })

  it('refusal is stored (R71, R72)', () => {
    const s = apply(started(), { type: 'refusal', data: { reason: 'out_of_scope', redirect_role: 'hrbp' } })
    expect(last(s).refusal?.redirect_role).toBe('hrbp')
  })

  it('error stops streaming, keeps the message and returns a confirming proposal to pending (R79)', () => {
    let s = apply(started(), { type: 'action_proposal', data: proposal }, { type: 'done', data: {} })
    s = startConfirm(s, 'p1')
    s = apply(s, { type: 'error', data: { code: 'upstream_unavailable', message: 'Сбой', retryable: true } })
    expect(s.streaming).toBe(false)
    expect(last(s).error?.retryable).toBe(true)
    expect(s.messages.find((m) => m.proposal)?.proposal?.state).toBe('pending')
  })

  it('done unlocks the input', () => {
    const s = apply(started(), { type: 'done', data: { message_id: 'm' } })
    expect(s.streaming).toBe(false)
    expect(s.stage).toBeNull()
  })

  it('events are ignored when the last message is not an assistant one', () => {
    const s: ChatState = { ...initialState, messages: [{ ...started().messages[0]! }] }
    expect(reduce(s, { type: 'token', data: { text: 'x' } })).toBe(s)
  })

  it('restartTurn replaces only the failed assistant message and keeps the user bubble', () => {
    let s = apply(started(), { type: 'error', data: { code: 'x', message: 'm', retryable: true } })
    const failedId = last(s).id
    s = restartTurn(s, failedId)
    expect(s.messages).toHaveLength(2)
    expect(last(s).id).not.toBe(failedId)
    expect(last(s).retryText).toBe('хочу отгул')
    expect(s.streaming).toBe(true)
    expect(restartTurn(s, 'missing')).toBe(s)
  })

  it('setProposalState cancels a proposal', () => {
    const s = setProposalState(apply(started(), { type: 'action_proposal', data: proposal }), 'p1', 'cancelled')
    expect(last(s).proposal?.state).toBe('cancelled')
  })
})
