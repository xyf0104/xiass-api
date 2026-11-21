import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PriceCell from './PriceCell.vue'

describe('PriceCell', () => {
  it('strikes the original sale price and discounts the final group price once', async () => {
    const wrapper = mount(PriceCell, { props: { basePrice: .000002, groupBasePrice: .000003, multiplier: .218, discountMultiplier: .8, mode: 'group' } })
    expect(wrapper.get('[data-test="original-price"]').text()).toBe('¥0.654')
    expect(wrapper.get('[data-test="current-price"]').text()).toBe('¥0.5232')
    expect(wrapper.get('[data-test="current-price"]').classes()).toContain('text-orange-700')
    expect(wrapper.find('[data-test="discount-icon"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('官方价格 ¥14')
    await wrapper.setProps({ discountMultiplier: 1 })
    expect(wrapper.find('[data-test="original-price"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="current-price"]').text()).toBe('¥0.654')
    expect(wrapper.find('[data-test="discount-icon"]').exists()).toBe(false)
    await wrapper.setProps({ mode: 'official', discountMultiplier: .8 })
    expect(wrapper.text()).toContain('¥14')
    expect(wrapper.find('[data-test="original-price"]').exists()).toBe(false)
  })

  it('discounts per-request and media prices', () => {
    const wrapper = mount(PriceCell, { props: { basePrice: .25, multiplier: 2, discountMultiplier: .8, mode: 'group', scale: 1, unit: '/ 次' } })
    expect(wrapper.get('[data-test="original-price"]').text()).toBe('¥0.5')
    expect(wrapper.get('[data-test="current-price"]').text()).toBe('¥0.4')
  })

  it('keeps the XIASS token conversion for group and official prices', () => {
    const wrapper = mount(PriceCell, {
      props: {
        basePrice: 0.000002,
        multiplier: 3.5,
        mode: 'group'
      }
    })

    expect(wrapper.text()).toContain('¥7')
    expect(wrapper.text()).toContain('官方价格 ¥14')
    expect(wrapper.text()).toContain('/ 1M tokens')
    expect(wrapper.text()).not.toContain('$')
    expect(wrapper.get('.font-mono').classes()).toContain('whitespace-nowrap')
  })

  it('supports per-second media prices without the per-million scale', () => {
    const wrapper = mount(PriceCell, {
      props: {
        basePrice: 0.08,
        multiplier: 3.5,
        mode: 'group',
        scale: 1,
        unit: '/ 秒'
      }
    })

    expect(wrapper.text()).toContain('¥0.28')
    expect(wrapper.text()).toContain('官方价格 ¥0.56')
    expect(wrapper.text()).toContain('/ 秒')
  })

  it('uses an independent group media base without changing the official price', () => {
    const wrapper = mount(PriceCell, {
      props: {
        basePrice: 0.08,
        groupBasePrice: 0.12,
        multiplier: 3.5,
        mode: 'group',
        scale: 1,
        unit: '/ 秒'
      }
    })

    expect(wrapper.text()).toContain('¥0.42')
    expect(wrapper.text()).toContain('官方价格 ¥0.56')
  })
})
