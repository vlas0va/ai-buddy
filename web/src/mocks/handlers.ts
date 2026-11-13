import { http, HttpResponse } from 'msw'
import type { User } from '@/api/schemas'
import { MOCK_FLAGS, MOCK_PASSWORD, MOCK_USERS, saveState, state, userByToken, uuid, type MockEscalation } from './db'
import { decideApproval, listPendingApprovals } from './approvals'
import { findTask, planFor } from './plan'
import { recordConfirm, recordTurn, setStoredProposalState } from './history'
import { confirmEvents, messageEvents, type MockAttachment } from './scenario'
import { sseResponse } from './sse'

// Paths and shapes mirror contracts/openapi.yaml; `*` keeps them independent of VITE_API_BASE_URL.

const unauthorized = () =>
  HttpResponse.json({ code: 'unauthorized', message: 'Войдите в систему.', retryable: false }, { status: 401 })
const forbidden = () =>
  HttpResponse.json({ code: 'forbidden', message: 'Недостаточно прав для этого действия.', retryable: false }, { status: 403 })
const notFound = () => HttpResponse.json({ code: 'not_found', message: 'Не найдено.', retryable: false }, { status: 404 })

/** mentor and lead see interns of their own team only (R71) */
const teamOf = (viewer: User, other: User) => viewer.team_id === other.team_id

