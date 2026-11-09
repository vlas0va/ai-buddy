import { useState } from 'react'
import { Button } from '@/components/ui/Button'

export function ChatInput({ disabled, onSend }: { disabled: boolean; onSend: (text: string) => void }) {
  const [text, setText] = useState('')
  const submit = () => {
    if (disabled || !text.trim()) return
    onSend(text)
    setText('')
  }
  return (
    <form
      className="chat-input"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <textarea
        className="ui-input chat-textarea"
        aria-label="Сообщение"
        placeholder="Напишите сообщение…"
        rows={2}
        value={text}
        disabled={disabled}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault()
            submit()
          }
        }}
      />
      <Button type="submit" disabled={disabled || !text.trim()}>
        Отправить
      </Button>
    </form>
  )
}
