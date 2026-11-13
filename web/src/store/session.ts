import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'
import type { User } from '@/api/schemas'

interface SessionState {
  token: string | null
  user: User | null
  setSession: (token: string, user: User) => void
  clear: () => void
}

export const safeStorage = {
  getItem: (name: string) => {
    try {
      return localStorage.getItem(name)
    } catch {
      return null
    }
  },
  setItem: (name: string, value: string) => {
    try {
      localStorage.setItem(name, value)
    } catch {
      /* хранилище недоступно: сессия живёт только в памяти */
    }
  },
  removeItem: (name: string) => {
    try {
      localStorage.removeItem(name)
    } catch {
      /* игнорируем */
    }
  },
}

export const useSession = create<SessionState>()(
  persist(
    (set) => ({
      token: null,
      user: null,
      setSession: (token, user) => set({ token, user }),
      clear: () => set({ token: null, user: null }),
    }),
    { name: 'ai-buddy-session', storage: createJSONStorage(() => safeStorage) },
  ),
)
