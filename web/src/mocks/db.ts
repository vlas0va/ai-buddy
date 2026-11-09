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

export interface StoredMessage {
  id: string
  author: 'user' | 'assistant'
  text: string
  sources: unknown[]
  created_at: string
  proposal?: unknown
  proposal_state?: 'pending' | 'confirmed' | 'cancelled'
  result?: unknown
}

export interface MockState {
  actions: Map<string, Action[]>
  proposals: Map<string, MockProposal>
  sessions: Map<string, StoredMessage[]>
  extraTasks: Map<string, Plan['periods'][number]['tasks']>
  failedOnce: Set<string>
  /** Idempotency-Key -> decision already applied (replay of POST /api/approvals/{id}/decision) */
  decisions: Map<string, { actionId: string; decision: 'approved' | 'rejected' }>
  seq: number
}

export function freshState(): MockState {
  return { actions: new Map(), proposals: new Map(), sessions: new Map(), extraTasks: new Map(), failedOnce: new Set(), decisions: new Map(), seq: 1041 }
}

export let state: MockState = freshState()

export function resetMockState(): void {
  state = freshState()
}

// Browser mock mode keeps the whole db under one localStorage key so it survives F5 (R66).
export const MOCK_STORAGE_KEY = 'ai-buddy-mock-db'
let persistEnabled = false

export function serializeState(s: MockState): string {
  return JSON.stringify({
    actions: [...s.actions],
    proposals: [...s.proposals],
    sessions: [...s.sessions],
    extraTasks: [...s.extraTasks],
    failedOnce: [...s.failedOnce],
    decisions: [...s.decisions],
    seq: s.seq,
  })
}

export function parseState(raw: string | null): MockState | null {
  if (!raw) return null
  try {
    const d = JSON.parse(raw) as Record<string, unknown>
    return {
      actions: new Map(d.actions as []),
      proposals: new Map(d.proposals as []),
      sessions: new Map(d.sessions as []),
      extraTasks: new Map(d.extraTasks as []),
      failedOnce: new Set(d.failedOnce as []),
      decisions: new Map((d.decisions as []) ?? []),
      seq: typeof d.seq === 'number' ? d.seq : 1041,
    }
  } catch {
    return null
  }
}

export function enableMockPersistence(): void {
  persistEnabled = true
  try {
    state = parseState(localStorage.getItem(MOCK_STORAGE_KEY)) ?? freshState()
  } catch {
    state = freshState()
  }
}

export function saveState(): void {
  if (!persistEnabled) return
  try {
    localStorage.setItem(MOCK_STORAGE_KEY, serializeState(state))
  } catch {
    /* storage unavailable: the mock db lives in memory only */
  }
}

export function clearMockStorage(): void {
  try {
    localStorage.removeItem(MOCK_STORAGE_KEY)
  } catch {
    /* ignore */
  }
}

export function userByToken(header: string | null): User | null {
  const login = header?.replace(/^Bearer mock-token-/, '')
  return MOCK_USERS.find((u) => u.login === login) ?? null
}

export function uuid(): string {
  return crypto.randomUUID()
}
