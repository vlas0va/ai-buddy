import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { SendIcon, SuggestIcon } from '@/components/ui/icons'
import { filterSuggestions } from '../suggestions'

interface Props {
  disabled: boolean
  onSend: (text: string) => void
}

// The only input of the chat: auto-growing field with an autocomplete list of suggestions (combobox pattern).
export function ChatInput({ disabled, onSend }: Props) {
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const areaRef = useRef<HTMLTextAreaElement>(null)
  const wrapRef = useRef<HTMLFormElement>(null)
  const listId = useId()

  const groups = filterSuggestions(text)
  const flat = groups.flatMap((g) => g.items)
  const expanded = open && flat.length > 0
  const optionId = (i: number) => `${listId}-opt-${i}`

  useLayoutEffect(() => {
    const el = areaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${el.scrollHeight}px` // capped at 5 lines by max-height in CSS
  }, [text])

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [])

  const send = (value: string) => {
    if (disabled || !value.trim()) return
    onSend(value)
    setText('')
    setOpen(false)
    setActive(-1)
  }

  const move = (delta: number) => {
    if (flat.length === 0) return
    setOpen(true)
    setActive((i) => (i + delta + flat.length) % flat.length)
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      move(1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      move(-1)
    } else if (e.key === 'Escape') {
      setOpen(false)
      setActive(-1)
    } else if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      const picked = expanded && active >= 0 ? flat[active] : undefined
      send(picked ?? text)
    }
  }

  const offsets = groups.map((_, gi) => groups.slice(0, gi).reduce((n, g) => n + g.items.length, 0))
  return (
    <form
      ref={wrapRef}
      className="chat-input"
      onSubmit={(e) => {
        e.preventDefault()
        send(text)
      }}
    >
      {expanded && (
        <div className="chat-suggest" id={listId} role="listbox" aria-label="Подсказки">
          {groups.map((g, gi) => (
            <div key={g.title} role="group" aria-label={g.title}>
              <p className="chat-suggest-title" aria-hidden="true">
                {g.title}
              </p>
              {g.items.map((item, ii) => {
                const i = (offsets[gi] ?? 0) + ii
                return (
                  <div
                    key={item}
                    id={optionId(i)}
                    role="option"
                    aria-selected={i === active}
                    className={`chat-suggest-item${i === active ? ' is-active' : ''}`}
                    onMouseDown={(e) => e.preventDefault()}
                    onMouseEnter={() => setActive(i)}
                    onClick={() => send(item)}
                  >
                    {item}
                  </div>
                )
              })}
            </div>
          ))}
        </div>
      )}
      <div className="chat-field">
        <button
          type="button"
          className="chat-icon-btn"
          aria-label="Подсказки"
          aria-expanded={expanded}
          onClick={() => {
            const next = !expanded
            areaRef.current?.focus() // onFocus opens the list; the explicit toggle below wins
            setOpen(next)
          }}
        >
          <SuggestIcon />
        </button>
        <textarea
          ref={areaRef}
          className="chat-textarea"
          role="combobox"
          aria-label="Сообщение"
          aria-autocomplete="list"
          aria-expanded={expanded}
          aria-controls={expanded ? listId : undefined}
          aria-activedescendant={expanded && active >= 0 ? optionId(active) : undefined}
          placeholder="Напишите вопрос или выберите подсказку…"
          rows={1}
          value={text}
          onFocus={() => setOpen(true)}
          onChange={(e) => {
            setText(e.target.value)
            setOpen(true)
            setActive(-1)
          }}
          onKeyDown={onKeyDown}
        />
        <button type="submit" className="chat-icon-btn chat-send" aria-label="Отправить" disabled={disabled || !text.trim()}>
          <SendIcon />
        </button>
      </div>
    </form>
  )
}
