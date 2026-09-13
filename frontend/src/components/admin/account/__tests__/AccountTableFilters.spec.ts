import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountTableFilters from '../AccountTableFilters.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue', 'change'],
  template: `
    <select :value="modelValue" @change="$emit('update:modelValue', $event.target.value); $emit('change')">
      <option v-for="option in options" :key="String(option.value)" :value="option.value">{{ option.label }}</option>
    </select>
  `
}

describe('AccountTableFilters account pool filter', () => {
  it('lists pools and emits the selected pool without changing other filters', async () => {
    const filters = { platform: '', type: '', status: '', privacy_mode: '', group: '', account_pool: '', execution_node_id: '' }
    const wrapper = mount(AccountTableFilters, {
      props: {
        searchQuery: '',
        filters,
        pools: [{ id: 8, name: '沐念云', proxy_id: null, account_ids: [1], account_count: 1 }]
      },
      global: {
        stubs: {
          Select: SelectStub,
          SearchInput: { template: '<input />' }
        }
      }
    })

    const selects = wrapper.findAll('select')
    expect(selects[5].text()).toContain('沐念云')
    await selects[5].setValue('8')

    expect(wrapper.emitted('update:filters')?.[0]?.[0]).toEqual({ ...filters, account_pool: '8' })
    expect(wrapper.emitted('change')).toHaveLength(1)
  })
})
