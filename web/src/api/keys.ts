export const keys = {
  me: ['me'] as const,
  plan: (userId?: string) => ['plan', userId ?? 'self'] as const,
  actions: ['actions'] as const,
  approvals: ['approvals'] as const,
  interns: ['interns'] as const,
  escalations: ['escalations'] as const,
  notifications: ['notifications'] as const,
}
