import { Fragment, type ReactNode } from 'react'
import { parseBlocks } from './blocks'

// Небольшое безопасное подмножество markdown для ответов buddy: абзацы, списки «- » и «1. », **жирный**, `код`, таблицы
// в виде простых строк. Собирается только из элементов React (без innerHTML), поэтому текст документа никогда не может
// внедрить разметку.

function inline(text: string, keyBase: string): ReactNode[] {
  const out: ReactNode[] = []
  const re = /(\*\*[^*]+\*\*|`[^`]+`)/g
  let last = 0
  let m: RegExpExecArray | null
  let i = 0
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index))
    const token = m[0]
    const key = `${keyBase}-${i++}`
    out.push(token.startsWith('**') ? <strong key={key}>{token.slice(2, -2)}</strong> : <code key={key}>{token.slice(1, -1)}</code>)
    last = m.index + token.length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

export function RichText({ text }: { text: string }) {
  const blocks = parseBlocks(text)
  return (
    <div className="rich">
      {blocks.map((b, i) => {
        const k = `b${i}`
        if (b.kind === 'ul') return <ul key={k}>{b.items.map((it, j) => <li key={j}>{inline(it, `${k}-${j}`)}</li>)}</ul>
        if (b.kind === 'ol')
          return (
            <ol key={k} start={b.start}>
              {b.items.map((it, j) => (
                <li key={j}>{inline(it, `${k}-${j}`)}</li>
              ))}
            </ol>
          )
        if (b.kind === 'table') {
          const [head, ...rows] = b.rows
          return (
            <div key={k} className="rich-table-wrap">
              <table className="rich-table">
                {head && (
                  <thead>
                    <tr>{head.map((c, j) => <th key={j}>{inline(c, `${k}-h${j}`)}</th>)}</tr>
                  </thead>
                )}
                <tbody>
                  {rows.map((r, j) => (
                    <tr key={j}>{r.map((c, n) => <td key={n}>{inline(c, `${k}-${j}-${n}`)}</td>)}</tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
        return (
          <p key={k}>
            {b.lines.map((l, j) => (
              <Fragment key={j}>
                {j > 0 && <br />}
                {inline(l, `${k}-${j}`)}
              </Fragment>
            ))}
          </p>
        )
      })}
    </div>
  )
}
