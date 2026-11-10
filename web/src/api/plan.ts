import { apiRequest } from './client'
import { planSchema } from './schemas'

export function getPlan(userId?: string) {
  const query = userId ? `?user_id=${encodeURIComponent(userId)}` : ''
  return apiRequest(`/api/plan${query}`, planSchema)
}
