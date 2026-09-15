import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import OpenAIModelPrioritySettings from '../OpenAIModelPrioritySettings.vue'

const { getSettings, updateSettings, listAccounts, getGroups, getPools, showError, showSuccess } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
  listAccounts: vi.fn(),
  getGroups: vi.fn(),
  getPools: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    settings: {
      getOpenAIModelPrioritySettings: getSettings,
      updateOpenAIModelPrioritySettings: updateSettings
    },
    accounts: { list: listAccounts },
    groups: { getAllIncludingInactive: getGroups },
    accountPools: { list: getPools }
  }
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values ? `${key}:${JSON.stringify(values)}` : key })
  }
})

const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>'
}
const SearchInputStub = {
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />'
}
const ToggleStub = {
  props: ['modelValue', 'disabled'],
  emits: ['update:modelValue'],
  template: '<button type="button" :disabled="disabled" @click="$emit(\'update:modelValue\', !modelValue)" />'
}
const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" data-testid="priority-dialog"><slot /><div><slot name="footer" /></div></div>'
}

function account(id: number, name: string, plan: string, groupIDs: number[], status = 'active') {
  return {
    id, name, platform: 'openai', type: 'oauth', credentials: { email: `${name}@example.com`, plan_type: plan },
    extra: {}, group_ids: groupIDs, execution_node_id: 'api', concurrency: 3, priority: id,
    status, schedulable: true, error_message: null, last_used_at: null, expires_at: null,
    auto_pause_on_expired: false, created_at: '', updated_at: '', proxy_id: null,
    rate_limited_at: null, rate_limit_reset_at: null, overload_until: null,
    temp_unschedulable_until: null, temp_unschedulable_reason: null,
    session_window_start: null, session_window_end: null, session_window_status: null
  }
}

describe('OpenAIModelPrioritySettings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getSettings.mockResolvedValue({ enabled: true, rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [3] }] })
    updateSettings.mockImplementation(async (payload) => payload)
    listAccounts.mockResolvedValue({
      items: [account(1, 'plus-one', 'plus', [14]), account(2, 'plus-two', 'plus', [14]), account(3, 'pro-one', 'pro', [14])],
      total: 3, pages: 1, page: 1, page_size: 200
    })
    getGroups.mockResolvedValue([{ id: 14, name: 'ChatGPT Pro 20x' }])
    getPools.mockResolvedValue({ items: [{ id: 5, name: 'QQ-plus', proxy_id: null, account_ids: [1], account_count: 1 }] })
  })

  function mountComponent() {
    return mount(OpenAIModelPrioritySettings, {
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Icon: true,
          Pagination: true,
          PlatformTypeBadge: true,
          SearchInput: SearchInputStub,
          Select: SelectStub,
          Toggle: ToggleStub
        }
      }
    })
  }

  it('loads the account catalog in batches and saves all filtered Plus accounts without per-account reads', async () => {
    const wrapper = mountComponent()
    await flushPromises()

    expect(listAccounts).toHaveBeenCalledTimes(1)
    expect(listAccounts).toHaveBeenCalledWith(1, 200, expect.objectContaining({ platform: 'openai' }))

    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="model-priority-plan-filter"]').setValue('plus')
    await wrapper.get('[data-testid="model-priority-clear-selection"]').trigger('click')
    await wrapper.get('[data-testid="model-priority-select-filtered"]').trigger('click')
    await wrapper.get('[data-testid="model-priority-apply-accounts"]').trigger('click')
    await wrapper.get('[data-testid="model-priority-save"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith({
      enabled: true,
      rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [1, 2] }]
    })
    expect(showSuccess).toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
  })
})
