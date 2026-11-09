import { z } from 'zod'
import { apiRequest } from './client'
import { approvalSchema } from './schemas'

export type Decision = 'approved' | 'rejected'

export function listApprovals() {
  return apiRequest('/api/approvals', z.array(approvalSchema))
}

/** The Idempotency-Key is created once per approval card and reused for every attempt of that decision. */
export function decideApproval(id: string, decision: Decision, comment: string | undefined, idempotencyKey: string) {
  return apiRequest(`/api/approvals/${id}/decision`, approvalSchema, {
    method: 'POST',
    headers: { 'Idempotency-Key': idempotencyKey },
    body: { decision, ...(comment ? { comment } : {}) },
  })
}
