export function PageHeader({ title, hint }: { title: string; hint?: string }) {
  return (
    <header className="page-header">
      <h1>{title}</h1>
      {hint && <p className="hint">{hint}</p>}
    </header>
  )
}
