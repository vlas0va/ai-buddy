export const DEFAULT_API_BASE_URL = 'http://localhost:8080'

export function getApiBaseUrl(): string {
  const raw = import.meta.env.VITE_API_BASE_URL
  const value = raw && raw.trim() !== '' ? raw.trim() : DEFAULT_API_BASE_URL
  return value.replace(/\/+$/, '')
}
