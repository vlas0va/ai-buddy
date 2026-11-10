import { describe, expect, it, vi } from 'vitest'
import { useSession } from '@/store/session'
import { ApiError } from '@/api/client'
import { jsonResponse } from '@/test/helpers'
import { readEvents, SseParser, streamPost } from './sse'

function bodyOf(chunks: string[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(c) {
      chunks.forEach((x) => c.enqueue(enc.encode(x)))
      c.close()
    },
  })
}

async function collect(gen: AsyncGenerator<unknown>) {
  const out: unknown[] = []
  for await (const e of gen) out.push(e)
  return out
}

describe('SseParser (R78)', () => {
  it('parses a full frame', () => {
    const p = new SseParser()
    expect(p.push('id: 1\nevent: token\ndata: {"text":"a"}\n\n')).toEqual([{ id: '1', event: 'token', data: '{"text":"a"}' }])
  })

  it('handles chunk boundaries inside a line', () => {
    const p = new SseParser()
    expect(p.push('event: tok')).toEqual([])
    expect(p.push('en\ndata: {"te')).toEqual([])
    expect(p.push('xt":"x"}\n')).toEqual([])
    expect(p.push('\n')).toHaveLength(1)
  })

  it('joins multi-line data and handles CRLF', () => {
    const p = new SseParser()
    const out = p.push('event: token\r\ndata: a\r\ndata: b\r\n\r\n')
    expect(out).toEqual([{ id: undefined, event: 'token', data: 'a\nb' }])
  })

  it('waits for the \\n of a split \\r\\n', () => {
    const p = new SseParser()
    expect(p.push('event: done\r')).toEqual([])
    expect(p.push('\ndata: {}\r\n\r\n')).toHaveLength(1)
  })

  it('ignores comments and keep-alive pings', () => {
    const p = new SseParser()
    expect(p.push(': ping\n\n')).toEqual([])
    expect(p.push('event: done\ndata: {}\n\n')).toHaveLength(1)
  })

  it('defaults event name to message, accepts field without colon', () => {
    const p = new SseParser()
    expect(p.push('data\n\n')).toEqual([{ id: undefined, event: 'message', data: '' }])
  })

  it('flushes an unterminated final event on end()', () => {
    const p = new SseParser()
    p.push('event: done\ndata: {}')
    expect(p.end()).toEqual([{ id: undefined, event: 'done', data: '{}' }])
    expect(new SseParser().end()).toEqual([])
  })
})

describe('readEvents', () => {
  it('yields typed events across chunks and turns bad payloads into an error event, skips unknown events', async () => {
    const events = await collect(
      readEvents(bodyOf(['event: token\ndata: {"text":"При', 'вет"}\n\nevent: token\ndata: not json\n\n', 'event: nope\ndata: {}\n\nevent: done\ndata: {"message_id":"m1"}'])),
    )
    expect(events[0]).toEqual({ type: 'token', data: { text: 'Привет' } })
    expect(events[1]).toMatchObject({ type: 'error', data: { retryable: true } })
    // contracts/agent-stream.md: new events are additive, an unknown one is skipped instead of breaking the turn
    expect(events[2]).toEqual({ type: 'ignored', data: { name: 'nope' } })
    expect(events[3]).toEqual({ type: 'done', data: { message_id: 'm1' } })
  })

  it('rejects a payload that fails its schema', async () => {
    const events = await collect(readEvents(bodyOf(['event: status\ndata: {"stage":"dancing"}\n\n'])))
    expect(events[0]).toMatchObject({ type: 'error' })
  })
})

describe('streamPost', () => {
  it('sends the bearer token and streams events', async () => {
    useSession.setState({ token: 't1', user: null })
    const fetchMock = vi.fn().mockResolvedValue(new Response(bodyOf(['event: done\ndata: {}\n\n'])))
    vi.stubGlobal('fetch', fetchMock)
    const events = await collect(streamPost('/api/x', { text: 'a' }))
    expect(events).toEqual([{ type: 'done', data: {} }])
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer t1')
    expect(init.method).toBe('POST')
  })

  it('throws ApiError for a non-2xx answer and clears the session on 401', async () => {
    useSession.setState({ token: 't1', user: null })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ code: 'unauthorized', message: 'Войдите.', retryable: false }, 401)))
    await expect(collect(streamPost('/api/x', undefined))).rejects.toBeInstanceOf(ApiError)
    expect(useSession.getState().token).toBeNull()
  })

  it('maps a network failure to a retryable ApiError and rethrows AbortError', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('failed')))
    await expect(collect(streamPost('/api/x', {}))).rejects.toMatchObject({ retryable: true })
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new DOMException('aborted', 'AbortError')))
    await expect(collect(streamPost('/api/x', {}))).rejects.toMatchObject({ name: 'AbortError' })
  })

  it('fails when the response has no body', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })))
    await expect(collect(streamPost('/api/x', {}))).rejects.toBeInstanceOf(ApiError)
  })
})
