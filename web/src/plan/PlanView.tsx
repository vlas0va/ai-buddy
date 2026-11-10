import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { errorMessage } from '@/api/client'
import { keys } from '@/api/keys'
import { getPlan, updateTaskStatus, type EditableStatus } from '@/api/plan'
import type { Plan, Task } from '@/api/schemas'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { STATUS_LABELS } from '@/components/ui/badgeText'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { useAsk } from '@/store/ask'
import { canToggle, NEXT_STEP_PROMPT, progress, shortDate, withTaskStatus } from './format'
import { GoalsSection } from './GoalsSection'
import './plan.css'

interface Props {
  /** intern whose plan a mentor or lead is viewing; omitted for the caller's own plan */
  userId?: string
  /** mentor and lead may edit goals of the intern (CL-01 step 4, CL-06) */
  canEditGoals?: boolean
}

export function PlanView({ userId, canEditGoals = false }: Props) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const ask = useAsk((s) => s.ask)
  const key = keys.plan(userId)
  const plan = useQuery({ queryKey: key, queryFn: () => getPlan(userId) })
  const readOnly = Boolean(userId)

  // optimistic: the checkbox flips at once (R76 < 1 s), the server answer or a rollback follows
  const toggle = useMutation({
    mutationFn: ({ id, status }: { id: string; status: EditableStatus }) => updateTaskStatus(id, status),
    onMutate: async ({ id, status }) => {
      await qc.cancelQueries({ queryKey: key })
      const before = qc.getQueryData<Plan>(key)
      if (before) qc.setQueryData<Plan>(key, withTaskStatus(before, id, status))
      return { before }
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.before) qc.setQueryData(key, ctx.before)
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: key }),
  })

  function askBuddy(text: string) {
    ask(text)
    void navigate({ to: '/chat' })
  }

  const data = plan.data
  const stats = data ? progress(data) : null
  const currentIndex = data ? data.periods.findIndex((p) => p.id === data.current_period) : -1

  return (
    <QueryState
      isLoading={plan.isPending}
      error={plan.error}
      isEmpty={!!data && data.periods.every((p) => p.tasks.length === 0)}
      emptyText="План пока пуст — его собирает наставник."
      onRetry={() => void plan.refetch()}
    >
      {data && stats && (
        <div className="plan">
          <Card className="plan-summary">
            <div className="plan-progress-text">
              <strong>
                Готово {stats.done} из {stats.total}
              </strong>
              {stats.overdue > 0 && <Badge variant="overdue">Просрочено: {stats.overdue}</Badge>}
            </div>
            <div
              className="plan-progress"
              role="progressbar"
              aria-label="Прогресс плана"
              aria-valuemin={0}
              aria-valuemax={stats.total}
              aria-valuenow={stats.done}
            >
              <span style={{ width: `${stats.total ? Math.round((stats.done / stats.total) * 100) : 0}%` }} />
            </div>
            <ol className="plan-timeline" aria-label="Этапы стажировки">
              {data.periods.map((p, i) => (
                <li
                  key={p.id}
                  className={i < currentIndex ? 'is-past' : i === currentIndex ? 'is-current' : undefined}
                  aria-current={i === currentIndex ? 'step' : undefined}
                >
                  {p.label}
                </li>
              ))}
            </ol>
          </Card>

          {data.next_step && !readOnly && (
            <Card className="plan-next" aria-label="Следующий шаг">
              <span className="next-step-label">Следующий шаг</span>
              <span className="next-step-title">{data.next_step.title}</span>
              <div className="chat-actions">
                {data.next_step.action === 'mark_done' ? (
                  <Button
                    size="sm"
                    onClick={() => toggle.mutate({ id: data.next_step!.task_id, status: 'done' })}
                  >
                    Отметить выполненным
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    onClick={() =>
                      askBuddy(
                        data.next_step!.prompt ??
                          (NEXT_STEP_PROMPT[data.next_step!.action] ?? NEXT_STEP_PROMPT.ask_buddy!)(data.next_step!.title),
                      )
                    }
                  >
                    Спросить buddy
                  </Button>
                )}
              </div>
            </Card>
          )}

          {toggle.isError && (
            <p className="ui-field-error" role="alert">
              {errorMessage(toggle.error)} Статус возвращён.
            </p>
          )}

          {data.periods.map((p) => (
            <section key={p.id} className="page-section" aria-label={p.label}>
              <h2 className="plan-period-title">
                {p.label}
                {p.id === data.current_period && <Badge variant="in_progress">Сейчас</Badge>}
              </h2>
              {p.tasks.length === 0 ? (
                <p className="hint">Задач на этом этапе нет.</p>
              ) : (
                <ul className="list">
                  {p.tasks.map((t) => (
                    <li key={t.id}>
                      <TaskRow
                        task={t}
                        readOnly={readOnly}
                        onToggle={(status) => toggle.mutate({ id: t.id, status })}
                        onToggleSub={(id, status) => toggle.mutate({ id, status })}
                        onAsk={() => askBuddy(`Как выполнить задачу «${t.title}»?`)}
                      />
                    </li>
                  ))}
                </ul>
              )}
            </section>
          ))}

          <GoalsSection
            internId={data.intern_id ?? data.user_id}
            goals={data.goals}
            editable={canEditGoals}
            planKey={key}
          />
        </div>
      )}
    </QueryState>
  )
}

function TaskRow({
  task,
  readOnly,
  onToggle,
  onToggleSub,
  onAsk,
}: {
  task: Task
  readOnly: boolean
  onToggle: (s: EditableStatus) => void
  onToggleSub: (id: string, s: EditableStatus) => void
  onAsk: () => void
}) {
  const done = task.status === 'done'
  const editable = !readOnly && canToggle(task)
  return (
    <Card className={`plan-task${done ? ' is-done' : ''}`}>
      <div className="plan-task-main">
        <input
          type="checkbox"
          className="plan-check"
          checked={done}
          disabled={!editable}
          onChange={() => onToggle(done ? 'in_progress' : 'done')}
          aria-label={`${done ? 'Вернуть в работу' : 'Отметить выполненной'}: ${task.title}`}
        />
        <div className="plan-task-text">
          <span className="plan-task-title">{task.title}</span>
          <span className="plan-task-meta">
            {task.checklist_item_id && <span>{task.checklist_item_id}</span>}
            {task.due_at && (
              <span className={task.status === 'overdue' ? 'is-overdue' : undefined}>до {shortDate(task.due_at)}</span>
            )}
            {task.human_only && <span>делаешь вместе с командой — buddy напомнит и поможет подготовиться</span>}
            {task.source === 'action' && <span>заявка, статус обновится после решения</span>}
          </span>
        </div>
        <Badge variant={task.status}>{STATUS_LABELS[task.status]}</Badge>
      </div>
      {task.subtasks && task.subtasks.length > 0 && (
        <ul className="plan-subtasks">
          {task.subtasks.map((s) => {
            const sDone = s.status === 'done'
            return (
              <li key={s.id}>
                <label>
                  <input
                    type="checkbox"
                    className="plan-check"
                    checked={sDone}
                    disabled={readOnly || !canToggle(s)}
                    onChange={() => onToggleSub(s.id, sDone ? 'in_progress' : 'done')}
                  />
                  <span className={sDone ? 'is-done' : undefined}>{s.title}</span>
                </label>
              </li>
            )
          })}
        </ul>
      )}
      {!readOnly && !done && (
        <button type="button" className="plan-ask" onClick={onAsk}>
          Спросить buddy
        </button>
      )}
    </Card>
  )
}
