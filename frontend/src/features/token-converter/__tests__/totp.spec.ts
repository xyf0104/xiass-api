import { webcrypto } from 'node:crypto'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import {
  cleanTwoFactorValue,
  generateTotp,
  normalizeBase32Secret,
  totpSecondsRemaining,
} from '../totp'

describe('local TOTP', () => {
  beforeAll(() => vi.stubGlobal('crypto', webcrypto))
  afterAll(() => vi.unstubAllGlobals())

  it('matches the RFC 6238 SHA-1 test vector', async () => {
    const secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'
    await expect(generateTotp(secret, 59_000, { digits: 8 })).resolves.toBe('94287082')
    await expect(generateTotp(secret, 59_000)).resolves.toBe('287082')
  })

  it('cleans trailing status marks and accepts otpauth links', () => {
    expect(cleanTwoFactorValue(' JBSWY3DPEHPK3PXP  ✅ '))
      .toBe('JBSWY3DPEHPK3PXP')
    expect(normalizeBase32Secret('otpauth://totp/Demo?secret=JBSW%20Y3DP%20EHPK3PXP'))
      .toBe('JBSWY3DPEHPK3PXP')
  })

  it('reports the next 30-second boundary', () => {
    expect(totpSecondsRemaining(0)).toBe(30)
    expect(totpSecondsRemaining(8_000)).toBe(22)
    expect(totpSecondsRemaining(29_999)).toBe(1)
  })
})
