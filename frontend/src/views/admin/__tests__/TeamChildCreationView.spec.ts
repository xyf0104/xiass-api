import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import i18n from '@/i18n'

const { teamChildAPI, accountsAPI, groupsAPI, proxiesAPI, appStore, nativeGenerateAuthUrl } = vi.hoisted(() => ({
  teamChildAPI: {
    getMailboxStatus: vi.fn(),
    createMailbox: vi.fn(),
    listMailboxes: vi.fn(),
    selectMailbox: vi.fn(),
    getActiveMailbox: vi.fn(),
    getActiveTeamChildWorkflow: vi.fn(),
    pollMailboxCode: vi.fn(),
    deleteMailboxSession: vi.fn(),
    importMailboxConfig: vi.fn(),
    createBrowserSession: vi.fn(),
    heartbeatTeamChildBrowserControl: vi.fn(),
    releaseTeamChildBrowserControl: vi.fn(),
    listTeamChildMembers: vi.fn(),
    refreshTeamChildMembers: vi.fn(),
    inspectTeamChildSeat: vi.fn(),
    inviteTeamChildMember: vi.fn(),
    updateTeamChildMember: vi.fn(),
    removeTeamChildMember: vi.fn(),
    startTeamChildWorkflow: vi.fn(),
    getTeamChildWorkflow: vi.fn(),
    continueTeamChildWorkflow: vi.fn(),
    pauseTeamChildWorkflow: vi.fn(),
    submitTeamChildWorkflowCallback: vi.fn(),
    restartTeamChildWorkflowOAuth: vi.fn(),
    reauthorizeTeamChildAccount: vi.fn(),
    saveOpenAIAccountReauthorizationCredentials: vi.fn(),
    reauthorizeOpenAIAccount: vi.fn(),
    submitTeamChildWorkflowEmailCode: vi.fn(),
    submitTeamChildWorkflowPhone: vi.fn(),
    submitTeamChildWorkflowSMSCode: vi.fn(),
    completeTeamChildWorkflow: vi.fn(),
    cancelTeamChildWorkflow: vi.fn(),
    revealTeamChildWorkflowPassword: vi.fn(),
    createOpenAIAccountFromOAuth: vi.fn()
  },
  accountsAPI: {
    list: vi.fn(),
    getUsage: vi.fn(),
    applyOAuthCredentials: vi.fn(),
    update: vi.fn(),
    delete: vi.fn()
  },
  groupsAPI: { getAll: vi.fn() },
  proxiesAPI: { getAll: vi.fn() },
  nativeGenerateAuthUrl: vi.fn(),
  appStore: {
    showError: vi.fn(),
    showInfo: vi.fn(),
    showSuccess: vi.fn()
  }
}))

vi.mock('@/api/admin/teamChild', () => ({ teamChildAPI, default: teamChildAPI }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI, default: accountsAPI }))
vi.mock('@/api/admin/groups', () => ({ groupsAPI, default: groupsAPI }))
vi.mock('@/api/admin/proxies', () => ({ proxiesAPI, default: proxiesAPI }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div data-testid="step-up-dialog-stub" />' } }))
vi.mock('@/composables/useOpenAIOAuth', async () => {
  const { ref } = await import('vue')
  return {
    useOpenAIOAuth: () => {
      const authUrl = ref('')
      const sessionId = ref('')
      const oauthState = ref('')
      const loading = ref(false)
      const error = ref('')
      return {
        authUrl,
        sessionId,
        oauthState,
        loading,
        error,
        resetState: () => {
          authUrl.value = ''
          sessionId.value = ''
          oauthState.value = ''
          loading.value = false
          error.value = ''
        },
        generateAuthUrl: async () => {
          loading.value = true
          const result = await nativeGenerateAuthUrl()
          authUrl.value = result.auth_url
          sessionId.value = result.session_id
          oauthState.value = new URL(result.auth_url).searchParams.get('state') || ''
          loading.value = false
          return true
        },
        exchangeAuthCode: vi.fn(),
        buildCredentials: vi.fn(() => ({})),
        buildExtraInfo: vi.fn(() => undefined)
      }
    }
  }
})
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))

import TeamChildCreationView from '../TeamChildCreationView.vue'

const testAuthURL = 'https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=team-state'
const workflowNodeDefinitions = [
  ['signup', '隐私页注册新邮箱'], ['email', '填入注册邮箱'], ['mail', '发送注册邮箱验证码'],
  ['mailbox', '读取注册邮箱验证码'], ['email_code', '提交注册邮箱验证码'], ['members', '读取成员席位'],
  ['remove', '移除已选成员'], ['invite', '提交成员邀请'], ['invite_confirm', '确认 Pending invites'],
  ['oauth', '同一隐私页打开 OAuth'], ['password', '完成 OAuth 邮箱登录验证'], ['phone', '进入手机号页面'],
  ['sms_confirm', '确认领取手机号'], ['phone_submit', '填入号码并选择 Text message'], ['sms_poll', '轮询短信验证码'],
  ['sms_code', '自动填入短信验证码'], ['profile_wait', '等待资料页 5 秒'], ['profile', '填写 black / 26'],
  ['workspace_wait', '等待工作空间 10 秒'], ['workspace', '默认工作空间继续'], ['callback', '捕获 OAuth 回调'],
  ['import', '按勾选配置导入 XIASS']
] as const

