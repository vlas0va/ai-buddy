import type { Plan, Task } from '@/api/schemas'

export const PERIOD_ORDER = ['week_1', 'week_2', 'month_1', 'month_2', 'after_3_months'] as const

/** 2026-11-13 → «13.11» (dates are calendar dates, no time zone shift). */
export function shortDate(iso?: string | null): string {
  if (!iso) return ''
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso)
  return m ? `${m[3]}.${m[2]}` : ''
}

export function flatTasks(plan: Plan): Task[] {
  return plan.periods.flatMap((p) => p.tasks)
}

export function progress(plan: Plan): { done: number; total: number; overdue: number } {
  const tasks = flatTasks(plan)
  return {
    done: tasks.filter((t) => t.status === 'done').length,
    total: tasks.length,
    overdue: tasks.filter((t) => t.status === 'overdue').length,
  }
}

/** Applies a status to a task or subtask in a plan copy (optimistic update). Parent follows its subtasks like core does. */
export function withTaskStatus(plan: Plan, taskId: string, status: Task['status']): Plan {
  const apply = (t: Task): Task => {
    if (t.id === taskId) {
      const subtasks = status === 'done' ? t.subtasks?.map((s) => ({ ...s, status: 'done' as const })) : t.subtasks
      return { ...t, status, subtasks }
    }
    if (!t.subtasks?.some((s) => s.id === taskId)) return t
    const subtasks = t.subtasks.map((s) => (s.id === taskId ? { ...s, status } : s))
    const allDone = subtasks.every((s) => s.status === 'done')
    const parentStatus = allDone ? 'done' : t.status === 'done' ? 'in_progress' : t.status
    return { ...t, subtasks, status: parentStatus }
  }
  return { ...plan, periods: plan.periods.map((p) => ({ ...p, tasks: p.tasks.map(apply) })) }
}

export function canToggle(t: Task): boolean {
  if (t.source !== 'checklist') return false
  if (t.confirm === 'quiz') return t.status === 'done' // done by the mini-check; can be reopened
  return t.status !== 'waiting_approval' || t.confirm === 'mentor' // the intern may take the request back
}

/** The status the intern's click asks for: a mentor-confirmed task waits for the mentor (organizers 12.13). */
export function nextStatus(t: Task): 'in_progress' | 'done' | 'waiting_approval' {
  if (t.status === 'done' || t.status === 'waiting_approval') return 'in_progress'
  return t.confirm === 'mentor' ? 'waiting_approval' : 'done'
}

export const CONFIRM_HINT: Record<string, string> = {
  quiz: 'отмечается после мини-проверки в чате',
  mentor: 'выполнение подтверждает наставник',
}

/** QA 12.11: a task made by a request (an absence, an access) asks for its status, not «how to do it» again */
/** Access tasks of the checklist: organizers 12.11 — the checklist has direct links to the request forms. */
const ACCESS_ITEMS = new Set(['CL-05'])

export function askText(t: Pick<Task, 'source' | 'title'> & { checklist_item_id?: string | null; confirm?: Task['confirm'] }): string {
  if (t.source !== 'action' && t.checklist_item_id && ACCESS_ITEMS.has(t.checklist_item_id)) return 'Какие доступы мне нужны?'
  if (t.source !== 'action' && t.confirm === 'quiz') return `Проверь меня по задаче «${t.title}»`
  if (t.source !== 'action') return `Как выполнить задачу «${t.title}»?`
  return t.title.startsWith('Доступ') ? 'Какие доступы мне нужны?' : `Что с моей заявкой «${t.title}»?`
}

export const NEXT_STEP_PROMPT: Record<string, (title: string) => string> = {
  ask_buddy: (title) => `Как выполнить задачу «${title}»?`,
  request_access: () => 'Какие доступы мне нужны?',
  absence_request: () => 'Как оформить отгул?',
  draft_message: (title) => `Помоги написать наставнику про задачу «${title}»`,
  mark_done: (title) => `Как понять, что задача «${title}» выполнена?`,
}

export function askLabel(t: Pick<Task, 'source'> & { checklist_item_id?: string | null; confirm?: Task['confirm'] }): string {
  if (t.source === 'action') return 'Что с заявкой?'
  if (t.confirm === 'quiz') return 'Проверить себя'
  return t.checklist_item_id && ACCESS_ITEMS.has(t.checklist_item_id) ? 'Подать заявку' : 'Спросить buddy'
}

export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10
  const m100 = n % 100
  if (m10 === 1 && m100 !== 11) return one
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few
  return many
}

/** «40 мин», «1 ч 20 мин», «3 ч» */
export function hours(minutes: number): string {
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  if (h === 0) return `${m} мин`
  return m === 0 ? `${h} ч` : `${h} ч ${m} мин`
}
