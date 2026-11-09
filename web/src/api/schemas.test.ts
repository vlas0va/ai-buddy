import { describe, expect, it } from 'vitest'
import {
  actionsSchema,
  approvalSchema,
  chatHistorySchema,
  chatSessionSchema,
  errorSchema,
  escalationSchema,
  healthSchema,
  internProgressSchema,
  loginResponseSchema,
  meResponseSchema,
  notificationSchema,
  planSchema,
} from './schemas'

const user = { id: 'u1', login: 'intern.anna', display_name: 'Анна', role: 'intern', team_id: 'delivery' }
const task = {
  id: 't1',
  source: 'checklist',
  checklist_item_id: 'CL-03',
  period: 'week_1',
  title: 'Познакомиться с командой',
  status: 'not_started',
  human_only: false,
  subtasks: [
    { id: 't2', source: 'action', checklist_item_id: null, period: 'week_1', title: 'Подзадача', status: 'done', human_only: true },
  ],
}

describe('R63 schemas accept contract payloads', () => {
  it('auth and system', () => {
    expect(loginResponseSchema.parse({ token: 'jwt', user }).user.role).toBe('intern')
    expect(meResponseSchema.parse({ user, flags: { scenario_s3: true } }).flags.scenario_s3).toBe(true)
    expect(healthSchema.parse({ status: 'ok', service: 'core' }).service).toBe('core')
    expect(errorSchema.parse({ code: 'forbidden', message: 'Нет доступа', retryable: false }).code).toBe('forbidden')
  })

  it('plan with nested subtasks and next step', () => {
    const plan = planSchema.parse({
      user_id: 'u1',
      plan_role: 'intern',
      current_period: 'week_1',
      periods: [{ id: 'week_1', label: '1 неделя', tasks: [task] }],
      goals: [{ id: 'g1', created_by: 'lead.maria', title: 'Цель' }],
      next_step: { task_id: 't1', title: 'Дальше', action: 'ask_buddy' },
    })
    expect(plan.periods[0]?.tasks[0]?.subtasks?.[0]?.human_only).toBe(true)
    expect(planSchema.parse({ ...plan, next_step: null }).next_step).toBeNull()
  })

  it('lists and records', () => {
    const now = '2026-11-09T10:00:00Z'
    expect(
      actionsSchema.parse([{ id: 'a1', kind: 'absence_request', status: 'pending_approval', summary: 'Отгул', created_at: now }]),
    ).toHaveLength(1)
    expect(approvalSchema.parse({ id: 'p1', kind: 'access_request', requester_display_name: 'Анна', summary: 's', status: 'pending', created_at: now }).id).toBe('p1')
    expect(escalationSchema.parse({ id: 'e1', to_role: 'mentor', text: 'Вопрос', status: 'open', created_at: now }).status).toBe('open')
    expect(notificationSchema.parse({ id: 'n1', kind: 'reminder', title: 'Напоминание', created_at: now }).kind).toBe('reminder')
    expect(internProgressSchema.parse({ user_id: 'u1', display_name: 'Анна', current_period: 'week_2', done: 1, total: 5, overdue: 0 }).total).toBe(5)
    expect(chatSessionSchema.parse({ session_id: 's1' }).session_id).toBe('s1')
    expect(
      chatHistorySchema.parse({
        session_id: 's1',
        messages: [{ id: 'm1', author: 'assistant', text: 'Привет', created_at: now, sources: [{ doc_id: 'd1', title: 'Doc', source_type: 'test' }] }],
      }).messages,
    ).toHaveLength(1)
  })
})

describe('R63 schemas reject invalid payloads', () => {
  it('rejects bad role, status and source type', () => {
    expect(meResponseSchema.safeParse({ user: { ...user, role: 'admin' }, flags: {} }).success).toBe(false)
    expect(planSchema.safeParse({ user_id: 'u1' }).success).toBe(false)
    expect(actionsSchema.safeParse([{ id: 'a1', kind: 'absence_request', status: 'unknown', summary: 's', created_at: 'x' }]).success).toBe(false)
    expect(
      chatHistorySchema.safeParse({
        session_id: 's',
        messages: [{ id: 'm', author: 'user', text: 't', created_at: 'x', sources: [{ doc_id: 'd', title: 't', source_type: 'prod' }] }],
      }).success,
    ).toBe(false)
  })
})
