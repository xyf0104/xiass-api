import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TeamChildOAuthWorkspace from '../TeamChildOAuthWorkspace.vue'
import i18n from '@/i18n'

const PixlabSMSReceiverStub = {
  props: { active: { type: Boolean, default: false } },
  template: '<div data-testid="team-sms-receiver" :data-active="String(active)" />'
}

const IconStub = { template: '<span />' }

const BrowserWorkspaceRefreshStub = {
  template: '<button type="button" data-testid="team-browser-refresh-members" @click="$emit(\'refresh-members\')" />'
}

const nodeDefinitions = [
  ['signup', '隐私页注册新邮箱'], ['email', '填入注册邮箱'], ['mail', '发送注册邮箱验证码'],
  ['mailbox', '读取注册邮箱验证码'], ['email_code', '提交注册邮箱验证码'], ['members', '读取成员席位'],
  ['remove', '移除已选成员'], ['invite', '提交成员邀请'], ['invite_confirm', '确认 Pending invites'],
  ['oauth', '同一隐私页打开 OAuth'], ['password', '完成 OAuth 邮箱登录验证'], ['phone', '进入手机号页面'],
  ['sms_confirm', '确认领取手机号'], ['phone_submit', '填入号码并选择 Text message'], ['sms_poll', '轮询短信验证码'],
  ['sms_code', '自动填入短信验证码'], ['profile_wait', '等待资料页 5 秒'], ['profile', '填写 black / 26'],
  ['workspace_wait', '等待工作空间 10 秒'], ['workspace', '默认工作空间继续'], ['callback', '捕获 OAuth 回调'],
  ['import', '按勾选配置导入 XIASS']
] as const

