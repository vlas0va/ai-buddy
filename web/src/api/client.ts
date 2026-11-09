import type { z } from 'zod'
import { getApiBaseUrl } from '@/config'
import { useSession } from '@/store/session'
import { errorSchema, type ApiErrorBody } from './schemas'

export class ApiError extends Error {
  readonly status: number
  readonly code: ApiErrorBody['code']
  readonly retryable: boolean

  constructor(status: number, body: ApiErrorBody) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.retryable = body.retryable
  }
}

export class ApiParseError extends Error {
  readonly detail: string

  constructor(path: string, detail: string) {
    super(`Неожиданный ответ сервера (${path}). Попробуйте позже.`)
    this.name = 'ApiParseError'
    this.detail = detail
  }
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: unknown
  headers?: Record<string, string>
  signal?: AbortSignal
}

export const NETWORK_ERROR: ApiErrorBody = {
  code: 'upstream_unavailable',
  message: 'Нет связи с сервером. Проверьте подключение и повторите.',
  retryable: true,
}

export async function readErrorBody(res: Response): Promise<ApiErrorBody> {
  try {
    const parsed = errorSchema.safeParse(await res.json())
    if (parsed.success) return parsed.data
  } catch {
    /* body is not JSON */
  }
  return {
    code: res.status >= 500 ? 'internal' : 'validation_error',
    message: 'Не удалось выполнить запрос.',
    retryable: res.status >= 500,
  }
}

export async function apiRequest<S extends z.ZodType>(
  path: string,
  schema: S,
  options: RequestOptions = {},
): Promise<z.infer<S>> {
  const { token, clear } = useSession.getState()
  const headers: Record<string, string> = { Accept: 'application/json', ...options.headers }
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`

  let res: Response
  try {
    res = await fetch(`${getApiBaseUrl()}${path}`, {
      method: options.method ?? 'GET',
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, NETWORK_ERROR)
  }

  if (!res.ok) {
    const body = await readErrorBody(res)
    if (res.status === 401 && token) clear()
    throw new ApiError(res.status, body)
  }

  let json: unknown
  try {
    json = res.status === 204 ? undefined : await res.json()
  } catch {
    throw new ApiParseError(path, 'invalid JSON')
  }
  const parsed = schema.safeParse(json)
  if (!parsed.success) throw new ApiParseError(path, parsed.error.message)
  return parsed.data
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError || e instanceof ApiParseError) return e.message
  return 'Что-то пошло не так. Повторите попытку.'
}

export function isRetryable(e: unknown): boolean {
  if (e instanceof ApiError) return e.retryable
  return !(e instanceof ApiParseError)
}
