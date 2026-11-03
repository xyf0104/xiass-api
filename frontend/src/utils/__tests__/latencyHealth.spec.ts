import { describe, expect, it } from 'vitest'

import { durationSeverity, firstTokenSeverity } from '../latencyHealth'

describe('latencyHealth', () => {
  it('keeps first-token latency green through 15s, then escalates at 30s/60s', () => {
    expect(firstTokenSeverity(0)).toBe('good')
    expect(firstTokenSeverity(14_999)).toBe('good')
    expect(firstTokenSeverity(15_000)).toBe('good')
    expect(firstTokenSeverity(15_001)).toBe('warn')
    expect(firstTokenSeverity(29_999)).toBe('warn')
    expect(firstTokenSeverity(30_000)).toBe('slow')
    expect(firstTokenSeverity(59_999)).toBe('slow')
    expect(firstTokenSeverity(60_000)).toBe('critical')
  })

  it('classifies total duration at 1min/3min/5min boundaries', () => {
    expect(durationSeverity(0)).toBe('good')
    expect(durationSeverity(59_999)).toBe('good')
    expect(durationSeverity(60_000)).toBe('warn')
    expect(durationSeverity(179_999)).toBe('warn')
    expect(durationSeverity(180_000)).toBe('slow')
    expect(durationSeverity(299_999)).toBe('slow')
    expect(durationSeverity(300_000)).toBe('critical')
  })
})
