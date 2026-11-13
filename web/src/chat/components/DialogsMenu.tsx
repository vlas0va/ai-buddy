import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { listSessions } from '@/api/chat'
import { keys } from '@/api/keys'
import { Button } from '@/components/ui/Button'

const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })
}

/** Отзыв 12.11: прежние диалоги сохраняются, их можно открыть снова, чтобы найти старые ответы и заявки. */
export function DialogsMenu({ current, onOpen }: { current?: string; onOpen: (sessionId: string) => void }) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const list = useQuery({ queryKey: keys.chatSessions, queryFn: listSessions, enabled: open })
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
  return (
    <div className="dialogs" ref={box}>
      <Button variant="secondary" size="sm" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        Мои диалоги
      </Button>
      {open && (
        <div className="dialogs-panel" role="menu" aria-label="Мои диалоги">
          {list.isPending && <p className="hint">Загружаю…</p>}
          {list.isError && <p className="ui-field-error">Не удалось загрузить диалоги.</p>}
          {list.data?.length === 0 && <p className="hint">Здесь появятся прошлые диалоги.</p>}
          {list.data?.map((s) => (
            <button
              key={s.session_id}
              type="button"
              role="menuitem"
              className={`dialogs-item${s.session_id === current ? ' is-current' : ''}`}
              onClick={() => {
                setOpen(false)
                if (s.session_id !== current) onOpen(s.session_id)
              }}
            >
              <span className="dialogs-title">{s.title}</span>
              <span className="hint">{s.session_id === current ? 'открыт сейчас' : when(s.updated_at)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
