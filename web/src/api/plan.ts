import { z } from 'zod'
import { apiRequest } from './client'
import { goalSchema, internProgressSchema, planSchema, taskSchema } from './schemas'

export type EditableStatus = 'not_started' | 'in_progress' | 'done'
export interface GoalInput {
  title: string
  criteria?: string
  status?: 'proposed' | 'active' | 'achieved' | 'missed'
}

/** Свой план без аргументов; наставник или руководитель передаёт `userId` стажёра своей команды (R71: иначе 403). */
export function getPlan(userId?: string) {
  const query = userId ? `?user_id=${encodeURIComponent(userId)}` : ''
  return apiRequest(`/api/plan${query}`, planSchema)
}

export function updateTaskStatus(taskId: string, status: EditableStatus) {
  return apiRequest(`/api/plan/tasks/${encodeURIComponent(taskId)}`, taskSchema, { method: 'PATCH', body: { status } })
}

export function saveGoals(userId: string, goals: GoalInput[]) {
  return apiRequest('/api/plan/goals', z.array(goalSchema), { method: 'PUT', body: { user_id: userId, goals } })
}

/** Стажёр записывает цели, согласованные с руководителем; они ждут наставника или руководителя (организаторы 12.11). */
export function proposeGoals(goals: GoalInput[]) {
  return apiRequest('/api/plan/goals/propose', z.array(goalSchema), { method: 'POST', body: { goals } })
}

export function listInterns() {
  return apiRequest('/api/team/interns', z.array(internProgressSchema))
}

const n = z.number().int()
export const benefitSchema = z.object({
  interns: n,
  minutes_per_question: n,
  asked: n,
  escalated: n,
  answered: n,
  added_to_kb: n,
  self_served: n,
  self_served_percent: n,
  saved_minutes: n,
  useful: n,
  not_useful: n,
  rows: z.array(
    z.object({
      user_id: z.string(),
      display_name: z.string(),
      asked: n,
      escalated: n,
      self_served: n,
      saved_minutes: n,
      useful: n,
      not_useful: n,
    }),
  ),
})
export type Benefit = z.infer<typeof benefitSchema>

/** «Польза buddy» по стажёрам вызывающего (идея 13.11): только наставник и руководитель (R71: иначе 403). */
export function getBenefit() {
  return apiRequest('/api/team/benefit', benefitSchema)
}

const reportTaskSchema = z.object({ title: z.string(), due_at: z.string().nullable(), completed_at: z.string().nullable() })

export const reportSchema = z.object({
  user_id: z.string(),
  name: z.string(),
  cohort_start: z.string().nullable(),
  days: z.number(),
  total_days: z.number(),
  final: z.boolean(),
  current_period: z.string(),
  tasks: z.object({
    total: z.number(),
    done: z.number(),
    early: z.number(),
    on_time: z.number(),
    late: z.number(),
    overdue_now: z.number(),
    upcoming: z.number(),
  }),
  periods: z.array(z.object({ period: z.string(), total: z.number(), done: z.number(), late: z.number() })),
  late: z.array(reportTaskSchema),
  overdue: z.array(reportTaskSchema),
  questions: z.object({
    asked: z.number(),
    escalated: z.number(),
    answered: z.number(),
    added_to_kb: z.number(),
    self_served: z.number().default(0),
    saved_minutes: z.number().default(0),
  }),
  accesses_requested: z.number(),
  goals: z.number(),
  goals_achieved: z.number().default(0),
  goals_proposed: z.number().default(0),
  discuss: z.array(z.string()),
})
export type Report = z.infer<typeof reportSchema>

/** Отчёт за стажировку (идея 12.11): наставник или руководитель команды передаёт `userId` стажёра (R71: иначе 403). */
export function getReport(userId: string) {
  return apiRequest(`/api/plan/report?user_id=${encodeURIComponent(userId)}`, reportSchema)
}
