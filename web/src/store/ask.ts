import { create } from 'zustand'

/** A question queued from another page (plan «Спросить buddy», next step) that the chat sends once it is ready. */
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
