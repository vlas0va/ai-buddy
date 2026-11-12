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
  return t.source === 'checklist' && t.status !== 'waiting_approval'
}

/** QA 12.11: a task made by a request (an absence, an access) asks for its status, not «how to do it» again */
export function askText(t: Pick<Task, 'source' | 'title'>): string {
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
