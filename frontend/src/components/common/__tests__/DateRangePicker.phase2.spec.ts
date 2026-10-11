import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import DateRangePicker from '../DateRangePicker.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') })
}))

let wrapper: ReturnType<typeof mount<typeof DateRangePicker>> | undefined

const dateInputs = () => Array.from(
  document.body.querySelectorAll<HTMLInputElement>('.date-picker-dropdown input[type="date"]')
)

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(2026, 8, 30, 23, 59))
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.useRealTimers()
})

describe('DateRangePicker phase 2 lifecycle', () => {
  it.each(['outside', 'escape', 'toggle'])('restores the applied range after %s dismissal', async (method) => {
    wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-09-30', endDate: '2026-09-30' },
      global: { stubs: { Icon: true } }
    })
    await wrapper.get('.date-picker-trigger').trigger('click')
    const start = dateInputs()[0]
    start.value = '2026-09-29'
    start.dispatchEvent(new Event('input', { bubbles: true }))
    start.dispatchEvent(new Event('change', { bubbles: true }))
    await nextTick()

    if (method === 'outside') document.body.click()
    else if (method === 'escape') document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    else await wrapper.get('.date-picker-trigger').trigger('click')
    await nextTick()

    expect(wrapper.emitted('change')).toBeUndefined()
    await wrapper.get('.date-picker-trigger').trigger('click')
    expect(dateInputs()[0]?.value).toBe('2026-09-30')
  })

  it('recomputes preset dates and max date after midnight without remounting', async () => {
    wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-09-30', endDate: '2026-09-30' },
      global: { stubs: { Icon: true } }
    })
    await wrapper.get('.date-picker-trigger').trigger('click')
    expect(dateInputs()[1]?.getAttribute('max')).toBe('2026-10-01')
    await wrapper.get('.date-picker-trigger').trigger('click')

    vi.setSystemTime(new Date(2026, 9, 1, 0, 1))
    await wrapper.get('.date-picker-trigger').trigger('click')
    expect(dateInputs()[1]?.getAttribute('max')).toBe('2026-10-02')
    const todayPreset = Array.from(document.body.querySelectorAll<HTMLButtonElement>('.date-picker-preset'))
      .find(button => button.textContent === 'dates.today')
    todayPreset?.click()
    await nextTick()

    expect(wrapper.emitted('change')?.at(-1)?.[0]).toMatchObject({
      startDate: '2026-10-01',
      endDate: '2026-10-01',
      preset: 'today'
    })
  })
})
