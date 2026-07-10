import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PriceCell from './PriceCell.vue'

describe('PriceCell', () => {
  it('keeps the NoWind token conversion for group and official prices', () => {
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