function currentWorkflow(overrides: Record<string, unknown> = {}) {
  const currentNode = String(overrides.current_node || 'sms_confirm')
  const currentIndex = workflowNodeDefinitions.findIndex(([key]) => key === currentNode)
  const workflowStatus = String(overrides.status || 'manual_required')
  return {
    schema_version: 4,
    id: 'workflow-token-abcdefghijklmnop',
    status: workflowStatus,
    manual_required: workflowStatus === 'manual_required',
    current_node: currentNode,
    expires_at: '2026-08-22T12:30:00.000Z',
    nodes: workflowNodeDefinitions.map(([key, label], index) => ({
      key,
      number: index + 1,
      label,
      status: index < currentIndex ? 'completed' : index === currentIndex
        ? workflowStatus === 'failed' ? 'failed' : 'waiting'
        : 'pending'
    })),
    ...overrides
  }
}

const PixlabSMSReceiverStub = {
  props: { active: { type: Boolean, default: false } },
  template: `<div data-testid="team-sms-receiver" :data-active="String(active)">
    <button type="button" data-testid="sms-phone-ready" @click="$emit('phone-ready', '+14155550123')" />
    <button type="button" data-testid="sms-code-received" @click="$emit('code-received', '123456')" />
    <button type="button" data-testid="sms-session-cancelled" @click="$emit('session-cancelled')" />
  </div>`
}

const BrowserWorkspaceStub = {
  props: {
    embedUrl: { type: String, default: '' },
    error: { type: String, default: '' }
  },
  template: `<div>
    <button type="button" data-testid="team-browser-workspace" :data-embed-url="embedUrl" :data-error="error" @click="$emit('open-modular')" />
    <button type="button" data-testid="team-browser-reload" @click="$emit('reload')" />
    <button type="button" data-testid="team-browser-refresh-members" @click="$emit('refresh-members')" />
  </div>`
}

const MembersWorkspaceStub = {
  template: `<div>
    <button type="button" data-testid="team-member-select" @click="$emit('select', 'member@example.test')" />
    <button type="button" data-testid="team-start-workflow" @click="$emit('start-workflow')" />
    <button type="button" data-testid="team-members-workspace" @click="$emit('open-browser')" />
  </div>`
}

const ConfirmDialogStub = {
  props: { show: { type: Boolean, default: false } },
  template: '<button v-if="show" type="button" data-testid="confirm-dialog" @click="$emit(\'confirm\')" />'
}

const GroupSelectorStub = {
  template: '<button type="button" data-testid="group-selector" @click="$emit(\'update:modelValue\', [19])" />'
}

const TeamChildAccountSuccessDialogStub = {
  props: ['show', 'account', 'groupIds'],
  template: `<div v-if="show" data-testid="team-child-account-success-dialog">
    {{ account?.name }}
    <span data-testid="success-dialog-group-ids">{{ groupIds.join(',') }}</span>
    <button type="button" data-testid="success-dialog-groups" @click="$emit('update:group-ids', [19])" />
    <button type="button" data-testid="success-dialog-concurrency" @click="$emit('update:concurrency', 18)" />
    <button type="button" data-testid="success-dialog-priority" @click="$emit('update:priority', 3)" />
    <button type="button" data-testid="success-dialog-save" @click="$emit('save')" />
  </div>`
}

function mountView() {
  return mount(TeamChildCreationView, {
    global: {
      plugins: [i18n],
      stubs: {
        Icon: true,
        PixlabSMSReceiver: PixlabSMSReceiverStub,
        TeamChildBrowserWorkspace: BrowserWorkspaceStub,
        TeamChildMembersWorkspace: MembersWorkspaceStub,
        GroupSelector: GroupSelectorStub,
        TeamChildAccountSuccessDialog: TeamChildAccountSuccessDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        RouterLink: true
      }
    }
  })
}

