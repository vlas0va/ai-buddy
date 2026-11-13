import type { Action, User } from '@/api/schemas'
import { MOCK_USERS, saveState, state, uuid, type MockProposal } from './db'
import { BASE_WEEK_2_TASKS } from './plan'
import { words, type MockEvent } from './sse'

// Мок-разговоры по сценарию. Отправляют только события из contracts/agent-stream.md; списки — обычный текст в токенах.

const src = (doc_id: string, title: string) => ({ doc_id, title, source_type: 'test', url: `/kb/${doc_id}` })
const ABSENCE_SOURCE = src('kb-absence-001', 'Порядок оформления отгула')
const ACCESS_SOURCE = src('kb-access-001', 'Доступы и каналы стажёра')
const TEAM_SOURCE = src('kb-team-001', 'Команда и контакты стажёра')
const ONBOARDING_SOURCE = src('kb-onboarding-001', 'Первая неделя стажёра')

const lead = () => MOCK_USERS.find((u) => u.login === 'lead.maria') as User
const mentorOf = (user: User) => MOCK_USERS.find((u) => u.role === 'mentor' && u.team_id === user.team_id)

function nextFriday(from = new Date()): Date {
  const d = new Date(from)
  d.setDate(d.getDate() + ((5 - d.getDay() + 7) % 7 || 7))
  return d
}

const iso = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const ru = (d: Date) => `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')}.${d.getFullYear()}`

const thinking = (): MockEvent => ({ event: 'status', data: { stage: 'thinking' } })
const searching = (): MockEvent => ({ event: 'status', data: { stage: 'searching_kb' } })
const sources = (...items: unknown[]): MockEvent => ({ event: 'sources', data: { items } })
const done = (): MockEvent => ({ event: 'done', data: { message_id: uuid() } })

// ---- ресурсы доступа ---------------------------------------------------------------------------------------------

interface Resource {
  id: string
  name: string
  elevated: boolean
}

function resourcesFor(user: User): Resource[] {
  return [
    { id: 'confluence', name: 'Confluence', elevated: false },
    { id: 'jira', name: 'Jira', elevated: false },
    { id: 'team-space', name: `Пространство команды «${user.team_name ?? user.team_id}»`, elevated: true },
  ]
}

type AccessStatus = 'есть' | 'нет' | 'запрошен'

function accessStatus(user: User, res: Resource): AccessStatus {
  if (res.id === 'confluence') return 'есть' // базовый: каждый стажёр получает в первый день
  const hit = (state.actions.get(user.id) ?? []).find(
    (a) => a.kind === 'access_request' && a.summary.startsWith(`Доступ: ${res.name}`) && a.status !== 'rejected' && a.status !== 'failed',
  )
  if (!hit) return 'нет'
  return hit.status === 'pending_approval' ? 'запрошен' : 'есть'
}

// ---- намерения ----------------------------------------------------------------------------------------------------

function isRefusal(lower: string): boolean {
  if (/зарплат|оклад|премия|бонус|компенсаци|медицин|диагноз|страховк/.test(lower)) return true
  return /коллег|чужо|другого сотрудника|соседа/.test(lower) && /отпуск|отгул|больнич|отсутств/.test(lower)
}

function refusalEvents(lower: string): MockEvent[] {
  const reason = /коллег|чужо|другого|соседа/.test(lower) ? 'no_rights' : 'out_of_scope'
  return [
    thinking(),
    ...words('Не могу помочь с этим вопросом: такие данные недоступны ассистенту.'),
    { event: 'refusal', data: { reason, redirect_role: 'hrbp' } },
    done(),
  ]
}

function procedureEvents(): MockEvent[] {
  return [
    thinking(),
    searching(),
    ...words(
      'Отгул оформляется в три шага:\n1. Согласуйте дату с руководителем и предупредите наставника.\n2. Создайте заявку на отгул в системе отсутствий: укажите дату и комментарий.\n3. Дождитесь решения руководителя: статус заявки появится в разделе «Мои заявки».\nМогу подготовить черновик заявки: напишите, на какую дату нужен отгул.',
    ),
    sources(ABSENCE_SOURCE),
    done(),
  ]
}

function dayOffEvents(sessionId: string, user: User): MockEvent[] {
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
    thinking(),
    searching(),
    ...words(
      `Отгул оформляется заявкой в системе отсутствий, её согласует руководитель. Я подготовил черновик на ${ru(friday)}: проверьте даты и нажмите «Отправить».`,
    ),
    sources(ABSENCE_SOURCE),
    {
      event: 'action_proposal',
      data: {
        proposal_id: proposal.id,
        kind: 'absence_request',
        draft: proposal.draft,
        summary: `Отгул ${ru(friday)}, согласующий — ${lead().display_name}`,
      },
    },
    done(),
  ]
}

