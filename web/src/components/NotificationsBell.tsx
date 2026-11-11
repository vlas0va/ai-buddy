import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { keys } from '@/api/keys'
import { listNotifications, markNotificationRead, type Notification } from '@/api/notifications'
import { Card } from './ui/Card'
import { BellIcon } from './ui/icons'
import '@/plan/plan.css'

/** AI-cron reminders and status changes (R54). Polls once a minute; marking read is optimistic. */
export function NotificationsBell() {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const list = useQuery({ queryKey: keys.notifications, queryFn: listNotifications, refetchInterval: 60_000, refetchOnWindowFocus: true })
  const read = useMutation({
    mutationFn: (id: string) => markNotificationRead(id),
    onMutate: (id) => {
      const before = qc.getQueryData<Notification[]>(keys.notifications)
      if (before) qc.setQueryData(keys.notifications, before.map((n) => (n.id === id ? { ...n, read: true } : n)))
      return { before }
    },
    onError: (_e, _id, ctx) => {
      if (ctx?.before) qc.setQueryData(keys.notifications, ctx.before)
    },
  })

  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const items = list.data ?? []
  const unread = items.filter((n) => !n.read).length
  return (
    <div className="notif" ref={box}>
      <button type="button" className="notif-btn" aria-expanded={open} aria-haspopup="true" onClick={() => setOpen((v) => !v)}>
        <BellIcon />
        Уведомления
        {unread > 0 && (
          <span className="notif-count" aria-label={`Непрочитанных: ${unread}`}>
            {unread}
          </span>
        )}
      </button>
      {open && (
        <Card className="notif-panel" role="region" aria-label="Уведомления">
          {list.isError && <p className="ui-field-error">Не удалось загрузить уведомления.</p>}
          {items.length === 0 && !list.isError && <p className="hint">Уведомлений нет.</p>}
          {items.map((n) => (
            <div key={n.id} className={`notif-item${n.read ? '' : ' is-unread'}`}>
              <strong>{n.title}</strong>
              {n.body && <p>{n.body}</p>}
              {!n.read && (
                <button type="button" onClick={() => read.mutate(n.id)}>
                  Прочитано
                </button>
              )}
            </div>
          ))}
        </Card>
      )}
    </div>
  )
}
