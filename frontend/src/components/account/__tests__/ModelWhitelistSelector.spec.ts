import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

const copyToClipboard = vi.fn().mockResolvedValue(true)

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => (key === 'common.copy' ? '复制' : key)
    })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'
import { accountsAPI } from '@/api/admin/accounts'

const originalInnerWidth = window.innerWidth
const originalInnerHeight = window.innerHeight
const originalVisualViewportDescriptor = Object.getOwnPropertyDescriptor(window, 'visualViewport')

let wrapper: VueWrapper | undefined

function mountSelector(props: Record<string, unknown> = {}) {
  wrapper = mount(ModelWhitelistSelector, {
    attachTo: document.body,
    props: {
      modelValue: [],
      platform: 'openai',
      ...props
    },
    global: {
      stubs: {
        ModelIcon: true
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
  await selector.get('div.cursor-pointer').trigger('click')
  await nextTick()
  return document.body.querySelector<HTMLElement>('[data-testid="model-dropdown"]')
}

function findModelRow(modelId: string) {
  const row = Array.from(
    document.body.querySelectorAll<HTMLElement>('[data-testid="model-option"]')
  ).find(candidate => candidate.textContent?.includes(modelId))

  if (!row) {
    throw new Error(`Model row not found: ${modelId}`)
  }

  return row
}

describe('ModelWhitelistSelector', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
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

  it('copies a model ID without selecting the model', async () => {
    const selector = mountSelector()
    await openDropdown(selector)

    const row = findModelRow('gpt-5.6-sol')
    const copyButton = row.querySelector<HTMLButtonElement>('[data-testid="copy-model-id"]')

    expect(copyButton?.getAttribute('aria-label')).toBe('复制 gpt-5.6-sol')

    copyButton?.click()
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(selector.emitted('update:modelValue')).toBeUndefined()
  })

  it('keeps the teleported model menu opaque instead of using the glass floating surface', async () => {
    const selector = mountSelector()
    const dropdown = await openDropdown(selector)

    expect(dropdown?.parentElement).toBe(document.body)
    expect(dropdown?.classList.contains('console-floating-surface')).toBe(false)
    expect(dropdown?.classList.contains('model-whitelist-dropdown')).toBe(true)
  })

  it('keeps search and model selection behavior inside the teleported dropdown', async () => {
    const selector = mountSelector()
    const dropdown = await openDropdown(selector)

    expect(dropdown?.parentElement).toBe(document.body)

    const search = dropdown?.querySelector<HTMLInputElement>('input')
    if (!search) throw new Error('Search input not found')
    search.value = 'gpt-5.6-sol'
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()

    const rows = document.body.querySelectorAll('[data-testid="model-option"]')
    expect(rows).toHaveLength(1)

    const selectButton = findModelRow('gpt-5.6-sol').querySelector<HTMLButtonElement>(
      '[data-testid="select-model"]'
    )
    selectButton?.click()
    await nextTick()

    expect(selector.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol']]])
    expect(copyToClipboard).not.toHaveBeenCalled()
  })

  it('Antigravity 上游同步把内部 3.7 模型转换成三档外部模型', async () => {
    vi.spyOn(accountsAPI, 'syncUpstreamModels').mockResolvedValue({
      models: [
        'gemini-3.7-flash-tiered',
        'gemini-3.7-flash',
        'gemini-3.7-flash-high'
      ]
    } as any)
    const selector = mountSelector({ platform: 'antigravity', accountId: 7 })
    const syncButton = selector
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.syncUpstreamModels'))

    if (!syncButton) throw new Error('Sync upstream button not found')
    await syncButton.trigger('click')
    await flushPromises()

    const updates = selector.emitted('update:modelValue') || []
    expect(updates[updates.length - 1]).toEqual([[
      'gemini-3.7-flash-high',
      'gemini-3.7-flash-medium',
      'gemini-3.7-flash-low'
    ]])
    expect(JSON.stringify(updates)).not.toContain('gemini-3.7-flash-tiered')
  })

  it('uses the larger upper space and constrains max-height near the viewport bottom', async () => {
    setViewport(800, 240)
    const selector = mountSelector()
    const trigger = selector.get('div.cursor-pointer').element as HTMLElement
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(rect(30, 150, 280, 40))

    const dropdown = await openDropdown(selector)

    expect(dropdown?.style.position).toBe('fixed')
    expect(dropdown?.style.left).toBe('30px')
    expect(dropdown?.style.top).toBe('146px')
    expect(dropdown?.style.width).toBe('280px')
    expect(dropdown?.style.maxHeight).toBe('138px')
    expect(dropdown?.style.transform).toBe('translateY(-100%)')
    expect(dropdown?.style.zIndex).toBe('100000020')
  })

  it('repositions on scroll, resize, and visual viewport changes', async () => {
    setViewport(900, 700)
    const visualViewport = installVisualViewport({
      offsetTop: 100,
      offsetLeft: 20,
      width: 360,
      height: 300
    })
    const selector = mountSelector()
    const trigger = selector.get('div.cursor-pointer').element as HTMLElement
    let triggerRect = rect(330, 330, 120, 40)
    vi.spyOn(trigger, 'getBoundingClientRect').mockImplementation(() => triggerRect)

    const dropdown = await openDropdown(selector)

    expect(dropdown?.style.left).toBe('252px')
    expect(dropdown?.style.top).toBe('326px')
    expect(dropdown?.style.maxHeight).toBe('218px')
    expect(dropdown?.style.transform).toBe('translateY(-100%)')

    triggerRect = rect(50, 120, 120, 40)
    window.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(dropdown?.style.left).toBe('50px')
    expect(dropdown?.style.top).toBe('164px')
    expect(dropdown?.style.maxHeight).toBe('228px')
    expect(dropdown?.style.transform).toBe('none')

    triggerRect = rect(70, 140, 120, 40)
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(dropdown?.style.left).toBe('70px')
    expect(dropdown?.style.top).toBe('184px')

    triggerRect = rect(80, 150, 120, 40)
    visualViewport.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(dropdown?.style.left).toBe('80px')
    expect(dropdown?.style.top).toBe('194px')

    triggerRect = rect(90, 160, 120, 40)
    visualViewport.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(dropdown?.style.left).toBe('90px')
    expect(dropdown?.style.top).toBe('204px')
  })
})
