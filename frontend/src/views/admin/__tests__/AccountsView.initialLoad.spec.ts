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
  getConcurrencyStats,
  routeState,
  routerReplace
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  getConcurrencyStats: vi.fn(),
  routeState: { query: {} as Record<string, string> },
  routerReplace: vi.fn()
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ replace: routerReplace, push: vi.fn() })
  }
})

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    ops: { getConcurrencyStats },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const DataTableStub = {
  props: ['data', 'loading'],
  template: '<div data-test="data-table"></div>'
}

const AccountTableActionsStub = {
  emits: ['refresh', 'create'],
  template: `
    <div data-test="account-actions">
      <slot name="beforeCreate" />
      <button data-test="manual-refresh" @click="$emit('refresh')">refresh</button>
      <button data-test="create-account" @click="$emit('create')">create</button>
    </div>
  `
}

const mountView = () => mount(AccountsView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: {
        template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
      },
      DataTable: DataTableStub,
      Pagination: true,
      ConfirmDialog: true,
      AccountTableActions: AccountTableActionsStub,
      AccountTableFilters: { template: '<div></div>' },
      AccountBulkActionsBar: true,
      AccountActionMenu: true,
      ImportDataModal: true,
      ReAuthAccountModal: true,
      AccountTestModal: true,
      AccountStatsModal: true,
      OAuthBillingBreakdownDialog: true,
      ScheduledTestsPanel: true,
      SyncFromCrsModal: true,
      TempUnschedStatusModal: true,
      ErrorPassthroughRulesModal: true,
      TLSFingerprintProfilesModal: true,
      TotpStepUpDialog: true,
      CreateAccountModal: true,
      EditAccountModal: true,
      BulkEditAccountModal: true,
      PlatformTypeBadge: true,
      AccountCapacityCell: true,
      AccountStatusIndicator: true,
      AccountTodayStatsCell: true,
      AccountGroupsCell: true,
      AccountUsageCell: true,
      UpstreamBillingRateCell: true,
      Icon: true
    }
  }
})

const liteAccount = {
  id: 224,
  name: 'team3',
  platform: 'openai',
  type: 'oauth',
  concurrency: 10,
  priority: 0,
  status: 'active',
  schedulable: true
}

const completeAccount = {
  ...liteAccount,
  credentials: {
    email: 'complete@example.com',
    plan_type: 'team',
    subscription_expires_at: 1788307200
  },
  groups: [{ id: 7, name: 'ChatGPT Team' }],
  current_concurrency: 1,
  current_window_cost: 40.24,
  active_sessions: 2,
  rate_multiplier: 0.1,
  extra: {
    privacy_mode: 'private',
    upstream_billing_probe_enabled: true,
    upstream_billing_rate: 0.1
  },
  created_at: '2026-08-01T00:00:00Z',
  updated_at: '2026-08-18T00:00:00Z'
}

const paginated = (account: typeof liteAccount | typeof completeAccount) => ({
  items: [account],
  total: 1,
  page: 1,
  page_size: 20,
  pages: 1
})

const tableAccount = (wrapper: ReturnType<typeof mountView>) => {
  const data = wrapper.getComponent(DataTableStub).props('data') as Array<Record<string, unknown>>
  return data[0]
}

describe('admin AccountsView initial data synchronization', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset()
    listWithEtag.mockReset()
    getBatchTodayStats.mockReset()
    getUpstreamBillingProbeSettings.mockReset()
    getAllProxies.mockReset()
    getAllGroups.mockReset()
    getConcurrencyStats.mockReset()
    routerReplace.mockReset()
    routeState.query = {}

    listAccounts.mockImplementation(async (_page: number, _pageSize: number, filters: { lite?: string }) => (
      paginated(filters?.lite === '1' ? liteAccount : completeAccount)
    ))
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({
      stats: {
        '224': { requests: 382, tokens: 56_300_000, cost: 40.24, standard_cost: 40.24, user_cost: 9.29 }
      }
    })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: true, interval_minutes: 30 })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([{ id: 7, name: 'ChatGPT Team' }])
    getConcurrencyStats.mockResolvedValue({ enabled: true, account: {} })
  })

  it('uses the complete account payload on first navigation, manual refresh, and a later revisit', async () => {
    const firstVisit = mountView()
    await flushPromises()

    expect(listAccounts.mock.calls[0]?.[2]).not.toHaveProperty('lite')
    expect(tableAccount(firstVisit)).toMatchObject({
      credentials: expect.objectContaining({ email: 'complete@example.com', plan_type: 'team' }),
      groups: [{ id: 7, name: 'ChatGPT Team' }],
      current_concurrency: 1,
      current_window_cost: 40.24,
      active_sessions: 2,
      extra: expect.objectContaining({ upstream_billing_rate: 0.1 })
    })
    expect(getBatchTodayStats).toHaveBeenLastCalledWith([224])
    expect(listWithEtag).not.toHaveBeenCalled()

    await firstVisit.get('[data-test="manual-refresh"]').trigger('click')
    await flushPromises()

    expect(listAccounts.mock.calls[1]?.[2]).not.toHaveProperty('lite')
    expect(tableAccount(firstVisit)).toMatchObject(completeAccount)
    firstVisit.unmount()

    const secondVisit = mountView()
    await flushPromises()

    expect(listAccounts.mock.calls[2]?.[2]).not.toHaveProperty('lite')
    expect(tableAccount(secondVisit)).toMatchObject(completeAccount)
    expect(getBatchTodayStats).toHaveBeenLastCalledWith([224])
    secondVisit.unmount()
  })

  it('shows the XIASS workbench and native create action without a duplicate Team entry', async () => {
    const wrapper = mountView()
    expect(wrapper.find('[data-testid="account-toolbar-loading"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="account-actions"]').exists()).toBe(false)

    await flushPromises()

    expect(wrapper.find('[data-testid="account-toolbar-loading"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="batch-openai-oauth"]').text()).toContain('XIASS工作台')
    expect(wrapper.find('[data-testid="create-team-child"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="create-account"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('updates the visible concurrency badge from the realtime snapshot without reloading the table', async () => {
    getConcurrencyStats.mockResolvedValue({
      enabled: true,
      account: {
        '224': {
          account_id: 224,
          current_in_use: 4,
          max_capacity: 10,
          load_percentage: 40,
          waiting_in_queue: 0
        }
      }
    })

    const wrapper = mountView()
    await flushPromises()

    expect(tableAccount(wrapper)).toMatchObject({ current_concurrency: 4 })
    expect(listAccounts).toHaveBeenCalledTimes(1)
    expect(getConcurrencyStats).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('opens an exact account-only view from the Team history link', async () => {
    routeState.query = { account_id: '317' }
    const wrapper = mountView()
    await flushPromises()

    expect(listAccounts.mock.calls[0]?.[2]).toMatchObject({ account_id: '317' })
    expect(wrapper.get('[data-test="focused-account-filter"]').text()).toContain('账号 #317')

    await wrapper.get('[data-test="focused-account-filter"] button').trigger('click')
    await flushPromises()
    expect(routerReplace).toHaveBeenCalledWith({ name: 'AdminAccounts', query: {} })
    wrapper.unmount()
  })
})
