import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { ClipIcon, SendIcon, SuggestIcon } from '@/components/ui/icons'
import { ACCEPT, KIND_TEXT, MAX_ATTACHMENTS, type OutgoingAttachment, formatSize, readAttachment } from '../attachments'
import { useSession } from '@/store/session'
import { filterSuggestions } from '../suggestions'

interface Props {
  disabled: boolean
  onSend: (text: string, attachments: OutgoingAttachment[]) => void
}

// The only input of the chat: auto-growing field with an autocomplete list of suggestions (combobox pattern).
export function ChatInput({ disabled, onSend }: Props) {
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const [files, setFiles] = useState<OutgoingAttachment[]>([])
  const [fileError, setFileError] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)
  const areaRef = useRef<HTMLTextAreaElement>(null)
  const wrapRef = useRef<HTMLFormElement>(null)
  const listId = useId()

  const role = useSession((s) => s.user?.role) ?? 'intern'
  const groups = filterSuggestions(text, role)
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
    if (disabled || (!value.trim() && files.length === 0)) return
    onSend(value, files)
    setText('')
    setFiles([])
    setFileError('')
    setOpen(false)
    setActive(-1)
  }

  const attach = async (list: FileList | null) => {
    const picked = Array.from(list ?? [])
    if (fileRef.current) fileRef.current.value = ''
    if (picked.length === 0) return
    const errors: string[] = []
    const room = MAX_ATTACHMENTS - files.length
    if (picked.length > room) errors.push(`Можно прикрепить не больше ${MAX_ATTACHMENTS} файлов.`)
    const read = await Promise.all(picked.slice(0, Math.max(room, 0)).map(readAttachment))
    const ok = read.filter((r): r is OutgoingAttachment => typeof r !== 'string')
    errors.push(...read.filter((r): r is string => typeof r === 'string'))
    setFiles((prev) => [...prev, ...ok].slice(0, MAX_ATTACHMENTS))
    setFileError(errors.join(' '))
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
      {files.length > 0 && (
        <div className="chat-input-attachments" aria-label="Прикреплённые файлы">
          {files.map((f, i) => (
            <span key={`${f.name}-${i}`} className="chat-attachment">
              {f.name} · {KIND_TEXT[f.kind]}, {formatSize(f.size)}
              <button type="button" aria-label={`Убрать ${f.name}`} onClick={() => setFiles((prev) => prev.filter((_, j) => j !== i))}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      {fileError && (
        <p className="chat-input-error" role="alert">
          {fileError}
        </p>
      )}
      <div className="chat-field">
        <input
          ref={fileRef}
          type="file"
          multiple
          accept={ACCEPT}
          hidden
          data-testid="chat-file-input"
          onChange={(e) => void attach(e.target.files)}
        />
        <button
          type="button"
          className="chat-icon-btn"
          aria-label="Прикрепить файл"
          title="Лог, конфиг, код, скриншот или документ (до 3 файлов). Скриншоты и документы не читаются, текст логов проверяется на секреты."
          disabled={disabled || files.length >= MAX_ATTACHMENTS}
          onClick={() => fileRef.current?.click()}
        >
          <ClipIcon />
        </button>
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
        <button type="submit" className="chat-icon-btn chat-send" aria-label="Отправить" disabled={disabled || (!text.trim() && files.length === 0)}>
          <SendIcon />
        </button>
      </div>
    </form>
  )
}
