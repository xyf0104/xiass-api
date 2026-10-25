import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'

import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import type { ApiKey } from '@/types'
import KeysView from '../KeysView.vue'

const {
  listKeys,
  createKey,
  updateKey,
  getPublicSettings,
  getDashboardApiKeysUsage,
  getAvailableGroups,
  getUserGroupRates,
  showError,
  showSuccess,
  copyToClipboard,
  isCurrentStep,
  nextStep,
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  createKey: vi.fn(),
  updateKey: vi.fn(),
  getPublicSettings: vi.fn(),
  getDashboardApiKeysUsage: vi.fn(),
  getAvailableGroups: vi.fn(),
  getUserGroupRates: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  copyToClipboard: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
}))

const messages: Record<string, string> = {
  'common.actions': 'Actions',
  'common.name': 'Name',
  'common.refresh': 'Refresh',
  'common.status': 'Status',
  'keys.apiKey': 'API Key',
  'keys.allGroups': 'All Groups',
  'keys.allStatus': 'All Status',
  'keys.columnSettings': 'Column Settings',
  'keys.createKey': 'Create API Key',
  'keys.created': 'Created',
  'keys.expiresAt': 'Expires',
  'keys.group': 'Group',
  'keys.id': 'ID',
  'keys.currentConcurrency': 'Current Concurrency',
  'keys.lastUsedAt': 'Last Used',
  'keys.lastUsedIP': 'Last Used IP',
  'keys.rateLimitColumn': 'Rate Limit',
  'keys.searchPlaceholder': 'Search name or key...',
  'keys.status.active': 'Active',
  'keys.status.expired': 'Expired',
  'keys.status.inactive': 'Inactive',
  'keys.status.quota_exhausted': 'Quota exhausted',
  'keys.usage': 'Usage',
}

