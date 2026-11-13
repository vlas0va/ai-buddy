import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { errorMessage } from '@/api/client'
import { proposeGoals, saveGoals, type GoalInput } from '@/api/plan'
import type { Goal } from '@/api/schemas'
import { Badge, type BadgeVariant } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'

type Status = NonNullable<GoalInput['status']>

interface Props {
  internId: string
  goals: Goal[]
  editable: boolean
  planKey: readonly unknown[]
  /** стажёр может записать цели, согласованные с руководителем (они ждут утверждения) */
  canPropose?: boolean
}

const STATUS: Record<Status, { text: string; variant: BadgeVariant }> = {
  proposed: { text: 'Ждёт согласования', variant: 'waiting_approval' },
  active: { text: 'В работе', variant: 'in_progress' },
  achieved: { text: 'Достигнута', variant: 'done' },
  missed: { text: 'Не достигнута', variant: 'overdue' },
}

const toInput = (g: Goal): GoalInput => ({ title: g.title, criteria: g.criteria ?? '', status: g.status })
const clean = (list: GoalInput[]) =>
  list
    .filter((g) => g.title.trim())
    .map((g) => ({ title: g.title.trim(), ...(g.criteria?.trim() ? { criteria: g.criteria.trim() } : {}), ...(g.status ? { status: g.status } : {}) }))

/**
 * Цели адаптации (CL-01 шаг 4, CL-06; организаторы 12.11): в первую неделю стажёр и руководитель договариваются о целях
 * и критериях успеха; стажёр может их записать, наставник или руководитель утверждает их и отслеживает (в работе →
 * достигнута / не достигнута).
 */
export function GoalsSection({ internId, goals, editable, planKey, canPropose = false }: Props) {
  const qc = useQueryClient()
  const [mode, setMode] = useState<'view' | 'edit' | 'propose'>('view')
  const [draft, setDraft] = useState<GoalInput[]>([])
  const refresh = () => {
    setMode('view')
    void qc.invalidateQueries({ queryKey: planKey })
    void qc.invalidateQueries({ queryKey: ['plan'] })
    void qc.invalidateQueries({ queryKey: ['report'] })
  }
  const save = useMutation({ mutationFn: (list: GoalInput[]) => saveGoals(internId, clean(list)), onSuccess: refresh })
  const propose = useMutation({ mutationFn: () => proposeGoals(clean(draft).map(({ title, criteria }) => ({ title, criteria }))), onSuccess: refresh })
  const busy = save.isPending || propose.isPending

  function start(next: 'edit' | 'propose') {
    setDraft(next === 'edit' && goals.length ? goals.map(toInput) : [{ title: '', criteria: '' }])
    setMode(next)
  }
  const setStatus = (id: string, status: Status) => save.mutate(goals.map((g) => (g.id === id ? { ...toInput(g), status } : toInput(g))))
  const update = (i: number, patch: Partial<GoalInput>) => setDraft((d) => d.map((g, n) => (n === i ? { ...g, ...patch } : g)))
  const done = goals.filter((g) => g.status === 'achieved').length
  const counted = goals.filter((g) => g.status !== 'proposed').length

  return (
    <section className="page-section" aria-label="Цели адаптации">
      <h2 className="plan-period-title">
        Цели адаптации{counted > 0 && <span className="hint"> · достигнуто {done} из {counted}</span>}
      </h2>
      {mode === 'view' && (
        <>
          {goals.length === 0 ? (
            <p className="hint">
              {editable
                ? 'Целей пока нет — задайте 2–3 цели с критериями.'
                : 'Цели обсуждают с руководителем на первой неделе. После встречи запиши их здесь — руководитель или наставник согласует.'}
            </p>
          ) : (
            <ul className="list">
              {goals.map((g) => (
                <li key={g.id}>
                  <Card className="plan-goal">
                    <div className="plan-goal-head">
                      <strong>{g.title}</strong>
                      <Badge variant={STATUS[g.status].variant}>{STATUS[g.status].text}</Badge>
                    </div>
                    {g.criteria && <span className="hint">Критерий: {g.criteria}</span>}
                    {editable && (
                      <div className="plan-goal-actions">
                        {g.status === 'proposed' ? (
                          <Button size="sm" disabled={busy} onClick={() => setStatus(g.id, 'active')}>
                            Согласовать
                          </Button>
                        ) : (
                          <>
                            {g.status !== 'achieved' && (
                              <Button size="sm" variant="secondary" disabled={busy} onClick={() => setStatus(g.id, 'achieved')}>
                                Достигнута
                              </Button>
                            )}
                            {g.status !== 'missed' && (
                              <Button size="sm" variant="ghost" disabled={busy} onClick={() => setStatus(g.id, 'missed')}>
                                Не достигнута
                              </Button>
                            )}
                            {g.status !== 'active' && (
                              <Button size="sm" variant="ghost" disabled={busy} onClick={() => setStatus(g.id, 'active')}>
                                Вернуть в работу
                              </Button>
                            )}
                          </>
                        )}
                      </div>
                    )}
                  </Card>
                </li>
              ))}
            </ul>
          )}
          {save.isError && <p className="ui-field-error">{errorMessage(save.error)}</p>}
          {(editable || canPropose) && (
            <div className="chat-actions">
              {editable && (
                <Button size="sm" variant="secondary" onClick={() => start('edit')}>
                  {goals.length ? 'Изменить цели' : 'Задать цели'}
                </Button>
              )}
              {canPropose && goals.length < 10 && (
                <Button size="sm" variant="secondary" onClick={() => start('propose')}>
                  Записать цели после встречи
                </Button>
              )}
            </div>
          )}
        </>
      )}
      {mode !== 'view' && (
        <Card className="plan-goals-form">
          {mode === 'propose' && (
            <p className="hint">Запиши цели и критерии, о которых договорились с руководителем. Они появятся у него на согласование.</p>
          )}
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
          {(save.isError || propose.isError) && <p className="ui-field-error">{errorMessage(save.error ?? propose.error)}</p>}
          <div className="chat-actions">
            {draft.length < (mode === 'propose' ? 5 : 10) && (
              <Button size="sm" variant="secondary" onClick={() => setDraft((d) => [...d, { title: '', criteria: '' }])}>
                Добавить цель
              </Button>
            )}
            <Button size="sm" loading={busy} onClick={() => (mode === 'propose' ? propose.mutate() : save.mutate(draft))}>
              {mode === 'propose' ? 'Отправить на согласование' : 'Сохранить'}
            </Button>
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => setMode('view')}>
              Отмена
            </Button>
          </div>
        </Card>
      )}
    </section>
  )
}
