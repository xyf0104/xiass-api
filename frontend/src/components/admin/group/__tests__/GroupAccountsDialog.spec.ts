import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account, AdminGroup } from '@/types'
import GroupAccountsDialog from '../GroupAccountsDialog.vue'

const { listAccounts, getAccount, checkRisk, updateAccount, showError, showSuccess } = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  getAccount: vi.fn(),
  checkRisk: vi.fn(),
  updateAccount: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getById: getAccount,
      checkMixedChannelRisk: checkRisk,
      update: updateAccount,
    },
  },
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, te: () => false }) }))

const group = { id: 10, name: 'OpenAI Main', platform: 'openai' } as AdminGroup
const account = (id: number, name: string, groupIDs: number[] = []): Account => ({
  id,
  name,
  platform: 'openai',
  type: 'oauth',
  group_ids: groupIDs,
  status: 'active',
  schedulable: true,
  auto_pause_on_expired: true,
  expires_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
} as Account)

const members = [account(1, 'Bound account', [10, 20])]
const candidates = [
  ...members,
  account(2, 'Candidate A', [30]),
  account(3, 'Fresh candidate', []),
]

const page = (items: Account[], currentPage = 1, pages = 1) => ({
  items,
  total: items.length,
  page: currentPage,
  page_size: 50,
  pages,
})

const BaseDialogStub = {
  props: ['show'],
  emits: ['close'],
  template: '<section v-if="show"><slot /><slot name="footer" /></section>',
}

function mountDialog() {
  return mount(GroupAccountsDialog, {
    props: { show: true, group },
    global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } },
  })
}

describe('GroupAccountsDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listAccounts.mockImplementation(async (_page: number, _size: number, filters?: { group?: string; search?: string }) => {
      if (filters?.group === '10') return page(members)
      if (filters?.search) return page(candidates.filter((item) => item.name.toLowerCase().includes(filters.search!.toLowerCase())))
      return page(candidates)
    })
    getAccount.mockImplementation(async (id: number) => candidates.find((item) => item.id === id))
    checkRisk.mockResolvedValue({ has_risk: false })
    updateAccount.mockResolvedValue({})
  })

  it('shows currently bound accounts checked and allows multiple accounts to be selected', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    expect((wrapper.get('[data-test="group-account-1"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-test="group-account-2"]').element as HTMLInputElement).checked).toBe(false)

    await wrapper.get('[data-test="group-account-2"]').trigger('change')
    await wrapper.get('[data-test="group-account-3"]').trigger('change')

    expect(wrapper.text()).toContain('待变更: 2')
  })

  it('searches matching accounts and loads further candidate pages', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    listAccounts.mockImplementation(async (_page: number, _size: number, filters?: { group?: string; search?: string }) => {
      if (filters?.group === '10') return page(members)
      if (filters?.search) return page([account(3, 'Fresh candidate')])
      return page(candidates)
    })

    await wrapper.get('[data-test="group-accounts-search"]').setValue('fresh')
    await wrapper.get('[data-test="group-accounts-search"]').trigger('keydown.enter')
    await flushPromises()

    expect(listAccounts).toHaveBeenCalledWith(1, 50, { platform: 'openai', search: 'fresh' })
    expect(wrapper.find('[data-test="group-account-3"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="group-account-2"]').exists()).toBe(false)
  })

  it('preserves other group bindings when adding and removing accounts', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get('[data-test="group-account-1"]').trigger('change')
    await wrapper.get('[data-test="group-account-2"]').trigger('change')
    await wrapper.get('[data-test="group-accounts-save"]').trigger('click')
    await flushPromises()

    expect(updateAccount).toHaveBeenCalledWith(1, { group_ids: [20] })
    expect(updateAccount).toHaveBeenCalledWith(2, { group_ids: [30, 10] })
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })

  it('requires explicit confirmation before applying mixed-channel changes', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="group-account-2"]').trigger('change')
    checkRisk.mockResolvedValue({ has_risk: true })

    await wrapper.get('[data-test="group-accounts-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="group-accounts-mixed-risk"]').exists()).toBe(true)
    expect(updateAccount).not.toHaveBeenCalled()

    await wrapper.get('[data-test="group-accounts-save"]').trigger('click')
    await flushPromises()

    expect(updateAccount).toHaveBeenCalledWith(2, { group_ids: [30, 10], confirm_mixed_channel_risk: true })
  })

  it('requires confirmation when one save would add both Anthropic and Antigravity accounts to a composite group', async () => {
    const compositeGroup = { ...group, platform: 'composite' } as AdminGroup
    const anthropic = { ...account(4, 'Claude account'), platform: 'anthropic', group_ids: [] } as Account
    const antigravity = { ...account(5, 'Antigravity account'), platform: 'antigravity', group_ids: [] } as Account
    listAccounts.mockImplementation(async (_page: number, _size: number, filters?: { group?: string }) =>
      filters?.group === '10' ? page([]) : page([anthropic, antigravity]))
    getAccount.mockImplementation(async (id: number) => id === 4 ? anthropic : antigravity)
    const wrapper = mount(GroupAccountsDialog, {
      props: { show: true, group: compositeGroup },
      global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } },
    })
    await flushPromises()

    await wrapper.get('[data-test="group-account-4"]').trigger('change')
    await wrapper.get('[data-test="group-account-5"]').trigger('change')
    await wrapper.get('[data-test="group-accounts-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="group-accounts-mixed-risk"]').exists()).toBe(true)
    expect(updateAccount).not.toHaveBeenCalled()
  })
})
