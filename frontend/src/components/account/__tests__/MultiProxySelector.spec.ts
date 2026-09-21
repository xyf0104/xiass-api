import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { Proxy } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => `${key}${values ? JSON.stringify(values) : ''}` })
  }
})
import MultiProxySelector from '../MultiProxySelector.vue'

const proxies: Proxy[] = [
  {
    id: 2,
    name: 'IPv6 exit',
    protocol: 'socks5',
    host: '2001:db8::20',
    port: 1080,
    username: null,
    status: 'active',
    expires_at: null,
    fallback_mode: 'none',
    expiry_warn_days: 7,
    created_at: '2026-09-19T00:00:00Z',
    updated_at: '2026-09-19T00:00:00Z'
  },
  {
    id: 1,
    name: 'IPv4 exit',
    protocol: 'http',
    host: '192.0.2.10',
    port: 8080,
    username: null,
    status: 'active',
    expires_at: null,
    fallback_mode: 'none',
    expiry_warn_days: 7,
    created_at: '2026-09-19T00:00:00Z',
    updated_at: '2026-09-19T00:00:00Z'
  }
]

describe('MultiProxySelector', () => {
  it('selects exits, edits capacity and rank, and preserves IPv6 formatting', async () => {
    const wrapper = mount(MultiProxySelector, {
      props: { modelValue: [], proxies },
      global: { stubs: { Icon: true } }
    })

    expect(wrapper.text()).toContain('[2001:db8::20]:1080')
    await wrapper.findAll('input[type="checkbox"]')[0].setValue(true)
    const first = wrapper.emitted('update:modelValue')?.at(-1)?.[0]
    expect(first).toEqual([{ proxy_id: 2, max_concurrency: 1, route_priority: 0 }])

    await wrapper.setProps({ modelValue: first })
    const numericInputs = wrapper.findAll('input[type="number"]')
    await numericInputs[0].setValue(3)
    const ranked = wrapper.emitted('update:modelValue')?.at(-1)?.[0]
    expect(ranked).toEqual([{ proxy_id: 2, max_concurrency: 1, route_priority: 3 }])

    await wrapper.setProps({ modelValue: ranked })
    await wrapper.findAll('input[type="number"]')[1].setValue(4)
    const updated = wrapper.emitted('update:modelValue')?.at(-1)?.[0]
    expect(updated).toEqual([{ proxy_id: 2, max_concurrency: 4, route_priority: 3 }])
  })

  it('keeps emitted bindings sorted for stable account payloads', async () => {
    const wrapper = mount(MultiProxySelector, {
      props: {
        modelValue: [{ proxy_id: 2, max_concurrency: 2 }],
        proxies
      },
      global: { stubs: { Icon: true } }
    })

    await wrapper.findAll('input[type="checkbox"]')[1].setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      { proxy_id: 1, max_concurrency: 1, route_priority: 0 },
      { proxy_id: 2, max_concurrency: 2, route_priority: 0 }
    ])
  })

  it('defaults to balanced and emits an explicit adaptive preference', async () => {
    const wrapper = mount(MultiProxySelector, {
      props: { modelValue: [], proxies, adaptiveAvailable: true },
      global: { stubs: { Icon: true } }
    })

    expect(wrapper.get('[data-testid="multi-proxy-balanced-mode"]').classes()).toContain('bg-white')
    await wrapper.get('[data-testid="multi-proxy-adaptive-mode"]').trigger('click')
    expect(wrapper.emitted('update:adaptiveEnabled')?.at(-1)?.[0]).toBe(true)
  })
})
