import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  getSettings,
  getExecutionNodeStatus,
  showError,
  showWarning
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  getSettings: vi.fn(),
  getExecutionNodeStatus: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      batchDelete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      bulkUpdate: vi.fn()
    },
    proxies: {
      getAll: getAllProxies
    },
    groups: {
      getAll: getAllGroups
    },
    settings: { getSettings },
    executionNodes: { getStatus: getExecutionNodeStatus }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
    showWarning
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    token: 'test-token'
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const makeAccounts = (count: number) => Array.from({ length: count }, (_, index) => ({
  id: index + 1,
  name: `account-${index + 1}`,
  platform: 'grok',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  created_at: '2026-07-23T00:00:00Z',
  updated_at: '2026-07-23T00:00:00Z'
}))

const AccountBulkActionsBarStub = {
  props: ['selectedIds', 'totalResults', 'selectingAll', 'allResultsSelected'],
  emits: ['select-all-results', 'select-page', 'clear', 'edit-selected', 'edit-filtered'],
  template: `
    <div>
      <span data-test="selected-count">{{ selectedIds.length }}</span>
      <span data-test="total-results">{{ totalResults }}</span>
      <span data-test="all-results-selected">{{ String(allResultsSelected) }}</span>
      <button data-test="select-page" @click="$emit('select-page')">select page</button>
      <button data-test="select-all-results" @click="$emit('select-all-results')">select all</button>
      <button data-test="edit-selected" @click="$emit('edit-selected')">edit selected</button>
      <button data-test="edit-filtered" @click="$emit('edit-filtered')">edit filtered</button>
      <button data-test="clear" @click="$emit('clear')">clear</button>
    </div>
  `
}

const BulkEditAccountModalStub = {
  props: ['show', 'target'],
  template: `
    <div v-if="show" data-test="bulk-edit-modal">
      <span data-test="bulk-edit-platforms">{{ target?.selectedPlatforms?.join(',') }}</span>
      <span data-test="bulk-edit-types">{{ target?.selectedTypes?.join(',') }}</span>
      <span data-test="bulk-edit-owner">{{ target?.filters?.execution_node_id || 'all' }}</span>
    </div>
  `
}

const AccountTableFiltersStub = {
  emits: ['change'],
  template: '<button data-test="change-filter" @click="$emit(\'change\')">change filter</button>'
}

const mountView = () => mount(AccountsView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: {
        template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
      },
      DataTable: {
        props: ['data'],
        template: `
          <div data-test="data-table">
            <slot name="header-select" />
            <div v-for="row in data" :key="row.id" :data-test="'account-row-' + row.id">
              <slot name="cell-select" :row="row" />
              <slot name="cell-name" :row="row" :value="row.name" />
            </div>
          </div>
        `
      },
      Pagination: true,
      ConfirmDialog: true,
      AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
      AccountTableFilters: AccountTableFiltersStub,
      AccountBulkActionsBar: AccountBulkActionsBarStub,
      AccountActionMenu: true,
      ImportDataModal: true,
      ReAuthAccountModal: true,
      AccountTestModal: true,
      PelicanBenchmarkModal: { props: ['show'], emits: ['close'], template: '<div v-if="show" data-test="pelican-modal"><button data-test="close-pelican" @click="$emit(\'close\')">close</button></div>' },
      AccountStatsModal: true,
      ScheduledTestsPanel: true,
      SyncFromCrsModal: true,
      TempUnschedStatusModal: true,
      ErrorPassthroughRulesModal: true,
      TLSFingerprintProfilesModal: true,
      CreateAccountModal: true,
      EditAccountModal: true,
      BulkEditAccountModal: BulkEditAccountModalStub,
      PlatformTypeBadge: true,
      AccountCapacityCell: true,
      AccountStatusIndicator: true,
      AccountTodayStatsCell: true,
      AccountGroupsCell: true,
      AccountUsageCell: true,
      Icon: { props: ['name'], template: '<i :data-icon="name" />' }
    }
  }
})

