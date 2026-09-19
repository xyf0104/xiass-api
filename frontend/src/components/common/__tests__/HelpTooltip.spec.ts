import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'

const originalInnerWidth = window.innerWidth

function setViewportWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    value: width,
  })
}

function getTooltipElement(): HTMLDivElement {
  const tooltip = document.body.querySelector('[role="tooltip"]')
  if (!(tooltip instanceof HTMLDivElement)) {
    throw new Error('tooltip element not found')
  }
  return tooltip
}

describe('HelpTooltip', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    setViewportWidth(originalInnerWidth)
    vi.restoreAllMocks()
  })

  it('keeps the existing hover interaction by default', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'hover details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave')
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('keeps a hover tooltip open while the pointer moves between the trigger and the tooltip', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'copyable details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave', { relatedTarget: tooltip })
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: trigger.element }))
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: null }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('supports click-to-toggle details and closes on outside click', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'click details',
        trigger: 'click',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')
    expect(tooltip.textContent).toContain('click details')

    const closeButton = tooltip.querySelector('button[aria-label="Close"]')
    if (!(closeButton instanceof HTMLButtonElement)) {
      throw new Error('close button not found')
    }
    closeButton.click()
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('uses viewport coordinates and opens below when there is no space above', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'viewport details',
      },
    })
    const trigger = wrapper.get('.group')
    vi.spyOn(trigger.element, 'getBoundingClientRect').mockReturnValue({
      x: 40,
      y: 4,
      top: 4,
      right: 56,
      bottom: 20,
      left: 40,
      width: 16,
      height: 16,
      toJSON: () => ({}),
    })

    const tooltip = getTooltipElement()
    Object.defineProperty(tooltip, 'offsetWidth', { configurable: true, value: 256 })
    Object.defineProperty(tooltip, 'offsetHeight', { configurable: true, value: 80 })

    await trigger.trigger('mouseenter')
    await nextTick()
    await nextTick()

    expect(tooltip.style.top).toBe('28px')
    expect(tooltip.className).not.toContain('-translate-y-full')
    expect(tooltip.className).toContain('z-[100000100]')

    wrapper.unmount()
  })

  it('uses a semantic dark surface and constrains its rendered width before 320px positioning', async () => {
    setViewportWidth(320)
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'narrow viewport details',
        widthClass: 'w-80',
      },
    })
    const trigger = wrapper.get('.group')
    vi.spyOn(trigger.element, 'getBoundingClientRect').mockReturnValue({
      x: 0,
      y: 100,
      top: 100,
      right: 16,
      bottom: 116,
      left: 0,
      width: 16,
      height: 16,
      toJSON: () => ({}),
    })

    const tooltip = getTooltipElement()
    Object.defineProperty(tooltip, 'offsetWidth', { configurable: true, value: 304 })
    Object.defineProperty(tooltip, 'offsetHeight', { configurable: true, value: 40 })

    await trigger.trigger('mouseenter')
    await nextTick()
    await nextTick()

    expect(tooltip.className).toContain('help-tooltip-surface')
    expect(tooltip.style.maxWidth).toBe('304px')
    expect(tooltip.style.left).toBe('160px')
    expect(Number.parseFloat(tooltip.style.left) - 304 / 2).toBe(8)
    expect(Number.parseFloat(tooltip.style.left) + 304 / 2).toBe(312)
    expect(tooltip.querySelector('.help-tooltip-arrow')).not.toBeNull()

    wrapper.unmount()
  })
})
