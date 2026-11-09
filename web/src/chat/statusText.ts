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
}

export const ACTION_STATUS_VARIANT: Record<string, 'waiting_approval' | 'done' | 'overdue' | 'neutral'> = {
  pending_approval: 'waiting_approval',
  approved: 'done',
  granted: 'done',
  created: 'done',
  rejected: 'overdue',
  failed: 'overdue',
}

export const ROLE_TEXT: Record<string, string> = {
  mentor: 'наставнику',
  lead: 'руководителю',
  hrbp: 'HR BP',
  curator: 'куратору',
}
