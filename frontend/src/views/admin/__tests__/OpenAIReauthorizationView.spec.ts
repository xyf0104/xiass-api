import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIReauthorizationView from '../OpenAIReauthorizationView.vue'

const mocks = vi.hoisted(() => ({
  accountsAPI: {
    getById: vi.fn(),
    list: vi.fn(),
    delete: vi.fn(),
  },
  groupsAPI: { getAll: vi.fn() },
  proxiesAPI: { getAll: vi.fn() },
  executionNodesAPI: {
    getStatus: vi.fn(),
  },
  openAIReauthorizationAPI: {
    list: vi.fn(),
    start: vi.fn(),
    complete: vi.fn(),
    cancel: vi.fn(),
    restart: vi.fn(),
    remove: vi.fn(),
    sms: vi.fn(),
  },
  route: { query: {} as Record<string, string> },
  routerPush: vi.fn(),
}))

const { accountsAPI, openAIReauthorizationAPI } = mocks

vi.mock('@/api/admin', () => ({
  accountsAPI: mocks.accountsAPI,
  executionNodesAPI: mocks.executionNodesAPI,
  groupsAPI: mocks.groupsAPI,
  proxiesAPI: mocks.proxiesAPI,
}))
vi.mock('@/api/admin/openaiReauthorization', () => ({ openAIReauthorizationAPI: mocks.openAIReauthorizationAPI }))
vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => ({ push: mocks.routerPush }),
}))

function account(id: number) {
  return {
    id,
    name: `account-${id}`,
    platform: 'openai',
    type: 'oauth',
    status: 'error',
    error_message: '401 unauthorized',
    credentials: { email: `person-${id}@example.test` },
    credentials_status: {
      has_xiass_openai_oauth_reauth_email: true,
      has_xiass_openai_oauth_reauth_password_encrypted: true,
      has_xiass_openai_oauth_reauth_totp_secret_encrypted: true,
    },
    extra: { error_code: 'unauthenticated', xiass_execution_node_id: 'api2' },
    execution_node_id: 'api2',
    concurrency: 1,
    priority: 2,
    group_ids: [],
  }
}

function task(id: number, status = 'running', stage = 'email', reason = '') {
  return {
    task_id: `task-${String(id).padStart(16, '0')}`,
    mode: 'reauthorization',
    target_account_id: id,
    email: `person-${id}@example.test`,
    login_method: 'password',
    status,
    stage,
    reason,
    restart_count: 0,
    requires_sms_confirmation: false,
    created_at: new Date().toISOString(),
    expires_at: new Date(Date.now() + 60_000).toISOString(),
  }
}

function emptyAccountPage() {
  return { items: [], total: 0, page: 1, page_size: 200, pages: 0 }
}