/** the wire shape has no sender id (openapi Escalation) */
function publicEscalation(e: MockEscalation) {
  const out: Partial<MockEscalation> = { ...e }
  delete out.from_id
  return out
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
    if (!user) return unauthorized()
    const target = new URL(request.url).searchParams.get('user_id')
    if (!target || target === user.id) return HttpResponse.json(planFor(user))
    const intern = MOCK_USERS.find((u) => u.id === target)
    if (!intern || intern.role !== 'intern' || !teamOf(user, intern)) return forbidden()
    return HttpResponse.json(planFor(intern))
  }),

  http.patch('*/api/plan/tasks/:taskId', async ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const body = (await request.json().catch(() => ({}))) as { status?: string }
    if (!['not_started', 'in_progress', 'done'].includes(body.status ?? '')) {
      return HttpResponse.json({ code: 'validation_error', message: 'Недопустимый статус.', retryable: false }, { status: 400 })
    }
    const task = findTask(user.id, String(params.taskId))
    if (!task) return notFound()
    state.taskStatus.set(`${user.id}:${task.id}`, body.status as string)
    // like core: a done parent completes its subtasks, the last done subtask completes the parent
    if (body.status === 'done') task.subtasks?.forEach((s) => state.taskStatus.set(`${user.id}:${s.id}`, 'done'))
    if (task.parent_task_id) {
      const parent = findTask(user.id, task.parent_task_id)
      if (parent?.subtasks?.every((s) => s.status === 'done')) state.taskStatus.set(`${user.id}:${parent.id}`, 'done')
      else if (parent?.status === 'done') state.taskStatus.set(`${user.id}:${parent.id}`, 'in_progress')
    }
    saveState()
    return HttpResponse.json({ ...task, status: body.status })
  }),

  http.post('*/api/plan/goals/propose', async ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'intern') return forbidden()
    const body = (await request.json().catch(() => ({}))) as { goals?: { title: string; criteria?: string }[] }
    const added = (body.goals ?? []).slice(0, 5).map((g) => ({ id: uuid(), created_by: user.id, title: g.title, status: 'proposed' as const, ...(g.criteria ? { criteria: g.criteria } : {}) }))
    state.goals.set(user.id, [...(state.goals.get(user.id) ?? []), ...added])
    saveState()
    return HttpResponse.json(added, { status: 201 })
  }),
  http.put('*/api/plan/goals', async ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'mentor' && user.role !== 'lead') return forbidden()
    const body = (await request.json().catch(() => ({}))) as {
      user_id?: string
      goals?: { title: string; criteria?: string; status?: 'proposed' | 'active' | 'achieved' | 'missed' }[]
    }
    const intern = MOCK_USERS.find((u) => u.id === body.user_id)
    if (!intern || !teamOf(user, intern)) return forbidden()
    const goals = (body.goals ?? [])
      .slice(0, 10)
      .map((g) => ({ id: uuid(), created_by: user.id, title: g.title, status: g.status ?? ('active' as const), ...(g.criteria ? { criteria: g.criteria } : {}) }))
    state.goals.set(intern.id, goals)
    saveState()
    return HttpResponse.json(goals)
  }),

  http.get('*/api/team/interns', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'mentor' && user.role !== 'lead') return forbidden()
    const interns = MOCK_USERS.filter((u) => u.role === 'intern' && teamOf(user, u))
    return HttpResponse.json(
      interns.map((i) => {
        const tasks = planFor(i).periods.flatMap((p) => p.tasks)
        return {
          user_id: i.id,
          display_name: i.display_name,
          current_period: 'week_2',
          done: tasks.filter((t) => t.status === 'done').length,
          total: tasks.length,
          overdue: tasks.filter((t) => t.status === 'overdue').length,
          pending_access_requests: 0,
        }
      }),
    )
  }),

  http.get('*/api/team/benefit', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'mentor' && user.role !== 'lead') return forbidden()
    const rows = MOCK_USERS.filter((u) => u.role === 'intern' && teamOf(user, u)).map((i, k) => {
      const asked = 6 + 3 * k
      const escalated = state.escalations.filter((e) => e.from_id === i.id).length
      const selfServed = Math.max(asked - escalated, 0)
      return { user_id: i.id, display_name: i.display_name, asked, escalated, self_served: selfServed, saved_minutes: selfServed * 10, useful: 2 + k, not_useful: k % 2 }
    })
    const sum = (f: (r: (typeof rows)[number]) => number) => rows.reduce((a, r) => a + f(r), 0)
    const asked = sum((r) => r.asked)
    const selfServed = sum((r) => r.self_served)
    return HttpResponse.json({
      interns: rows.length,
      minutes_per_question: 10,
      asked,
      escalated: sum((r) => r.escalated),
      answered: 0,
      added_to_kb: 0,
      self_served: selfServed,
      self_served_percent: asked ? Math.round((selfServed * 100) / asked) : 0,
      saved_minutes: selfServed * 10,
      useful: sum((r) => r.useful),
      not_useful: sum((r) => r.not_useful),
      rows,
    })
  }),

  http.get('*/api/escalations', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const mine = state.escalations.filter((e) =>
      user.role === 'intern' ? e.from_id === user.id : e.to_role === user.role && (user.role === 'hrbp' || teamOf(user, MOCK_USERS.find((u) => u.id === e.from_id)!)),
    )
    return HttpResponse.json(mine.map(publicEscalation))
  }),

  http.get('*/api/escalations/queue', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const open = state.escalations
      .filter((e) => e.status === 'open' && e.to_role === user.role && (user.role === 'hrbp' || teamOf(user, MOCK_USERS.find((u) => u.id === e.from_id)!)))
      .slice()
      .reverse() // oldest first
    const groups: { ids: string[]; question: string; count: number; askers: string[]; first_asked_at: string; last_asked_at: string }[] = []
    for (const e of open) {
      const g = groups.find((x) => x.question.toLowerCase() === e.text.toLowerCase())
      if (g) {
        g.ids.push(e.id)
        g.count++
        g.last_asked_at = e.created_at
        if (!g.askers.includes(e.from_display_name)) g.askers.push(e.from_display_name)
      } else groups.push({ ids: [e.id], question: e.text, count: 1, askers: [e.from_display_name], first_asked_at: e.created_at, last_asked_at: e.created_at })
    }
    return HttpResponse.json(groups.sort((a, b) => b.count - a.count))
  }),

  http.post('*/api/escalations/:id/answer', async ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const e = state.escalations.find((x) => x.id === params.id)
    if (!e) return notFound()
    if (e.to_role !== user.role) return forbidden()
    const body = (await request.json().catch(() => ({}))) as { text?: string; add_to_kb?: boolean; also_ids?: string[] }
    if (!body.text?.trim()) return HttpResponse.json({ code: 'validation_error', message: 'Введите ответ.', retryable: false }, { status: 400 })
    const group = [e, ...state.escalations.filter((x) => body.also_ids?.includes(x.id) && x.status === 'open' && x.to_role === user.role)]
    for (const x of group) {
      x.status = 'answered'
      x.answer = body.text.trim()
      x.added_to_kb = Boolean(body.add_to_kb)
      const list = state.notifications.get(x.from_id) ?? []
      list.unshift({ id: uuid(), kind: 'escalation_answer', title: `Ответ от ${user.display_name}`, body: x.answer, read: false, created_at: new Date().toISOString() })
      state.notifications.set(x.from_id, list)
    }
    saveState()
    return HttpResponse.json(publicEscalation(e))
  }),

  http.get('*/api/notifications', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    return user ? HttpResponse.json(state.notifications.get(user.id) ?? []) : unauthorized()
  }),

  http.post('*/api/notifications/:id/read', ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    const n = (state.notifications.get(user.id) ?? []).find((x) => x.id === params.id)
    if (n) n.read = true
    saveState()
    return new HttpResponse(null, { status: 204 })
  }),

  http.post('*/api/feedback', ({ request }) => {
    if (!userByToken(request.headers.get('Authorization'))) return unauthorized()
    return new HttpResponse(null, { status: 204 })
  }),

  // feedback 12.11: a source chip opens the test document
  http.get('*/api/kb/:docId', ({ request, params }) => {
    if (!userByToken(request.headers.get('Authorization'))) return unauthorized()
    const id = String(params.docId)
    return HttpResponse.json({ doc_id: id, title: id, body: `# ${id}\n\nТестовая инструкция прототипа: текст документа.`, badge: 'Тестовая инструкция прототипа' })
  }),

  http.get('*/api/actions', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    return user ? HttpResponse.json(state.actions.get(user.id) ?? []) : unauthorized()
  }),

  http.get('*/api/approvals', ({ request }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'lead' && user.role !== 'hr_admin') return forbidden()
    return HttpResponse.json(listPendingApprovals(user))
  }),

  http.post('*/api/approvals/:approvalId/decision', async ({ request, params }) => {
    const user = userByToken(request.headers.get('Authorization'))
    if (!user) return unauthorized()
    if (user.role !== 'lead' && user.role !== 'hr_admin') return forbidden()
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
    const body = (await request.json()) as { text?: string; attachments?: MockAttachment[] }
    const sessionId = String(params.sessionId)
    if (!state.sessions.has(sessionId)) return notFound()
    const files = body.attachments ?? []
    const events = messageEvents(sessionId, user, body.text ?? '', files)
    recordTurn(sessionId, body.text ?? '', events, files)
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
    if (user.role !== 'intern') return forbidden()
    const body = (await request.json()) as { to_role: MockEscalation['to_role']; text: string }
    const e: MockEscalation = { id: uuid(), from_id: user.id, from_display_name: user.display_name, to_role: body.to_role, text: body.text, status: 'open', answer: null, added_to_kb: false, created_at: new Date().toISOString() }
    state.escalations.unshift(e)
    saveState()
    return HttpResponse.json(publicEscalation(e), { status: 201 })
  }),
]
