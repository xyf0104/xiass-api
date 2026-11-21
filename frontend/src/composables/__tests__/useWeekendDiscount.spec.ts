import { describe, expect, it, vi } from 'vitest'
import { weekendMultiplierAt, weekendNextChangeAt, formatPromotionCountdown, formatPromotionRate } from '../useWeekendDiscount'
import type { WeekendDiscountStatus } from '@/api/weekendDiscount'
import { getWeekendDiscount } from '@/api/weekendDiscount'
import { useWeekendDiscount, updateWeekendDiscountStatus } from '../useWeekendDiscount'
import { flushPromises, mount } from '@vue/test-utils'
import { h, nextTick } from 'vue'

vi.mock('@/api/weekendDiscount', () => ({ getWeekendDiscount: vi.fn() }))

const status: WeekendDiscountStatus = {
  enabled: true, starts_at: null, ends_at: null, timezone: 'Asia/Shanghai',
  discount_multiplier: .8, applied_multiplier: 1, active: false,
  server_time: '2026-11-20T12:00:00+08:00', next_change_at: null
}

describe('weekend promotion clock', () => {
  it.each([
    ['2026-11-20T23:59:59+08:00', '2026-11-21T00:00:00+08:00'],
    ['2026-11-21T00:00:00+08:00', '2026-11-23T00:00:00+08:00'],
    ['2026-11-22T23:59:59+08:00', '2026-11-23T00:00:00+08:00'],
    ['2026-11-23T00:00:00+08:00', '2026-11-28T00:00:00+08:00']
  ])('counts down to the next actual promotion boundary at %s', (at, target) => {
    expect(weekendNextChangeAt(status, Date.parse(at))).toBe(Date.parse(target))
  })

  it('honors start and end limits without advertising an impossible weekend', () => {
    const now = Date.parse('2026-11-20T12:00:00+08:00')
    expect(weekendNextChangeAt({ ...status, starts_at: '2026-11-22T15:00:00+08:00' }, now)).toBe(Date.parse('2026-11-22T15:00:00+08:00'))
    expect(weekendNextChangeAt({ ...status, ends_at: '2026-11-20T23:00:00+08:00' }, now)).toBeNull()
    expect(weekendNextChangeAt({ ...status, ends_at: '2026-11-22T15:00:00+08:00' }, Date.parse('2026-11-21T12:00:00+08:00'))).toBe(Date.parse('2026-11-22T15:00:00+08:00'))
    expect(weekendNextChangeAt({ ...status, enabled: false }, now)).toBeNull()
    expect(weekendNextChangeAt({ ...status, mode: 'custom' }, now)).toBeNull()
    expect(formatPromotionCountdown(1)).toBe('00:00:01')
    expect(formatPromotionCountdown(172_800_000)).toBe('2天 00:00:00')
  })

  it('uses the server clock and switches countdowns exactly at Saturday midnight', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    vi.mocked(getWeekendDiscount).mockResolvedValueOnce({ ...status, server_time: '2026-11-20T23:59:59+08:00' })
    const wrapper = mount({ setup() { const { countdown, active } = useWeekendDiscount(); return () => h('span', `${active.value}:${countdown.value}`) } })
    try {
      await flushPromises()
      expect(wrapper.text()).toBe('false:00:00:01')
      await vi.advanceTimersByTimeAsync(1000)
      expect(wrapper.text()).toBe('true:2天 00:00:00')
      await vi.advanceTimersByTimeAsync(1000)
      expect(wrapper.text()).toBe('true:1天 23:59:59')
    } finally { wrapper.unmount(); vi.useRealTimers() }
  })
  it('fetches immediately on a new page even when the old page request has not finished', async () => {
    const finishes: Array<(value: WeekendDiscountStatus) => void> = []
    vi.mocked(getWeekendDiscount).mockImplementationOnce(() => new Promise(resolve => finishes.push(resolve)))
    vi.mocked(getWeekendDiscount).mockImplementationOnce(() => new Promise(resolve => finishes.push(resolve)))
    const component = { setup() { const { multiplier } = useWeekendDiscount(); return () => h('span', String(multiplier.value)) } }
    const first = mount(component)
    first.unmount()
    const second = mount(component)
    try {
      expect(finishes).toHaveLength(2)
      finishes[1]!({ ...status, mode: 'custom' })
      await flushPromises()
      expect(second.text()).toBe('0.8')
      finishes[0]!({ ...status, enabled: false })
      await flushPromises()
      expect(second.text()).toBe('0.8')
    } finally { second.unmount() }
  })
  it('updates mounted rates immediately after saving and ignores an older in-flight fetch', async () => {
    let finishFetch!: (value: WeekendDiscountStatus) => void
    vi.mocked(getWeekendDiscount).mockImplementationOnce(() => new Promise(resolve => { finishFetch = resolve }))
    const wrapper = mount({ setup() { const { multiplier } = useWeekendDiscount(); return () => h('span', String(multiplier.value)) } })
    try {
      updateWeekendDiscountStatus({ ...status, mode: 'custom' })
      await nextTick()
      expect(wrapper.text()).toBe('0.8')
      finishFetch({ ...status, enabled: false })
      await flushPromises()
      expect(wrapper.text()).toBe('0.8')
      updateWeekendDiscountStatus({ ...status, enabled: false })
      await nextTick()
      expect(wrapper.text()).toBe('1')
    } finally { wrapper.unmount() }
  })
  it.each([
    ['2026-11-20T15:59:59Z', 1],
    ['2026-11-20T16:00:00Z', .8],
    ['2026-11-22T15:59:59Z', .8],
    ['2026-11-22T16:00:00Z', 1]
  ])('uses Beijing time at %s', (at, rate) => {
    expect(weekendMultiplierAt(status, Date.parse(at))).toBe(rate)
  })

  it('honors disabled state, scheduled start and exclusive end', () => {
    const now = Date.parse('2026-11-21T12:00:00+08:00')
    expect(weekendMultiplierAt({ ...status, enabled: false }, now)).toBe(1)
    expect(weekendMultiplierAt({ ...status, starts_at: '2026-11-21T13:00:00+08:00' }, now)).toBe(1)
    expect(weekendMultiplierAt({ ...status, starts_at: '2026-11-21T12:00:00+08:00' }, now)).toBe(.8)
    expect(weekendMultiplierAt({ ...status, ends_at: '2026-11-21T12:00:00+08:00' }, now)).toBe(1)
    expect(weekendMultiplierAt(null, now)).toBe(1)
  })

  it('keeps small group rates without floating-point noise', () => {
    expect(formatPromotionRate(.218 * .8)).toBe('0.1744')
    expect(formatPromotionRate(.3 * .8)).toBe('0.24')
  })

  it('uses the custom range on weekdays and does not stop at Monday midnight', () => {
    const custom: WeekendDiscountStatus = { ...status, mode: 'custom', starts_at: '2026-11-20T12:00:00+08:00', ends_at: '2026-11-23T15:00:00+08:00' }
    for (const [at, rate] of [
      ['2026-11-20T11:59:59+08:00', 1],
      ['2026-11-20T12:00:00+08:00', .8],
      ['2026-11-23T00:00:00+08:00', .8],
      ['2026-11-23T15:00:00+08:00', 1]
    ] as const) expect(weekendMultiplierAt(custom, Date.parse(at))).toBe(rate)
    expect(weekendMultiplierAt({ ...custom, enabled: false }, Date.parse('2026-11-20T12:00:00+08:00'))).toBe(1)
    expect(weekendMultiplierAt({ ...custom, starts_at: null, ends_at: null }, Date.parse('2026-11-20T09:00:00+08:00'))).toBe(.8)
  })
})
