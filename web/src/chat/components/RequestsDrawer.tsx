import { useEffect, useRef, type ReactNode } from 'react'
import { CloseIcon } from '@/components/ui/icons'

export function RequestsDrawer({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  const closeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="chat-backdrop" onClick={onClose}>
      <aside className="chat-drawer" role="dialog" aria-modal="true" aria-label="Мои заявки" onClick={(e) => e.stopPropagation()}>
        <div className="chat-drawer-head">
          <h2>Мои заявки</h2>
          <button ref={closeRef} type="button" className="chat-icon-btn" aria-label="Закрыть" onClick={onClose}>
            <CloseIcon />
          </button>
        </div>
        {children}
      </aside>
    </div>
  )
}
