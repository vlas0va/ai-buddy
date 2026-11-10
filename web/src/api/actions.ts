import { apiRequest } from './client'
import { actionsSchema } from './schemas'

export function listActions() {
  return apiRequest('/api/actions', actionsSchema)
}
