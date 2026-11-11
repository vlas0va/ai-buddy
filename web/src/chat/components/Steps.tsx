import type { Step } from '../statusText'

const MARK: Record<Step['state'], string> = { done: '✓', waiting: '…', skipped: '—' }

/** Approval chain of a request, e.g. lead → HR admin → HR BP for a day off. */
export function Steps({ steps }: { steps: Step[] }) {
  return (
    <ol className="chat-steps" aria-label="Шаги согласования">
      {steps.map((s) => (
        <li key={s.who} className={`is-${s.state}`}>
          <span aria-hidden="true">{MARK[s.state]} </span>
          {s.who}: {s.text}
        </li>
      ))}
    </ol>
  )
}
