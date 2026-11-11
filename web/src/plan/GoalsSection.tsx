import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { errorMessage } from '@/api/client'
import { saveGoals, type GoalInput } from '@/api/plan'
import type { Goal } from '@/api/schemas'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'

interface Props {
  internId: string
  goals: Goal[]
  editable: boolean
  planKey: readonly unknown[]
}

/** Adaptation goals: the mentor or lead writes them (CL-01 step 4, CL-06), the intern sees them in the plan. */
export function GoalsSection({ internId, goals, editable, planKey }: Props) {
  const qc = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<GoalInput[]>([])
  const save = useMutation({
    mutationFn: () => saveGoals(internId, draft.filter((g) => g.title.trim()).map((g) => ({ title: g.title.trim(), ...(g.criteria?.trim() ? { criteria: g.criteria.trim() } : {}) }))),
    onSuccess: () => {
      setEditing(false)
      void qc.invalidateQueries({ queryKey: planKey })
      void qc.invalidateQueries({ queryKey: ['plan'] })
    },
  })

  function start() {
    setDraft(goals.length ? goals.map((g) => ({ title: g.title, criteria: g.criteria ?? '' })) : [{ title: '', criteria: '' }])
    setEditing(true)
  }
  const update = (i: number, patch: Partial<GoalInput>) => setDraft((d) => d.map((g, n) => (n === i ? { ...g, ...patch } : g)))

  return (
    <section className="page-section" aria-label="Цели адаптации">
      <h2 className="plan-period-title">Цели адаптации</h2>
      {!editing && (
        <>
          {goals.length === 0 ? (
            <p className="hint">{editable ? 'Целей пока нет — задайте 2–3 цели с критериями.' : 'Цели пока не заданы — их ставят наставник и руководитель.'}</p>
          ) : (
            <ul className="list">
              {goals.map((g) => (
                <li key={g.id}>
                  <Card className="plan-goal">
                    <strong>{g.title}</strong>
                    {g.criteria && <span className="hint">Критерий: {g.criteria}</span>}
                  </Card>
                </li>
              ))}
            </ul>
          )}
          {editable && (
            <div className="chat-actions">
              <Button size="sm" variant="secondary" onClick={start}>
                {goals.length ? 'Изменить цели' : 'Задать цели'}
              </Button>
            </div>
          )}
        </>
      )}
      {editing && (
        <Card className="plan-goals-form">
          {draft.map((g, i) => (
            <div key={i} className="plan-goal-edit">
              <input
                className="ui-input"
                placeholder="Цель, например: самостоятельно выкатить задачу в прод"
                value={g.title}
                maxLength={300}
                onChange={(e) => update(i, { title: e.target.value })}
                aria-label={`Цель ${i + 1}`}
              />
              <input
                className="ui-input"
                placeholder="Критерий выполнения"
                value={g.criteria ?? ''}
                maxLength={1000}
                onChange={(e) => update(i, { criteria: e.target.value })}
                aria-label={`Критерий ${i + 1}`}
              />
              <Button size="sm" variant="ghost" onClick={() => setDraft((d) => d.filter((_, n) => n !== i))}>
                Удалить
              </Button>
            </div>
          ))}
          {save.isError && <p className="ui-field-error">{errorMessage(save.error)}</p>}
          <div className="chat-actions">
            {draft.length < 10 && (
              <Button size="sm" variant="secondary" onClick={() => setDraft((d) => [...d, { title: '', criteria: '' }])}>
                Добавить цель
              </Button>
            )}
            <Button size="sm" loading={save.isPending} onClick={() => save.mutate()}>
              Сохранить
            </Button>
            <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => setEditing(false)}>
              Отмена
            </Button>
          </div>
        </Card>
      )}
    </section>
  )
}
