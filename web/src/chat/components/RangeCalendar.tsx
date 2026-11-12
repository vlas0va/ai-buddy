import { useState } from 'react'

const MONTHS = ['январь', 'февраль', 'март', 'апрель', 'май', 'июнь', 'июль', 'август', 'сентябрь', 'октябрь', 'ноябрь', 'декабрь']
const MONTHS_GEN = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря']
const WEEKDAYS = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс']

export interface BusyRange {
  start: string
  end: string
  label: string
}

const pad = (n: number) => String(n).padStart(2, '0')
export const iso = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
const parse = (s: string) => {
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y ?? 1970, (m ?? 1) - 1, d ?? 1)
}

/**
 * Feedback 12.13: a small pop-up calendar — click the first day, then the last one (as when booking a flight). Days
 * before min and after max are disabled, weekends are dimmed, days of the user's other requests are marked.
 */
export function RangeCalendar({
  min,
  max,
  start,
  end,
  busy = [],
  onChange,
}: {
  min: string
  max: string
  start: string
  end: string
  busy?: BusyRange[]
  onChange: (start: string, end: string) => void
}) {
  const [month, setMonth] = useState(() => {
    const d = parse(start || min)
    return new Date(d.getFullYear(), d.getMonth(), 1)
  })
  const first = new Date(month.getFullYear(), month.getMonth(), 1)
  const offset = (first.getDay() + 6) % 7 // Monday first
  const daysIn = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  const cells: (Date | null)[] = [...Array<null>(offset).fill(null)]
  for (let d = 1; d <= daysIn; d++) cells.push(new Date(month.getFullYear(), month.getMonth(), d))
  const canPrev = iso(new Date(month.getFullYear(), month.getMonth(), 0)) >= min
  const canNext = iso(new Date(month.getFullYear(), month.getMonth() + 1, 1)) <= max
  const busyOf = (day: string) => busy.find((b) => b.start <= day && day <= b.end)

  function pick(day: string) {
    if (!start || end || day < start) onChange(day, '')
    else onChange(start, day)
  }

  return (
    <div className="range-cal" role="group" aria-label="Календарь">
      <div className="range-cal-head">
        <button type="button" className="range-cal-nav" disabled={!canPrev} aria-label="Предыдущий месяц"
          onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}>
          ‹
        </button>
        <span className="range-cal-title">
          {MONTHS[month.getMonth()]} {month.getFullYear()}
        </span>
        <button type="button" className="range-cal-nav" disabled={!canNext} aria-label="Следующий месяц"
          onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}>
          ›
        </button>
      </div>
      <div className="range-cal-grid">
        {WEEKDAYS.map((w) => (
          <span key={w} className="range-cal-wd">
            {w}
          </span>
        ))}
        {cells.map((d, i) => {
          if (!d) return <span key={`e${i}`} />
          const day = iso(d)
          const disabled = day < min || day > max
          const inRange = Boolean(start && end && start <= day && day <= end)
          const edge = day === start || day === end
          const weekend = d.getDay() === 0 || d.getDay() === 6
          const b = busyOf(day)
          const cls = ['range-cal-day', weekend && 'is-weekend', inRange && 'is-in', edge && 'is-edge', b && 'is-busy']
            .filter(Boolean)
            .join(' ')
          return (
            <button
              key={day}
              type="button"
              className={cls}
              disabled={disabled}
              aria-pressed={edge || inRange}
              aria-label={`${d.getDate()} ${MONTHS_GEN[d.getMonth()]} ${d.getFullYear()}${b ? `, занято: ${b.label}` : ''}`}
              title={b ? `Занято: ${b.label}` : undefined}
              onClick={() => pick(day)}
            >
              {d.getDate()}
            </button>
          )
        })}
      </div>
      {busy.length > 0 && <p className="range-cal-legend">● — дни твоих других заявок</p>}
    </div>
  )
}