vi.mock('@/api', () => ({
  keysAPI: {
    list: listKeys,
    create: createKey,
    update: updateKey,
    delete: vi.fn(),
    toggleStatus: vi.fn(),
  },
  authAPI: {
    getPublicSettings,
  },
  usageAPI: {
    getDashboardApiKeysUsage,
  },
  userGroupsAPI: {
    getAvailable: getAvailableGroups,
    getUserGroupRates,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep,
    nextStep,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const createApiKey = (): ApiKey => ({
  id: 1,
  user_id: 1,
  key: 'sk-test-key',
  name: 'test-key',
  group_id: null,
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-06-27T00:00:00Z',
  updated_at: '2026-06-27T00:00:00Z',
  current_concurrency: 3,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
})

const AppLayoutStub = {
  template: '<div><slot /></div>',
}

const TablePageLayoutStub = {
  template: `
    <div>
      <slot name="filters" />
      <slot name="actions" />
      <slot name="table" />
      <slot name="pagination" />
    </div>
  `,
}

const DataTableStub = {
  name: 'DataTable',
  props: ['columns', 'data'],
  emits: ['sort'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="columns-meta">{{ JSON.stringify(columns.map((col) => ({ key: col.key, sortable: !!col.sortable }))) }}</div>
      <button data-test="sort-current-concurrency" @click="$emit('sort', 'current_concurrency', 'asc')">
        Sort Current Concurrency
      </button>
      <div v-for="row in data" :key="row.id">
        <div
          v-if="columns.some((col) => col.key === 'id')"
          data-test="key-id"
        >
          <slot name="cell-id" :value="row.id" :row="row" />
        </div>
        <slot name="cell-name" :value="row.name" :row="row" />
        <div data-test="group-cell">
          <slot name="cell-group" :value="row.group" :row="row" />
        </div>
        <div data-test="current-concurrency">
          <slot name="cell-current_concurrency" :value="row.current_concurrency" :row="row" />
        </div>
        <div data-test="actions-cell">
          <slot name="cell-actions" :row="row" />
        </div>
        <div
          v-if="columns.some((col) => col.key === 'last_used_ip')"
          data-test="last-used-ip"
        >
          <slot name="cell-last_used_ip" :value="row.last_used_ip" :row="row" />
        </div>
      </div>
      <slot name="empty" />
    </div>
  `,
}

const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"></select>',
}

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const SearchInputStub = {
  name: 'SearchInput',
  props: ['modelValue'],
  emits: ['update:modelValue', 'search'],
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
}

const PaginationStub = {
  name: 'Pagination',
  props: ['page', 'total', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: `
    <div>
      <button data-test="page-size-50" @click="$emit('update:pageSize', 50)">50</button>
    </div>
  `,
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const GroupBadgeStub = {
  name: 'GroupBadge',
  props: ['name', 'userRateMultiplier'],
  template: '<span data-test="group-badge">{{ name }}</span>',
}

const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    modelValue: { type: Array, required: true },
    groups: { type: Array, required: true },
    userGroupRates: { type: Object, default: () => ({}) },
  },
  emits: ['update:modelValue'],
  template: `
    <div data-test="group-selector">
      <div data-test="group-selector-order">{{ groups.map((group) => group.id).join(',') }}</div>
      <button
        v-for="group in groups"
        :key="group.id"
        type="button"
        :data-group-id="group.id"
        @click="$emit('update:modelValue', modelValue.includes(group.id) ? modelValue.filter((id) => id !== group.id) : [...modelValue, group.id])"
      >
        {{ group.name }}
      </button>
    </div>
  `,
})

const mountView = async () => {
  const wrapper = mount(KeysView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: PaginationStub,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        SearchInput: SearchInputStub,
        Icon: IconStub,
        UseKeyModal: true,
        EndpointPopover: true,
        GroupBadge: GroupBadgeStub,
        GroupSelector: GroupSelectorStub,
        Teleport: true,
      },
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

const visibleColumnKeys = (wrapper: VueWrapper) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

const visibleColumnMeta = (wrapper: VueWrapper): Array<{ key: string; sortable: boolean }> =>
  JSON.parse(wrapper.get('[data-test="columns-meta"]').text())

const getButtonByText = (wrapper: VueWrapper, text: string) => {
  const button = wrapper.findAll('button').find((item) => item.text().includes(text))
  if (!button) {
    throw new Error(`Button not found: ${text}`)
  }
  return button
}

describe('user KeysView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    createKey.mockResolvedValue(createApiKey())
    updateKey.mockResolvedValue(createApiKey())
    isCurrentStep.mockReturnValue(false)
  })

  it('uses the default API key columns with low-frequency columns hidden', async () => {
    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'group',
      'current_concurrency',
      'usage',
      'expires_at',
      'status',
      'created_at',
      'actions',
    ])
    expect(visibleColumnKeys(wrapper)).not.toContain('rate_limit')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_at')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_ip')
    expect(visibleColumnKeys(wrapper)).not.toContain('id')
  })

  it('shows a hidden column when toggled and persists the preference', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Rate Limit').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('rate_limit')
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['id', 'last_used_at', 'last_used_ip'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('shows the API key ID column when toggled', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'ID').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('id')
    expect(wrapper.get('[data-test="key-id"]').text()).toBe('#1')
    expect(visibleColumnMeta(wrapper).find((column) => column.key === 'id')?.sortable).toBe(true)
  })

  it('shows the last used IP column when toggled', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), last_used_ip: '203.0.113.10' }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Last Used IP').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('last_used_ip')
    expect(wrapper.get('[data-test="last-used-ip"]').text()).toBe('203.0.113.10')
  })

  it('restores column preferences from localStorage on mount', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['group', 'created_at']))
    localStorage.setItem('api-key-column-settings-version', '1')

    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'current_concurrency',
      'usage',
      'rate_limit',
      'expires_at',
      'status',
      'last_used_at',
      'actions',
    ])
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['group', 'created_at', 'last_used_ip', 'id'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('does not include always-visible columns in the toggleable menu', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await nextTick()

    const columnMenuText = wrapper.text()
    expect(columnMenuText).toContain('API Key')
    expect(columnMenuText).toContain('ID')
    expect(columnMenuText).toContain('Current Concurrency')
    expect(columnMenuText).toContain('Rate Limit')
    expect(columnMenuText).toContain('Last Used IP')
    expect(columnMenuText).not.toContain('Name')
    expect(columnMenuText).not.toContain('Actions')
  })

  it('renders the current concurrency value', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="current-concurrency"]').text()).toBe('3')
  })

  it('marks current concurrency as sortable', async () => {
    const wrapper = await mountView()

    const currentConcurrencyColumn = visibleColumnMeta(wrapper).find(
      (column) => column.key === 'current_concurrency'
    )
    expect(currentConcurrencyColumn?.sortable).toBe(true)
  })

  it('keeps filters and selected page size when sorting by current concurrency', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 42, name: 'OpenAI' }])
    const wrapper = await mountView()

    await wrapper.get('[data-test="page-size-50"]').trigger('click')
    await flushPromises()

    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('update:modelValue', 'target')
    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    await flushPromises()

    const selects = wrapper.findAllComponents({ name: 'Select' })
    await selects[0].vm.$emit('update:modelValue', 42)
    await flushPromises()
    await selects[1].vm.$emit('update:modelValue', 'active')
    await flushPromises()

    listKeys.mockClear()

    await wrapper.get('[data-test="sort-current-concurrency"]').trigger('click')
    await flushPromises()

    expect(listKeys).toHaveBeenLastCalledWith(
      1,
      50,
      {
        search: 'target',
        status: 'active',
        group_id: 42,
        sort_by: 'current_concurrency',
        sort_order: 'asc',
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })

  it('renders real group menu names and rates and searches names and descriptions', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'OpenAI Standard', description: 'General requests', platform: 'openai', rate_multiplier: 1, is_exclusive: false },
      { id: 2, name: 'Claude Premium', description: 'Long context access', platform: 'anthropic', rate_multiplier: 1.5, is_exclusive: false },
    ])
    getUserGroupRates.mockResolvedValue({ 2: 0.25 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="group-cell"] button').trigger('click')

    const options = wrapper.findAllComponents(GroupOptionItem)
    expect(options).toHaveLength(2)
    expect(options[0].text()).toContain('OpenAI Standard')
    expect(options[0].text()).toContain('1x')
    expect(options[1].text()).toContain('Claude Premium')
    expect(options[1].text()).toContain('1.5x')
    expect(options[1].text()).toContain('0.25x')

    const search = wrapper.get('input[placeholder="keys.searchGroup"]')
    await search.setValue('  cLaUdE  ')
    expect(wrapper.findAllComponents(GroupOptionItem)).toHaveLength(1)
    expect(wrapper.getComponent(GroupOptionItem).text()).toContain('Claude Premium')

    await search.setValue('GENERAL REQUESTS')
    expect(wrapper.findAllComponents(GroupOptionItem)).toHaveLength(1)
    expect(wrapper.getComponent(GroupOptionItem).text()).toContain('OpenAI Standard')

    await search.setValue('missing-group')
    expect(wrapper.findAllComponents(GroupOptionItem)).toHaveLength(0)
    expect(wrapper.text()).toContain('keys.noGroupFound')

    await search.setValue('')
    expect(wrapper.findAllComponents(GroupOptionItem)).toHaveLength(2)
    expect(updateKey).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('updates the key when selecting a different group from the real menu', async () => {
    const groups = [
      { id: 1, name: 'OpenAI Standard', platform: 'openai', rate_multiplier: 1, is_exclusive: false },
      { id: 2, name: 'Claude Premium', platform: 'anthropic', rate_multiplier: 1.5, is_exclusive: false },
    ]
    getAvailableGroups.mockResolvedValue(groups)
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), group_id: 1, group_ids: [1], group: groups[0], groups: [groups[0]] }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('[data-test="group-cell"] button').trigger('click')
    await wrapper.get('input[placeholder="keys.searchGroup"]').setValue('OpenAI Standard')
    await wrapper.getComponent(GroupOptionItem).trigger('click')
    await flushPromises()
    expect(updateKey).not.toHaveBeenCalled()

    await wrapper.get('[data-test="group-cell"] button').trigger('click')
    expect(wrapper.findAllComponents(GroupOptionItem)).toHaveLength(2)
    await wrapper.get('input[placeholder="keys.searchGroup"]').setValue('Claude Premium')
    await wrapper.getComponent(GroupOptionItem).trigger('click')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledExactlyOnceWith(1, { group_id: 2 })
    expect(listKeys).toHaveBeenCalledTimes(2)
    expect(showSuccess).toHaveBeenCalledWith('keys.groupChangedSuccess')
    expect(showError).not.toHaveBeenCalled()
    expect(wrapper.find('input[placeholder="keys.searchGroup"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('orders the multi-select groups by public status, platform, and effective rate', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'Exclusive OpenAI', platform: 'openai', rate_multiplier: 0.01, is_exclusive: true },
      { id: 2, name: 'Claude', platform: 'anthropic', rate_multiplier: 0.1, is_exclusive: false },
      { id: 3, name: 'OpenAI Base 0.1', platform: 'openai', rate_multiplier: 0.1, is_exclusive: false },
      { id: 4, name: 'OpenAI Custom 0.2', platform: 'openai', rate_multiplier: 0.2, is_exclusive: false },
      { id: 5, name: 'Gemini', platform: 'gemini', rate_multiplier: 0.01, is_exclusive: false },
    ])
    getUserGroupRates.mockResolvedValue({ 3: 0.3, 4: 0.05 })
    const wrapper = await mountView()

    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await nextTick()

    expect(wrapper.get('[data-test="group-selector-order"]').text()).toBe('4,3,2,5,1')
    expect(wrapper.findComponent({ name: 'GroupSelector' }).props('userGroupRates')).toEqual({ 3: 0.3, 4: 0.05 })
  })

  it('submits the selected groups in stable first-selection order when creating a key', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'OpenAI', platform: 'openai', rate_multiplier: 0.1, is_exclusive: false },
      { id: 2, name: 'Claude', platform: 'anthropic', rate_multiplier: 0.1, is_exclusive: false },
    ])
    const wrapper = await mountView()

    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('multi-group-key')
    await wrapper.get('[data-group-id="2"]').trigger('click')
    await wrapper.get('[data-group-id="1"]').trigger('click')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledWith(
      'multi-group-key',
      2,
      [2, 1],
      undefined,
      [],
      [],
      0,
      undefined,
      { rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 },
    )
  })

  it('hydrates and persists all selected groups when editing a key', async () => {
    const groups = [
      { id: 1, name: 'OpenAI', platform: 'openai', rate_multiplier: 0.1, is_exclusive: false },
      { id: 2, name: 'Claude', platform: 'anthropic', rate_multiplier: 0.1, is_exclusive: false },
      { id: 3, name: 'Gemini', platform: 'gemini', rate_multiplier: 0.1, is_exclusive: false },
    ]
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), group_id: 2, group_ids: [2, 1], groups: [groups[1], groups[0]] }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAvailableGroups.mockResolvedValue(groups)
    const wrapper = await mountView()

    await getButtonByText(wrapper, 'common.edit').trigger('click')
    await nextTick()
    expect(wrapper.findComponent({ name: 'GroupSelector' }).props('modelValue')).toEqual([2, 1])

    await wrapper.get('[data-group-id="3"]').trigger('click')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({
      group_id: 2,
      group_ids: [2, 1, 3],
    }))
  })

  it('renders the ordered group badges and count for multi-group keys', async () => {
    const groups = [
      { id: 1, name: 'OpenAI', platform: 'openai', rate_multiplier: 0.1, is_exclusive: false },
      { id: 2, name: 'Claude', platform: 'anthropic', rate_multiplier: 0.1, is_exclusive: false },
      { id: 3, name: 'Gemini', platform: 'gemini', rate_multiplier: 0.1, is_exclusive: false },
    ]
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), group_id: 2, group_ids: [2, 1, 3], groups: [groups[1], groups[0], groups[2]] }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    expect(wrapper.findAll('[data-test="group-badge"]').map((badge) => badge.text())).toEqual(['Claude', 'OpenAI'])
    expect(wrapper.get('[data-test="group-cell"]').text()).toContain('+1')
  })
})
