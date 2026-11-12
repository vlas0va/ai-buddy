import { z } from 'zod'
import { apiRequest } from './client'
import { escalationSchema } from './schemas'

export type Escalation = z.infer<typeof escalationSchema>

/** Questions sent by the caller (intern) or addressed to the caller (mentor, lead, HR BP). */
export function listEscalations() {
  return apiRequest('/api/escalations', z.array(escalationSchema))
}

/** kbScope (feedback 12.13): «team» — interns of the mentor's team; «all» — a general answer for every team. */
export function answerEscalation(id: string, text: string, addToKb: boolean, kbScope: 'team' | 'all' = 'team') {
  return apiRequest(`/api/escalations/${encodeURIComponent(id)}/answer`, escalationSchema, {
    method: 'POST',
    body: addToKb ? { text, add_to_kb: true, kb_scope: kbScope } : { text, add_to_kb: false },
  })
}
