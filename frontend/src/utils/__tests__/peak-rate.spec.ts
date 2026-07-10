import { describe, expect, it } from 'vitest'
import { isPeakRateActive } from '../peak-rate'

const peak = {
  peak_rate_enabled: true,
  peak_start: '14:00',
  peak_end: '18:00',
  peak_rate_multiplier: 2,
}

describe('isPeakRateActive', () => {
  it('evaluates the window in the server UTC offset', () => {
    expect(isPeakRateActive(peak, '+08:00', new Date('2026-07-10T06:00:00Z'))).toBe(true)
    expect(isPeakRateActive(peak, '+08:00', new Date('2026-07-10T10:00:00Z'))).toBe(false)
  })

  it('keeps the end boundary exclusive and rejects invalid windows', () => {
    expect(isPeakRateActive(peak, '+08:00', new Date('2026-07-10T09:59:00Z'))).toBe(true)
    expect(isPeakRateActive(peak, '+08:00', new Date('2026-07-10T10:00:00Z'))).toBe(false)
    expect(isPeakRateActive({ ...peak, peak_start: '18:00', peak_end: '14:00' }, '+08:00')).toBe(false)
  })
})
