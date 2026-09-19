import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import SubscriptionProgressMini from '../SubscriptionProgressMini.vue'

const { fetchActiveSubscriptions, subscriptionStore } = vi.hoisted(() => {
  const fetchActiveSubscriptions = vi.fn().mockResolvedValue([])
  return {
    fetchActiveSubscriptions,
    subscriptionStore: {
      activeSubscriptions: [
        {
          id: 1,
          group_id: 1,
          daily_usage_usd: 1,
          weekly_usage_usd: 2,
          monthly_usage_usd: 3,
          expires_at: null,
          group: {
            name: 'Starter',
            daily_limit_usd: 10,
            weekly_limit_usd: 20,
            monthly_limit_usd: 30,
          },
        },
      ],
      hasActiveSubscriptions: true,
      fetchActiveSubscriptions,
      },
  }
})

vi.mock('@/stores', () => ({
  useSubscriptionStore: () => subscriptionStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    cachedPublicSettings: { subscription_enabled: true },
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const originalInnerWidth = window.innerWidth
const originalVisualViewport = window.visualViewport

function setViewportWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    value: width,
  })
}

function setVisualViewport(value: Partial<VisualViewport> | undefined) {
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
          ...value,
        }
      : undefined,
  })
}

describe('SubscriptionProgressMini viewport positioning', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    setViewportWidth(originalInnerWidth)
    setVisualViewport(originalVisualViewport)
    vi.restoreAllMocks()
  })

  it('teleports and clamps the 340px desktop popover inside a 320px viewport', async () => {
    setViewportWidth(320)
    setVisualViewport({ width: 320, height: 568 })
    const wrapper = mount(SubscriptionProgressMini, {
      attachTo: document.body,
      global: {
        stubs: {
          Icon: true,
          RouterLink: {
            template: '<a><slot /></a>',
          },
        },
      },
    })
    vi.spyOn(wrapper.element, 'getBoundingClientRect').mockReturnValue({
      x: 220,
      y: 16,
      top: 16,
      right: 280,
      bottom: 52,
      left: 220,
      width: 60,
      height: 36,
      toJSON: () => ({}),
    })

    await wrapper.get('button').trigger('click')
    await nextTick()
    await nextTick()
    const popover = document.body.querySelector<HTMLElement>('.subscription-popover')

    expect(popover?.parentElement).toBe(document.body)
    expect(popover?.style.position).toBe('')
    expect(popover?.className).toContain('fixed')
    expect(popover?.style.left).toBe('8px')
    expect(popover?.style.width).toBe('304px')
    expect(Number.parseFloat(popover?.style.left || '0') + Number.parseFloat(popover?.style.width || '0')).toBe(312)
    expect(fetchActiveSubscriptions).toHaveBeenCalled()

    wrapper.unmount()
  })
})
