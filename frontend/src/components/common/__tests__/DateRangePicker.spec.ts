import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'

import DateRangePicker from '../DateRangePicker.vue'

const originalInnerWidth = window.innerWidth
const originalVisualViewport = window.visualViewport

const setViewportWidth = (width: number) => {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    value: width
  })
}

const setVisualViewport = (value: Partial<VisualViewport> | undefined) => {
  Object.defineProperty(window, 'visualViewport', {
    configurable: true,
    value: value
      ? {
          offsetLeft: 0,
          offsetTop: 0,
          width: window.innerWidth,
          height: window.innerHeight,
          addEventListener: vi.fn(),
          removeEventListener: vi.fn(),
          ...value
        }
      : undefined
  })
}

const messages: Record<string, string> = {
  'dates.today': 'Today',
  'dates.yesterday': 'Yesterday',
  'dates.last24Hours': 'Last 24 Hours',
  'dates.last7Days': 'Last 7 Days',
  'dates.last14Days': 'Last 14 Days',
  'dates.last30Days': 'Last 30 Days',
  'dates.thisMonth': 'This Month',
  'dates.lastMonth': 'Last Month',
  'dates.startDate': 'Start Date',
  'dates.endDate': 'End Date',
  'dates.apply': 'Apply',
  'dates.selectDateRange': 'Select date range'
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => messages[key] ?? key,
    locale: ref('en')
  })
}))

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

describe('DateRangePicker', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    setViewportWidth(originalInnerWidth)
    setVisualViewport(originalVisualViewport)
    vi.restoreAllMocks()
  })

  it('uses last 24 hours as the default recognized preset', () => {
    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: formatLocalDate(yesterday),
        endDate: formatLocalDate(now)
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    expect(wrapper.text()).toContain('Last 24 Hours')
  })

  it('applies a quick preset immediately', async () => {
    const now = new Date()
    const today = formatLocalDate(now)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: today,
        endDate: today
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    await wrapper.find('.date-picker-trigger').trigger('click')
    const dropdown = document.body.querySelector<HTMLElement>('.date-picker-dropdown')
    const presetButton = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>('.date-picker-preset')
    ).find((node) =>
      node.textContent?.includes('Last 24 Hours')
    )

    expect(dropdown?.parentElement).toBe(document.body)
    expect(presetButton).toBeDefined()

    presetButton!.click()
    await nextTick()

    const nowAfterClick = new Date()
    const yesterdayAfterClick = new Date(nowAfterClick.getTime() - 24 * 60 * 60 * 1000)
    const expectedStart = formatLocalDate(yesterdayAfterClick)
    const expectedEnd = formatLocalDate(nowAfterClick)

    expect(wrapper.emitted('update:startDate')?.[0]).toEqual([expectedStart])
    expect(wrapper.emitted('update:endDate')?.[0]).toEqual([expectedEnd])
    expect(wrapper.emitted('change')?.[0]).toEqual([
      {
        startDate: expectedStart,
        endDate: expectedEnd,
        preset: 'last24Hours'
      }
    ])
    expect(
      document.body.querySelector('.date-picker-dropdown')?.classList.contains('date-picker-dropdown-leave-active')
    ).toBe(true)
  })

  it('fits a 320px viewport and keeps the panel reachable above a reduced visual viewport', async () => {
    setViewportWidth(320)
    setVisualViewport({
      offsetLeft: 0,
      offsetTop: 100,
      width: 320,
      height: 240
    })
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 20,
      y: 250,
      top: 250,
      right: 120,
      bottom: 290,
      left: 20,
      width: 100,
      height: 40,
      toJSON: () => ({})
    })
    const today = formatLocalDate(new Date())
    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: today,
        endDate: today
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    await wrapper.get('.date-picker-trigger').trigger('click')
    await nextTick()
    const dropdown = document.body.querySelector<HTMLElement>('.date-picker-dropdown')

    expect(dropdown?.style.left).toBe('8px')
    expect(dropdown?.style.width).toBe('304px')
    expect(dropdown?.style.top).toBe('108px')
    expect(dropdown?.style.maxHeight).toBe('134px')
    expect(Number.parseFloat(dropdown?.style.left || '0') + Number.parseFloat(dropdown?.style.width || '0')).toBe(312)

    wrapper.unmount()
  })
})
