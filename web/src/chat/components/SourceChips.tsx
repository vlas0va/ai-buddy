import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { getKBDoc } from '@/api/chat'
import { errorMessage } from '@/api/client'
import { RichText } from '@/components/rich/RichText'
import { Badge } from '@/components/ui/Badge'
import { CloseIcon } from '@/components/ui/icons'
import type { Source } from '../stream'
import { safeUrl } from '../attachments'

/** Тестовый документ за чипом источника, в боковой панели (отзыв 12.11: чипы не нажимались). */
function DocDrawer({ source, onClose }: { source: Source; onClose: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null)
  const doc = useQuery({ queryKey: ['kb-doc', source.doc_id], queryFn: () => getKBDoc(source.doc_id), staleTime: 5 * 60_000 })
  useEffect(() => {
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="doc-backdrop" onClick={onClose}>
      <aside className="doc-drawer" role="dialog" aria-modal="true" aria-label={source.title} onClick={(e) => e.stopPropagation()}>
        <div className="chat-drawer-head">
          <h2>{source.title}</h2>
          <button ref={closeRef} type="button" className="chat-icon-btn" aria-label="Закрыть" onClick={onClose}>
            <CloseIcon />
          </button>
        </div>
        <Badge variant="test" />
        {doc.isPending && <p className="hint">Загружаю документ…</p>}
        {doc.isError && <p className="ui-field-error">{errorMessage(doc.error)}</p>}
        {doc.data && <RichText text={doc.data.body} />}
      </aside>
    </div>
  )
}

// R69: каждый ответ из базы знаний показывает, что источник — тестовая инструкция прототипа. Результаты веб-поиска по
// общим техническим вопросам — публичные страницы: они открываются в новой вкладке и помечены «Внешний источник»,
// никогда не плашкой базы знаний.
export function SourceChips({ sources }: { sources: Source[] }) {
  const [open, setOpen] = useState<Source | null>(null)
  if (sources.length === 0) return null
  const kb = sources.filter((s) => s.source_type === 'test')
  const web = sources.filter((s) => s.source_type === 'external')
  return (
    <>
      {kb.length > 0 && (
        <div className="chat-sources" aria-label="Источники">
          {/* R69: откуда взят ответ; клик открывает документ, чтобы прочитать его целиком или проверить ответ */}
          <span className="chat-sources-label">{kb.length > 1 ? 'Источники:' : 'Источник:'}</span>
          {kb.map((s) => (
            <button key={s.doc_id} type="button" className="chat-chip chat-chip-doc" onClick={() => setOpen(s)} title="Открыть документ">
              {/* ответ наставника, добавленный в базу знаний команды (kb.add_faq), помечен, чтобы стажёр видел, откуда он */}
              {s.doc_id.startsWith('faq-') ? `Ответ наставника: ${s.title}` : s.title}
            </button>
          ))}
          <Badge variant="test" />
        </div>
      )}
      {web.length > 0 && (
        <div className="chat-sources" aria-label="Внешние источники">
          {web.map((s) => (
            <a key={s.doc_id} className="chat-chip chat-chip-link" href={safeUrl(s.url)} target="_blank" rel="noopener noreferrer">
              {s.title}
            </a>
          ))}
          <Badge variant="external" />
        </div>
      )}
      {open && <DocDrawer source={open} onClose={() => setOpen(null)} />}
    </>
  )
}
