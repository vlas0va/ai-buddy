import { z } from 'zod'
import { apiRequest } from './client'

/** «Полезно / Не полезно» на сообщении ассистента (метрика R24). */
export function sendFeedback(messageId: string, useful: boolean) {
  return apiRequest('/api/feedback', z.unknown(), { method: 'POST', body: { message_id: messageId, useful } })
}
