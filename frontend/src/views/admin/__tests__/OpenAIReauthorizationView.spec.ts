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
    accounts: vi.fn(),
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

function reauthorizationStatus(id: number, overrides: Record<string, unknown> = {}) {
  return {
    account: account(id),
    current_needs_reauthorization: true,
    current_authorization_number: 1,
    has_history: false,
    has_attempted: false,
    has_reauthorized: false,
    attempt_count: 0,
    success_count: 0,
    cooldown_remaining_seconds: 0,
    can_start: true,
    requires_risk_confirmation: false,
    risk_level: 'first',
    ...overrides,
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
          props: ['show', 'title', 'message', 'confirmText', 'danger'],
          emits: ['confirm', 'cancel'],
          template: '<div v-if="show" data-testid="confirmation"><h2>{{ title }}</h2><p>{{ message }}</p><button data-testid="confirm-action" @click="$emit(\'confirm\')">{{ confirmText }}</button></div>',
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
    openAIReauthorizationAPI.accounts.mockReset().mockResolvedValue({ items: [], cooldown_seconds: 604800 })
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

  it('shows the 401 workbench and keeps Team creation as a separate native route', async () => {
    mocks.route.query = { account_ids: '448' }
    openAIReauthorizationAPI.accounts.mockResolvedValue({ items: [reauthorizationStatus(448)], cooldown_seconds: 604800 })
    const wrapper = await mountView()

    expect(wrapper.text()).toContain('XIASS工作台')
    expect(wrapper.get('[data-testid="reauthorization-workspace-tab"]').text()).toContain('401 重新授权')
    expect(wrapper.get('[data-testid="reauthorization-account-448"]').text()).toContain('account-448')
    expect(wrapper.text()).toContain('尚未启动')
    expect(wrapper.get('[data-testid="team-child-creation-entry"]').text()).toContain('创建 Team 子号')
    expect(wrapper.text()).not.toContain('覆盖导入原 Team 账号')
    expect(wrapper.text()).not.toContain('等待工作空间 10 秒')
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'auto' })

    await wrapper.get('[data-testid="team-child-creation-entry"]').trigger('click')
    expect(mocks.routerPush).toHaveBeenCalledWith({ name: 'AdminTeamChildCreation' })
    wrapper.unmount()
  })

  it('opens the native workbench on 401 reauthorization by default', async () => {
    mocks.route.query = {}
    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="reauthorization-workspace-tab"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-testid="batch-workspace-tab"]').attributes('aria-selected')).toBe('false')
    expect(wrapper.text()).toContain('401 重新授权')
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
    openAIReauthorizationAPI.accounts.mockResolvedValue({
      items: [reauthorizationStatus(12, { account: missing })],
      cooldown_seconds: 604800,
    })
    const wrapper = await mountView()
    await wrapper.get('[data-testid="reauthorization-workspace-tab"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('未保存登录资料')
    expect(wrapper.text()).toContain('补充登录资料')
    expect(wrapper.find('[data-testid="start-reauthorization-12"]').exists()).toBe(false)
    expect(mocks.openAIReauthorizationAPI.start).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('separates current 401 accounts from completed authorization history', async () => {
    openAIReauthorizationAPI.accounts.mockResolvedValue({
      items: [
        reauthorizationStatus(201),
        reauthorizationStatus(401, {
          current_needs_reauthorization: false,
          has_history: true,
          has_attempted: true,
          has_reauthorized: true,
          attempt_count: 1,
          success_count: 1,
          can_start: false,
          risk_level: 'success',
        }),
      ],
      cooldown_seconds: 604800,
    })

    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="reauthorization-account-201"]').text()).toContain('account-201')
    expect(wrapper.find('[data-testid="authorization-history-account-401"]').exists()).toBe(false)

    await wrapper.get('[data-testid="authorization-history-workspace-tab"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="authorization-history-account-401"]').text()).toContain('account-401')
    expect(wrapper.find('[data-testid="reauthorization-account-201"]').exists()).toBe(false)
    expect(openAIReauthorizationAPI.accounts).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('starts at most three independent accounts and advances the remaining queue', async () => {
    mocks.route.query = { account_ids: '1,2,3,4' }
    openAIReauthorizationAPI.accounts.mockResolvedValue({ items: [1, 2, 3, 4].map(id => reauthorizationStatus(id)), cooldown_seconds: 604800 })
    const resolvers = new Map<number, (value: ReturnType<typeof task>) => void>()
    openAIReauthorizationAPI.start.mockImplementation((id: number) => new Promise(resolve => resolvers.set(id, resolve)))
    const wrapper = await mountView()

    await wrapper.get('[data-testid="reauthorize-all"]').trigger('click')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await Promise.resolve()
    expect(openAIReauthorizationAPI.start).toHaveBeenCalledTimes(3)
    expect(openAIReauthorizationAPI.start.mock.calls.map(call => call[0])).toEqual([1, 2, 3])
    expect(openAIReauthorizationAPI.start.mock.calls.every(call => call[1] === false)).toBe(true)

    resolvers.get(1)?.(task(1))
    await flushPromises()
    expect(openAIReauthorizationAPI.start).toHaveBeenCalledTimes(4)
    expect(openAIReauthorizationAPI.start).toHaveBeenLastCalledWith(4, false)

    for (const id of [2, 3, 4]) resolvers.get(id)?.(task(id))
    await flushPromises()
    wrapper.unmount()
  })

  it('continues the one-click queue when one account fails to start', async () => {
    mocks.route.query = { account_ids: '1,2,3,4' }
    openAIReauthorizationAPI.accounts.mockResolvedValue({ items: [1, 2, 3, 4].map(id => reauthorizationStatus(id)), cooldown_seconds: 604800 })
    openAIReauthorizationAPI.start.mockImplementation(async (id: number) => {
      if (id === 1) throw new Error('该账号缺少已保存的登录密码')
      return task(id)
    })
    const wrapper = await mountView()

    await wrapper.get('[data-testid="reauthorize-all"]').trigger('click')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await flushPromises()

    expect(openAIReauthorizationAPI.start).toHaveBeenCalledTimes(4)
    expect(openAIReauthorizationAPI.start.mock.calls.map(call => call[0])).toEqual([1, 2, 3, 4])
    expect(wrapper.text()).toContain('该账号缺少已保存的登录密码')
    wrapper.unmount()
  })

  it('keeps a failed account visible with its reason and retries only that row', async () => {
    mocks.route.query = { account_ids: '9' }
    openAIReauthorizationAPI.accounts.mockResolvedValue({ items: [reauthorizationStatus(9, { has_history: true, has_attempted: true, attempt_count: 1, risk_level: 'failed' })], cooldown_seconds: 604800 })
    openAIReauthorizationAPI.list.mockResolvedValue({ items: [task(9, 'failed', 'failed', 'invalid_credentials')], max_concurrency: 3, max_restarts: 2 })
    openAIReauthorizationAPI.restart.mockResolvedValue(task(9, 'running', 'opening'))
    const wrapper = await mountView()

    expect(wrapper.text()).toContain('邮箱、密码或登录后的账号身份未通过验证。')
    const retry = wrapper.get('[data-testid="start-reauthorization-9"]')
    expect(retry.text()).toContain('重试本次授权')
    await retry.trigger('click')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await flushPromises()
    expect(openAIReauthorizationAPI.restart).toHaveBeenCalledWith(task(9, 'failed', 'failed', 'invalid_credentials').task_id, false)
    wrapper.unmount()
  })

  it('allows a deleted or disabled account to retry only after red high-risk confirmation', async () => {
    mocks.route.query = { account_ids: '10' }
    openAIReauthorizationAPI.accounts.mockResolvedValue({ items: [reauthorizationStatus(10, { can_start: true, requires_risk_confirmation: true, risk_level: 'blocked', last_result: 'blocked', last_reason: 'account_deleted_or_disabled' })], cooldown_seconds: 604800 })
    openAIReauthorizationAPI.list.mockResolvedValue({ items: [task(10, 'blocked', 'blocked', 'account_deleted_or_disabled')], max_concurrency: 3, max_restarts: 2 })
    openAIReauthorizationAPI.restart.mockResolvedValue(task(10, 'running', 'opening'))
    const wrapper = await mountView()

    expect(wrapper.text()).toContain('账号已删除或停用')
    const retry = wrapper.get('[data-testid="start-reauthorization-10"]')
    expect(retry.text()).toContain('继续授权')
    await retry.trigger('click')
    expect(wrapper.get('[data-testid="confirmation"]').text()).toContain('账号已删除或停用')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await flushPromises()
    expect(openAIReauthorizationAPI.restart).toHaveBeenCalledWith(task(10, 'blocked', 'blocked', 'account_deleted_or_disabled').task_id, true)
    wrapper.unmount()
  })

  it('keeps cooldown copy short and allows a manual continuation after confirmation', async () => {
    const firstSucceededAt = new Date(Date.now() - 3 * 86400_000).toISOString()
    openAIReauthorizationAPI.accounts.mockResolvedValue({
      items: [reauthorizationStatus(20, {
        current_authorization_number: 2,
        has_history: true,
        has_attempted: true,
        has_reauthorized: true,
        attempt_count: 1,
        success_count: 1,
        first_succeeded_at: firstSucceededAt,
        seconds_since_first_reauthorization: 3 * 86400,
        cooldown_remaining_seconds: 4 * 86400,
        can_start: true,
        requires_risk_confirmation: true,
        risk_level: 'cooldown',
      })],
      cooldown_seconds: 604800,
    })
    openAIReauthorizationAPI.start.mockResolvedValue(task(20))

    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="reauthorization-history-20"]').text()).toContain('第 2 次授权')
    expect(wrapper.get('[data-testid="reauthorization-cooldown-20"]').text()).toContain('4 天 0 小时')
    expect(wrapper.get('[data-testid="reauthorization-cooldown-20"]').text()).not.toContain('仍可')
    const retry = wrapper.get('[data-testid="start-reauthorization-20"]')
    expect(retry.text()).toContain('继续授权')
    await retry.trigger('click')
    expect(wrapper.get('[data-testid="confirmation"]').text()).toContain('仍在 7 天冷静期内')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await flushPromises()
    expect(openAIReauthorizationAPI.start).toHaveBeenCalledWith(20, true)

    await wrapper.get('[data-testid="authorization-history-workspace-tab"]').trigger('click')
    const history = wrapper.get('[data-testid="authorization-history-account-20"]')
    expect(history.text()).toContain('第 1 次授权')
    expect(history.get('[data-testid="history-pending-authorization-20"]').text()).toContain('待第 2 次授权')
    expect(history.text()).toContain('第 2 次授权建议等待 7 天')
    wrapper.unmount()
  })

  it('requires a red high-risk confirmation after the second-drop cooldown', async () => {
    openAIReauthorizationAPI.accounts.mockResolvedValue({
      items: [reauthorizationStatus(21, {
        current_authorization_number: 2,
        has_history: true,
        has_attempted: true,
        has_reauthorized: true,
        attempt_count: 1,
        success_count: 1,
        first_succeeded_at: new Date(Date.now() - 8 * 86400_000).toISOString(),
        seconds_since_first_reauthorization: 8 * 86400,
        can_start: true,
        requires_risk_confirmation: true,
        risk_level: 'repeated',
      })],
      cooldown_seconds: 604800,
    })
    openAIReauthorizationAPI.start.mockResolvedValue(task(21))
    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="reauthorize-all"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="start-reauthorization-21"]').trigger('click')
    expect(wrapper.get('[data-testid="confirmation"]').text()).toContain('建议不要再授权')
    expect(wrapper.get('[data-testid="confirm-action"]').text()).toContain('我已知风险')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await flushPromises()

    expect(openAIReauthorizationAPI.start).toHaveBeenCalledWith(21, true)
    wrapper.unmount()
  })

  it('starts an untracked account as the first authorization instead of showing an old-version warning', async () => {
    openAIReauthorizationAPI.accounts.mockResolvedValue({
      items: [reauthorizationStatus(22, {
        current_authorization_number: 1,
        has_history: true,
        has_attempted: false,
        has_reauthorized: false,
        last_result: 'legacy_unknown',
        last_reason: 'reauthorization_history_unavailable',
        history_confidence: 'unknown',
        can_start: true,
        requires_risk_confirmation: true,
        risk_level: 'unknown',
      })],
      cooldown_seconds: 604800,
    })
    openAIReauthorizationAPI.start.mockResolvedValue(task(22))
    const wrapper = await mountView()

    expect(wrapper.text()).not.toContain('旧版')
    expect(wrapper.text()).not.toContain('历史不明')
    expect(wrapper.get('[data-testid="reauthorization-history-22"]').text()).toContain('第 1 次授权')
    await wrapper.get('[data-testid="start-reauthorization-22"]').trigger('click')
    expect(wrapper.get('[data-testid="confirmation"]').text()).toContain('第一次 401 重新授权')
    expect(wrapper.get('[data-testid="confirm-action"]').text()).toContain('确认开始')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await flushPromises()

    expect(openAIReauthorizationAPI.start).toHaveBeenCalledWith(22, false)
    wrapper.unmount()
  })

  it('shows only the immediately previous success time before the third and fourth authorizations', async () => {
    const first = new Date(Date.now() - 20 * 86400_000).toISOString()
    const second = new Date(Date.now() - 10 * 86400_000).toISOString()
    const third = new Date(Date.now() - 2 * 86400_000).toISOString()
    openAIReauthorizationAPI.accounts.mockResolvedValue({
      items: [
        reauthorizationStatus(30, {
          current_authorization_number: 3,
          has_history: true,
          has_attempted: true,
          has_reauthorized: true,
          attempt_count: 2,
          success_count: 2,
          first_succeeded_at: first,
          last_succeeded_at: second,
          successful_authorization_times: [first, second],
          requires_risk_confirmation: true,
          risk_level: 'repeated',
        }),
        reauthorizationStatus(31, {
          current_authorization_number: 4,
          has_history: true,
          has_attempted: true,
          has_reauthorized: true,
          attempt_count: 3,
          success_count: 3,
          first_succeeded_at: first,
          last_succeeded_at: third,
          successful_authorization_times: [first, second, third],
          requires_risk_confirmation: true,
          risk_level: 'cooldown',
        }),
      ],
      cooldown_seconds: 604800,
    })
    const wrapper = await mountView()
    await wrapper.get('[data-testid="authorization-history-workspace-tab"]').trigger('click')

    const thirdPending = wrapper.get('[data-testid="authorization-history-account-30"]')
    expect(thirdPending.text()).toContain('第 2 次授权')
    expect(thirdPending.text()).not.toContain('第 1 次授权')
    expect(thirdPending.text()).toContain('待第 3 次授权')

    const fourthPending = wrapper.get('[data-testid="authorization-history-account-31"]')
    expect(fourthPending.text()).toContain('第 3 次授权')
    expect(fourthPending.text()).not.toContain('第 2 次授权')
    expect(fourthPending.text()).toContain('待第 4 次授权')
    wrapper.unmount()
  })

  it('fully deletes a 401 account and its saved-login task after confirmation', async () => {
    mocks.route.query = { account_ids: '11', workspace: 'reauthorization' }
    openAIReauthorizationAPI.accounts.mockResolvedValue({ items: [reauthorizationStatus(11, { has_history: true, has_attempted: true, attempt_count: 1, risk_level: 'failed' })], cooldown_seconds: 604800 })
    const failedTask = task(11, 'failed', 'failed', 'invalid_credentials')
    openAIReauthorizationAPI.list.mockResolvedValue({ items: [failedTask], max_concurrency: 3, max_restarts: 2 })
    openAIReauthorizationAPI.remove.mockResolvedValue({ task_id: failedTask.task_id })
    const wrapper = await mountView()

    await wrapper.get('[data-testid="delete-reauthorization-account-11"]').trigger('click')
    expect(wrapper.get('[data-testid="confirmation"]').text()).toContain('邮箱、密码、2FA 和邮箱验证码 Token 会同时删除')
    await wrapper.get('[data-testid="confirm-action"]').trigger('click')
    await flushPromises()

    expect(accountsAPI.delete).toHaveBeenCalledWith(11)
    expect(openAIReauthorizationAPI.remove).toHaveBeenCalledWith(failedTask.task_id)
    expect(wrapper.find('[data-testid="reauthorization-account-11"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('已从账号管理和密码库删除')
    wrapper.unmount()
  })
})
