import type { ReactNode } from 'react'

export function PageHeader({ title, hint, actions }: { title: string; hint?: string; actions?: ReactNode }) {
  return (
    <header className="page-header">
      <div className="page-header-text">
        <h1>{title}</h1>
        {hint && <p className="hint">{hint}</p>}
      </div>
      {actions && <div className="page-header-actions">{actions}</div>}
    </header>
  )
}
