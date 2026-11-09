import type { Action, User } from '@/api/schemas'
import { MOCK_USERS, saveState, state, uuid, type MockProposal } from './db'
import { words, type MockEvent } from './sse'

// Scripted S3 conversation (docs/context/product.md, S3).

const SOURCES = [
  { doc_id: 'kb-absence-001', title: 'Порядок оформления отгула', source_type: 'test', url: '/kb/kb-absence-001' },
]

const lead = () => MOCK_USERS.find((u) => u.login === 'lead.maria') as User

function nextFriday(from = new Date()): Date {
  const d = new Date(from)
  d.setDate(d.getDate() + ((5 - d.getDay() + 7) % 7 || 7))
  return d
}

const iso = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const ru = (d: Date) => `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')}.${d.getFullYear()}`

export function messageEvents(sessionId: string, user: User, text: string): MockEvent[] {
  const lower = text.toLowerCase()

  if (lower.includes('ошибк') && !state.failedOnce.has(lower)) {
    state.failedOnce.add(lower)
    saveState()
    return [
      { event: 'status', data: { stage: 'thinking' } },
      { event: 'error', data: { code: 'upstream_unavailable', message: 'Сервис ответов временно недоступен. Повторите попытку.', retryable: true } },
    ]
  }

  if (lower.includes('отгул')) {
    const friday = nextFriday()
    const proposal: MockProposal = {
      id: uuid(),
      sessionId,
      userId: user.id,
      state: 'pending',
      draft: {
        tool: 'absence.create_request',
        args: { type: 'day_off', start_date: iso(friday), end_date: iso(friday), comment: '' },
        display: { approver_display_name: lead().display_name, dates: ru(friday) },
      },
    }
    state.proposals.set(proposal.id, proposal)
    return [
      { event: 'status', data: { stage: 'thinking' } },
      { event: 'status', data: { stage: 'searching_kb' } },
      ...words(
        `Отгул оформляется заявкой в системе отсутствий, её согласует руководитель. Я подготовил черновик на ${ru(friday)}: проверьте даты и нажмите «Отправить».`,
      ),
      { event: 'sources', data: { items: SOURCES } },
      {
        event: 'action_proposal',
        data: {
          proposal_id: proposal.id,
          kind: 'absence_request',
          draft: proposal.draft,
          summary: `Отгул ${ru(friday)}, согласующий — ${lead().display_name}`,
        },
      },
      { event: 'done', data: { message_id: uuid() } },
    ]
  }

  return [
    { event: 'status', data: { stage: 'thinking' } },
    { event: 'status', data: { stage: 'searching_kb' } },
    ...words('Это тестовый ответ прототипа. Подробности есть в тестовой инструкции для стажёров.'),
    {
      event: 'sources',
      data: { items: [{ doc_id: 'kb-onboarding-001', title: 'Первая неделя стажёра', source_type: 'test', url: '/kb/kb-onboarding-001' }] },
    },
    { event: 'done', data: { message_id: uuid() } },
  ]
}

export function confirmEvents(proposal: MockProposal): MockEvent[] {
  if (proposal.state === 'executed' && proposal.result) {
    // replay of an executed proposal: no second request (R79)
    return [
      { event: 'action_result', data: proposal.result },
      { event: 'done', data: { message_id: uuid() } },
    ]
  }
  const approver = lead().display_name
  const dates = String(proposal.draft.display.dates)
  const actionId = uuid()
  const taskId = uuid()
  state.seq += 1
  const externalId = `ABS-${state.seq}`
  const now = new Date().toISOString()
  const action: Action = {
    id: actionId,
    kind: 'absence_request',
    external_id: externalId,
    status: 'pending_approval',
    summary: `Отгул ${dates}, согласующий — ${approver}`,
    approver_display_name: approver,
    task_id: taskId,
    created_at: now,
    updated_at: now,
  }
  state.actions.set(proposal.userId, [action, ...(state.actions.get(proposal.userId) ?? [])])
  state.extraTasks.set(proposal.userId, [
    ...(state.extraTasks.get(proposal.userId) ?? []),
    {
      id: taskId,
      source: 'action',
      checklist_item_id: null,
      period: 'week_2',
      title: `Отгул ${dates}`,
      status: 'waiting_approval',
      human_only: false,
      linked_action_id: actionId,
    },
  ])
  const result = {
    action_id: actionId,
    kind: 'absence_request',
    external_id: externalId,
    status: 'pending_approval',
    summary: action.summary,
    approver_display_name: approver,
    task_id: taskId,
  }
  proposal.state = 'executed'
  proposal.result = result
  saveState()
  return [
    { event: 'status', data: { stage: 'calling_service' } },
    { event: 'status', data: { stage: 'saving' } },
    { event: 'action_result', data: result },
    ...words('Заявка создана и отправлена на согласование. Если отгул влияет на ваши задачи, предупредите наставника.'),
    { event: 'done', data: { message_id: uuid() } },
  ]
}
