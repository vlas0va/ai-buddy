import { http, HttpResponse } from 'msw'
import type { Plan, User } from '@/api/schemas'
import { MOCK_FLAGS, MOCK_PASSWORD, MOCK_USERS, state, userByToken, uuid } from './db'
import { confirmEvents, messageEvents } from './scenario'
import { sseResponse } from './sse'

// Paths and shapes mirror contracts/openapi.yaml; `*` keeps them independent of VITE_API_BASE_URL.

const unauthorized = () =>
  HttpResponse.json({ code: 'unauthorized', message: 'Войдите в систему.', retryable: false }, { status: 401 })
const notFound = () => HttpResponse.json({ code: 'not_found', message: 'Не найдено.', retryable: false }, { status: 404 })

function plan(user: User): Plan {
  return {
    user_id: user.id,
    plan_role: user.role === 'mentor' ? 'mentor' : 'intern',
    current_period: 'week_2',
    periods: [
      {
        id: 'week_1',
        label: '1 неделя',
        tasks: [
          { id: '00000000-0000-4000-8000-000000000001', source: 'checklist', checklist_item_id: 'CL-03', period: 'week_1', title: 'Познакомиться с командой', status: 'done', human_only: false },
          { id: '00000000-0000-4000-8000-000000000002', source: 'checklist', checklist_item_id: 'CL-04', period: 'week_1', title: 'Получить доступы', status: 'in_progress', human_only: false },
        ],
      },
      {
        id: 'week_2',
        label: '2 неделя',
        tasks: [
          { id: '00000000-0000-4000-8000-000000000003', source: 'checklist', checklist_item_id: 'CL-07', period: 'week_2', title: 'Встреча с наставником', status: 'not_started', human_only: true },
          ...(state.extraTasks.get(user.id) ?? []),
        ],
      },
    ],
    goals: [],
    next_step: null,
  }
}

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
    return user ? HttpResponse.json(plan(user)) : unauthorized()
  }),

  http.get('*/api/actions', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    return user ? HttpResponse.json(state.actions.get(user.id) ?? []) : unauthorized()
  }),

  http.post('*/api/chat/sessions', ({ request }) => {
    if (!userByToken(request.headers.get('Authorization'))) return unauthorized()
    const id = uuid()
    state.sessions.add(id)
    return HttpResponse.json({ session_id: id }, { status: 201 })
  }),

  http.get('*/api/chat/sessions/:sessionId', ({ params }) => {
    const id = String(params.sessionId)
    return state.sessions.has(id) ? HttpResponse.json({ session_id: id, messages: [] }) : notFound()
  }),

  http.post('*/api/chat/sessions/:sessionId/messages', async ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const body = (await request.json()) as { text?: string }
    return sseResponse(messageEvents(String(params.sessionId), user, body.text ?? ''))
  }),

  http.post('*/api/chat/sessions/:sessionId/proposals/:proposalId/confirm', ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const proposal = state.proposals.get(String(params.proposalId))
    if (!proposal || proposal.userId !== user.id) return notFound()
    if (proposal.state === 'cancelled') {
      return HttpResponse.json({ code: 'conflict', message: 'Заявка уже отменена.', retryable: false }, { status: 409 })
    }
    return sseResponse(confirmEvents(proposal))
  }),

  http.post('*/api/chat/sessions/:sessionId/proposals/:proposalId/cancel', ({ params }) => {
    const proposal = state.proposals.get(String(params.proposalId))
    if (!proposal) return notFound()
    if (proposal.state === 'pending') proposal.state = 'cancelled'
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
