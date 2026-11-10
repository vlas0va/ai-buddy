import { http, HttpResponse } from 'msw'
import { MOCK_FLAGS, MOCK_PASSWORD, MOCK_USERS, saveState, state, userByToken, uuid } from './db'
import { decideApproval, listPendingApprovals } from './approvals'
import { planFor } from './plan'
import { recordConfirm, recordTurn, setStoredProposalState } from './history'
import { confirmEvents, messageEvents } from './scenario'
import { sseResponse } from './sse'

// Paths and shapes mirror contracts/openapi.yaml; `*` keeps them independent of VITE_API_BASE_URL.

const unauthorized = () =>
  HttpResponse.json({ code: 'unauthorized', message: 'Войдите в систему.', retryable: false }, { status: 401 })
const forbidden = () =>
  HttpResponse.json({ code: 'forbidden', message: 'Недостаточно прав для этого действия.', retryable: false }, { status: 403 })
const notFound = () => HttpResponse.json({ code: 'not_found', message: 'Не найдено.', retryable: false }, { status: 404 })

export const handlers = [
  http.post('*/api/auth/login', async ({ request }) => {
    const body = (await request.json()) as { login?: string; password?: string }
    const user = MOCK_USERS.find((u) => u.login === body.login)
    if (!user || body.password !== MOCK_PASSWORD) return unauthorized()
    return HttpResponse.json({ token: `mock-token-${user.login}`, user })
  }),

  http.get('*/api/me', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    return user ? HttpResponse.json({ user, flags: MOCK_FLAGS }) : unauthorized()
  }),

  http.get('*/api/plan', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    return user ? HttpResponse.json(planFor(user)) : unauthorized()
  }),

  http.get('*/api/actions', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    return user ? HttpResponse.json(state.actions.get(user.id) ?? []) : unauthorized()
  }),

  http.get('*/api/approvals', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'lead') return forbidden()
    return HttpResponse.json(listPendingApprovals(user))
  }),

  http.post('*/api/approvals/:approvalId/decision', async ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'lead') return forbidden()
    const key = request.headers.get('Idempotency-Key')
    const body = (await request.json().catch(() => ({}))) as { decision?: string; comment?: string }
    if (!key || (body.decision !== 'approved' && body.decision !== 'rejected')) {
      return HttpResponse.json({ code: 'validation_error', message: 'Укажите решение.', retryable: false }, { status: 400 })
    }
    const out = decideApproval(user, String(params.approvalId), body.decision, key, body.comment)
    if (out === 'not_found') return notFound()
    if (out === 'forbidden') return forbidden()
    if (out === 'conflict') {
      return HttpResponse.json({ code: 'conflict', message: 'По этой заявке уже принято решение.', retryable: false }, { status: 409 })
    }
    saveState()
    return HttpResponse.json(out)
  }),

  http.post('*/api/chat/sessions', ({ request }) => {
    if (!userByToken(request.headers.get('Authorization'))) return unauthorized()
    const id = uuid()
    state.sessions.set(id, [])
    saveState()
    return HttpResponse.json({ session_id: id }, { status: 201 })
  }),

  http.get('*/api/chat/sessions/:sessionId', ({ params }) => {
    const id = String(params.sessionId)
    const messages = state.sessions.get(id)
    return messages ? HttpResponse.json({ session_id: id, messages }) : notFound()
  }),

  http.post('*/api/chat/sessions/:sessionId/messages', async ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const body = (await request.json()) as { text?: string }
    const sessionId = String(params.sessionId)
    if (!state.sessions.has(sessionId)) return notFound()
    const events = messageEvents(sessionId, user, body.text ?? '')
    recordTurn(sessionId, body.text ?? '', events)
    saveState()
    return sseResponse(events)
  }),

  http.post('*/api/chat/sessions/:sessionId/proposals/:proposalId/confirm', ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const proposal = state.proposals.get(String(params.proposalId))
    if (!proposal || proposal.userId !== user.id) return notFound()
    if (proposal.state === 'cancelled') {
      return HttpResponse.json({ code: 'conflict', message: 'Заявка уже отменена.', retryable: false }, { status: 409 })
    }
    const events = confirmEvents(proposal)
    recordConfirm(String(params.sessionId), proposal.id, events)
    saveState()
    return sseResponse(events)
  }),

  http.post('*/api/chat/sessions/:sessionId/proposals/:proposalId/cancel', ({ params }) => {
    const proposal = state.proposals.get(String(params.proposalId))
    if (!proposal) return notFound()
    if (proposal.state === 'pending') {
      proposal.state = 'cancelled'
      setStoredProposalState(String(params.sessionId), proposal.id, 'cancelled')
      saveState()
    }
    return new HttpResponse(null, { status: 204 })
  }),

  http.post('*/api/escalations', async ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const body = (await request.json()) as { to_role: string; text: string }
    return HttpResponse.json(
      { id: uuid(), from_display_name: user.display_name, to_role: body.to_role, text: body.text, status: 'open', answer: null, added_to_kb: false, created_at: new Date().toISOString() },
      { status: 201 },
    )
  }),
]
