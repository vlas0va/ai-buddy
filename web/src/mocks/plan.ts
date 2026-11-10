import type { Plan, User } from '@/api/schemas'
import { MOCK_USERS, state } from './db'

type Task = Plan['periods'][number]['tasks'][number]

export const BASE_WEEK_2_TASKS: Task[] = [
  { id: '00000000-0000-4000-8000-000000000003', source: 'checklist', checklist_item_id: 'CL-07', period: 'week_2', title: 'Встреча с наставником', status: 'not_started', human_only: true },
  { id: '00000000-0000-4000-8000-000000000004', source: 'checklist', checklist_item_id: 'CL-08', period: 'week_2', title: 'Настроить рабочее окружение', status: 'in_progress', human_only: false, due_at: '2026-11-13' },
  { id: '00000000-0000-4000-8000-000000000005', source: 'checklist', checklist_item_id: 'CL-09', period: 'week_2', title: 'Пройти вводный курс по безопасности', status: 'not_started', human_only: false },
]

const WEEK_1: Task[] = [
  { id: '00000000-0000-4000-8000-000000000001', source: 'checklist', checklist_item_id: 'CL-03', period: 'week_1', title: 'Познакомиться с командой', status: 'done', human_only: false },
  {
    id: '00000000-0000-4000-8000-000000000002', source: 'checklist', checklist_item_id: 'CL-04', period: 'week_1', title: 'Получить доступы', status: 'in_progress', human_only: false, due_at: '2026-11-06',
    subtasks: [
      { id: '00000000-0000-4000-8000-000000000021', source: 'checklist', checklist_item_id: 'CL-04', parent_task_id: '00000000-0000-4000-8000-000000000002', period: 'week_1', title: 'Confluence', status: 'done', human_only: false },
      { id: '00000000-0000-4000-8000-000000000022', source: 'checklist', checklist_item_id: 'CL-04', parent_task_id: '00000000-0000-4000-8000-000000000002', period: 'week_1', title: 'Jira', status: 'not_started', human_only: false },
    ],
  },
]

const MONTH_1: Task[] = [
  { id: '00000000-0000-4000-8000-000000000006', source: 'checklist', checklist_item_id: 'CL-10', period: 'month_1', title: 'Первая задача в команде', status: 'not_started', human_only: false },
]

function withStatus(userId: string, t: Task): Task {
  const status = (state.taskStatus.get(`${userId}:${t.id}`) as Task['status'] | undefined) ?? t.status
  return { ...t, status, subtasks: t.subtasks?.map((s) => withStatus(userId, s)) }
}

export function findTask(userId: string, taskId: string): Task | undefined {
  const plan = planFor(MOCK_USERS.find((u) => u.id === userId)!)
  const all = plan.periods.flatMap((p) => p.tasks.flatMap((t) => [t, ...(t.subtasks ?? [])]))
  return all.find((t) => t.id === taskId)
}

export function planFor(user: User): Plan {
  const owner = user.role === 'mentor' ? 'intern.anna' : user.id
  const periods = [
    { id: 'week_1' as const, label: '1 неделя', tasks: WEEK_1 },
    { id: 'week_2' as const, label: '2 неделя', tasks: [...BASE_WEEK_2_TASKS, ...(state.extraTasks.get(user.id) ?? [])] },
    { id: 'month_1' as const, label: '1 месяц', tasks: MONTH_1 },
  ].map((p) => ({ ...p, tasks: p.tasks.map((t) => withStatus(user.id, t)) }))
  const next = periods.flatMap((p) => p.tasks).find((t) => t.status !== 'done' && !t.human_only)
  return {
    user_id: user.id,
    plan_role: user.role === 'mentor' ? 'mentor' : 'intern',
    intern_id: owner,
    current_period: 'week_2',
    periods,
    goals: state.goals.get(owner) ?? [],
    next_step: next ? { task_id: next.id, title: next.title, action: 'ask_buddy' } : null,
  }
}
