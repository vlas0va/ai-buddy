import type { Role } from '@/api/schemas'

export const ROLE_LABELS: Record<Role, string> = {
  intern: 'Стажёр',
  mentor: 'Наставник',
  lead: 'Руководитель',
  hrbp: 'HR BP',
  alumni: 'Выпускник',
}
