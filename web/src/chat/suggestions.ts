export interface SuggestionGroup {
  title: string
  items: string[]
}

export type SuggestionRole = 'intern' | 'mentor' | 'lead' | 'hrbp' | 'alumni' | 'hr_admin'

/**
 * Подсказки по ролям: только запросы, которые агент обрабатывает для этой роли (чекпоинт 11.11: у руководителя и
 * наставника они другие, чем у стажёра).
 */
export const SUGGESTIONS: Record<SuggestionRole, SuggestionGroup[]> = {
  intern: [
    { title: 'Отсутствия', items: ['Хочу взять отгул в пятницу', 'Как оформить отгул?'] },
    { title: 'Доступы', items: ['Какие доступы мне нужны?', 'В какие каналы вступить?'] },
    { title: 'Команда', items: ['Кто мой наставник?', 'Что мне сделать на этой неделе?'] },
  ],
  mentor: [
    { title: 'Стажёры', items: ['Как дела у моих стажёров?', 'Подготовь меня к 1:1 со стажёром'] },
    { title: 'Наставничество', items: ['Как давать обратную связь стажёру?', 'Что входит в работу наставника?'] },
  ],
  lead: [
    { title: 'Согласования', items: ['Покажи заявки на согласование', 'Как согласовывать заявки стажёров?'] },
    { title: 'Стажёры', items: ['Как дела у моих стажёров?', 'Подготовь меня к 1:1 со стажёром'] },
  ],
  hrbp: [
    { title: 'Стажировка', items: ['Как оформляется больничный стажёра?', 'Куда стажёру отправлять документы?'] },
    { title: 'Контакты', items: ['Кто куратор стажировки?'] },
  ],
  hr_admin: [
    { title: 'Согласования', items: ['Покажи заявки на согласование', 'Как оформляется отпуск?'] },
  ],
  alumni: [
    { title: 'Работа', items: ['Как оформить отгул?', 'Где лежат документы команды?', 'К кому идти с вопросом?'] },
  ],
}

/** @deprecated оставлено для старых импортов: список для стажёра */
export const SUGGESTION_GROUPS = SUGGESTIONS.intern

/** Фильтр по введённому тексту без учёта регистра; пустые группы убираются. */
export function filterSuggestions(query: string, role: string = 'intern'): SuggestionGroup[] {
  const q = query.trim().toLowerCase()
  const groups = SUGGESTIONS[role as SuggestionRole] ?? SUGGESTIONS.intern
  return groups.map((g) => ({ ...g, items: g.items.filter((i) => i.toLowerCase().includes(q)) })).filter((g) => g.items.length > 0)
}