function accessEvents(sessionId: string, user: User): MockEvent[] {
  const resources = resourcesFor(user)
  const lines = resources.map((r) => `- ${r.name}: ${accessStatus(user, r)}`).join('\n')
  const missing = resources.find((r) => accessStatus(user, r) === 'нет')
  const head = `Для команды «${user.team_name ?? user.team_id}» нужны такие доступы:\n${lines}\n`
  if (!missing) {
    return [thinking(), searching(), ...words(`${head}Все доступы уже есть или запрошены.`), sources(ACCESS_SOURCE), done()]
  }
  const proposal: MockProposal = {
    id: uuid(),
    sessionId,
    userId: user.id,
    state: 'pending',
    draft: {
      tool: 'access.request_access',
      args: { resource_id: missing.id, reason: 'Доступ нужен для работы в команде' },
      display: { resource_name: missing.name, elevated: missing.elevated },
    },
  }
  state.proposals.set(proposal.id, proposal)
  return [
    thinking(),
    searching(),
    ...words(
      `${head}Не хватает: ${missing.name}. Подготовил черновик заявки${missing.elevated ? '; повышенные права согласует руководитель' : ''}.`,
    ),
    sources(ACCESS_SOURCE),
    {
      event: 'action_proposal',
      data: {
        proposal_id: proposal.id,
        kind: 'access_request',
        draft: proposal.draft,
        summary: `Доступ: ${missing.name}`,
      },
    },
    done(),
  ]
}

function channelsEvents(user: User): MockEvent[] {
  const team = user.team_id
  const channels = [`#${team}-general`, `#${team}-releases`, `#${team}-help`]
  const mentor = mentorOf(user)
  const first = mentor?.display_name.split(' ')[0] ?? 'Наставник'
  return [
    thinking(),
    searching(),
    ...words(`В команде «${user.team_name ?? team}» есть каналы:\n${channels.map((c) => `- ${c}`).join('\n')}\nДобавить в каналы может наставник. Подготовил для него сообщение.`),
    sources(ACCESS_SOURCE),
    { event: 'escalation_draft', data: { to_role: 'mentor', text: `${first}, добавь меня, пожалуйста, в каналы: ${channels.join(', ')}.` } },
    done(),
  ]
}

function mentorEvents(user: User): MockEvent[] {
  const mentor = mentorOf(user)
  const name = mentor?.display_name ?? 'Наставник пока не назначен'
  return [
    thinking(),
    searching(),
    ...words(
      `Ваш наставник — ${name}, старший разработчик команды «${user.team_name ?? user.team_id}». Обращайтесь к нему по рабочим вопросам, по задачам и при блокерах, лучше в первой половине дня.\nКонтакты:\n- Наставник: ${name}\n- Руководитель: ${lead().display_name}, для согласований и вопросов по целям\n- Куратор программы стажировок: для организационных вопросов`,
    ),
    sources(TEAM_SOURCE),
    done(),
  ]
}

const TASK_STATUS_TEXT: Record<string, string> = {
  not_started: 'не начата',
  in_progress: 'в работе',
  waiting_approval: 'на согласовании',
  done: 'выполнена',
  overdue: 'просрочена',
}

function weekEvents(user: User): MockEvent[] {
  const tasks = [...BASE_WEEK_2_TASKS, ...(state.extraTasks.get(user.id) ?? [])].filter((t) => t.status !== 'done').slice(0, 3)
  const lines = tasks.map((t) => `- ${t.title}: ${TASK_STATUS_TEXT[t.status] ?? t.status}`).join('\n')
  return [
    thinking(),
    searching(),
    ...words(`На этой неделе в вашем плане:\n${lines}\n`),
    // ссылка на стороне клиента, см. MessageBubble: в agent-stream.md нет события-ссылки
    { event: 'token', data: { text: 'Открыть мой план' } },
    sources(ONBOARDING_SOURCE),
    done(),
  ]
}

function genericEvents(): MockEvent[] {
  return [
    thinking(),
    searching(),
    ...words('Это тестовый ответ прототипа. Подробности есть в тестовой инструкции для стажёров.'),
    sources(ONBOARDING_SOURCE),
    done(),
  ]
}