describe('TeamChildCreationView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.history.replaceState({}, '', '/')
    teamChildAPI.getMailboxStatus.mockResolvedValue({ configured: true, browser_configured: true })
    teamChildAPI.listMailboxes.mockResolvedValue([])
    teamChildAPI.selectMailbox.mockResolvedValue({
      session_id: 'selected-mailbox-session',
      email: 'team1003@example.test',
      expires_at: '2026-08-24T12:00:00.000Z'
    })
    groupsAPI.getAll.mockResolvedValue([])
    proxiesAPI.getAll.mockResolvedValue([])
    accountsAPI.list.mockResolvedValue({ items: [], total: 0 })
    accountsAPI.getUsage.mockResolvedValue(null)
    teamChildAPI.listTeamChildMembers.mockResolvedValue({
      ready: true,
      members: [{ id: 'member@example.test', email: 'member@example.test', role: 'member' }],
      seat_email: 'member@example.test'
    })
    teamChildAPI.createMailbox.mockResolvedValue({
      session_id: 'mailbox-session',
      email: 'team-child@example.test',
      expires_at: '2026-08-22T12:30:00.000Z'
    })
    teamChildAPI.getActiveMailbox.mockResolvedValue(null)
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue(null)
    nativeGenerateAuthUrl.mockResolvedValue({
      auth_url: testAuthURL,
      session_id: 'oauth-session'
    })
    teamChildAPI.pollMailboxCode.mockResolvedValue({ status: 'waiting' })
    teamChildAPI.deleteMailboxSession.mockResolvedValue({ deleted: true })
    teamChildAPI.heartbeatTeamChildBrowserControl.mockResolvedValue({
      controller_id: 'controller-token-abcdefghijklmnop',
      control_expires_at: '2026-08-22T12:02:00.000Z'
    })
    teamChildAPI.releaseTeamChildBrowserControl.mockResolvedValue({
      controller_id: 'controller-token-abcdefghijklmnop',
      released: true
    })
    teamChildAPI.createBrowserSession.mockResolvedValue({
      embed_url: '/browser?ticket=test',
      expires_at: '2026-08-22T12:30:00.000Z',
      controller_id: 'controller-token-abcdefghijklmnop',
      control_expires_at: '2026-08-22T12:02:00.000Z'
    })
    teamChildAPI.startTeamChildWorkflow.mockResolvedValue(currentWorkflow())
    teamChildAPI.getTeamChildWorkflow.mockResolvedValue(currentWorkflow())
    teamChildAPI.continueTeamChildWorkflow.mockResolvedValue(currentWorkflow({ status: 'running', manual_required: false, current_node: 'signup' }))
    teamChildAPI.submitTeamChildWorkflowCallback.mockResolvedValue(currentWorkflow({
      status: 'callback_ready',
      manual_required: false,
      current_node: 'import',
      callback_url: 'http://localhost:1455/auth/callback?code=callback-code&state=team-state'
    }))
    teamChildAPI.restartTeamChildWorkflowOAuth.mockResolvedValue(currentWorkflow({ current_node: 'mailbox' }))
    teamChildAPI.reauthorizeTeamChildAccount.mockResolvedValue(currentWorkflow({
      mode: 'reauthorization',
      target_account_id: 317,
      status: 'running',
      manual_required: false,
      current_node: 'oauth'
    }))
    teamChildAPI.reauthorizeOpenAIAccount.mockResolvedValue(currentWorkflow({
      mode: 'reauthorization',
      target_account_id: 415,
      status: 'running',
      manual_required: false,
      current_node: 'oauth'
    }))
    teamChildAPI.submitTeamChildWorkflowEmailCode.mockResolvedValue(currentWorkflow({ status: 'running', manual_required: false, current_node: 'email_code' }))
    teamChildAPI.submitTeamChildWorkflowPhone.mockResolvedValue(currentWorkflow({ status: 'running', manual_required: false, current_node: 'phone_submit' }))
    teamChildAPI.submitTeamChildWorkflowSMSCode.mockResolvedValue(currentWorkflow({ status: 'running', manual_required: false, current_node: 'sms_code' }))
    teamChildAPI.pauseTeamChildWorkflow.mockResolvedValue(currentWorkflow({ status: 'paused', manual_required: false, current_node: 'sms_poll', pause_requested: true }))
    teamChildAPI.completeTeamChildWorkflow.mockResolvedValue(currentWorkflow({ status: 'completed', manual_required: false, current_node: 'import' }))
    teamChildAPI.cancelTeamChildWorkflow.mockResolvedValue(currentWorkflow({ status: 'cancelled', manual_required: false, current_node: 'invite' }))
    teamChildAPI.refreshTeamChildMembers.mockResolvedValue({
      ready: true,
      members: [{ id: 'member@example.test', email: 'member@example.test', role: 'member' }],
      seat_email: 'member@example.test'
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('defaults to the member automation workspace without minting a graphical browser session', async () => {
    const wrapper = mountView()

    await flushPromises()
    expect(teamChildAPI.listTeamChildMembers).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.refreshTeamChildMembers).not.toHaveBeenCalled()
    expect(teamChildAPI.createBrowserSession).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="team-oauth-workspace"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="team-members-workspace"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('流程状态')
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')
    expect(wrapper.text()).not.toContain('需要你在 OpenAI 页面完成验证')

    await wrapper.get('[data-testid="team-manual-browser"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.createBrowserSession).toHaveBeenCalledWith({})
    expect(wrapper.find('[data-testid="team-browser-workspace"]').exists()).toBe(true)

    wrapper.unmount()
    expect(teamChildAPI.releaseTeamChildBrowserControl).toHaveBeenCalledWith('controller-token-abcdefghijklmnop')
  })

  it('keeps the embedded browser mounted after a transient heartbeat failure', async () => {
    vi.useFakeTimers()
    teamChildAPI.heartbeatTeamChildBrowserControl.mockRejectedValue({ status: 502, message: 'temporary gateway error' })
    const wrapper = mountView()
    await vi.runAllTimersAsync()
    await flushPromises()

    await wrapper.get('[data-testid="team-manual-browser"]').trigger('click')
    await flushPromises()
    const browser = wrapper.get('[data-testid="team-browser-workspace"]')
    expect(browser.attributes('data-embed-url')).toBe('/browser?ticket=test')

    await vi.advanceTimersByTimeAsync(45_000)
    await flushPromises()
    expect(teamChildAPI.heartbeatTeamChildBrowserControl).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="team-browser-workspace"]').attributes('data-embed-url')).toBe('/browser?ticket=test')
    expect(wrapper.get('[data-testid="team-browser-workspace"]').attributes('data-error')).toBe('')
    wrapper.unmount()
  })

  it('performs explicit browser and member-page refreshes from the embedded workspace', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-manual-browser"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.createBrowserSession).toHaveBeenCalledWith({})

    await wrapper.get('[data-testid="team-browser-reload"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.createBrowserSession).toHaveBeenLastCalledWith({
      controller_id: 'controller-token-abcdefghijklmnop'
    })

    await wrapper.get('[data-testid="team-browser-refresh-members"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.refreshTeamChildMembers).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('restores an active workflow after the page is reopened', async () => {
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue(currentWorkflow({ current_node: 'sms_confirm' }))

    const wrapper = mountView()
    await flushPromises()

    expect(teamChildAPI.getActiveTeamChildWorkflow).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('等待当前节点')
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')
    expect(wrapper.find('[data-testid="team-pause-workflow"]').exists()).toBe(false)
    await wrapper.get('[data-testid="team-resume-workflow"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.continueTeamChildWorkflow).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')
    wrapper.unmount()
  })

  it('retries a failed registration with the same mailbox and workflow', async () => {
    const failedWorkflow = currentWorkflow({
      status: 'failed',
      manual_required: false,
      current_node: 'signup',
      error: 'ChatGPT 注册页面暂时未完成加载'
    })
    teamChildAPI.getActiveMailbox.mockResolvedValue({
      session_id: 'rejected-mailbox-session',
      email: 'rejected@example.test',
      expires_at: '2026-09-07T18:00:00.000Z'
    })
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue(failedWorkflow)
    const wrapper = mountView()
    await flushPromises()

    expect(teamChildAPI.createMailbox).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="team-continue-after-failure"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.continueTeamChildWorkflow).toHaveBeenCalledWith(failedWorkflow.id)
    expect(teamChildAPI.createMailbox).not.toHaveBeenCalled()
    expect(teamChildAPI.deleteMailboxSession).not.toHaveBeenCalledWith('rejected-mailbox-session')
    wrapper.unmount()
  })

  it('requires confirmation before starting the login-only OAuth workflow opened from account management', async () => {
    window.history.replaceState({}, '', '/admin/team-child-creation?reauthorize=317')
    accountsAPI.list.mockResolvedValue({
      items: [{
        id: 317,
        name: 'team1004@example.test',
        platform: 'openai',
        type: 'oauth',
        status: 'error',
        error_message: 'HTTP 401 Unauthorized',
        schedulable: false,
        proxy_id: null,
        concurrency: 10,
        priority: 1,
        group_ids: [],
        credentials_status: { has_xiass_team_child_password_encrypted: true },
        extra: { xiass_team_child: true, xiass_team_child_email: 'team1004@example.test' }
      }],
      total: 1
    })
    accountsAPI.getUsage.mockResolvedValue({ needs_reauth: true, error_code: 'unauthenticated', error: 'HTTP 401' })
    teamChildAPI.listMailboxes.mockResolvedValue(['team1004@example.test'])
    teamChildAPI.selectMailbox.mockResolvedValue({
      session_id: 'selected-mailbox-session',
      email: 'team1004@example.test',
      expires_at: '2026-08-24T12:00:00.000Z'
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(true)
    expect(teamChildAPI.selectMailbox).not.toHaveBeenCalled()
    expect(nativeGenerateAuthUrl).not.toHaveBeenCalled()
    expect(teamChildAPI.reauthorizeTeamChildAccount).not.toHaveBeenCalled()
    expect(teamChildAPI.startTeamChildWorkflow).not.toHaveBeenCalled()
    expect(window.location.search).toBe('')

    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.selectMailbox).toHaveBeenCalledWith('team1004@example.test')
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.reauthorizeTeamChildAccount).toHaveBeenCalledWith(317, testAuthURL, 'oauth-session')
    expect(teamChildAPI.startTeamChildWorkflow).not.toHaveBeenCalled()
    expect(window.location.search).toBe('')
    wrapper.unmount()
  })

  it('reads Team history through the OpenAI quota endpoint instead of Anthropic passive usage', async () => {
    accountsAPI.list.mockResolvedValue({
      items: [{
        id: 319,
        name: 'team1015@example.test',
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        schedulable: true,
        proxy_id: null,
        concurrency: 10,
        priority: 1,
        credentials_status: { has_xiass_team_child_password_encrypted: true },
        extra: { xiass_team_child: true, xiass_team_child_email: 'team1015@example.test' }
      }],
      total: 1
    })
    accountsAPI.getUsage.mockResolvedValue({
      five_hour: { utilization: 7 },
      seven_day: { utilization: 33, weekly_estimate_usd: 138 }
    })

    const wrapper = mountView()
    await flushPromises()

    expect(accountsAPI.getUsage).toHaveBeenCalledWith(319, 'active', true)
    expect(wrapper.text()).toContain('7%')
    expect(wrapper.text()).toContain('33%')
    expect(wrapper.text()).toContain('$138.00')
    wrapper.unmount()
  })

  it('restarts a failed automatic 401 workflow from the history reauthorization button', async () => {
    const failedWorkflow = currentWorkflow({
      mode: 'reauthorization',
      target_account_id: 317,
      status: 'failed',
      manual_required: false,
      current_node: 'oauth',
      error: 'OpenAI 登录页面未完成加载'
    })
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue(failedWorkflow)
    accountsAPI.list.mockResolvedValue({
      items: [{
        id: 317,
        name: 'team1004@example.test',
        platform: 'openai',
        type: 'oauth',
        status: 'error',
        error_message: 'HTTP 401 Unauthorized',
        schedulable: false,
        proxy_id: null,
        concurrency: 10,
        priority: 1,
        group_ids: [],
        credentials_status: { has_xiass_team_child_password_encrypted: true },
        extra: { xiass_team_child: true, xiass_team_child_email: 'team1004@example.test' }
      }],
      total: 1
    })
    accountsAPI.getUsage.mockResolvedValue({ needs_reauth: true, error_code: 'unauthenticated', error: 'HTTP 401' })
    teamChildAPI.listMailboxes.mockResolvedValue(['team1004@example.test'])

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-history-reauthorize-317"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.cancelTeamChildWorkflow).not.toHaveBeenCalled()
    expect(teamChildAPI.reauthorizeTeamChildAccount).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.cancelTeamChildWorkflow).toHaveBeenCalledWith(failedWorkflow.id)
    expect(teamChildAPI.selectMailbox).toHaveBeenCalledWith('team1004@example.test')
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.reauthorizeTeamChildAccount).toHaveBeenCalledWith(317, testAuthURL, 'oauth-session')
    expect(teamChildAPI.startTeamChildWorkflow).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('offers a fresh XIASS OAuth session when OpenAI invalidates the current authorization step', async () => {
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue(currentWorkflow({
      status: 'failed',
      manual_required: false,
      current_node: 'phone_submit',
      error: 'OpenAI invalid_auth_step：授权步骤已失效'
    }))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="team-reauthorize"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')
    wrapper.unmount()
  })

  it('deletes a Team account directly while retaining its historical mailbox', async () => {
    const account = {
      id: 317,
      name: 'team1004@example.test',
      platform: 'openai',
      type: 'oauth',
      status: 'active',
      schedulable: true,
      proxy_id: null,
      concurrency: 10,
      priority: 1,
      group_ids: [],
      credentials_status: { has_xiass_team_child_password_encrypted: true },
      extra: { xiass_team_child: true, xiass_team_child_email: 'team1004@example.test' }
    }
    accountsAPI.list
      .mockResolvedValueOnce({ items: [account], total: 1 })
      .mockResolvedValue({ items: [], total: 0 })
    accountsAPI.delete.mockResolvedValue({ message: 'deleted' })
    teamChildAPI.listMailboxes.mockResolvedValue(['team1004@example.test'])

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-history-delete-317"]').trigger('click')
    await flushPromises()

    expect(accountsAPI.delete).toHaveBeenCalledWith(317)
    expect(teamChildAPI.listMailboxes).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('stores ordinary OpenAI OAuth login details and only starts reauthorization after an explicit click', async () => {
    const account = {
      id: 415,
      name: 'ordinary@example.test',
      platform: 'openai',
      type: 'oauth',
      status: 'error',
      schedulable: true,
      proxy_id: null,
      concurrency: 10,
      priority: 1,
      group_ids: [19],
      parent_account_id: null,
      credentials: { email: 'ordinary@example.test' },
      credentials_status: {},
      extra: {}
    }
    accountsAPI.list.mockResolvedValue({ items: [account], total: 1 })
    teamChildAPI.saveOpenAIAccountReauthorizationCredentials.mockResolvedValue({
      ...account,
      credentials_status: {
        has_xiass_openai_oauth_reauth_email: true,
        has_xiass_openai_oauth_reauth_password_encrypted: true
      }
    })

    const wrapper = mountView()
    await flushPromises()

    expect(teamChildAPI.reauthorizeOpenAIAccount).not.toHaveBeenCalled()
    await wrapper.get('#openai-reauth-email').setValue('ordinary@example.test')
    await wrapper.get('#openai-reauth-password').setValue('Abc123456789!')
    const saveButton = wrapper.findAll('button').find((button) => button.text().includes('保存登录信息'))
    await saveButton!.trigger('click')
    await flushPromises()

    expect(teamChildAPI.saveOpenAIAccountReauthorizationCredentials).toHaveBeenCalledWith(415, {
      email: 'ordinary@example.test',
      password: 'Abc123456789!'
    })
    expect(teamChildAPI.reauthorizeOpenAIAccount).not.toHaveBeenCalled()

    const reauthorizeButton = wrapper.findAll('button').find((button) => button.text().includes('一键重新授权'))
    await reauthorizeButton!.trigger('click')
    await flushPromises()
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.reauthorizeOpenAIAccount).toHaveBeenCalledWith(415, testAuthURL, 'oauth-session')
    wrapper.unmount()
  })

  it('allows ordinary OpenAI OAuth reauthorization with only a saved login email', async () => {
    const account = {
      id: 416,
      name: 'passwordless@example.test',
      platform: 'openai',
      type: 'oauth',
      status: 'error',
      schedulable: true,
      proxy_id: null,
      concurrency: 10,
      priority: 1,
      group_ids: [19],
      parent_account_id: null,
      credentials: { email: 'passwordless@example.test' },
      credentials_status: {},
      extra: {}
    }
    accountsAPI.list.mockResolvedValue({ items: [account], total: 1 })
    teamChildAPI.saveOpenAIAccountReauthorizationCredentials.mockResolvedValue({
      ...account,
      credentials_status: { has_xiass_openai_oauth_reauth_email: true }
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('#openai-reauth-email').setValue('passwordless@example.test')
    const saveButton = wrapper.findAll('button').find((button) => button.text().includes('保存登录信息'))
    await saveButton!.trigger('click')
    await flushPromises()

    expect(teamChildAPI.saveOpenAIAccountReauthorizationCredentials).toHaveBeenCalledWith(416, {
      email: 'passwordless@example.test',
      password: ''
    })
    const reauthorizeButton = wrapper.findAll('button').find((button) => button.text().includes('一键重新授权'))
    await reauthorizeButton!.trigger('click')
    await flushPromises()
    expect(teamChildAPI.reauthorizeOpenAIAccount).toHaveBeenCalledWith(416, testAuthURL, 'oauth-session')
    wrapper.unmount()
  })

  it('cancels a failed workflow and returns to the first-step workspace', async () => {
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue(currentWorkflow({
      status: 'failed',
      current_node: 'invite',
      error: '待处理邀请页面未完成加载'
    }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-reset-workflow"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.cancelTeamChildWorkflow).toHaveBeenCalledWith('workflow-token-abcdefghijklmnop')
    expect(teamChildAPI.refreshTeamChildMembers).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="team-reset-workflow"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="team-one-click-authorize"]').text()).toContain('一键授权')
    wrapper.unmount()
  })

  it('keeps the mailbox configuration action on one line', async () => {
    const wrapper = mountView()
    await flushPromises()

    const configButton = wrapper.findAll('button').find((button) => button.text().includes('导入邮箱配置'))
    expect(configButton).toBeDefined()
    expect(configButton!.classes()).toContain('whitespace-nowrap')

    wrapper.unmount()
  })

  it('requires the in-page confirmation and reuses the displayed official OAuth session', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.refreshTeamChildMembers).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.refreshTeamChildMembers.mock.invocationCallOrder[0]).toBeLessThan(
      teamChildAPI.createMailbox.mock.invocationCallOrder[0]
    )
    expect(teamChildAPI.createMailbox).toHaveBeenCalledTimes(1)
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.startTeamChildWorkflow).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.startTeamChildWorkflow).toHaveBeenCalledWith({
      seat_email: 'member@example.test',
      invite_email: 'team-child@example.test',
      auth_url: testAuthURL,
      oauth_session_id: 'oauth-session',
      seat_already_removed: false,
      confirmed: true
    })
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')

    wrapper.unmount()
  })

  it('submits a pasted callback only after the workflow state is present', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    const callback = 'http://localhost:1455/auth/callback?code=callback-code&state=team-state'
    await wrapper.get('textarea[placeholder^="http://localhost"]')
      .setValue(callback)
    await flushPromises()
    const confirmCallback = wrapper.findAll('button').find((button) => button.text().includes('确认回调状态'))
    expect(confirmCallback).toBeDefined()
    await confirmCallback!.trigger('click')
    await flushPromises()

    expect(teamChildAPI.submitTeamChildWorkflowCallback).toHaveBeenCalledWith(
      'workflow-token-abcdefghijklmnop',
      callback
    )
    wrapper.unmount()
  })

  it('always starts the full workflow when the live page shows only protected members', async () => {
    const protectedMembers = {
      ready: true,
      pending_invites: 1,
      members: [{ id: 'owner@example.test', email: 'owner@example.test', role: 'owner', protected: true }]
    }
    teamChildAPI.listTeamChildMembers.mockResolvedValue(protectedMembers)
    teamChildAPI.refreshTeamChildMembers.mockResolvedValue(protectedMembers)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.startTeamChildWorkflow).toHaveBeenCalledWith({
      invite_email: 'team-child@example.test',
      auth_url: testAuthURL,
      oauth_session_id: 'oauth-session',
      seat_already_removed: true,
      confirmed: true
    })
    wrapper.unmount()
  })

  it('keeps the verification code control stable and copies from the code itself', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    teamChildAPI.pollMailboxCode.mockResolvedValue({ status: 'received', code: '123456' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-create-mailbox"]').trigger('click')
    await flushPromises()
    await wrapper.get('button[aria-label="立即检查邮箱"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="team-sidebar-mailbox-code"]').trigger('click')
    await flushPromises()

    const codeBox = wrapper.get('[data-testid="team-sidebar-mailbox-code"]')
    expect(codeBox.attributes('role')).toBe('button')
    expect(codeBox.attributes('tabindex')).toBe('0')
    expect(writeText).toHaveBeenCalledWith('123456')
    expect(wrapper.findAll('button').some((button) => button.text().trim() === '复制')).toBe(false)
    wrapper.unmount()
  })

  it('polls the active mailbox every five seconds until a code arrives', async () => {
    vi.useFakeTimers()
    teamChildAPI.pollMailboxCode.mockResolvedValue({ status: 'waiting' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-create-mailbox"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('button[aria-label="立即检查邮箱"] .animate-spin').exists()).toBe(true)

    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(teamChildAPI.pollMailboxCode).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(4999)
    expect(teamChildAPI.pollMailboxCode).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(teamChildAPI.pollMailboxCode).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('reopens a stale mailbox session after two waiting polls and continues from the same email', async () => {
    vi.useFakeTimers()
    teamChildAPI.pollMailboxCode.mockResolvedValue({ status: 'waiting' })
    teamChildAPI.selectMailbox.mockResolvedValue({
      session_id: 'fresh-mailbox-session',
      email: 'team-child@example.test',
      expires_at: '2026-08-22T12:35:00.000Z'
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-create-mailbox"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()

    expect(teamChildAPI.selectMailbox).toHaveBeenCalledWith('team-child@example.test')
    expect(teamChildAPI.deleteMailboxSession).toHaveBeenCalledWith('mailbox-session')

    await vi.advanceTimersByTimeAsync(50)
    await flushPromises()
    expect(teamChildAPI.pollMailboxCode).toHaveBeenLastCalledWith('fresh-mailbox-session')
    wrapper.unmount()
  })

  it('continues after a manually released seat only when the live members are protected', async () => {
    const protectedMembers = {
      ready: true,
      members: [
        { id: 'owner@example.test', email: 'owner@example.test', role: 'owner', protected: true },
        { id: 'admin@example.test', email: 'admin@example.test', role: 'admin', protected: true }
      ]
    }
    teamChildAPI.listTeamChildMembers.mockResolvedValue(protectedMembers)
    teamChildAPI.refreshTeamChildMembers.mockResolvedValue(protectedMembers)
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.startTeamChildWorkflow).toHaveBeenCalledWith({
      invite_email: 'team-child@example.test',
      auth_url: testAuthURL,
      oauth_session_id: 'oauth-session',
      seat_already_removed: true,
      confirmed: true
    })
    wrapper.unmount()
  })

  it('restores the active temporary mailbox after a refresh without creating another address', async () => {
    teamChildAPI.getActiveMailbox.mockResolvedValue({
      session_id: 'restored-mailbox-session',
      email: 'restored@example.test',
      expires_at: '2026-08-22T12:30:00.000Z'
    })
    const wrapper = mountView()
    await flushPromises()

    expect(teamChildAPI.getActiveMailbox).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.createMailbox).not.toHaveBeenCalled()
    expect(nativeGenerateAuthUrl).not.toHaveBeenCalled()
    expect(teamChildAPI.pollMailboxCode).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('restored@example.test')
    wrapper.unmount()
  })

  it('reopens a known Team mailbox through a new server-side session', async () => {
    teamChildAPI.listMailboxes.mockResolvedValue(['team1003@example.test'])
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-open-mailbox"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.selectMailbox).toHaveBeenCalledWith('team1003@example.test')
    expect(wrapper.text()).toContain('team1003@example.test')
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('binds the protocol-two workflow ID to the final OAuth import', async () => {
    teamChildAPI.createOpenAIAccountFromOAuth.mockResolvedValue({ id: 300, name: 'team-child@example.test' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('textarea[placeholder^="http://localhost"]').setValue('http://localhost:1455/auth/callback?code=callback-code&state=team-state')
    const confirmCallback = wrapper.findAll('button').find((button) => button.text().includes('确认回调状态'))
    await confirmCallback!.trigger('click')
    await flushPromises()
    const importButton = wrapper.findAll('button').find((button) => button.text().includes('校验并导入'))
    await importButton!.trigger('click')
    await flushPromises()

    expect(teamChildAPI.createOpenAIAccountFromOAuth).toHaveBeenCalledWith(expect.objectContaining({
      session_id: 'oauth-session',
      code: 'callback-code',
      state: 'team-state',
      name: 'team-child@example.test',
      skip_default_group_bind: true,
      schedulable: true,
      team_child: true,
      workflow_id: 'workflow-token-abcdefghijklmnop'
    }))
    expect(teamChildAPI.completeTeamChildWorkflow).toHaveBeenCalledWith('workflow-token-abcdefghijklmnop')
    expect(wrapper.get('[data-testid="team-child-account-success-dialog"]').text()).toBe('team-child@example.test')
    expect(wrapper.findAll('[data-testid="team-oauth-current-node"]').some((card) => card.text().includes('已完成创建 Team 子账号'))).toBe(true)
    wrapper.unmount()
  })

  it('persists the compact success dialog group, concurrency, and priority changes', async () => {
    teamChildAPI.createOpenAIAccountFromOAuth.mockResolvedValue({
      id: 300,
      name: 'team-child@example.test',
      concurrency: 10,
      priority: 1,
      group_ids: []
    })
    accountsAPI.update.mockResolvedValue({
      id: 300,
      name: 'team-child@example.test',
      concurrency: 18,
      priority: 3,
      group_ids: [19],
      groups: [{ id: 19, name: 'OpenAI Team' }]
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    await wrapper.get('textarea[placeholder^="http://localhost"]').setValue('http://localhost:1455/auth/callback?code=callback-code&state=team-state')
    const confirmCallback = wrapper.findAll('button').find((button) => button.text().includes('确认回调状态'))
    await confirmCallback!.trigger('click')
    await flushPromises()
    const importButton = wrapper.findAll('button').find((button) => button.text().includes('校验并导入'))
    await importButton!.trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="success-dialog-groups"]').trigger('click')
    await wrapper.get('[data-testid="success-dialog-concurrency"]').trigger('click')
    await wrapper.get('[data-testid="success-dialog-priority"]').trigger('click')
    await wrapper.get('[data-testid="success-dialog-save"]').trigger('click')
    await flushPromises()

    expect(accountsAPI.update).toHaveBeenCalledWith(300, {
      group_ids: [19],
      concurrency: 18,
      priority: 3
    })
    expect(appStore.showSuccess).toHaveBeenCalledWith('Team 子账号配置已保存')
    wrapper.unmount()
  })

  it('uses an explicit empty group_ids response instead of a stale pre-import selection', async () => {
    teamChildAPI.createOpenAIAccountFromOAuth.mockResolvedValue({
      id: 300,
      name: 'team-child@example.test',
      concurrency: 10,
      priority: 1,
      group_ids: []
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="group-selector"]').trigger('click')
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    await wrapper.get('textarea[placeholder^="http://localhost"]').setValue('http://localhost:1455/auth/callback?code=callback-code&state=team-state')
    const confirmCallback = wrapper.findAll('button').find((button) => button.text().includes('确认回调状态'))
    await confirmCallback!.trigger('click')
    await flushPromises()
    const importButton = wrapper.findAll('button').find((button) => button.text().includes('校验并导入'))
    await importButton!.trigger('click')
    await flushPromises()

    expect(teamChildAPI.createOpenAIAccountFromOAuth).toHaveBeenCalledWith(expect.objectContaining({ group_ids: [19] }))
    expect(wrapper.get('[data-testid="success-dialog-group-ids"]').text()).toBe('')
    wrapper.unmount()
  })

  it('keeps the pre-import group checked when an older create response omits group_ids', async () => {
    teamChildAPI.createOpenAIAccountFromOAuth.mockResolvedValue({
      id: 300,
      name: 'team-child@example.test',
      concurrency: 10,
      priority: 1
    })
    accountsAPI.update.mockResolvedValue({
      id: 300,
      name: 'team-child@example.test',
      concurrency: 10,
      priority: 1,
      group_ids: [19]
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="group-selector"]').trigger('click')
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    await wrapper.get('textarea[placeholder^="http://localhost"]').setValue('http://localhost:1455/auth/callback?code=callback-code&state=team-state')
    const confirmCallback = wrapper.findAll('button').find((button) => button.text().includes('确认回调状态'))
    await confirmCallback!.trigger('click')
    await flushPromises()
    const importButton = wrapper.findAll('button').find((button) => button.text().includes('校验并导入'))
    await importButton!.trigger('click')
    await flushPromises()

    expect(teamChildAPI.createOpenAIAccountFromOAuth).toHaveBeenCalledWith(expect.objectContaining({ group_ids: [19] }))
    expect(wrapper.get('[data-testid="success-dialog-group-ids"]').text()).toBe('19')

    await wrapper.get('[data-testid="success-dialog-save"]').trigger('click')
    await flushPromises()
    expect(accountsAPI.update).toHaveBeenCalledWith(300, {
      group_ids: [19],
      concurrency: 10,
      priority: 1
    })
    wrapper.unmount()
  })

  it('forwards a confirmed SMS receiver number only at the matching workflow node', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="sms-phone-ready"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.submitTeamChildWorkflowPhone).toHaveBeenCalledWith(
      'workflow-token-abcdefghijklmnop',
      '+14155550123'
    )
    expect(teamChildAPI.submitTeamChildWorkflowSMSCode).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('forwards a replacement number while an old SMS code is still being polled', async () => {
    teamChildAPI.startTeamChildWorkflow.mockResolvedValueOnce(currentWorkflow({ current_node: 'sms_poll' }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="sms-phone-ready"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.submitTeamChildWorkflowPhone).toHaveBeenCalledWith(
      'workflow-token-abcdefghijklmnop',
      '+14155550123'
    )
    wrapper.unmount()
  })

  it('pauses the persisted workflow when an automation-owned SMS session is cancelled', async () => {
    teamChildAPI.startTeamChildWorkflow.mockResolvedValueOnce(currentWorkflow({ current_node: 'sms_poll' }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="sms-session-cancelled"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.pauseTeamChildWorkflow).toHaveBeenCalledWith('workflow-token-abcdefghijklmnop')
    expect(teamChildAPI.restartTeamChildWorkflowOAuth).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('forwards a Cloudflare mailbox code only while the mailbox node is waiting', async () => {
    teamChildAPI.startTeamChildWorkflow.mockResolvedValueOnce(currentWorkflow({
      current_node: 'mailbox',
      email_code_generation: 1,
      email_code_purpose: 'registration'
    }))
    teamChildAPI.pollMailboxCode.mockResolvedValue({ status: 'received', code: '123456' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    await wrapper.get('button[aria-label="立即检查邮箱"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.submitTeamChildWorkflowEmailCode).toHaveBeenCalledWith(
      'workflow-token-abcdefghijklmnop',
      '123456'
    )
    expect(teamChildAPI.submitTeamChildWorkflowSMSCode).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('waits for a new mailbox code when OAuth starts a second verification round', async () => {
    vi.useFakeTimers()
    const registrationWaiting = currentWorkflow({
      current_node: 'mailbox',
      email_code_generation: 1,
      email_code_purpose: 'registration'
    })
    const registrationSubmitted = currentWorkflow({
      status: 'running',
      manual_required: false,
      current_node: 'email_code',
      email_code_generation: 1,
      email_code_purpose: 'registration'
    })
    const oauthWaiting = currentWorkflow({
      current_node: 'password',
      email_code_generation: 2,
      email_code_purpose: 'oauth_login'
    })
    teamChildAPI.startTeamChildWorkflow.mockResolvedValueOnce(registrationWaiting)
    teamChildAPI.getTeamChildWorkflow.mockResolvedValue(oauthWaiting)
    teamChildAPI.pollMailboxCode
      .mockResolvedValueOnce({ status: 'received', code: '111111' })
      .mockResolvedValueOnce({ status: 'received', code: '111111' })
      .mockResolvedValueOnce({ status: 'received', code: '222222' })
    teamChildAPI.submitTeamChildWorkflowEmailCode
      .mockResolvedValueOnce(registrationSubmitted)
      .mockResolvedValueOnce(currentWorkflow({
        status: 'running',
        manual_required: false,
        current_node: 'password',
        email_code_generation: 2,
        email_code_purpose: 'oauth_login'
      }))

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(teamChildAPI.submitTeamChildWorkflowEmailCode).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.submitTeamChildWorkflowEmailCode).toHaveBeenLastCalledWith(
      'workflow-token-abcdefghijklmnop',
      '111111'
    )

    await vi.advanceTimersByTimeAsync(400)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(50)
    await flushPromises()
    expect(teamChildAPI.submitTeamChildWorkflowEmailCode).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(teamChildAPI.submitTeamChildWorkflowEmailCode).toHaveBeenCalledTimes(2)
    expect(teamChildAPI.submitTeamChildWorkflowEmailCode).toHaveBeenLastCalledWith(
      'workflow-token-abcdefghijklmnop',
      '222222'
    )
    wrapper.unmount()
  })

  it('forwards an SMS code only while the SMS polling node is waiting', async () => {
    teamChildAPI.startTeamChildWorkflow.mockResolvedValueOnce(currentWorkflow({ current_node: 'sms_poll' }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="sms-code-received"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.submitTeamChildWorkflowSMSCode).toHaveBeenCalledWith(
      'workflow-token-abcdefghijklmnop',
      '123456'
    )
    expect(teamChildAPI.submitTeamChildWorkflowEmailCode).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not mint a new OAuth session after confirmed SMS cancellation', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="team-one-click-authorize"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="sms-session-cancelled"]').trigger('click')
    await flushPromises()

    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.pauseTeamChildWorkflow).toHaveBeenCalledWith('workflow-token-abcdefghijklmnop')
    expect(teamChildAPI.restartTeamChildWorkflowOAuth).not.toHaveBeenCalled()
    expect(teamChildAPI.cancelTeamChildWorkflow).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
