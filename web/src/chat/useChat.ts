import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { cancelProposal, createSession, getSession } from '@/api/chat'
import { ApiError, errorMessage, isRetryable } from '@/api/client'
import { keys } from '@/api/keys'
import { useChatStore } from '@/store/chat'
import { useSession } from '@/store/session'
import type { StreamEvent } from './events'
import { streamPost } from './sse'
import {
  fromHistory,
  initialState,
  reduce,
  restartTurn,
  setProposalState,
  startConfirm,
  startTurn,
  type ChatState,
} from './stream'

export function useChat() {
  const qc = useQueryClient()
  const userId = useSession((s) => s.user?.id ?? 'anonymous')
  const [state, setState] = useState<ChatState>(initialState)
  const [restoring, setRestoring] = useState(() => Boolean(useChatStore.getState().sessions[userId]))
  const streamingRef = useRef(false)
  const sessionRef = useRef<Promise<string> | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const inflight = useRef(new Set<string>())

  useEffect(() => () => abortRef.current?.abort(), [])

  // restore the previous conversation; a missing session (404) or any failure falls back to a new one
  useEffect(() => {
    const stored = useChatStore.getState().sessions[userId]
    if (!stored) return
    let cancelled = false
    sessionRef.current = Promise.resolve(stored)
    getSession(stored)
      .then((h) => {
        if (!cancelled) setState(fromHistory(h))
      })
      .catch((e: unknown) => {
        sessionRef.current = null
        if (e instanceof ApiError && e.status === 404) useChatStore.getState().clearSession(userId)
      })
      .finally(() => {
        if (!cancelled) setRestoring(false)
      })
    return () => {
      cancelled = true
    }
  }, [userId])

  const ensureSession = useCallback(() => {
    if (!sessionRef.current) {
      sessionRef.current = createSession()
        .then((r) => {
          useChatStore.getState().setSession(userId, r.session_id)
          return r.session_id
        })
        .catch((e: unknown) => {
          sessionRef.current = null
          throw e
        })
    }
    return sessionRef.current
  }, [userId])

  const apply = useCallback(
    (event: StreamEvent) => {
      setState((s) => reduce(s, event))
      if (event.type === 'action_result') {
        void qc.invalidateQueries({ queryKey: ['plan'] })
        void qc.invalidateQueries({ queryKey: keys.actions })
      }
    },
    [qc],
  )

  const run = useCallback(
    async (open: (sessionId: string, signal: AbortSignal) => AsyncGenerator<StreamEvent>) => {
      abortRef.current?.abort()
      const ctrl = new AbortController()
      abortRef.current = ctrl
      streamingRef.current = true
      let finished = false
      try {
        const sessionId = await ensureSession()
        for await (const ev of open(sessionId, ctrl.signal)) {
          if (ctrl.signal.aborted) return
          apply(ev)
          if (ev.type === 'done' || ev.type === 'error') finished = true
        }
        if (!finished && !ctrl.signal.aborted) apply({ type: 'done', data: {} })
      } catch (e) {
        if (ctrl.signal.aborted || (e instanceof DOMException && e.name === 'AbortError')) return
        apply({
          type: 'error',
          data: {
            code: e instanceof ApiError ? e.code : 'internal',
            message: errorMessage(e),
            retryable: isRetryable(e),
          },
        })
      } finally {
        if (abortRef.current === ctrl) streamingRef.current = false
      }
    },
    [apply, ensureSession],
  )

  const send = useCallback(
    (text: string) => {
      const trimmed = text.trim()
      if (!trimmed || streamingRef.current) return
      streamingRef.current = true
      setState((s) => startTurn(s, trimmed))
      void run((sid, signal) => streamPost(`/api/chat/sessions/${sid}/messages`, { text: trimmed }, signal))
    },
    [run],
  )

  const retry = useCallback(
    (messageId: string, text: string) => {
      if (streamingRef.current) return
      streamingRef.current = true
      setState((s) => restartTurn(s, messageId))
      void run((sid, signal) => streamPost(`/api/chat/sessions/${sid}/messages`, { text }, signal))
    },
    [run],
  )

  const confirm = useCallback(
    (proposalId: string) => {
      // idempotency on the client: one in-flight confirm per proposal_id; the server replays a stored result (R66, R79)
      if (inflight.current.has(proposalId) || streamingRef.current) return
      inflight.current.add(proposalId)
      streamingRef.current = true
      setState((s) => startConfirm(s, proposalId))
      void run((sid, signal) => streamPost(`/api/chat/sessions/${sid}/proposals/${proposalId}/confirm`, undefined, signal)).finally(
        () => inflight.current.delete(proposalId),
      )
    },
    [run],
  )

  const cancel = useCallback(async (proposalId: string) => {
    if (inflight.current.has(proposalId)) return
    try {
      const sid = await ensureSession()
      await cancelProposal(sid, proposalId)
      setState((s) => setProposalState(s, proposalId, 'cancelled'))
    } catch (e) {
      setState((s) => ({
        ...s,
        messages: [
          ...s.messages,
          {
            id: `cancel-${proposalId}`,
            author: 'assistant',
            text: '',
            sources: [],
            proposal: null,
            result: null,
            escalation: null,
            refusal: null,
            error: { code: 'internal', message: errorMessage(e), retryable: false },
          },
        ],
      }))
    }
  }, [ensureSession])

  const newDialog = useCallback(() => {
    abortRef.current?.abort()
    streamingRef.current = false
    sessionRef.current = null
    inflight.current.clear()
    useChatStore.getState().clearSession(userId)
    setState(initialState)
  }, [userId])

  return { state, restoring, send, retry, confirm, cancel, newDialog }
}