export interface MockAttachment {
  name: string
  kind: 'text' | 'image' | 'document'
  size: number
  text?: string
}

const SECRET = /\b(\w*?(?:password|secret|token|api[_-]?key)\w*)([ \t]*[=:][ \t]*)\S+/gi

/**
 * Вложения — так, как на них отвечает настоящий агент (openspec attachments-websearch): логи маскируются, скриншоты и
 * документы не читаются.
 */
function attachmentEvents(files: MockAttachment[]): MockEvent[] {
  const text = files.filter((f) => f.kind === 'text').map((f) => f.text ?? '').join('\n')
  const masked = (text.match(SECRET) ?? []).length
  const parts: string[] = []
  if (masked) parts.push(`В файле были секреты (${masked}) — я их скрыл.`)
  if (text) parts.push(`Главная строка: «${text.replace(SECRET, '$1$2***').split('\n')[0]?.slice(0, 120) ?? ''}».`)
  if (files.some((f) => f.kind === 'image')) parts.push('Скриншот я в прототипе не распознаю — скопируйте текст ошибки.')
  if (files.some((f) => f.kind === 'document')) parts.push('Документы я не открываю: кадровые документы загружаются в кадровый портал.')
  return [thinking(), ...words(parts.join(' ')), done()]
}

export function messageEvents(sessionId: string, user: User, text: string, files: MockAttachment[] = []): MockEvent[] {
  const lower = text.toLowerCase()
  if (files.length > 0 && !text.trim()) return attachmentEvents(files)

  if (lower.includes('ошибк') && !state.failedOnce.has(lower)) {
    state.failedOnce.add(lower)
    saveState()
    return [
      thinking(),
      { event: 'error', data: { code: 'upstream_unavailable', message: 'Сервис ответов временно недоступен. Повторите попытку.', retryable: true } },
    ]
  }
  if (isRefusal(lower)) return refusalEvents(lower)
  if (lower.includes('отгул')) return /как (оформ|взять|получ)|порядок/.test(lower) ? procedureEvents() : dayOffEvents(sessionId, user)
  if (lower.includes('канал')) return channelsEvents(user)
  if (lower.includes('доступ')) return accessEvents(sessionId, user)
  if (lower.includes('наставник')) return mentorEvents(user)
  if (lower.includes('на этой неделе')) return weekEvents(user)
  return genericEvents()
}

// ---- подтверждение
// ----------------------------------------------------------------------------------------------------

export function confirmEvents(proposal: MockProposal): MockEvent[] {
  if (proposal.state === 'executed' && proposal.result) {
    // повтор выполненного черновика: второй заявки нет (R79)
    return [{ event: 'action_result', data: proposal.result }, done()]
  }
  const isAccess = proposal.draft.tool.startsWith('access.')
  const approver = lead().display_name
  const elevated = isAccess ? proposal.draft.display.elevated === true : true
  const pending = !isAccess || elevated
  const label = isAccess ? `Доступ: ${String(proposal.draft.display.resource_name)}` : `Отгул ${String(proposal.draft.display.dates)}`
  const kind = isAccess ? 'access_request' : 'absence_request'
  const actionId = uuid()
  const taskId = uuid()
  state.seq += 1
  const externalId = `${isAccess ? 'ACC' : 'ABS'}-${state.seq}`
  const now = new Date().toISOString()
  const summary = pending ? `${label}, согласующий — ${approver}` : label
  const action: Action = {
    id: actionId,
    kind,
    external_id: externalId,
    status: pending ? 'pending_approval' : 'granted',
    summary,
    approver_display_name: pending ? approver : null,
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
      title: label,
      status: pending ? 'waiting_approval' : 'done',
      human_only: false,
      linked_action_id: actionId,
    },
  ])
  const result = {
    action_id: actionId,
    kind,
    external_id: externalId,
    status: action.status,
    summary,
    approver_display_name: action.approver_display_name,
    task_id: taskId,
  }
  proposal.state = 'executed'
  proposal.result = result
  saveState()
  const closing = !pending
    ? 'Доступ выдан. Заявка сохранена в журнале.'
    : isAccess
      ? 'Запрос отправлен руководителю: повышенные права выдаются после согласования.'
      : 'Заявка создана и отправлена на согласование. Если отгул влияет на ваши задачи, предупредите наставника.'
  return [
    { event: 'status', data: { stage: 'calling_service' } },
    { event: 'status', data: { stage: 'saving' } },
    { event: 'action_result', data: result },
    ...words(closing),
    done(),
  ]
}
