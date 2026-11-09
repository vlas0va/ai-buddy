import { beforeEach, describe, expect, it, vi } from 'vitest'
import { z } from 'zod'
import { getApiBaseUrl } from '@/config'
import { useSession } from '@/store/session'
import { jsonResponse } from '@/test/helpers'
import { getMe, login } from './auth'
import { ApiError, ApiParseError, apiRequest, errorMessage, isRetryable } from './client'
import { getPlan } from './plan'

const user = { id: 'u1', login: 'intern.anna', display_name: 'Анна', role: 'intern', team_id: 'delivery' }

beforeEach(() => {
  useSession.getState().clear()
})

describe('R63 api client', () => {
  it('uses the base URL and sends the bearer token', async () => {
    useSession.getState().setSession('tok', user as never)
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ user, flags: {} }))
    vi.stubGlobal('fetch', fetchMock)
    await getMe()
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe(`${getApiBaseUrl()}/api/me`)
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer tok')
  })

  it('posts JSON for login and builds the plan query', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ token: 't', user }))
      .mockResolvedValueOnce(
        jsonResponse({ user_id: 'u2', plan_role: 'intern', current_period: 'week_1', periods: [], goals: [] }),
      )
    vi.stubGlobal('fetch', fetchMock)
    await login('intern.anna', 'demo')
    await getPlan('u2')
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('POST')
    expect(init.body).toBe(JSON.stringify({ login: 'intern.anna', password: 'demo' }))
    expect((fetchMock.mock.calls[1] as [string])[0]).toContain('/api/plan?user_id=u2')
  })

  it('turns an error body into ApiError and clears the session on 401', async () => {
    useSession.getState().setSession('tok', user as never)
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ code: 'unauthorized', message: 'Войдите снова', retryable: false }, 401)),
    )
    const err = await apiRequest('/api/me', z.object({})).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(401)
    expect(errorMessage(err)).toBe('Войдите снова')
    expect(isRetryable(err)).toBe(false)
    expect(useSession.getState().token).toBeNull()
  })

  it('handles a non-JSON error body', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('oops', { status: 503 })))
    const err = await apiRequest('/x', z.object({})).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(isRetryable(err)).toBe(true)
  })

  it('reports a network failure as retryable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('failed')))
    const err = await apiRequest('/x', z.object({})).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(0)
    expect(isRetryable(err)).toBe(true)
  })

  it('rethrows aborts untouched', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new DOMException('aborted', 'AbortError')))
    await expect(apiRequest('/x', z.object({}))).rejects.toMatchObject({ name: 'AbortError' })
  })

  it('shows a parse failure as an error, never swallows it', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ unexpected: true })))
    const err = await apiRequest('/api/me', z.object({ user: z.string() })).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiParseError)
    expect(isRetryable(err)).toBe(false)
    expect(errorMessage(err)).toContain('/api/me')

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not json', { status: 200 })))
    await expect(apiRequest('/x', z.object({}))).rejects.toBeInstanceOf(ApiParseError)
    expect(errorMessage(new Error('x'))).toContain('Повторите')
    expect(isRetryable(new Error('x'))).toBe(true)
  })
})
