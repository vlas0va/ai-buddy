import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { CalendarIcon } from '@/components/ui/icons'
import type { DatePicker } from '@/chat/events'
import { RangeCalendar } from './RangeCalendar'

const ru = (iso: string) => iso.split('-').reverse().join('.')

/**
 * Feedback 12.11 / 12.13: pick the absence dates in a small pop-up calendar (first day, then last day); the choice is
 * sent as an ordinary message («с … по …» or «перенеси заявку ABS-… на …»).
 */
export function DatePickerCard({ picker, disabled, onPick }: { picker: DatePicker; disabled?: boolean; onPick: (text: string) => void }) {
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [open, setOpen] = useState(false)
  const [sent, setSent] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const last = end && end >= start ? end : start
  const dates = !start ? '' : last === start ? ru(start) : `с ${ru(start)} по ${ru(last)}`
  const text = picker.purpose === 'reschedule' ? `перенеси заявку ${picker.ref_id ?? ''} на ${dates}`.replace('  ', ' ') : dates

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
    <Card className="chat-card" aria-label="Выбор дат">
      <h3>{picker.label}</h3>
      <div className="date-pop" ref={box}>
        <button
          type="button"
          className="ui-input date-pop-trigger"
          disabled={sent}
          aria-expanded={open}
          aria-haspopup="dialog"
          onClick={() => setOpen((v) => !v)}
        >
          {dates ? <span>{dates}</span> : <span className="hint">Выбери даты: первый день, затем последний</span>}
          <CalendarIcon />
        </button>
        {open && !sent && (
          <div className="date-pop-panel" role="dialog" aria-label="Выбор дат в календаре">
            <RangeCalendar
              min={picker.min}
              max={picker.max}
              start={start}
              end={end}
              busy={picker.busy}
              onChange={(s, e) => {
                setStart(s)
                setEnd(e)
                if (e) setOpen(false) // the range is complete
              }}
            />
            {start && !end && (
              <div className="date-pop-foot">
                <span className="hint">Выбери последний день или</span>
                <Button size="sm" variant="secondary" onClick={() => setOpen(false)}>
                  Один день
                </Button>
              </div>
            )}
          </div>
        )}
      </div>
      {sent ? (
        <p className="hint">Даты отправлены: {dates}.</p>
      ) : (
        <div className="chat-actions">
          <Button
            disabled={!start || disabled}
            onClick={() => {
              setSent(true)
              setOpen(false)
              onPick(text)
            }}
          >
            {picker.purpose === 'reschedule' ? 'Перенести на эти даты' : 'Выбрать даты'}
          </Button>
          {start && !end && !open && <span className="hint">один день — или выбери последний день в календаре</span>}
        </div>
      )}
    </Card>
  )
}
