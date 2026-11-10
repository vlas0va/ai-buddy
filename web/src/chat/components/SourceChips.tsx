import { Badge } from '@/components/ui/Badge'
import type { Source } from '../stream'

// R69: every KB answer shows that the source is a test instruction of the prototype.
export function SourceChips({ sources }: { sources: Source[] }) {
  if (sources.length === 0) return null
  return (
    <div className="chat-sources" aria-label="Источники">
      {sources.map((s) => (
        <span key={s.doc_id} className="chat-chip">
          {s.title}
        </span>
      ))}
      <Badge variant="test" />
    </div>
  )
}
