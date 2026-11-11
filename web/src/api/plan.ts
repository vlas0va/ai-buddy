import { z } from 'zod'
import { apiRequest } from './client'
import { goalSchema, internProgressSchema, planSchema, taskSchema } from './schemas'

export type EditableStatus = 'not_started' | 'in_progress' | 'done'
export interface GoalInput {
  title: string
  criteria?: string
}

/** Own plan without arguments; a mentor or lead passes `userId` of an intern of their team (R71: 403 otherwise). */
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

export function listInterns() {
  return apiRequest('/api/team/interns', z.array(internProgressSchema))
}
