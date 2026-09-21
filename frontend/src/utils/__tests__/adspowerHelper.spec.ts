import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  adsPowerHelperMacDownloadURL,
  adsPowerHelperWindowsDownloadURL,
  isAdsPowerHelperAvailable,
} from '../adspowerHelper'

describe('AdsPower helper integration', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('uses fixed independent release assets', () => {
    expect(adsPowerHelperMacDownloadURL).toContain('/releases/download/adspower-helper-latest/')
    expect(adsPowerHelperMacDownloadURL).toMatch(/\.dmg$/)
    expect(adsPowerHelperWindowsDownloadURL).toMatch(/\.exe$/)
  })

  it('returns false when the local helper is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('connection refused')))
    await expect(isAdsPowerHelperAvailable()).resolves.toBe(false)
  })

  it('requires a successful health response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true }))
    await expect(isAdsPowerHelperAvailable()).resolves.toBe(true)
  })

  it('waits for the helper five-second AdsPower check instead of failing at 2.5s', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn().mockImplementation((_url, { signal }) => new Promise((resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
      setTimeout(() => resolve({ ok: true }), 4500)
    })))
    const result = isAdsPowerHelperAvailable()
    await vi.advanceTimersByTimeAsync(4500)
    await expect(result).resolves.toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('still times out an unresponsive helper', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn().mockImplementation((_url, { signal }) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
    })))
    const result = isAdsPowerHelperAvailable()
    await vi.advanceTimersByTimeAsync(8000)
    await expect(result).resolves.toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not report AdsPower API errors as healthy', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 503 }))
    await expect(isAdsPowerHelperAvailable()).resolves.toBe(false)
  })
})
