import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { Proposal } from '@/chat/events'
import type { ProposalState } from '../stream'

type Item = Proposal & { state: ProposalState }

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

const LEVEL_TEXT: Record<string, string> = { read: 'чтение', write: 'запись', admin: 'администрирование' }

/** One label per draft: «Jira: проект DLV · чтение · stage». */
function itemLabel(p: Item): string {
  const { args, display } = p.draft
  const name = str(display?.resource_name) || str(args.resource_id)
  const level = str(display?.level_label) || LEVEL_TEXT[str(args.level)] || ''
  return [name, level, str(args.environment)].filter(Boolean).join(' · ')
}

function needsApproval(p: Item): boolean {
  return Boolean(p.draft.display?.elevated)
}

interface Props {
  proposals: Item[]
  disabled?: boolean
  /** sends the chosen drafts one after another: plain reading is granted at once, the rest goes to the lead */
  onConfirmMany: (proposalIds: string[]) => void
  onCancel: (proposalId: string) => void
}

/** Feedback 12.11: the missing accesses are one list with checkboxes instead of a card per access. */
export function AccessBundleCard({ proposals, disabled, onConfirmMany, onCancel }: Props) {
  const open = proposals.filter((p) => p.state === 'pending')
  const [chosen, setChosen] = useState<Set<string>>(() => new Set(open.filter((p) => !needsApproval(p)).map((p) => p.proposal_id)))
  const busy = proposals.some((p) => p.state === 'confirming')
  const selected = open.filter((p) => chosen.has(p.proposal_id))
  const toggle = (id: string) =>
    setChosen((s) => {
      const next = new Set(s)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  return (
    <Card className="chat-card" aria-label="Набор заявок на доступ">
      <h3>Недостающие доступы</h3>
      <ul className="bundle-list">
        {proposals.map((p) => {
          const pending = p.state === 'pending'
          return (
            <li key={p.proposal_id} className="bundle-item">
              <label>
                <input
                  type="checkbox"
                  checked={pending ? chosen.has(p.proposal_id) : p.state !== 'cancelled'}
                  disabled={!pending || busy || disabled}
                  onChange={() => toggle(p.proposal_id)}
                  aria-label={itemLabel(p)}
                />
                <span className="bundle-name">{itemLabel(p)}</span>
              </label>
              <span className={`hint ${needsApproval(p) ? 'bundle-approval' : ''}`}>
                {p.state === 'confirmed'
                  ? 'отправлено'
                  : p.state === 'cancelled'
                    ? 'отменено'
                    : p.state === 'confirming'
                      ? 'отправляю…'
                      : needsApproval(p)
                        ? `согласует ${str(p.draft.display?.approver_display_name) || str(p.draft.display?.approver_label) || 'руководитель'}`
                        : 'выдаётся сразу'}
              </span>
            </li>
          )
        })}
      </ul>
      {open.length > 0 ? (
        <div className="chat-actions">
          <Button
            loading={busy}
            disabled={selected.length === 0 || busy || disabled}
            onClick={() => onConfirmMany(selected.map((p) => p.proposal_id))}
          >
            Запросить выбранные ({selected.length})
          </Button>
          <Button variant="secondary" disabled={busy || disabled} onClick={() => open.forEach((p) => onCancel(p.proposal_id))}>
            Отменить остальные
          </Button>
        </div>
      ) : (
        <p className="hint">Готово: статусы — в «Мои заявки».</p>
      )}
    </Card>
  )
}
