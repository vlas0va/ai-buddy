import type { ReactNode } from 'react'

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="ui-state ui-state-empty">{children}</div>
}
