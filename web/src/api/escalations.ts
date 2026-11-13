import { z } from 'zod'
import { apiRequest } from './client'
import { escalationSchema } from './schemas'

export type Escalation = z.infer<typeof escalationSchema>

/** Вопросы, отправленные вызывающим (стажёр) или адресованные вызывающему (наставник, руководитель, HR BP). */
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

/** Организаторы 13.11: список вопросов вызывающего — открытые вопросы к нему, похожие сгруппированы. */
export function listQueue() {
  return apiRequest('/api/escalations/queue', z.array(queueGroupSchema))
}

/** kbScope (отзыв 12.13): «team» — стажёрам команды наставника; «all» — общий ответ для всех команд. */
export function answerEscalation(id: string, text: string, addToKb: boolean, kbScope: 'team' | 'all' = 'team', alsoIds: string[] = []) {
  const also = alsoIds.length > 0 ? { also_ids: alsoIds } : {}
  return apiRequest(`/api/escalations/${encodeURIComponent(id)}/answer`, escalationSchema, {
    method: 'POST',
    body: addToKb ? { text, add_to_kb: true, kb_scope: kbScope, ...also } : { text, add_to_kb: false, ...also },
  })
}
