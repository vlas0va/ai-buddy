import { z } from 'zod'
import { apiRequest } from './client'
import { approvalSchema } from './schemas'

export type Decision = 'approved' | 'rejected'

export function listApprovals() {
  return apiRequest('/api/approvals', z.array(approvalSchema))
}

/** Idempotency-Key создаётся один раз на карточку согласования и переиспользуется во всех попытках этого решения. */
export function decideApproval(id: string, decision: Decision, comment: string | undefined, idempotencyKey: string) {
  return apiRequest(`/api/approvals/${id}/decision`, approvalSchema, {
    method: 'POST',
    headers: { 'Idempotency-Key': idempotencyKey },
    body: { decision, ...(comment ? { comment } : {}) },
  })
}
