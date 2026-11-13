// Построчный разбор для RichText: абзацы, списки и таблицы; заголовки markdown становятся простыми строками.

export type Block =
  | { kind: 'p'; lines: string[] }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[]; start: number }
  | { kind: 'table'; rows: string[][] }

/**
 * Нумерованные разделы, разбитые маркированными списками («1. Кто ревьюит» → пункты → «2. Правила» → пункты), — одна
 * нумерация: список, который продолжается после пунктов или пустых строк, продолжает предыдущий, даже если модель
 * каждый раз писала «1.» (Рита 12.13: у всех разделов было «1.»). Абзац или таблица между ними начинают новую
 * нумерацию.
 */
export function parseBlocks(text: string): Block[] {
  const blocks: Block[] = []
  let lastNumber = 0 // последний номер текущей нумерации, 0 — нет
  for (const raw of text.split('\n')) {
    const line = raw.replace(/^#{1,6}\s+/, '')
    const last = blocks.at(-1)
    const ul = /^\s*[-*•]\s+(.*)$/.exec(line)
    const ol = /^\s*\d+[.)]\s+(.*)$/.exec(line)
    if (line.trim() === '') {
      blocks.push({ kind: 'p', lines: [] })
    } else if (/^\s*\|.*\|\s*$/.test(line)) {
      if (/^\s*\|[\s:|-]+\|\s*$/.test(line)) continue // строка-разделитель
      const cells = line.trim().slice(1, -1).split('|').map((c) => c.trim())
      lastNumber = 0
      if (last?.kind === 'table') last.rows.push(cells)
      else blocks.push({ kind: 'table', rows: [cells] })
    } else if (ul) {
      if (last?.kind === 'ul') last.items.push(ul[1] ?? '')
      else blocks.push({ kind: 'ul', items: [ul[1] ?? ''] })
    } else if (ol) {
      const written = Number(/^\s*(\d+)/.exec(line)?.[1] ?? '1')
      if (last?.kind === 'ol') last.items.push(ol[1] ?? '')
      else blocks.push({ kind: 'ol', items: [ol[1] ?? ''], start: written > lastNumber ? written : lastNumber + 1 })
      const cur = blocks.at(-1) as { kind: 'ol'; items: string[]; start: number }
      lastNumber = cur.start + cur.items.length - 1
    } else if (last?.kind === 'p') {
      last.lines.push(line)
      lastNumber = 0
    } else {
      blocks.push({ kind: 'p', lines: [line] })
      lastNumber = 0
    }
  }
  return blocks.filter((b) => b.kind !== 'p' || b.lines.length > 0)
}
