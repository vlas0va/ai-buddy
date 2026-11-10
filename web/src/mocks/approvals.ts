import type { Action, User } from '@/api/schemas'
import type { z } from 'zod'
import type { approvalSchema } from '@/api/schemas'
import { MOCK_USERS, state } from './db'

type Approval = z.infer<typeof approvalSchema>

// Mock of GET /api/approvals and POST /api/approvals/{id}/decision. Approvals are the intern actions that wait for the lead.

function requesterOf(actionId: string): { user: User; action: Action } | null {
  for (const [userId, actions] of state.actions) {
    const action = actions.find((a) => a.id === actionId)
    const user = MOCK_USERS.find((u) => u.id === userId)
    if (action && user) return { user, action }
  }
  return null
}

function toApproval(user: User, action: Action, comment?: string): Approval {
  const status = action.status === 'pending_approval' ? 'pending' : action.status === 'rejected' ? 'rejected' : 'approved'
  return {
    id: action.id,
    kind: action.kind === 'access_request' ? 'access_request' : 'absence_request',
    requester_display_name: user.display_name,
    summary: action.summary,
    status,
    comment: comment ?? null,
    created_at: action.created_at,
  }
}

export function listPendingApprovals(lead: User): Approval[] {
  const out: Approval[] = []
  for (const [userId, actions] of state.actions) {
    const user = MOCK_USERS.find((u) => u.id === userId)
    if (!user || user.team_id !== lead.team_id) continue
    for (const a of actions) if (a.status === 'pending_approval') out.push(toApproval(user, a))
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
  if (found.user.team_id !== lead.team_id) return 'forbidden'
  const { user, action } = found

  const replay = state.decisions.get(key)
  if (replay) return replay.actionId === approvalId && replay.decision === decision ? toApproval(user, action, comment) : 'conflict'
  if (action.status !== 'pending_approval') return 'conflict'

  // an approved access request means the access is granted; an approved absence just gets "approved"
  action.status = decision === 'rejected' ? 'rejected' : action.kind === 'access_request' ? 'granted' : 'approved'
  action.approver_display_name = lead.display_name
  action.updated_at = new Date().toISOString()
  const task = state.extraTasks.get(user.id)?.find((t) => t.id === action.task_id)
  if (task) task.status = decision === 'approved' ? 'done' : 'in_progress'
  state.decisions.set(key, { actionId: approvalId, decision })
  return toApproval(user, action, comment)
}
