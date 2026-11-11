import type { Stage } from './events'

export const STAGE_TEXT: Record<Stage, string> = {
  thinking: 'Формулирую ответ…',
  searching_kb: 'Ищу в базе знаний…',
  calling_service: 'Обращаюсь к сервису…',
  saving: 'Сохраняю…',
}

export const ACTION_STATUS_TEXT: Record<string, string> = {
  pending_approval: 'На согласовании',
  approved: 'Согласовано',
  rejected: 'Отклонено',
  granted: 'Доступ выдан',
  created: 'Создано',
  failed: 'Ошибка',
  signed: 'Подписано',
}

export const ACTION_STATUS_VARIANT: Record<string, 'waiting_approval' | 'done' | 'overdue' | 'neutral'> = {
  pending_approval: 'waiting_approval',
  approved: 'done',
  granted: 'done',
  created: 'done',
  signed: 'done',
  rejected: 'overdue',
  failed: 'overdue',
}

export const ROLE_TEXT: Record<string, string> = {
  mentor: 'наставнику',
  lead: 'руководителю',
  hrbp: 'HR BP',
  curator: 'куратору',
}

/** The summary may end with ", согласующий — Имя"; the approver is shown on its own line, so drop it here. */
export function withoutApprover(summary: string): string {
  return summary.replace(/,?\s*согласующий\s*[—-].*$/i, '').trim()
}

export interface Step {
  who: string
  state: 'done' | 'current' | 'waiting' | 'skipped'
  text: string
}

/**
 * Day off goes lead → HR admin → HR BP (organizers' Q&A 10.11). approvals.decide records both HR steps together with the
 * lead's approval, so the chain follows from the action status.
 */
export function absenceSteps(status: string): Step[] {
  if (status === 'approved') {
    return [
      { who: 'Руководитель', state: 'done', text: 'согласовал' },
      { who: 'HR-администратор', state: 'done', text: 'оформил в кадровой системе' },
      { who: 'HR BP', state: 'done', text: 'уведомлён' },
    ]
  }
  if (status === 'rejected') {
    return [
      { who: 'Руководитель', state: 'done', text: 'отклонил' },
      { who: 'HR-администратор', state: 'skipped', text: 'не требуется' },
      { who: 'HR BP', state: 'skipped', text: 'не требуется' },
    ]
  }
  return [
    { who: 'Руководитель', state: 'current', text: 'согласует' },
    { who: 'HR-администратор', state: 'waiting', text: 'оформит после согласования' },
    { who: 'HR BP', state: 'waiting', text: 'получит уведомление' },
  ]
}
