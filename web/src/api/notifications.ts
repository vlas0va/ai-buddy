import { z } from 'zod'
import { apiRequest } from './client'
import { notificationSchema } from './schemas'

export type Notification = z.infer<typeof notificationSchema>

export function listNotifications() {
  return apiRequest('/api/notifications', z.array(notificationSchema))
}

export function markNotificationRead(id: string) {
  return apiRequest(`/api/notifications/${encodeURIComponent(id)}/read`, z.unknown(), { method: 'POST' })
}
