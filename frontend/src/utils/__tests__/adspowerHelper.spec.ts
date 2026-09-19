import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  adsPowerHelperMacDownloadURL,
  adsPowerHelperWindowsDownloadURL,
  isAdsPowerHelperAvailable,
} from '../adspowerHelper'

describe('AdsPower helper integration', () => {
  afterEach(() => vi.unstubAllGlobals())

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
})
