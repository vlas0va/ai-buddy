import { z } from 'zod'
import { apiRequest } from './client'
import { escalationSchema } from './schemas'

export type Escalation = z.infer<typeof escalationSchema>

/** Questions sent by the caller (intern) or addressed to the caller (mentor, lead, HR BP). */
export function listEscalations() {
  return apiRequest('/api/escalations', z.array(escalationSchema))
}

export const queueGroupSchema = z.object({
  ids: z.array(z.string()).min(1),
  question: z.string(),
  count: z.number().int(),
  askers: z.array(z.string()),
  first_asked_at: z.string(),
  last_asked_at: z.string(),
})
export type QueueGroup = z.infer<typeof queueGroupSchema>

/** Organizers 13.11: the question stack of the caller — open questions to the caller, similar ones grouped. */
export function listQueue() {
  return apiRequest('/api/escalations/queue', z.array(queueGroupSchema))
}

/** kbScope (feedback 12.13): «team» — interns of the mentor's team; «all» — a general answer for every team. */
export function answerEscalation(id: string, text: string, addToKb: boolean, kbScope: 'team' | 'all' = 'team', alsoIds: string[] = []) {
  const also = alsoIds.length > 0 ? { also_ids: alsoIds } : {}
  return apiRequest(`/api/escalations/${encodeURIComponent(id)}/answer`, escalationSchema, {
    method: 'POST',
    body: addToKb ? { text, add_to_kb: true, kb_scope: kbScope, ...also } : { text, add_to_kb: false, ...also },
  })
}
