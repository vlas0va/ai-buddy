import type { Action, User } from '@/api/schemas'
import type { z } from 'zod'
import type { approvalSchema } from '@/api/schemas'
import { MOCK_USERS, state } from './db'

type Approval = z.infer<typeof approvalSchema>

// Мок GET /api/approvals и POST /api/approvals/{id}/decision. Согласования — это действия стажёров, которые ждут
// руководителя (pending_approval) или, для отсутствия, согласованного руководителем, — HR-администратора (pending_hr;
// чекпоинт 11.11).

function requesterOf(actionId: string): { user: User; action: Action } | null {
  for (const [userId, actions] of state.actions) {
    const action = actions.find((a) => a.id === actionId)
    const user = MOCK_USERS.find((u) => u.id === userId)
    if (action && user) return { user, action }
  }
  return null
}

function toApproval(user: User, action: Action, comment?: string, stage: 'lead' | 'hr_admin' = 'lead'): Approval {
  const waiting = stage === 'hr_admin' ? 'pending_hr' : 'pending_approval'
  const status = action.status === waiting ? 'pending' : action.status === 'rejected' ? 'rejected' : 'approved'
  return {
    stage,
    id: action.id,
    kind: action.kind === 'access_request' ? 'access_request' : 'absence_request',
    requester_display_name: user.display_name,
    summary: action.summary,
    status,
    comment: comment ?? null,
    created_at: action.created_at,
  }
}

export function listPendingApprovals(approver: User): Approval[] {
  const out: Approval[] = []
  const hr = approver.role === 'hr_admin'
  for (const [userId, actions] of state.actions) {
    const user = MOCK_USERS.find((u) => u.id === userId)
    if (!user || (!hr && user.team_id !== approver.team_id)) continue
    for (const a of actions) {
      if (hr && a.status === 'pending_hr') out.push(toApproval(user, a, undefined, 'hr_admin'))
      if (!hr && a.status === 'pending_approval') out.push(toApproval(user, a))
    }
  }
  return out.sort((x, y) => y.created_at.localeCompare(x.created_at))
}

export function decideApproval(
  lead: User,
  approvalId: string,
  decision: 'approved' | 'rejected',
  key: string,
  comment?: string,
): Approval | 'not_found' | 'forbidden' | 'conflict' {
  const found = requesterOf(approvalId)
  if (!found) return 'not_found'
  const hr = lead.role === 'hr_admin'
  if (!hr && found.user.team_id !== lead.team_id) return 'forbidden'
  const { user, action } = found
  const stage = hr ? 'hr_admin' : 'lead'

  const replay = state.decisions.get(key)
  if (replay) return replay.actionId === approvalId && replay.decision === decision ? toApproval(user, action, comment, stage) : 'conflict'
  if (action.status !== (hr ? 'pending_hr' : 'pending_approval')) return 'conflict'

  // доступ: approved = выдан; отсутствие: руководитель передаёт HR-администратору, HR-администратор согласует
  const toHR = !hr && decision === 'approved' && action.kind === 'absence_request'
  action.status = decision === 'rejected' ? 'rejected' : action.kind === 'access_request' ? 'granted' : toHR ? 'pending_hr' : 'approved'
  if (!hr) action.approver_display_name = lead.display_name
  action.updated_at = new Date().toISOString()
  const task = state.extraTasks.get(user.id)?.find((t) => t.id === action.task_id)
  if (task) task.status = toHR ? 'waiting_approval' : decision === 'approved' ? 'done' : 'in_progress'
  state.decisions.set(key, { actionId: approvalId, decision })
  const out = toApproval(user, action, comment, stage)
  return toHR ? { ...out, status: 'approved' } : out
}
