import { afterEach, describe, expect, it, vi } from 'vitest'
import { openExternalUrl, setWindowBackground } from './appBridge'

function stubWindow(value: unknown): void {
  ;(globalThis as unknown as { window: unknown }).window = value
}

afterEach(() => {
  vi.unstubAllGlobals()
  stubWindow(undefined)
})

describe('shell appBridge (Go gateway era)', () => {
  it('routes URL opens through the gateway without window.open', async () => {
    stubWindow({ __GITFORGE_BACKEND__: 'wails', open: vi.fn() })
    const post = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response('{"ok":true}', { status: 200 }) as unknown as Response)
    await openExternalUrl('https://example.com')
    const [url, init] = post.mock.calls[0] ?? []
    expect(url).toBe('/gitforge/api/git_open_external')
    expect(JSON.parse(String((init as RequestInit | undefined)?.body))).toEqual({
      url: 'https://example.com',
    })
    expect((globalThis.window as unknown as { open: ReturnType<typeof vi.fn> }).open).not.toHaveBeenCalled()
    post.mockRestore()
  })

  it('falls back to window.open in a plain browser (no gateway)', async () => {
    stubWindow({ __GITFORGE_BACKEND__: undefined, open: vi.fn() })
    const post = vi
      .spyOn(globalThis, 'fetch')
      .mockRejectedValue(new TypeError('fetch failed'))
    await openExternalUrl('https://example.com')
    expect((globalThis.window as unknown as { open: ReturnType<typeof vi.fn> }).open)
      .toHaveBeenCalledWith('https://example.com', '_blank', 'noopener')
    post.mockRestore()
  })

  it('never falls back inside the wails webview (gateway owns opening)', async () => {
    stubWindow({ __GITFORGE_BACKEND__: 'wails', open: vi.fn() })
    const post = vi
      .spyOn(globalThis, 'fetch')
      .mockRejectedValue(new TypeError('fetch failed'))
    await openExternalUrl('https://example.com')
    expect((globalThis.window as unknown as { open: ReturnType<typeof vi.fn> }).open)
      .not.toHaveBeenCalled()
    post.mockRestore()
  })

  it('is a no-op without a window (SSR-ish shapes)', async () => {
    stubWindow(undefined)
    await expect(openExternalUrl('https://example.com')).resolves.toBeUndefined()
    await expect(setWindowBackground('#12151a')).resolves.toBeUndefined()
  })

  it('sends the theme color to the gateway only inside the wails webview', async () => {
    stubWindow({ __GITFORGE_BACKEND__: undefined })
    const post = vi.spyOn(globalThis, 'fetch')
    await setWindowBackground('#12151a')
    expect(post).not.toHaveBeenCalled()
    post.mockRestore()

    stubWindow({ __GITFORGE_BACKEND__: 'wails' })
    const postWails = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response('{"ok":true}', { status: 200 }) as unknown as Response)
    await setWindowBackground('#282a36')
    const [url, init] = postWails.mock.calls[0] ?? []
    expect(url).toBe('/gitforge/api/git_set_window_background')
    expect(JSON.parse(String((init as RequestInit | undefined)?.body))).toEqual({ color: '#282a36' })
    postWails.mockRestore()
  })
})
