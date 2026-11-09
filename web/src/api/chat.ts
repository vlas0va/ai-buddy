import { z } from 'zod'
import { apiRequest } from './client'
import { chatSessionSchema, escalationSchema } from './schemas'

export function createSession() {
  return apiRequest('/api/chat/sessions', chatSessionSchema, { method: 'POST' })
}

export function sendEscalation(body: { to_role: string; text: string; session_id?: string }) {
  return apiRequest('/api/escalations', escalationSchema, { method: 'POST', body })
}

export async function cancelProposal(sessionId: string, proposalId: string) {
  await apiRequest(`/api/chat/sessions/${sessionId}/proposals/${proposalId}/cancel`, z.unknown(), { method: 'POST' })
}
