import { z } from 'zod'
import { apiRequest } from './client'
import { escalationSchema } from './schemas'

export type Escalation = z.infer<typeof escalationSchema>

/** Questions sent by the caller (intern) or addressed to the caller (mentor, lead, HR BP). */
export function listEscalations() {
  return apiRequest('/api/escalations', z.array(escalationSchema))
}

export function answerEscalation(id: string, text: string, addToKb: boolean) {
  return apiRequest(`/api/escalations/${encodeURIComponent(id)}/answer`, escalationSchema, {
    method: 'POST',
    body: { text, add_to_kb: addToKb },
  })
}
