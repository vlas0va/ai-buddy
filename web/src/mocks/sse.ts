// Builds an SSE body exactly as described in contracts/agent-stream.md: incrementing id, event name, one JSON object per data line.

export const mockConfig = { stepDelayMs: 250 }

export interface MockEvent {
  event: string
  data: unknown
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

export function frame(id: number, ev: MockEvent): string {
  return `id: ${id}\nevent: ${ev.event}\ndata: ${JSON.stringify(ev.data)}\n\n`
}

export function sseResponse(events: MockEvent[]): Response {
  const enc = new TextEncoder()
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      let id = 1
      for (const ev of events) {
        if (mockConfig.stepDelayMs > 0) await sleep(mockConfig.stepDelayMs)
        controller.enqueue(enc.encode(frame(id++, ev)))
      }
      controller.close()
    },
  })
  return new Response(stream, { headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' } })
}

export function words(text: string): MockEvent[] {
  return text.split(/(?<=\s)/).map((t) => ({ event: 'token', data: { text: t } }))
}
