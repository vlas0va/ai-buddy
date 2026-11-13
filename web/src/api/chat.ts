import { z } from 'zod'
import { apiRequest } from './client'
import { datePickerSchema, escalationDraftSchema, nextStepSchema, proposalSchema, refusalSchema, resultSchema } from '@/chat/events'
import { chatHistorySchema, chatMessageSchema, chatSessionSchema, escalationSchema } from './schemas'

export function createSession() {
  return apiRequest('/api/chat/sessions', chatSessionSchema, { method: 'POST' })
}

export function sendEscalation(body: { to_role: string; text: string; session_id?: string; message_id?: string }) {
  return apiRequest('/api/escalations', escalationSchema, { method: 'POST', body })
}

export async function cancelProposal(sessionId: string, proposalId: string) {
  await apiRequest(`/api/chat/sessions/${sessionId}/proposals/${proposalId}/cancel`, z.unknown(), { method: 'POST' })
}

// У ChatMessage в contracts/openapi.yaml нет карточек; proposal/result/proposal_state — необязательные расширения,
// чтобы перезагрузка могла их восстановить.
const historySchema = chatHistorySchema.extend({
  messages: z.array(
    chatMessageSchema.extend({
      proposal: proposalSchema.optional(),
      proposal_state: z.enum(['pending', 'confirmed', 'cancelled']).optional(),
      proposals: z.array(z.object({ proposal: proposalSchema, state: z.enum(['pending', 'confirmed', 'cancelled']) })).optional(),
      result: resultSchema.optional(),
      // отзыв 11.11: черновик наставнику, отказ и следующий шаг сохраняются после ухода из чата
      escalation: escalationDraftSchema.optional().catch(undefined),
      escalation_sent: z.boolean().optional(),
      refusal: refusalSchema.optional().catch(undefined),
      next_step: nextStepSchema.optional().catch(undefined),
      date_picker: datePickerSchema.optional().catch(undefined),
    }),
  ),
})
export type ChatHistory = z.infer<typeof historySchema>

// отзыв 12.11: чип источника открывает тестовый документ (ядро спрашивает агента, который проверяет ACL)
export const kbDocSchema = z.object({ doc_id: z.string(), title: z.string(), body: z.string(), badge: z.string().optional() })

export function getKBDoc(docId: string) {
  return apiRequest(`/api/kb/${encodeURIComponent(docId)}`, kbDocSchema)
}

const sessionsListSchema = z.array(z.object({ session_id: z.string(), title: z.string(), created_at: z.string(), updated_at: z.string() }))
export type SessionSummary = z.infer<typeof sessionsListSchema>[number]

export function listSessions() {
  return apiRequest('/api/chat/sessions', sessionsListSchema)
}

export function getSession(sessionId: string) {
  return apiRequest(`/api/chat/sessions/${sessionId}`, historySchema)
}
