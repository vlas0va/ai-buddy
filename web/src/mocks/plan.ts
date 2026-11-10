import type { Plan, User } from '@/api/schemas'
import { state } from './db'

export const BASE_WEEK_2_TASKS: Plan['periods'][number]['tasks'] = [
  { id: '00000000-0000-4000-8000-000000000003', source: 'checklist', checklist_item_id: 'CL-07', period: 'week_2', title: 'Встреча с наставником', status: 'not_started', human_only: true },
  { id: '00000000-0000-4000-8000-000000000004', source: 'checklist', checklist_item_id: 'CL-08', period: 'week_2', title: 'Настроить рабочее окружение', status: 'in_progress', human_only: false },
  { id: '00000000-0000-4000-8000-000000000005', source: 'checklist', checklist_item_id: 'CL-09', period: 'week_2', title: 'Пройти вводный курс по безопасности', status: 'not_started', human_only: false },
]

export function planFor(user: User): Plan {
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
      { id: 'week_2', label: '2 неделя', tasks: [...BASE_WEEK_2_TASKS, ...(state.extraTasks.get(user.id) ?? [])] },
    ],
    goals: [],
    next_step: null,
  }
}
