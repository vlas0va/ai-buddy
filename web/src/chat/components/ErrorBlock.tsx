import { Button } from '@/components/ui/Button'
import type { StreamError } from '@/chat/events'

export function ErrorBlock({ error, onRetry }: { error: StreamError; onRetry?: () => void }) {
  return (
    <div className="ui-state ui-state-error chat-error" role="alert">
      <p>{error.message}</p>
      {error.retryable && onRetry && (
        <Button size="sm" variant="secondary" onClick={onRetry}>
          Повторить
        </Button>
      )}
    </div>
  )
}
