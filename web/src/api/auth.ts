import { apiRequest } from './client'
import { loginResponseSchema, meResponseSchema } from './schemas'

export function login(loginName: string, password: string) {
  return apiRequest('/api/auth/login', loginResponseSchema, {
    method: 'POST',
    body: { login: loginName, password },
  })
}

export function getMe() {
  return apiRequest('/api/me', meResponseSchema)
}
