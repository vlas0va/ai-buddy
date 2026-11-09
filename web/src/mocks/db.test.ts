import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearMockStorage, enableMockPersistence, freshState, MOCK_STORAGE_KEY, parseState, resetMockState, saveState, serializeState, state } from './db'

afterEach(() => resetMockState())

describe('mock db persistence (R66)', () => {
  it('round-trips through serialize/parse', () => {
    const s = freshState()
    s.seq = 1050
    s.failedOnce.add('ошибка')
    s.sessions.set('s1', [{ id: 'm', author: 'user', text: 'hi', sources: [], created_at: 'x' }])
    const back = parseState(serializeState(s))!
    expect(back.seq).toBe(1050)
    expect(back.failedOnce.has('ошибка')).toBe(true)
    expect(back.sessions.get('s1')).toHaveLength(1)
  })

  it('ignores empty or corrupt storage', () => {
    expect(parseState(null)).toBeNull()
    expect(parseState('{not json')).toBeNull()
  })

  it('saves to one key, reloads, and clears', () => {
    enableMockPersistence()
    state.seq = 2000
    saveState()
    expect(localStorage.getItem(MOCK_STORAGE_KEY)).toContain('2000')
    enableMockPersistence()
    expect(state.seq).toBe(2000)
    clearMockStorage()
    expect(localStorage.getItem(MOCK_STORAGE_KEY)).toBeNull()
  })

  it('survives unavailable storage', () => {
    enableMockPersistence()
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(() => saveState()).not.toThrow()
    expect(() => clearMockStorage()).not.toThrow()
    enableMockPersistence()
    expect(state.seq).toBe(1041)
  })
})
