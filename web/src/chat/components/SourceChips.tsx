import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { getKBDoc } from '@/api/chat'
import { errorMessage } from '@/api/client'
import { RichText } from '@/components/rich/RichText'
import { Badge } from '@/components/ui/Badge'
import { CloseIcon } from '@/components/ui/icons'
import type { Source } from '../stream'
import { safeUrl } from '../attachments'

/** The test document behind a source chip, in a side panel (feedback 12.11: chips were not clickable). */
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

// R69: every KB answer shows that the source is a test instruction of the prototype. Web search results for general
// technical questions are public pages: they open in a new tab and carry «Внешний источник», never the KB badge.
export function SourceChips({ sources }: { sources: Source[] }) {
  const [open, setOpen] = useState<Source | null>(null)
  if (sources.length === 0) return null
  const kb = sources.filter((s) => s.source_type === 'test')
  const web = sources.filter((s) => s.source_type === 'external')
  return (
    <>
      {kb.length > 0 && (
        <div className="chat-sources" aria-label="Источники">
          {kb.map((s) => (
            <button key={s.doc_id} type="button" className="chat-chip chat-chip-doc" onClick={() => setOpen(s)} title="Открыть документ">
              {s.title}
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
