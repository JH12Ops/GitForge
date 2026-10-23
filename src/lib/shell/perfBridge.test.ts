import { describe, expect, it, vi } from 'vitest'
import { createTerminalPerfSink } from './perfBridge'

describe('perf terminal bridge (Go gateway)', () => {
  it('forwards a sample to the gateway log sink', async () => {
    const post = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response('{"ok":true}', { status: 200 }) as unknown as Response)
    const sink = createTerminalPerfSink()

    sink({ label: 'switch: click→painted', ms: 1234.56, at: Date.now() })

    void post.mock.results[0]
    await vi.waitFor(() => {
      const [url, init] = post.mock.calls[0] ?? []
      expect(url).toBe('/gitforge/api/gitforge_log')
      expect(JSON.parse(String((init as RequestInit | undefined)?.body))).toEqual({
        src: 'perf',
        msg: 'switch: click→painted 1234.56ms',
      })
    })
    post.mockRestore()
  })

  it('never rejects when the gateway call fails', () => {
    const post = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('fetch failed'))
    const sink = createTerminalPerfSink()
    expect(() => sink({ label: 'switch: x', ms: 1, at: Date.now() })).not.toThrow()
    post.mockRestore()
  })
})