async function mountView() {
  const wrapper = mount(OpenAIReauthorizationView, {
    global: {
      stubs: {
        Icon: { template: '<span />' },
        BatchOpenAIOAuthModal: { template: '<div data-testid="batch-oauth" />' },
        ConfirmDialog: {
          props: ['show', 'title', 'message'],
          emits: ['confirm', 'cancel'],
          template: '<div v-if="show" data-testid="delete-confirmation"><p>{{ message }}</p><button data-testid="confirm-delete" @click="$emit(\'confirm\')">确认</button></div>',
        },
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('OpenAIReauthorizationView', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mocks.route.query = { workspace: 'reauthorization' }
    mocks.routerPush.mockReset()
    accountsAPI.getById.mockReset()
    accountsAPI.list.mockReset().mockResolvedValue(emptyAccountPage())
    accountsAPI.delete.mockReset().mockResolvedValue({ message: 'deleted' })
    mocks.groupsAPI.getAll.mockReset().mockResolvedValue([])
    mocks.proxiesAPI.getAll.mockReset().mockResolvedValue([])
    mocks.executionNodesAPI.getStatus.mockReset().mockResolvedValue({
      admin_write_allowed: true,
      admin_write_mode: 'single_node',
      runtime: { enabled: false, node_id: 'api', legacy_unassigned_node_id: 'api' },
    })
    openAIReauthorizationAPI.list.mockReset().mockResolvedValue({ items: [], max_concurrency: 3, max_restarts: 2 })
    openAIReauthorizationAPI.start.mockReset()
    openAIReauthorizationAPI.complete.mockReset()
    openAIReauthorizationAPI.cancel.mockReset()
    openAIReauthorizationAPI.restart.mockReset()
    openAIReauthorizationAPI.remove.mockReset()
    openAIReauthorizationAPI.sms.mockReset()
    vi.stubGlobal('scrollTo', vi.fn())
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('shows the real 401 flow at the top without Team registration copy', async () => {
    mocks.route.query = { account_ids: '448' }
    accountsAPI.getById.mockResolvedValue(account(448))
    const wrapper = await mountView()

    expect(wrapper.text()).toContain('OpenAI OAuth 工作台')
    expect(wrapper.get('[data-testid="reauthorization-workspace-tab"]').text()).toContain('401 重新授权')
    expect(wrapper.text()).toContain('#448 account-448')
    expect(wrapper.text()).toContain('尚未启动')
    expect(wrapper.text()).not.toContain('Team 子号创建')
    expect(wrapper.text()).not.toContain('覆盖导入原 Team 账号')
    expect(wrapper.text()).not.toContain('等待工作空间 10 秒')
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'auto' })
    wrapper.unmount()
  })

  it('opens the native workbench on batch add by default', async () => {
    mocks.route.query = {}
    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="batch-workspace-tab"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-testid="batch-oauth"]').isVisible()).toBe(true)
    expect(wrapper.text()).toContain('批量添加账号')
    wrapper.unmount()
  })

  it('keeps the account password library inside the 401 management page', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="credential-library-workspace-tab"]').text()).toContain('账号库')
    await wrapper.get('[data-testid="credential-library-workspace-tab"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="openai-credential-library"]').isVisible()).toBe(true)
    expect(wrapper.text()).toContain('OpenAI 账号登录资料')
    expect(wrapper.text()).toContain('密码 + 2FA 与邮箱验证码 Token 分开保存')
    expect(mocks.executionNodesAPI.getStatus).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('does not start a 401 account until one explicit login method is saved', async () => {
    const missing = account(12)
    missing.credentials_status = {}
    mocks.accountsAPI.list.mockResolvedValueOnce({
      items: [missing],
      total: 1,
      page: 1,
      page_size: 200,
      pages: 1,
    }).mockResolvedValue(emptyAccountPage())
    const wrapper = await mountView()
    await wrapper.get('[data-testid="reauthorization-workspace-tab"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('未保存登录资料')
    expect(wrapper.text()).toContain('补充登录资料')
    expect(wrapper.find('[data-testid="start-reauthorization-12"]').exists()).toBe(false)
    expect(mocks.openAIReauthorizationAPI.start).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('loads every page of failed OpenAI OAuth accounts', async () => {
    accountsAPI.list.mockImplementation(async (page: number) => ({
      items: page === 1 ? [account(201)] : [account(401)],
      total: 2,
      page,
      page_size: 1,
      pages: 2,
    }))

    const wrapper = await mountView()

    expect(wrapper.text()).toContain('#201 account-201')
    expect(wrapper.text()).toContain('#401 account-401')
    expect(accountsAPI.list).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('starts at most three independent accounts and advances the remaining queue', async () => {
    mocks.route.query = { account_ids: '1,2,3,4' }
    accountsAPI.getById.mockImplementation(async (id: number) => account(id))
    const resolvers = new Map<number, (value: ReturnType<typeof task>) => void>()
    openAIReauthorizationAPI.start.mockImplementation((id: number) => new Promise(resolve => resolvers.set(id, resolve)))
    const wrapper = await mountView()

    await wrapper.get('[data-testid="reauthorize-all"]').trigger('click')
    await Promise.resolve()
    expect(openAIReauthorizationAPI.start).toHaveBeenCalledTimes(3)
    expect(openAIReauthorizationAPI.start.mock.calls.map(call => call[0])).toEqual([1, 2, 3])

    resolvers.get(1)?.(task(1))
    await flushPromises()
    expect(openAIReauthorizationAPI.start).toHaveBeenCalledTimes(4)
    expect(openAIReauthorizationAPI.start).toHaveBeenLastCalledWith(4)

    for (const id of [2, 3, 4]) resolvers.get(id)?.(task(id))
    await flushPromises()
    wrapper.unmount()
  })

  it('continues the one-click queue when one account fails to start', async () => {
    mocks.route.query = { account_ids: '1,2,3,4' }
    accountsAPI.getById.mockImplementation(async (id: number) => account(id))
    openAIReauthorizationAPI.start.mockImplementation(async (id: number) => {
      if (id === 1) throw new Error('该账号缺少已保存的登录密码')
      return task(id)
    })
    const wrapper = await mountView()

    await wrapper.get('[data-testid="reauthorize-all"]').trigger('click')
    await flushPromises()

    expect(openAIReauthorizationAPI.start).toHaveBeenCalledTimes(4)
    expect(openAIReauthorizationAPI.start.mock.calls.map(call => call[0])).toEqual([1, 2, 3, 4])
    expect(wrapper.text()).toContain('该账号缺少已保存的登录密码')
    wrapper.unmount()
  })

  it('keeps a failed account visible with its reason and retries only that row', async () => {
    mocks.route.query = { account_ids: '9' }
    accountsAPI.getById.mockResolvedValue(account(9))
    openAIReauthorizationAPI.list.mockResolvedValue({ items: [task(9, 'failed', 'failed', 'invalid_credentials')], max_concurrency: 3, max_restarts: 2 })
    openAIReauthorizationAPI.restart.mockResolvedValue(task(9, 'running', 'opening'))
    const wrapper = await mountView()

    expect(wrapper.text()).toContain('邮箱、密码或登录后的账号身份未通过验证。')
    const retry = wrapper.get('[data-testid="start-reauthorization-9"]')
    expect(retry.text()).toContain('重新授权')
    await retry.trigger('click')
    await flushPromises()
    expect(openAIReauthorizationAPI.restart).toHaveBeenCalledWith(task(9, 'failed', 'failed', 'invalid_credentials').task_id)
    wrapper.unmount()
  })

  it('does not offer another retry after OpenAI marks the account restricted', async () => {
    mocks.route.query = { account_ids: '10' }
    accountsAPI.getById.mockResolvedValue(account(10))
    openAIReauthorizationAPI.list.mockResolvedValue({ items: [task(10, 'blocked', 'blocked', 'account_blocked')], max_concurrency: 3, max_restarts: 2 })
    const wrapper = await mountView()

    expect(wrapper.text()).toContain('账号受限，不再重试')
    expect(wrapper.find('[data-testid="start-reauthorization-10"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('fully deletes a 401 account and its saved-login task after confirmation', async () => {
    mocks.route.query = { account_ids: '11', workspace: 'reauthorization' }
    accountsAPI.getById.mockResolvedValue(account(11))
    const failedTask = task(11, 'failed', 'failed', 'invalid_credentials')
    openAIReauthorizationAPI.list.mockResolvedValue({ items: [failedTask], max_concurrency: 3, max_restarts: 2 })
    openAIReauthorizationAPI.remove.mockResolvedValue({ task_id: failedTask.task_id })
    const wrapper = await mountView()

    await wrapper.get('[data-testid="delete-reauthorization-account-11"]').trigger('click')
    expect(wrapper.get('[data-testid="delete-confirmation"]').text()).toContain('邮箱、密码、2FA 和邮箱验证码 Token 会同时删除')
    await wrapper.get('[data-testid="confirm-delete"]').trigger('click')
    await flushPromises()

    expect(accountsAPI.delete).toHaveBeenCalledWith(11)
    expect(openAIReauthorizationAPI.remove).toHaveBeenCalledWith(failedTask.task_id)
    expect(wrapper.find('[data-testid="reauthorization-account-11"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('已从账号管理和密码库删除')
    wrapper.unmount()
  })
})
