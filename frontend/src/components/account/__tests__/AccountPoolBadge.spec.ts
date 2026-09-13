import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountPoolBadge from '../AccountPoolBadge.vue'

describe('AccountPoolBadge', () => {
  it('keeps pool colors stable and distinct by pool id', () => {
    const first = mount(AccountPoolBadge, { props: { pool: { id: 7, name: '日本号池' } } })
    const second = mount(AccountPoolBadge, { props: { pool: { id: 8, name: '美国号池' } } })

    expect(first.text()).toBe('日本号池')
    expect(first.attributes('title')).toBe('号池：日本号池')
    expect(first.attributes('style')).not.toBe(second.attributes('style'))
    expect(first.attributes('style')).toBe(mount(AccountPoolBadge, { props: { pool: { id: 7, name: '已改名号池' } } }).attributes('style'))
  })
})