function workflow(overrides: Record<string, unknown> = {}) {
  const currentNode = String(overrides.current_node || 'sms_confirm')
  const currentIndex = nodeDefinitions.findIndex(([key]) => key === currentNode)
  const workflowStatus = String(overrides.status || 'manual_required')
  return {
    schema_version: 4,
    id: 'workflow-token-abcdefghijklmnop',
    status: workflowStatus,
    manual_required: workflowStatus === 'manual_required',
    current_node: currentNode,
    expires_at: '2026-08-24T00:00:00.000Z',
    nodes: nodeDefinitions.map(([key, label], index) => ({
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

describe('TeamChildOAuthWorkspace', () => {
  it('keeps the mailbox-share link action in the mailbox module header without crowding the bottom actions', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow(),
        mailboxEmail: 'team1061@example.test'
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    const button = wrapper.get('[data-testid="team-open-mailbox-share"]')
    expect(button.text()).toContain('接码链接')
    expect(button.classes()).toContain('ml-auto')
    expect(wrapper.get('.team-oauth-side-module-heading').find('[data-testid="team-open-mailbox-share"]').exists()).toBe(true)
    expect(wrapper.get('.team-oauth-mailbox-actions').find('[data-testid="team-open-mailbox-share"]').exists()).toBe(false)
    await button.trigger('click')
    expect(wrapper.emitted('open-mailbox-share')?.[0]).toEqual(['team1061@example.test'])
  })

  it('uses the same card workspace for preflight member controls', () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ current_node: 'members' }),
        preflight: true,
        showOneClick: true
      },
      slots: {
        operation: '<div data-testid="preflight-operation">成员席位</div>',
        'operation-after': '<div data-testid="preflight-import">账号导入配置</div>'
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    expect(wrapper.get('[data-testid="team-oauth-progress-card"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="preflight-operation"]').text()).toBe('成员席位')
    expect(wrapper.get('[data-testid="preflight-import"]').text()).toBe('账号导入配置')
    expect(wrapper.text()).not.toContain('XIASS 官方 OAuth PKCE 链接')
  })

  it('activates the confirmed SMS receiver only at the phone node', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow(),
        automationEnabled: true,
        mailboxEmail: 'team-child@example.test',
        authUrl: 'https://auth.openai.com/oauth/authorize?state=test-state'
      },
      global: {
        plugins: [i18n],
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    expect(wrapper.get('[data-testid="team-oauth-workspace"]').text()).not.toContain('成员授权工作区')
    expect(wrapper.get('[data-testid="team-oauth-current-node"]').text()).toContain('领取手机号')
    expect(wrapper.text()).toContain('已完成')
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')
    expect(wrapper.find('[data-testid="team-oauth-manual-code"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('如果仍出现登录页，会再次输入该邮箱并读取新一轮验证码')
    expect(wrapper.text()).toContain('上方只显示当前节点')
    expect(wrapper.get('[data-testid="team-oauth-progress-card"]').classes()).toContain('relative')
    expect(wrapper.get('[data-testid="team-oauth-progress-card"]').classes()).not.toContain('sticky')
    expect(wrapper.get('[data-testid="team-oauth-operation-card"]').text()).toContain('XIASS 官方 OAuth PKCE 链接')
    expect(wrapper.find('[data-testid="team-open-auth"]').exists()).toBe(false)
  })

  it('keeps the generated login password masked until an explicit reveal', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ password_available: true }),
        mailboxEmail: 'team-child@example.test'
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    expect(wrapper.get('[data-testid="team-child-password-value"]').text()).toBe('•••••••••••••')
    await wrapper.get('[data-testid="team-child-password-panel"] button:last-child').trigger('click')
    expect(wrapper.emitted('reveal-password')).toHaveLength(1)

    await wrapper.setProps({ revealedPassword: 'Abc123456789!' })
    expect(wrapper.get('[data-testid="team-child-password-value"]').text()).toBe('Abc123456789!')
    expect(wrapper.get('[data-testid="team-child-password-panel"] button:last-child').attributes('aria-label')).toBe('隐藏登录密码')
  })

  it('keeps mailbox, password, history, and SMS tools in the side rail', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ password_available: true }),
        mailboxEmail: 'team-child@example.test',
        historyMailboxes: ['team-child@example.test', 'old@example.test'],
        selectedMailboxEmail: 'team-child@example.test',
        mailboxCode: '123456',
        mailboxConfigured: true
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    expect(wrapper.get('[data-testid="team-oauth-side-actions"]').text()).toContain('邮箱与验证码')
    expect(wrapper.get('[data-testid="team-sidebar-mailbox-code-value"]').text()).toBe('123456')
    expect(wrapper.get('[data-testid="team-history-mailbox-select"]').text()).toContain('team-child@example.test')
    await wrapper.get('[data-testid="team-open-mailbox"]').trigger('click')
    await wrapper.get('[data-testid="team-sidebar-mailbox-code"]').exists()
    expect(wrapper.emitted('open-mailbox')).toEqual([['team-child@example.test']])
  })

  it('keeps only the mailbox refresh icon spinning during automatic polling', () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ current_node: 'mailbox' }),
        mailboxEmail: 'team-child@example.test',
        mailboxCodePolling: true
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    expect(wrapper.get('[data-testid="team-sidebar-mailbox-code-value"]').text()).toBe('轮询中')
    expect(wrapper.get('button[aria-label="立即检查邮箱"] .animate-spin').exists()).toBe(true)
  })

  it('replaces the numbered final node with an explicit completed Team card', () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({
          status: 'completed',
          manual_required: false,
          current_node: 'import',
          nodes: nodeDefinitions.map(([key, label], index) => ({ key, number: index + 1, label, status: 'completed' }))
        }),
        mailboxEmail: 'team-child@example.test'
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    expect(wrapper.get('[data-testid="team-oauth-current-node"]').text()).toContain('已完成创建 Team 子账号')
    expect(wrapper.get('[data-testid="team-oauth-step-stack"]').findAll('article')).toHaveLength(1)
    expect(wrapper.text()).toContain('22/22')
  })

  it('shows callback state without activating the receiver for import', () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'callback_ready', manual_required: false }),
        mailboxEmail: 'team-child@example.test',
        callbackURL: 'http://localhost:1455/auth/callback?code=one&state=two'
      },
      global: {
        plugins: [i18n],
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    expect(wrapper.text()).toContain('已校验授权回调')
    expect(wrapper.text()).toContain('复制回调地址')
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')
  })

  it('offers embedded-browser recovery and continuation for every failed node', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'failed', current_node: 'profile', error: '资料页字段变化' }),
        authUrl: 'https://auth.openai.com/oauth/authorize?state=test-state'
      },
      global: {
        plugins: [i18n],
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    await wrapper.get('[data-testid="team-manual-browser"]').trigger('click')
    await wrapper.get('[data-testid="team-continue-after-failure"]').trigger('click')

    expect(wrapper.get('[data-testid="team-oauth-progress-card"]').find('[data-testid="team-manual-browser"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="team-oauth-operation-card"]').find('[data-testid="team-manual-browser"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="team-oauth-side-actions"]').find('[data-testid="team-manual-browser"]').exists()).toBe(false)
    expect(wrapper.emitted('open-browser')).toHaveLength(1)
    expect(wrapper.emitted('continue-workflow')).toHaveLength(1)
    await wrapper.get('[data-testid="team-reset-workflow"]').trigger('click')
    expect(wrapper.emitted('reset-workflow')).toHaveLength(1)
  })

  it('offers explicit continuation after manual OpenAI reauthorization login', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({
          mode: 'reauthorization',
          status: 'manual_required',
          current_node: 'password'
        }),
        automationEnabled: true
      },
      global: {
        plugins: [i18n],
        stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub }
      }
    })

    expect(wrapper.get('[data-testid="team-oauth-current-node"]').text()).toContain('判断是否需要登录密码')
    await wrapper.get('[data-testid="team-resume-workflow"]').trigger('click')
    expect(wrapper.emitted('continue-workflow')).toHaveLength(1)
  })

  it('forwards an embedded browser member-page refresh to the Team workspace', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow(),
        browserVisible: true,
        browserConfigured: true,
        browserEmbedUrl: '/api/v1/team-child-browser/?ticket=test'
      },
      global: {
        plugins: [i18n],
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub,
          TeamChildBrowserWorkspace: BrowserWorkspaceRefreshStub
        }
      }
    })

    await wrapper.get('[data-testid="team-browser-refresh-members"]').trigger('click')

    expect(wrapper.emitted('refresh-members')).toHaveLength(1)
  })

  it('routes a rejected phone directly to one confirmed replacement instead of generic continuation', () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'failed', current_node: 'phone_submit', error: '当前手机号不可用或使用次数过多' })
      },
      global: {
        plugins: [i18n],
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: {
            props: ['active', 'replacementRequired'],
            template: '<div data-testid="team-sms-receiver" :data-replacement-required="String(replacementRequired)" />'
          }
        }
      }
    })

    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-replacement-required')).toBe('true')
    expect(wrapper.find('[data-testid="team-continue-after-failure"]').exists()).toBe(false)
  })

  it('keeps SMS inactive while paused and exposes separate resume and cancel controls', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'paused', manual_required: false, current_node: 'sms_poll', pause_requested: true })
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')
    await wrapper.get('[data-testid="team-resume-workflow"]').trigger('click')
    await wrapper.get('[data-testid="team-reset-workflow"]').trigger('click')
    expect(wrapper.emitted('continue-workflow')).toHaveLength(1)
    expect(wrapper.emitted('reset-workflow')).toHaveLength(1)
    expect(wrapper.find('[data-testid="team-pause-workflow"]').exists()).toBe(false)
  })

  it('does not resume a restored SMS node until continue is explicitly clicked', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'manual_required', current_node: 'sms_poll' }),
        automationEnabled: false
      },
      global: { plugins: [i18n], stubs: { Icon: IconStub, PixlabSMSReceiver: PixlabSMSReceiverStub } }
    })

    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')
    expect(wrapper.find('[data-testid="team-pause-workflow"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="team-one-click-authorize"]').exists()).toBe(false)
    await wrapper.get('[data-testid="team-resume-workflow"]').trigger('click')
    expect(wrapper.emitted('continue-workflow')).toHaveLength(1)
  })
})
