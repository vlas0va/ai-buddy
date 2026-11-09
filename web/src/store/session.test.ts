import { describe, expect, it } from 'vitest'
import { useSession } from './session'

const user = { id: 'u1', login: 'mentor.oleg', display_name: 'Олег', role: 'mentor' as const, team_id: 'delivery' }

describe('session store', () => {
  it('stores and clears the session', () => {
    useSession.getState().setSession('tok', user)
    expect(useSession.getState().token).toBe('tok')
    expect(localStorage.getItem('ai-buddy-session')).toContain('tok')
    useSession.getState().clear()
    expect(useSession.getState().user).toBeNull()
  })
})
