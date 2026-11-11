import { z } from 'zod'
import { apiRequest } from './client'

/** «Полезно / Не полезно» on an assistant message (R24 metric). */
export function sendFeedback(messageId: string, useful: boolean) {
  return apiRequest('/api/feedback', z.unknown(), { method: 'POST', body: { message_id: messageId, useful } })
}
