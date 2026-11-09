import { getApiBaseUrl } from '@/config'
import { ApiError, NETWORK_ERROR, readErrorBody } from '@/api/client'
import { useSession } from '@/store/session'
import { parseEvent, type StreamEvent } from './events'

export interface RawSseEvent {
  id?: string
  event: string
  data: string
}

/** Incremental SSE parser: feed text chunks, get complete events. Handles chunk splits, CRLF, multi-line data, comments. */
export class SseParser {
  private buffer = ''
  private id: string | undefined
  private event = ''
  private data: string[] = []

  push(chunk: string): RawSseEvent[] {
    this.buffer += chunk
    const out: RawSseEvent[] = []
    let idx: number
    while ((idx = this.buffer.search(/\r\n|\n|\r/)) !== -1) {
      const m = /^(\r\n|\n|\r)/.exec(this.buffer.slice(idx))
      const eol = m ? m[0].length : 1
      // a lone \r at the end of the buffer may be the first half of \r\n: wait for more input
      if (this.buffer[idx] === '\r' && idx + 1 === this.buffer.length) break
      const line = this.buffer.slice(0, idx)
      this.buffer = this.buffer.slice(idx + eol)
      const ev = this.line(line)
      if (ev) out.push(ev)
    }
    return out
  }

  /** Flush an unterminated final event (stream closed without a blank line). */
  end(): RawSseEvent[] {
    const out: RawSseEvent[] = []
    if (this.buffer) {
      const ev = this.line(this.buffer)
      this.buffer = ''
      if (ev) out.push(ev)
    }
    const last = this.dispatch()
    if (last) out.push(last)
    return out
  }

  private line(line: string): RawSseEvent | null {
    if (line === '') return this.dispatch()
    if (line.startsWith(':')) return null
    const c = line.indexOf(':')
    const field = c === -1 ? line : line.slice(0, c)
    let value = c === -1 ? '' : line.slice(c + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') this.event = value
    else if (field === 'data') this.data.push(value)
    else if (field === 'id') this.id = value
    return null
  }

  private dispatch(): RawSseEvent | null {
    if (this.data.length === 0) {
      this.event = ''
      return null
    }
    const ev: RawSseEvent = { id: this.id, event: this.event || 'message', data: this.data.join('\n') }
    this.event = ''
    this.data = []
    return ev
  }
}

/** Reads a response body as typed stream events. */
export async function* readEvents(body: ReadableStream<Uint8Array>): AsyncGenerator<StreamEvent> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  const parser = new SseParser()
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      for (const raw of parser.push(decoder.decode(value, { stream: true }))) yield parseEvent(raw.event, raw.data)
    }
    for (const raw of parser.push(decoder.decode())) yield parseEvent(raw.event, raw.data)
    for (const raw of parser.end()) yield parseEvent(raw.event, raw.data)
  } finally {
    reader.releaseLock()
  }
}

/** POST that answers text/event-stream (EventSource cannot POST). Throws ApiError for non-2xx, AbortError on abort. */
export async function* streamPost(path: string, body: unknown, signal?: AbortSignal): AsyncGenerator<StreamEvent> {
  const { token, clear } = useSession.getState()
  const headers: Record<string, string> = { Accept: 'text/event-stream' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`

  let res: Response
  try {
    res = await fetch(`${getApiBaseUrl()}${path}`, {
      method: 'POST',
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, NETWORK_ERROR)
  }
  if (!res.ok) {
    if (res.status === 401 && token) clear()
    throw new ApiError(res.status, await readErrorBody(res))
  }
  if (!res.body) throw new ApiError(0, NETWORK_ERROR)
  yield* readEvents(res.body)
}
