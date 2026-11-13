import { create } from 'zustand'

/**
 * Вопрос, поставленный в очередь с другой страницы (план «Спросить buddy», следующий шаг), который чат отправит, как
 * только будет готов.
 */
interface AskStore {
  pending: string | null
  ask: (text: string) => void
  take: () => string | null
}

export const useAsk = create<AskStore>()((set, get) => ({
  pending: null,
  ask: (text) => set({ pending: text }),
  take: () => {
    const text = get().pending
    if (text !== null) set({ pending: null })
    return text
  },
}))
