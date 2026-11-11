// Line-level parser for RichText: paragraphs, lists and tables; markdown headings become plain lines.

export type Block =
  | { kind: 'p'; lines: string[] }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[] }
  | { kind: 'table'; rows: string[][] }

export function parseBlocks(text: string): Block[] {
  const blocks: Block[] = []
  for (const raw of text.split('\n')) {
    const line = raw.replace(/^#{1,6}\s+/, '')
    const last = blocks.at(-1)
    const ul = /^\s*[-*•]\s+(.*)$/.exec(line)
    const ol = /^\s*\d+[.)]\s+(.*)$/.exec(line)
    if (line.trim() === '') {
      blocks.push({ kind: 'p', lines: [] })
    } else if (/^\s*\|.*\|\s*$/.test(line)) {
      if (/^\s*\|[\s:|-]+\|\s*$/.test(line)) continue // separator row
      const cells = line.trim().slice(1, -1).split('|').map((c) => c.trim())
      if (last?.kind === 'table') last.rows.push(cells)
      else blocks.push({ kind: 'table', rows: [cells] })
    } else if (ul) {
      if (last?.kind === 'ul') last.items.push(ul[1] ?? '')
      else blocks.push({ kind: 'ul', items: [ul[1] ?? ''] })
    } else if (ol) {
      if (last?.kind === 'ol') last.items.push(ol[1] ?? '')
      else blocks.push({ kind: 'ol', items: [ol[1] ?? ''] })
    } else if (last?.kind === 'p') {
      last.lines.push(line)
    } else {
      blocks.push({ kind: 'p', lines: [line] })
    }
  }
  return blocks.filter((b) => b.kind !== 'p' || b.lines.length > 0)
}
