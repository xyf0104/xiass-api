import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import type { Proxy } from '@/types'

const { testProxy } = vi.hoisted(() => ({
  testProxy: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

vi.mock('@/api/admin', () => ({
  adminAPI: {
    proxies: {
      testProxy
    }
  }
}))

import ProxySelector from '../ProxySelector.vue'

const originalInnerWidth = window.innerWidth
const originalInnerHeight = window.innerHeight
const originalVisualViewportDescriptor = Object.getOwnPropertyDescriptor(window, 'visualViewport')

const proxies: Proxy[] = [
  {
    id: 1,
    name: 'Alpha Proxy',
    protocol: 'http',
    host: 'alpha.example.com',
    port: 8080,
    username: null,
    status: 'active',
    expires_at: null,
    fallback_mode: 'none',
    expiry_warn_days: 7,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z'
  },
  {
    id: 2,
    name: 'Beta Proxy',
    protocol: 'socks5',
    host: 'beta.example.com',
    port: 1080,
    username: null,
    status: 'active',
    expires_at: null,
    fallback_mode: 'none',
    expiry_warn_days: 7,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z'
  }
]

let wrapper: VueWrapper | undefined

function mountSelector() {
  wrapper = mount(ProxySelector, {
    attachTo: document.body,
    props: {
      modelValue: null,
      proxies
    },
    global: {
      stubs: {
        Icon: true
      }
    }
  })
  return wrapper
}

function setViewport(width: number, height: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: height })
}

function installVisualViewport(bounds: {
  offsetTop: number
  offsetLeft: number
  width: number
  height: number
}) {
  const viewport = new EventTarget()
  Object.assign(viewport, bounds)
  Object.defineProperty(window, 'visualViewport', {
    configurable: true,
    value: viewport
  })
  return viewport
}

function rect(left: number, top: number, width: number, height: number): DOMRect {
  return {
    x: left,
    y: top,
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    toJSON: () => ({})
  } as DOMRect
}

async function openDropdown(selector: VueWrapper) {
  await selector.get('button.select-trigger').trigger('click')
  await nextTick()
  return document.body.querySelector<HTMLElement>('[data-testid="proxy-dropdown"]')
}

function findProxyRow(name: string) {
  const row = Array.from(document.body.querySelectorAll<HTMLElement>('.select-option')).find(
    candidate => candidate.textContent?.includes(name)
  )
  if (!row) throw new Error(`Proxy row not found: ${name}`)
  return row
}

describe('ProxySelector', () => {
  beforeEach(() => {
    testProxy.mockReset()
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    document.body.innerHTML = ''
    setViewport(originalInnerWidth, originalInnerHeight)
    if (originalVisualViewportDescriptor) {
      Object.defineProperty(window, 'visualViewport', originalVisualViewportDescriptor)
    } else {
      delete (window as Window & { visualViewport?: VisualViewport }).visualViewport
    }
    vi.restoreAllMocks()
  })

  it('keeps search, individual proxy testing, and selection behavior', async () => {
    testProxy.mockResolvedValue({
      success: true,
      message: 'ok',
      country: 'CN',
      latency_ms: 42
    })
    const selector = mountSelector()
    const dropdown = await openDropdown(selector)

    expect(dropdown?.parentElement).toBe(document.body)

    const search = dropdown?.querySelector<HTMLInputElement>('.select-search-input')
    if (!search) throw new Error('Proxy search input not found')
    search.value = 'alpha'
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()

    expect(dropdown?.textContent).toContain('Alpha Proxy')
    expect(dropdown?.textContent).not.toContain('Beta Proxy')

    const alphaRow = findProxyRow('Alpha Proxy')
    alphaRow.querySelector<HTMLButtonElement>('.test-btn')?.click()
    await flushPromises()

    expect(testProxy).toHaveBeenCalledWith(1)
    expect(alphaRow.textContent).toContain('CN')
    expect(alphaRow.textContent).toContain('42ms')

    alphaRow.click()
    await nextTick()

    expect(selector.emitted('update:modelValue')).toEqual([[1]])
  })

  it('keeps batch proxy testing behavior', async () => {
    testProxy.mockResolvedValue({ success: true, message: 'ok' })
    const selector = mountSelector()
    const dropdown = await openDropdown(selector)

    dropdown?.querySelector<HTMLButtonElement>('.batch-test-btn')?.click()
    await flushPromises()

    expect(testProxy).toHaveBeenCalledTimes(2)
    expect(testProxy).toHaveBeenNthCalledWith(1, 1)
    expect(testProxy).toHaveBeenNthCalledWith(2, 2)
  })

  it('shortens labels while searching original subscription names and selecting by ID', async () => {
    const subscriptionProxies = proxies.map((proxy, index) => Object.freeze({
      ...proxy,
      name: `[Subscription] Source ${index + 1} / Tokyo`
    }))
    const selector = mountSelector()
    await selector.setProps({ proxies: subscriptionProxies, modelValue: 1 })

    expect(selector.get('button.select-trigger').text()).toContain('Tokyo (http://alpha.example.com:8080)')
    expect(selector.get('button.select-trigger').text()).not.toContain('[Subscription]')

    const dropdown = await openDropdown(selector)
    const search = dropdown!.querySelector<HTMLInputElement>('.select-search-input')!
    search.value = 'Source 2'
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()

    const row = findProxyRow('Tokyo')
    expect(dropdown?.textContent).not.toContain('[Subscription]')
    expect(dropdown?.textContent).not.toContain('alpha.example.com')
    expect(row.textContent).toContain('beta.example.com')
    row.click()
    await nextTick()

    expect(selector.emitted('update:modelValue')).toEqual([[2]])
    expect(subscriptionProxies.map(proxy => proxy.name)).toEqual([
      '[Subscription] Source 1 / Tokyo',
      '[Subscription] Source 2 / Tokyo'
    ])
  })

  it('flips upward and exposes only the reachable viewport height', async () => {
    setViewport(800, 240)
    const selector = mountSelector()
    const trigger = selector.get('button.select-trigger').element as HTMLButtonElement
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(rect(30, 150, 280, 40))

    const dropdown = await openDropdown(selector)

    expect(dropdown?.style.position).toBe('fixed')
    expect(dropdown?.style.left).toBe('30px')
    expect(dropdown?.style.top).toBe('142px')
    expect(dropdown?.style.width).toBe('280px')
    expect(dropdown?.style.maxHeight).toBe('134px')
    expect(dropdown?.style.transform).toBe('translateY(-100%)')
    expect(dropdown?.style.zIndex).toBe('100000020')
  })

  it('tracks scroll, resize, and visual viewport changes while open', async () => {
    setViewport(900, 700)
    const visualViewport = installVisualViewport({
      offsetTop: 100,
      offsetLeft: 20,
      width: 360,
      height: 300
    })
    const selector = mountSelector()
    const trigger = selector.get('button.select-trigger').element as HTMLButtonElement
    let triggerRect = rect(330, 330, 120, 40)
    vi.spyOn(trigger, 'getBoundingClientRect').mockImplementation(() => triggerRect)

    const dropdown = await openDropdown(selector)

    expect(dropdown?.style.left).toBe('252px')
    expect(dropdown?.style.top).toBe('322px')
    expect(dropdown?.style.maxHeight).toBe('214px')
    expect(dropdown?.style.transform).toBe('translateY(-100%)')

    triggerRect = rect(50, 120, 120, 40)
    window.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(dropdown?.style.left).toBe('50px')
    expect(dropdown?.style.top).toBe('168px')
    expect(dropdown?.style.maxHeight).toBe('224px')
    expect(dropdown?.style.transform).toBe('none')

    triggerRect = rect(70, 140, 120, 40)
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(dropdown?.style.left).toBe('70px')
    expect(dropdown?.style.top).toBe('188px')

    triggerRect = rect(75, 150, 120, 40)
    visualViewport.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(dropdown?.style.left).toBe('75px')
    expect(dropdown?.style.top).toBe('198px')

    triggerRect = rect(80, 160, 120, 40)
    visualViewport.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(dropdown?.style.left).toBe('80px')
    expect(dropdown?.style.top).toBe('208px')
  })
})