describe('admin AccountsView select all filtered results', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset()
    listWithEtag.mockReset()
    getBatchTodayStats.mockReset()
    getUpstreamBillingProbeSettings.mockReset()
    getAllProxies.mockReset()
    getAllGroups.mockReset()
    getSettings.mockReset()
    getExecutionNodeStatus.mockReset()
    showError.mockReset()
    showWarning.mockReset()

    listWithEtag.mockResolvedValue({
      notModified: true,
      etag: null,
      data: null
    })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: true, interval_minutes: 30 })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
    getSettings.mockResolvedValue({ team_child_creation_enabled: false })
    getExecutionNodeStatus.mockResolvedValue({
      balancing_enabled: false,
      can_enable: true,
      admin_write_allowed: true,
      admin_write_mode: 'single_node',
      database_reachable: true,
      heartbeat_store_reachable: true,
      runtime: {
        enabled: false,
        node_id: '',
        default_proxy_id: 0,
        emergency_local_egress: false,
        control_plane: true,
        legacy_unassigned_node_id: 'api',
        legacy_unassigned_proxy_id: 0
      },
      nodes: [],
      issues: []
    })
  })

  it('selects all matching IDs in one commit and clears the selection when filters change', async () => {
    const allAccounts = makeAccounts(45)
    listAccounts.mockImplementation(async (_page: number, pageSize: number) => {
      if (pageSize === 1000) {
        return {
          items: allAccounts,
          total: 45,
          page: 1,
          page_size: 1000,
          pages: 1
        }
      }
      return {
        items: allAccounts.slice(0, 20),
        total: 45,
        page: 1,
        page_size: 20,
        pages: 3
      }
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('45')
    expect(wrapper.get('[data-test="total-results"]').text()).toBe('45')
    expect(wrapper.get('[data-test="all-results-selected"]').text()).toBe('true')
    expect(listAccounts).toHaveBeenCalledWith(1, 1000, expect.objectContaining({
      lite: '1',
      include_scheduler_score: '0'
    }))

    await wrapper.get('[data-test="change-filter"]').trigger('click')

    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('0')
    expect(wrapper.get('[data-test="all-results-selected"]').text()).toBe('false')
  })

  it('starts every visit with recent account activity first instead of restoring an old table sort', async () => {
    localStorage.setItem('account-table-sort', JSON.stringify({ key: 'name', order: 'asc' }))
    listAccounts.mockResolvedValue({
      items: makeAccounts(1),
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })

    mountView()
    await flushPromises()

    expect(listAccounts).toHaveBeenCalledWith(
      1,
      20,
      expect.objectContaining({
        sort_by: 'recent_activity',
        sort_order: 'desc'
      }),
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })

  it('does not reload today statistics when an automatic account refresh is unchanged', async () => {
    listAccounts.mockResolvedValue({
      items: makeAccounts(1),
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()
    const initialTodayStatsCalls = getBatchTodayStats.mock.calls.length

    await wrapper.vm.$nextTick()
    expect(typeof (wrapper.vm as any).refreshAccountsIncrementally).toBe('function')
    await (wrapper.vm as any).refreshAccountsIncrementally()
    await flushPromises()

    expect(getBatchTodayStats).toHaveBeenCalledTimes(initialTodayStatsCalls)
  })

  it('keeps the original page selection when loading all results fails', async () => {
    const currentPage = makeAccounts(20)
    listAccounts.mockImplementation(async (_page: number, pageSize: number) => {
      if (pageSize === 1000) {
        throw new Error('load all failed')
      }
      return {
        items: currentPage,
        total: 45,
        page: 1,
        page_size: 20,
        pages: 3
      }
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="select-page"]').trigger('click')
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('20')

    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('20')
    expect(wrapper.get('[data-test="all-results-selected"]').text()).toBe('false')
    expect(showError).toHaveBeenCalledWith('admin.accounts.bulkActions.selectAllFailed')
  })

  it('uses metadata from every selected results page before opening bulk edit', async () => {
    const firstPage = makeAccounts(1000)
    const lastAccount = {
      ...makeAccounts(1)[0],
      id: 1001,
      name: 'account-1001',
      platform: 'openai',
      type: 'apikey'
    }

    listAccounts.mockImplementation(async (page: number, pageSize: number) => {
      if (pageSize === 1000) {
        return {
          items: page === 1 ? firstPage : [lastAccount],
          total: 1001,
          page,
          page_size: 1000,
          pages: 2
        }
      }
      return {
        items: firstPage.slice(0, 20),
        total: 1001,
        page: 1,
        page_size: 20,
        pages: 51
      }
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="edit-selected"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="bulk-edit-platforms"]').text()).toBe('grok,openai')
    expect(wrapper.get('[data-test="bulk-edit-types"]').text()).toBe('oauth,apikey')
    expect(listAccounts).toHaveBeenCalledWith(2, 1000, expect.objectContaining({
      lite: '1',
      include_scheduler_score: '0'
    }))
  })

  it('keeps an online remote api2 account amber, read-only, and outside page or result selection', async () => {
    const localAccount = { ...makeAccounts(1)[0], id: 1, name: 'local', execution_node_id: 'api' }
    const remoteAccount = { ...makeAccounts(1)[0], id: 2, name: 'remote', execution_node_id: 'api2' }
    const accounts = [localAccount, remoteAccount]
    listAccounts.mockResolvedValue({ items: accounts, total: 2, page: 1, page_size: 20, pages: 1 })
    getExecutionNodeStatus.mockResolvedValue({
      balancing_enabled: true,
      can_enable: true,
      admin_write_allowed: true,
      admin_write_mode: 'primary',
      database_reachable: true,
      heartbeat_store_reachable: true,
      runtime: {
        enabled: true,
        node_id: 'api',
        default_proxy_id: 84,
        emergency_local_egress: false,
        control_plane: true,
        legacy_unassigned_node_id: 'api',
        legacy_unassigned_proxy_id: 84
      },
      nodes: [
        { node_id: 'api', online: true, is_local: true },
        { node_id: 'api2', online: true, is_local: false }
      ],
      issues: []
    })

    const wrapper = mountView()
    await flushPromises()

    const remoteRow = wrapper.get('[data-test="account-row-2"]')
    const remoteCheckbox = remoteRow.get('input[type="checkbox"]')
    const remoteBadge = remoteRow.get('span[title="admin.accounts.executionNodeRemoteReadOnly"]')
    expect(remoteCheckbox.attributes('disabled')).toBeDefined()
    expect(remoteBadge.classes()).toEqual(expect.arrayContaining([
      'bg-amber-50',
      'text-amber-700',
      'whitespace-nowrap',
      'shrink-0'
    ]))
    expect(remoteBadge.text()).toContain('api2')
    expect(remoteBadge.text()).toContain('admin.accounts.executionNodeReadOnlyBadge')

    await wrapper.get('[data-test="select-page"]').trigger('click')
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('1')

    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('1')
    expect(showWarning).toHaveBeenCalledWith('admin.accounts.executionNodeBulkExcluded')
    wrapper.unmount()
  })

  it.each([
    ['api', 'api2'],
    ['api2', 'api']
  ])('allows paired management from %s of %s without changing owner styling or filtered selection', async (local, remote) => {
    const accounts = [
      { ...makeAccounts(1)[0], id: 1, execution_node_id: local },
      { ...makeAccounts(1)[0], id: 2, execution_node_id: remote }
    ]
    listAccounts.mockResolvedValue({ items: accounts, total: 2, page: 1, page_size: 20, pages: 1 })
    getExecutionNodeStatus.mockResolvedValue({
      admin_write_mode: 'paired_full_access', admin_write_allowed: true,
      runtime: { enabled: true, node_id: local, emergency_local_egress: false, legacy_unassigned_node_id: 'api' },
      nodes: [{ node_id: local, online: true, is_local: true }, { node_id: remote, online: true, is_local: false }]
    })
    const wrapper = mountView()
    await flushPromises()
    const row = wrapper.get('[data-test="account-row-2"]')
    expect(row.get('input[type="checkbox"]').attributes('disabled')).toBeUndefined()
    const badge = row.get('span[title="admin.accounts.columns.executionNodeHint"]')
    expect(badge.classes()).toEqual(expect.arrayContaining(['bg-amber-50', 'text-amber-700']))
    expect(badge.get('[data-icon="server"]').exists()).toBe(true)
    expect(badge.text()).toContain(remote)
    expect(badge.text()).toContain('admin.accounts.executionNodeManageableBadge')
    expect(badge.text()).not.toContain('executionNodeTakeoverBadge')
    expect(badge.text()).not.toContain('executionNodeReadOnlyBadge')
    await wrapper.get('[data-test="select-page"]').trigger('click')
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('2')
    await wrapper.get('[data-test="clear"]').trigger('click')
    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('2')
    expect(showWarning).not.toHaveBeenCalled()
    await wrapper.get('[data-test="edit-selected"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="bulk-edit-modal"]').exists()).toBe(true)
    await wrapper.get('[data-test="edit-filtered"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="bulk-edit-owner"]').text()).toBe('all')
    wrapper.unmount()
  })

  it.each([false, undefined])('does not grant paired access without explicit write permission (%s)', async allowed => {
    const account = { ...makeAccounts(1)[0], execution_node_id: 'api2' }
    listAccounts.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    getExecutionNodeStatus.mockResolvedValue({
      admin_write_mode: 'paired_full_access', admin_write_allowed: allowed,
      runtime: { enabled: true, node_id: 'api', emergency_local_egress: false },
      nodes: [{ node_id: 'api2', online: true, is_local: false }]
    })
    const wrapper = mountView()
    await flushPromises()
    const row = wrapper.get('[data-test="account-row-1"]')
    expect(row.get('input[type="checkbox"]').attributes('disabled')).toBeDefined()
    expect(row.text()).toContain('executionNodeReadOnlyBadge')
    expect(row.text()).not.toContain('executionNodeManageableBadge')
    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('0')
    wrapper.unmount()
  })

  it('denies local, legacy-unassigned and remote writes when pairing is unavailable', async () => {
    const accounts = ['api', '', 'api2'].map((owner, i) => ({ ...makeAccounts(1)[0], id: i + 1, execution_node_id: owner }))
    listAccounts.mockResolvedValue({ items: accounts, total: 3, page: 1, page_size: 20, pages: 1 })
    getExecutionNodeStatus.mockResolvedValue({
      admin_write_mode: 'pairing_unavailable', admin_write_allowed: false,
      runtime: { enabled: true, node_id: 'api', legacy_unassigned_node_id: 'api', emergency_local_egress: true },
      nodes: [{ node_id: 'api', online: true, is_local: true }, { node_id: 'api2', online: false, is_local: false }]
    })
    const wrapper = mountView()
    await flushPromises()
    for (let i = 1; i <= 3; i++) expect(wrapper.get(`[data-test="account-row-${i}"] input[type="checkbox"]`).attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="account-pairing-unavailable"]').text()).toContain('executionNodePairingUnavailable')
    expect(wrapper.find('[data-testid="pelican-benchmark"]').exists()).toBe(false)
    await wrapper.get('[data-test="edit-filtered"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="bulk-edit-modal"]').exists()).toBe(false)
    expect(showError).toHaveBeenCalledWith('admin.accounts.executionNodePairingUnavailable')
    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('0')
    wrapper.unmount()
  })

  it('opens and closes the benchmark without changing list requests, selection or pagination', async () => {
    const accounts = makeAccounts(45)
    listAccounts.mockResolvedValue({ items: accounts.slice(0, 20), total: 45, page: 1, page_size: 20, pages: 3 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="select-page"]').trigger('click')
    const calls = listAccounts.mock.calls.length
    await wrapper.get('[data-testid="pelican-benchmark"]').trigger('click')
    expect(wrapper.find('[data-test="pelican-modal"]').exists()).toBe(true)
    await wrapper.get('[data-test="close-pelican"]').trigger('click')
    expect(wrapper.find('[data-test="pelican-modal"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('20')
    expect(wrapper.get('[data-test="total-results"]').text()).toBe('45')
    expect(listAccounts).toHaveBeenCalledTimes(calls)
    expect(listWithEtag).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps a taken-over remote api2 badge amber while restoring account selection', async () => {
    const remoteAccount = { ...makeAccounts(1)[0], id: 2, name: 'remote', execution_node_id: 'api2' }
    listAccounts.mockResolvedValue({ items: [remoteAccount], total: 1, page: 1, page_size: 20, pages: 1 })
    getExecutionNodeStatus.mockResolvedValue({
      balancing_enabled: true,
      can_enable: true,
      admin_write_allowed: true,
      admin_write_mode: 'emergency_takeover',
      database_reachable: true,
      heartbeat_store_reachable: true,
      runtime: {
        enabled: true,
        node_id: 'api',
        default_proxy_id: 84,
        emergency_local_egress: true,
        control_plane: true,
        legacy_unassigned_node_id: 'api',
        legacy_unassigned_proxy_id: 84
      },
      nodes: [
        { node_id: 'api', online: true, is_local: true },
        { node_id: 'api2', online: false, is_local: false }
      ],
      issues: []
    })

    const wrapper = mountView()
    await flushPromises()

    const remoteRow = wrapper.get('[data-test="account-row-2"]')
    const remoteCheckbox = remoteRow.get('input[type="checkbox"]')
    const remoteBadge = remoteRow.get('span[title="admin.accounts.columns.executionNodeHint"]')
    expect(remoteCheckbox.attributes('disabled')).toBeUndefined()
    expect(remoteBadge.classes()).toEqual(expect.arrayContaining([
      'bg-amber-50',
      'text-amber-700',
      'whitespace-nowrap',
      'shrink-0'
    ]))
    expect(remoteBadge.text()).toContain('api2')
    expect(remoteBadge.text()).toContain('admin.accounts.executionNodeTakeoverBadge')

    await wrapper.get('[data-test="select-page"]').trigger('click')
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('1')
    wrapper.unmount()
  })
})
