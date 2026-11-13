import type { Stage } from './events'

export const STAGE_TEXT: Record<Stage, string> = {
  thinking: 'Формулирую ответ…',
  searching_kb: 'Ищу в базе знаний…',
  calling_service: 'Обращаюсь к сервису…',
  saving: 'Сохраняю…',
}

export const ACTION_STATUS_TEXT: Record<string, string> = {
  pending_approval: 'На согласовании',
  pending_hr: 'Ждёт HR-администратора',
  approved: 'Согласовано',
  rejected: 'Отклонено',
  granted: 'Доступ выдан',
  created: 'Создано',
  failed: 'Ошибка',
  signed: 'Подписано',
  cancelled: 'Отменена',
}

export const ACTION_STATUS_VARIANT: Record<string, 'waiting_approval' | 'done' | 'overdue' | 'neutral'> = {
  pending_approval: 'waiting_approval',
  pending_hr: 'waiting_approval',
  approved: 'done',
  granted: 'done',
  created: 'done',
  signed: 'done',
  rejected: 'overdue',
  failed: 'overdue',
  cancelled: 'neutral',
}

export const ROLE_TEXT: Record<string, string> = {
  mentor: 'наставнику',
  lead: 'руководителю',
  hrbp: 'HR BP',
  curator: 'куратору',
}

/**
 * Сводка может заканчиваться на «, согласующий — Имя»; согласующий показывается отдельной строкой, поэтому здесь
 * убираем.
 */
export function withoutApprover(summary: string): string {
  return summary.replace(/,?\s*согласующий\s*[—-].*$/i, '').trim()
}

export interface Step {
  who: string
  state: 'done' | 'current' | 'waiting' | 'skipped'
  text: string
}

/**
 * Заявка на отсутствие идёт руководитель → HR-администратор → HR BP (ответы организаторов 10.11, чекпоинт 11.11):
 * руководитель согласует, затем HR-администратор согласует и оформляет, HR BP получает уведомление. Цепочка следует из
 * статуса действия.
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
  if (status === 'pending_hr') {
    return [
      { who: 'Руководитель', state: 'done', text: 'согласовал' },
      { who: 'HR-администратор', state: 'current', text: 'согласует; до этого даты можно перенести' },
      { who: 'HR BP', state: 'waiting', text: 'получит уведомление' },
    ]
  }
  return [
    { who: 'Руководитель', state: 'current', text: 'согласует' },
    { who: 'HR-администратор', state: 'waiting', text: 'согласует после руководителя' },
    { who: 'HR BP', state: 'waiting', text: 'получит уведомление' },
  ]
}
