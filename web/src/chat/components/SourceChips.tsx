import { Badge } from '@/components/ui/Badge'
import type { Source } from '../stream'
import { safeUrl } from '../attachments'

// R69: every KB answer shows that the source is a test instruction of the prototype. Web search results for general
// technical questions are public pages: they open in a new tab and carry «Внешний источник», never the KB badge.
export function SourceChips({ sources }: { sources: Source[] }) {
  if (sources.length === 0) return null
  const kb = sources.filter((s) => s.source_type === 'test')
  const web = sources.filter((s) => s.source_type === 'external')
  return (
    <>
      {kb.length > 0 && (
        <div className="chat-sources" aria-label="Источники">
          {kb.map((s) => (
            <span key={s.doc_id} className="chat-chip">
              {s.title}
            </span>
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
    </>
  )
}
