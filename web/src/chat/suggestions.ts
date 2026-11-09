export interface SuggestionGroup {
  title: string
  items: string[]
}

export const SUGGESTION_GROUPS: SuggestionGroup[] = [
  { title: 'Отсутствия', items: ['Хочу взять отгул в пятницу', 'Как оформить отгул?'] },
  { title: 'Доступы', items: ['Какие доступы мне нужны?', 'Добавь меня в каналы команды'] },
  { title: 'Команда', items: ['Кто мой наставник?', 'Что мне сделать на этой неделе?'] },
]

/** Case-insensitive filter by the typed text; empty groups are dropped. */
export function filterSuggestions(query: string): SuggestionGroup[] {
  const q = query.trim().toLowerCase()
  return SUGGESTION_GROUPS.map((g) => ({ ...g, items: g.items.filter((i) => i.toLowerCase().includes(q)) })).filter((g) => g.items.length > 0)
}
