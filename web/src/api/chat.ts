import { z } from 'zod'
import { apiRequest } from './client'
import { escalationDraftSchema, nextStepSchema, proposalSchema, refusalSchema, resultSchema } from '@/chat/events'
import { chatHistorySchema, chatMessageSchema, chatSessionSchema, escalationSchema } from './schemas'

export function createSession() {
  return apiRequest('/api/chat/sessions', chatSessionSchema, { method: 'POST' })
}

export function sendEscalation(body: { to_role: string; text: string; session_id?: string }) {
  return apiRequest('/api/escalations', escalationSchema, { method: 'POST', body })
}

export async function cancelProposal(sessionId: string, proposalId: string) {
  await apiRequest(`/api/chat/sessions/${sessionId}/proposals/${proposalId}/cancel`, z.unknown(), { method: 'POST' })
}

// ChatMessage in contracts/openapi.yaml has no cards; proposal/result/proposal_state are optional extensions so a reload can restore them.
const historySchema = chatHistorySchema.extend({
  messages: z.array(
    chatMessageSchema.extend({
      proposal: proposalSchema.optional(),
      proposal_state: z.enum(['pending', 'confirmed', 'cancelled']).optional(),
      proposals: z.array(z.object({ proposal: proposalSchema, state: z.enum(['pending', 'confirmed', 'cancelled']) })).optional(),
      result: resultSchema.optional(),
      // feedback 11.11: the draft to the mentor, a refusal and the next step survive leaving the chat
      escalation: escalationDraftSchema.optional().catch(undefined),
      escalation_sent: z.boolean().optional(),
      refusal: refusalSchema.optional().catch(undefined),
      next_step: nextStepSchema.optional().catch(undefined),
    }),
  ),
})
export type ChatHistory = z.infer<typeof historySchema>

export function getSession(sessionId: string) {
  return apiRequest(`/api/chat/sessions/${sessionId}`, historySchema)
}
