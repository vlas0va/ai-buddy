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
import { askLabel, askText, canToggle, CONFIRM_HINT, NEXT_STEP_PROMPT, nextStatus, progress, shortDate, withTaskStatus } from './format'
import { GoalsSection } from './GoalsSection'
import './plan.css'

interface Props {
  /** стажёр, план которого смотрит наставник или руководитель; не передаётся для собственного плана вызывающего */
  userId?: string
  /** наставник и руководитель могут редактировать цели стажёра (CL-01 шаг 4, CL-06) */
  canEditGoals?: boolean
}

export function PlanView({ userId, canEditGoals = false }: Props) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const ask = useAsk((s) => s.ask)
  const key = keys.plan(userId)
  const plan = useQuery({ queryKey: key, queryFn: () => getPlan(userId) })
  const readOnly = Boolean(userId)

  // оптимистично: галочка переключается сразу (R76 < 1 с), затем приходит ответ сервера или откат. Задача с
  // подтверждением наставника, отмеченная выполненной, сразу показывает «ждёт» (организаторы 12.13); сервер решает так
  // же
  const toggle = useMutation({
    mutationFn: ({ id, status }: { id: string; status: EditableStatus | 'waiting_approval' }) =>
      updateTaskStatus(id, status === 'waiting_approval' ? 'done' : status),
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
  // наставник или руководитель подтверждает задачу, которую отметил стажёр (или возвращает её в работу)
  const review = useMutation({
    mutationFn: ({ id, status }: { id: string; status: 'done' | 'in_progress' }) => updateTaskStatus(id, status),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: key })
      void qc.invalidateQueries({ queryKey: ['report'] })
    },
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
                        canReview={canEditGoals}
                        reviewing={review.isPending}
                        onReview={(status) => review.mutate({ id: t.id, status })}
                        onToggle={(status) => toggle.mutate({ id: t.id, status })}
                        onToggleSub={(id, status) => toggle.mutate({ id, status })}
                        onAsk={() => askBuddy(askText(t))}
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
            canPropose={!userId && data.plan_role === 'intern'}
          />
        </div>
      )}
    </QueryState>
  )
}

function TaskRow({
  task,
  readOnly,
  canReview = false,
  reviewing = false,
  onReview,
  onToggle,
  onToggleSub,
  onAsk,
}: {
  task: Task
  readOnly: boolean
  canReview?: boolean
  reviewing?: boolean
  onReview?: (s: 'done' | 'in_progress') => void
  onToggle: (s: EditableStatus | 'waiting_approval') => void
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
          onChange={() => onToggle(nextStatus(task))}
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
            {task.confirm && CONFIRM_HINT[task.confirm] && !done && <span>{CONFIRM_HINT[task.confirm]}</span>}
          </span>
        </div>
        <Badge variant={task.status}>
          {task.confirm === 'mentor' && task.status === 'waiting_approval' ? 'Ждёт подтверждения' : STATUS_LABELS[task.status]}
        </Badge>
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
          {askLabel(task)}
        </button>
      )}
      {canReview && task.confirm === 'mentor' && task.status === 'waiting_approval' && onReview && (
        <div className="plan-goal-actions">
          <Button size="sm" disabled={reviewing} onClick={() => onReview('done')}>
            Подтвердить выполнение
          </Button>
          <Button size="sm" variant="ghost" disabled={reviewing} onClick={() => onReview('in_progress')}>
            Вернуть в работу
          </Button>
        </div>
      )}
    </Card>
  )
}
