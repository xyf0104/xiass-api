import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import ChannelsView from '../ChannelsView.vue'

const { listChannels, getAllGroups, getWebSearchEmulationConfig } = vi.hoisted(() => ({
  listChannels: vi.fn(),
  getAllGroups: vi.fn(),
  getWebSearchEmulationConfig: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channels: {
      list: listChannels,
      create: vi.fn(),
      update: vi.fn(),
      remove: vi.fn(),
      syncPricingModels: vi.fn()
    },
    groups: { getAll: getAllGroups },
    settings: { getWebSearchEmulationConfig },
    accounts: { list: vi.fn(), getById: vi.fn() }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))

vi.mock('@/composables/useExecutionNodeAdminAccess', () => ({
  useExecutionNodeAdminAccess: () => ({
    executionNodeStatus: {
      __v_isRef: true,
      value: {
        admin_write_allowed: false,
        admin_write_mode: 'secondary_read_only',
        runtime: { enabled: true }
      }
    },
    sharedWriteAllowed: { __v_isRef: true, value: false },
    sharedReadOnly: { __v_isRef: true, value: true },
    loadExecutionNodeAdminAccess: vi.fn().mockResolvedValue(null)
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-test="'channel-row-' + row.id">
        <slot name="cell-status" :row="row" :value="row.status" />
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
}

const ToggleStub = {
  props: ['modelValue', 'disabled'],
  template: '<button type="button" data-test="channel-status-toggle" :disabled="disabled" />'
}

function mountView() {
  return mount(ChannelsView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: true,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        Icon: { props: ['name'], template: '<i :data-icon="name" />' },
        PlatformIcon: true,
        Toggle: ToggleStub,
        PricingEntryCard: true
      }
    }
  })
}

describe('ChannelsView execution-node shared access', () => {
  beforeEach(() => {
    listChannels.mockReset()
    getAllGroups.mockReset()
    getWebSearchEmulationConfig.mockReset()
    listChannels.mockResolvedValue({
      items: [{
        id: 7,
        name: 'Shared OpenAI',
        description: '',
        status: 'active',
        group_ids: [],
        model_pricing: [],
        created_at: '2026-09-01T00:00:00Z'
      }],
      total: 1
    })
    getAllGroups.mockResolvedValue([])
    getWebSearchEmulationConfig.mockResolvedValue({ enabled: false, providers: [] })
  })

  it('shows the secondary read-only notice and disables channel writes', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="execution-node-shared-access-notice"]').text()).toContain(
      'admin.executionNodes.sharedAccess.readOnlyTitle'
    )
    const createButton = wrapper.findAll('button').find((button) => (
      button.text().includes('admin.channels.createChannel')
    ))
    expect(createButton).toBeDefined()
    expect(createButton!.attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="channel-status-toggle"]').attributes('disabled')).toBeDefined()

    const row = wrapper.get('[data-test="channel-row-7"]')
    const editButton = row.findAll('button').find((button) => button.text().includes('common.edit'))
    const deleteButton = row.findAll('button').find((button) => button.text().includes('common.delete'))
    expect(editButton?.attributes('disabled')).toBeDefined()
    expect(deleteButton?.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
