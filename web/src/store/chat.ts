import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'
import { safeStorage } from './session'

// Current chat session id per user, so a reload restores the conversation instead of starting a new one.
interface ChatStore {
  sessions: Record<string, string>
  setSession: (userId: string, sessionId: string) => void
  clearSession: (userId: string) => void
}

export const useChatStore = create<ChatStore>()(
  persist(
    (set) => ({
      sessions: {},
      setSession: (userId, sessionId) => set((s) => ({ sessions: { ...s.sessions, [userId]: sessionId } })),
      clearSession: (userId) =>
        set((s) => {
          const rest = { ...s.sessions }
          delete rest[userId]
          return { sessions: rest }
        }),
    }),
    { name: 'ai-buddy-chat', storage: createJSONStorage(() => safeStorage) },
  ),
)
