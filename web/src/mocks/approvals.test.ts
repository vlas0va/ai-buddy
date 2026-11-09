import { afterEach, describe, expect, it } from 'vitest'
import { decideApproval, listPendingApprovals } from './approvals'
import { parseState, resetMockState, serializeState, state } from './db'
import { seedRequest, userByLogin } from '@/test/mockWorld'

afterEach(() => resetMockState())

describe('mock approvals (S3 step 8)', () => {
  it('lists pending requests of the lead team only', () => {
    seedRequest('intern.anna', 'хочу отгул в пятницу')
    seedRequest('intern.ivan', 'хочу отгул в пятницу')
    const list = listPendingApprovals(userByLogin('lead.maria'))
    expect(list.map((a) => a.requester_display_name)).toEqual(['Анна Соколова'])
    expect(list[0]!.status).toBe('pending')
  })

  it('decides once, replays the same key, conflicts on another key or decision', () => {
    const action = seedRequest('intern.anna', 'хочу отгул в пятницу')
    const lead = userByLogin('lead.maria')
    expect(decideApproval(lead, action.id, 'approved', 'k1')).toMatchObject({ status: 'approved' })
    expect(decideApproval(lead, action.id, 'approved', 'k1')).toMatchObject({ status: 'approved' })
    expect(decideApproval(lead, action.id, 'rejected', 'k1')).toBe('conflict')
    expect(decideApproval(lead, action.id, 'approved', 'k2')).toBe('conflict')
    expect(decideApproval(lead, 'missing', 'approved', 'k3')).toBe('not_found')
  })

  it('approved access becomes granted; other team lead gets forbidden; decisions survive serialize/parse', () => {
    const action = seedRequest('intern.anna', 'какие доступы')
    expect(action.status).toBe('granted') // Jira is not elevated
    const space = seedRequest('intern.anna', 'какие доступы') // second ask: the team space is elevated
    expect(space.status).toBe('pending_approval')
    const lead = userByLogin('lead.maria')
    expect(decideApproval({ ...lead, team_id: 'search' }, space.id, 'approved', 'k5')).toBe('forbidden')
    expect(decideApproval(lead, space.id, 'approved', 'k6')).toMatchObject({ kind: 'access_request', status: 'approved' })
    expect(state.actions.get('intern.anna')!.find((a) => a.id === space.id)!.status).toBe('granted')
    expect(parseState(serializeState(state))!.decisions.size).toBe(state.decisions.size)
  })
})
