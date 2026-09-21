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
const VueDraggableStub = {
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: '<div><slot /><button type="button" data-testid="draggable-model-update" @click="$emit(\'update:modelValue\', [...modelValue].reverse())" /></div>'
}

function account(id: number, name: string, plan: string, groupIDs: number[], status = 'active', priority = id) {
  return {
    id, name, platform: 'openai', type: 'oauth', credentials: { email: `${name}@example.com`, plan_type: plan },
    extra: {}, group_ids: groupIDs, execution_node_id: 'api', concurrency: 3, priority,
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
          Toggle: ToggleStub,
          VueDraggable: VueDraggableStub
        }
      }
    })
  }

  function selectedIDs(wrapper: ReturnType<typeof mountComponent>): number[] {
    return wrapper.findAll('[data-testid^="model-priority-selected-"]')
      .filter(node => node.attributes('data-testid') !== 'model-priority-selected-order')
      .map(node => Number(node.attributes('data-testid').split('-').at(-1)))
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
      rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [1, 2], account_order: [1, 2] }]
    })
    expect(showSuccess).toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
  })

  it('shows legacy selections by global priority then ID while retaining missing account placeholders', async () => {
    getSettings.mockResolvedValue({ enabled: true, rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [3, 99, 2, 1] }] })
    listAccounts.mockResolvedValue({
      items: [account(1, 'one', 'plus', [14], 'active', 20), account(2, 'two', 'plus', [14], 'active', 10), account(3, 'three', 'pro', [14], 'active', 20)],
      total: 3, pages: 1, page: 1, page_size: 200
    })
    const wrapper = mountComponent()
    await flushPromises()

    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    await flushPromises()

    expect(selectedIDs(wrapper)).toEqual([2, 1, 3, 99])
    expect(wrapper.get('[data-testid="model-priority-selected-99"]').text()).toContain('missingPriorityAccount')

    await wrapper.get('[data-testid="model-priority-save"]').trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({
      enabled: true,
      rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [3, 99, 2, 1] }]
    })
  })

  it('preserves explicit order across reopen and save', async () => {
    getSettings.mockResolvedValue({ enabled: true, rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [1, 2, 3], account_order: [3, 1, 2] }] })
    const wrapper = mountComponent()
    await flushPromises()

    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    await flushPromises()
    expect(selectedIDs(wrapper)).toEqual([3, 1, 2])
    await wrapper.get('[data-testid="model-priority-apply-accounts"]').trigger('click')
    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    expect(selectedIDs(wrapper)).toEqual([3, 1, 2])
    await wrapper.get('[data-testid="model-priority-apply-accounts"]').trigger('click')
    await wrapper.get('[data-testid="model-priority-save"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith({
      enabled: true,
      rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [3, 1, 2], account_order: [3, 1, 2] }]
    })
  })

  it('reorders through draggable model updates, keyboard arrows, and move buttons', async () => {
    getSettings.mockResolvedValue({ enabled: true, rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [1, 2, 3], account_order: [1, 2, 3] }] })
    const wrapper = mountComponent()
    await flushPromises()
    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="model-priority-drag-1"]').attributes('title')).toContain('dragPriorityAccount')
    expect(wrapper.get('[data-testid="model-priority-up-1"]').attributes('title')).toContain('movePriorityAccountUp')
    expect(wrapper.get('[data-testid="model-priority-down-1"]').attributes('title')).toContain('movePriorityAccountDown')
    await wrapper.get('[data-testid="draggable-model-update"]').trigger('click')
    expect(selectedIDs(wrapper)).toEqual([3, 2, 1])
    await wrapper.get('[data-testid="model-priority-drag-2"]').trigger('keydown', { key: 'ArrowUp' })
    expect(selectedIDs(wrapper)).toEqual([2, 3, 1])
    await wrapper.get('[data-testid="model-priority-down-2"]').trigger('click')
    expect(selectedIDs(wrapper)).toEqual([3, 2, 1])
    await wrapper.get('[data-testid="model-priority-up-1"]').trigger('click')
    expect(selectedIDs(wrapper)).toEqual([3, 1, 2])
  })

  it('appends and removes filtered selections without disturbing the remaining order', async () => {
    getSettings.mockResolvedValue({ enabled: true, rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [3], account_order: [3] }] })
    const wrapper = mountComponent()
    await flushPromises()
    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    await wrapper.get('[data-testid="model-priority-plan-filter"]').setValue('plus')
    await wrapper.get('[data-testid="model-priority-select-filtered"]').trigger('click')
    expect(selectedIDs(wrapper)).toEqual([3, 1, 2])

    await wrapper.get('[data-testid="model-priority-account-1"] input').setValue(false)
    expect(selectedIDs(wrapper)).toEqual([3, 2])
  })

  it('cancels picker edits without changing the saved rule', async () => {
    getSettings.mockResolvedValue({ enabled: true, rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [1, 2] }] })
    const wrapper = mountComponent()
    await flushPromises()
    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    await wrapper.get('[data-testid="draggable-model-update"]').trigger('click')
    await wrapper.get('[data-testid="model-priority-cancel-accounts"]').trigger('click')
    expect(wrapper.find('[data-testid="priority-dialog"]').exists()).toBe(false)
    await wrapper.get('[data-testid="model-priority-save"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith({
      enabled: true,
      rules: [{ model_pattern: 'gpt-5.6-luna', account_ids: [1, 2] }]
    })
  })

  it('keeps a closed picker closed when a pending catalog load resolves', async () => {
    let resolveAccounts: (value: unknown) => void = () => {}
    listAccounts.mockImplementation(() => new Promise(resolve => { resolveAccounts = resolve }))
    const wrapper = mountComponent()
    await flushPromises()

    await wrapper.get('[data-testid="model-priority-configure-0"]').trigger('click')
    expect(wrapper.get('[data-testid="model-priority-apply-accounts"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-testid="model-priority-cancel-accounts"]').trigger('click')
    expect(wrapper.find('[data-testid="priority-dialog"]').exists()).toBe(false)

    resolveAccounts({
      items: [account(3, 'pro-one', 'pro', [14])],
      total: 1, pages: 1, page: 1, page_size: 200
    })
    await flushPromises()

    expect(wrapper.find('[data-testid="priority-dialog"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="model-priority-selected-order"]').exists()).toBe(false)
    expect(showError).not.toHaveBeenCalled()
  })
})
