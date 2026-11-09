export const keys = {
  me: ['me'] as const,
  plan: (userId?: string) => ['plan', userId ?? 'self'] as const,
  actions: ['actions'] as const,
  approvals: ['approvals'] as const,
}
