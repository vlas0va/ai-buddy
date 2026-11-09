import type { Action, Plan, User } from '@/api/schemas'

// In-memory mock state (dev mock mode and tests). Shapes follow contracts/openapi.yaml.

export const MOCK_PASSWORD = 'demo'

export const MOCK_USERS: User[] = [
  { id: 'intern.anna', login: 'intern.anna', display_name: 'Анна Соколова', role: 'intern', team_id: 'delivery', team_name: 'Доставка', cohort_start: '2026-10-19' },
  { id: 'intern.ivan', login: 'intern.ivan', display_name: 'Иван Петров', role: 'intern', team_id: 'search', team_name: 'Поиск', cohort_start: '2026-10-19' },
  { id: 'mentor.oleg', login: 'mentor.oleg', display_name: 'Олег Крылов', role: 'mentor', team_id: 'delivery', team_name: 'Доставка' },
  { id: 'lead.maria', login: 'lead.maria', display_name: 'Мария Лебедева', role: 'lead', team_id: 'delivery', team_name: 'Доставка' },
  { id: 'hrbp.elena', login: 'hrbp.elena', display_name: 'Елена Орлова', role: 'hrbp', team_id: 'hr', team_name: 'HR' },
  { id: 'alumni.petr', login: 'alumni.petr', display_name: 'Пётр Волков', role: 'alumni', team_id: 'delivery', team_name: 'Доставка' },
]

export const MOCK_FLAGS = {
  scenario_s1: true,
  scenario_s2: true,
  scenario_s3: true,
  scenario_s4: true,
  proactive_digest: true,
  escalation_to_kb: false,
}

export interface MockProposal {
  id: string
  sessionId: string
  userId: string
  draft: { tool: string; args: Record<string, unknown>; display: Record<string, unknown> }
  state: 'pending' | 'executed' | 'cancelled'
  result?: Record<string, unknown>
}

export interface MockState {
  actions: Map<string, Action[]>
  proposals: Map<string, MockProposal>
  sessions: Set<string>
  extraTasks: Map<string, Plan['periods'][number]['tasks']>
  failedOnce: Set<string>
  seq: number
}

export function freshState(): MockState {
  return { actions: new Map(), proposals: new Map(), sessions: new Set(), extraTasks: new Map(), failedOnce: new Set(), seq: 1041 }
}

export let state: MockState = freshState()

export function resetMockState(): void {
  state = freshState()
}

export function userByToken(header: string | null): User | null {
  const login = header?.replace(/^Bearer mock-token-/, '')
  return MOCK_USERS.find((u) => u.login === login) ?? null
}

export function uuid(): string {
  return crypto.randomUUID()
}
