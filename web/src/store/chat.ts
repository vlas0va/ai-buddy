import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'
import { safeStorage } from './session'

// Текущий id сессии чата для каждого пользователя, чтобы перезагрузка восстанавливала разговор, а не начинала новый.
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
