import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { DatePicker } from '@/chat/events'

const ru = (iso: string) => iso.split('-').reverse().join('.')

/** Feedback 12.11: pick the absence dates in a calendar; the choice is sent as an ordinary message («с … по …»). */
export function DatePickerCard({ picker, disabled, onPick }: { picker: DatePicker; disabled?: boolean; onPick: (text: string) => void }) {
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [sent, setSent] = useState(false)
  const last = end && end >= start ? end : start
  const dates = !start ? '' : last === start ? ru(start) : `с ${ru(start)} по ${ru(last)}`
  const text = picker.purpose === 'reschedule' ? `перенеси заявку ${picker.ref_id ?? ''} на ${dates}`.replace('  ', ' ') : dates
  return (
    <Card className="chat-card" aria-label="Выбор дат">
      <h3>{picker.label}</h3>
      <div className="date-pick">
        <label>
          <span className="hint">С</span>
          <input
            type="date"
            className="ui-input"
            min={picker.min}
            max={picker.max}
            value={start}
            disabled={sent}
            onChange={(e) => {
              setStart(e.target.value)
              if (end && end < e.target.value) setEnd('')
            }}
            aria-label="Дата начала"
          />
        </label>
        <label>
          <span className="hint">По (необязательно)</span>
          <input
            type="date"
            className="ui-input"
            min={start || picker.min}
            max={picker.max}
            value={end}
            disabled={sent || !start}
            onChange={(e) => setEnd(e.target.value)}
            aria-label="Дата окончания"
          />
        </label>
      </div>
      {sent ? (
        <p className="hint">Даты отправлены: {dates}.</p>
      ) : (
        <div className="chat-actions">
          <Button
            disabled={!start || disabled}
            onClick={() => {
              setSent(true)
              onPick(text)
            }}
          >
            {picker.purpose === 'reschedule' ? 'Перенести на эти даты' : 'Выбрать даты'}
          </Button>
        </div>
      )}
    </Card>
  )
}
