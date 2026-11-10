import { afterEach, describe, expect, it, vi } from 'vitest'
import { DEFAULT_API_BASE_URL, getApiBaseUrl } from './config'

afterEach(() => vi.unstubAllEnvs())

describe('R63 base URL from VITE_API_BASE_URL', () => {
  it('uses the env value without trailing slash', () => {
    vi.stubEnv('VITE_API_BASE_URL', 'http://api.test:9000/')
    expect(getApiBaseUrl()).toBe('http://api.test:9000')
  })

  it('falls back to the default when empty', () => {
    vi.stubEnv('VITE_API_BASE_URL', '  ')
    expect(getApiBaseUrl()).toBe(DEFAULT_API_BASE_URL)
  })
})
