import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountUsageCell from '../AccountUsageCell.vue'
import type { Account } from '@/types'

const { getUsage } = vi.hoisted(() => ({
  getUsage: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getUsage
    }
  }
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

function makeAccount(overrides: Partial<Account>): Account {
  return {
    id: 1,
    name: 'account',
    platform: 'antigravity',
    type: 'oauth',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: true,
    created_at: '2026-03-15T00:00:00Z',
    updated_at: '2026-03-15T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides,
  }
}

const billingUsageProgressBarStub = {
  props: ['label', 'utilization', 'resetsAt', 'windowStats', 'highlightBilling', 'wide'],
  emits: ['open-billing-details'],
  template: `
    <div class="usage-bar">
      <button
        type="button"
        :data-window="label"
        :data-highlight-billing="String(highlightBilling)"
        :data-wide="String(wide)"
        @click="$emit('open-billing-details')"
      >
        {{ label }}|{{ utilization }}|{{ windowStats?.cost }}
      </button>
      <div data-test="stub-footer"><slot name="footer" /></div>
      <div data-test="stub-aside"><slot name="aside" /></div>
    </div>
  `
}

describe('AccountUsageCell', () => {
  beforeEach(() => {
    getUsage.mockReset()
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation(() => ({
        matches: true,
        media: '(min-width: 768px)',
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    })
  })

  it('remote OpenAI usage and manual refresh stay passive while billing remains clickable', async () => {
    getUsage.mockResolvedValue({
      source: 'passive',
      seven_day: {
        utilization: 100,
        resets_at: '2099-03-13T12:00:00Z',
        weekly_estimate_usd: 0,
        window_stats: { requests: 0, tokens: 0, cost: 0, standard_cost: 0, user_cost: 0 }
      }
    })
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 9101, platform: 'openai', type: 'oauth' }),
        readOnly: true,
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          OpenAIQuotaResetCell: {
            name: 'OpenAIQuotaResetCell',
            props: ['account', 'readOnly'],
            template: '<div><slot name="pre-actions" /></div>'
          }
        }
      }
    })
    await flushPromises()
    expect(getUsage).toHaveBeenCalledWith(9101, 'passive', undefined)
    expect(wrapper.findComponent({ name: 'OpenAIQuotaResetCell' }).props('readOnly')).toBe(true)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$0.00')
    const activeButton = wrapper.findAll('button').find(button => button.text().includes('usageWindow.activeQuery'))!
    expect(activeButton.attributes('disabled')).toBeDefined()
    await activeButton.trigger('click')
    expect(getUsage).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-window="7d"]').trigger('click')
    expect(wrapper.emitted('open-billing-details')).toHaveLength(1)

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(2)
    expect(getUsage).toHaveBeenLastCalledWith(9101, 'passive', undefined)
    wrapper.unmount()
  })

  it('permission changes invalidate cached usage and reject stale active responses', async () => {
    const snapshot = (estimate: number) => ({
      seven_day: { utilization: 25, weekly_estimate_usd: estimate, resets_at: '2099-03-13T12:00:00Z' }
    })
    let resolveActive!: (value: unknown) => void
    getUsage
      .mockImplementationOnce(() => new Promise(resolve => { resolveActive = resolve }))
      .mockResolvedValueOnce(snapshot(2400))
      .mockResolvedValueOnce(snapshot(2500))
    const wrapper = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 9102, platform: 'openai', type: 'oauth' }) },
      global: { stubs: { UsageProgressBar: billingUsageProgressBarStub, OpenAIQuotaResetCell: true } }
    })
    await flushPromises()
    expect(getUsage).toHaveBeenNthCalledWith(1, 9102)
    await wrapper.setProps({ readOnly: true })
    await flushPromises()
    expect(getUsage).toHaveBeenNthCalledWith(2, 9102, 'passive', undefined)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$2,400.00')
    resolveActive(snapshot(9900))
    await flushPromises()
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$2,400.00')
    await wrapper.setProps({ readOnly: false })
    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(3)
    expect(getUsage).toHaveBeenLastCalledWith(9102)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$2,500.00')
    wrapper.unmount()
  })

  it('a reused account row cannot receive the previous account passive result', async () => {
    let resolveOld!: (value: unknown) => void
    getUsage
      .mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
      .mockResolvedValueOnce({ seven_day: { utilization: 25, weekly_estimate_usd: 2400 } })
    const wrapper = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 9103, platform: 'openai', type: 'oauth' }), readOnly: true },
      global: { stubs: { UsageProgressBar: billingUsageProgressBarStub, OpenAIQuotaResetCell: true } }
    })
    await flushPromises()
    await wrapper.setProps({ account: makeAccount({ id: 9104, platform: 'openai', type: 'oauth' }) })
    await flushPromises()
    resolveOld({ seven_day: { utilization: 25, weekly_estimate_usd: 9900 } })
    await flushPromises()
    expect(getUsage).toHaveBeenLastCalledWith(9104, 'passive', undefined)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$2,400.00')
    wrapper.unmount()
  })

  it('manual OpenAI reauthorization markers supersede an older usage request for the same account', async () => {
    let resolveOldUsage!: (value: unknown) => void
    getUsage
      .mockImplementationOnce(() => new Promise(resolve => { resolveOldUsage = resolve }))
      .mockResolvedValueOnce({
        seven_day: { utilization: 25, weekly_estimate_usd: 2400 }
      })
    const initialAccount = makeAccount({
      id: 9105,
      platform: 'openai',
      type: 'oauth',
      credentials: { expires_at: 1_800_000_000 },
      extra: {
        xiass_openai_reauthorization_state: {
          last_succeeded_at: '2026-09-30T10:00:00Z',
          success_count: 1
        }
      }
    })
    const wrapper = mount(AccountUsageCell, {
      props: { account: initialAccount },
      global: { stubs: { UsageProgressBar: billingUsageProgressBarStub, OpenAIQuotaResetCell: true } }
    })

    await flushPromises()
    await wrapper.setProps({
      account: makeAccount({
        ...initialAccount,
        credentials: { expires_at: 1_900_000_000 },
        extra: {
          xiass_openai_reauthorization_state: {
            last_succeeded_at: '2026-10-01T10:00:00Z',
            success_count: 2
          }
        }
      })
    })
    await flushPromises()

    resolveOldUsage({
      needs_reauth: true,
      error_code: 'unauthenticated',
      error: 'late pre-reauthorization 401',
      seven_day: { utilization: 99, weekly_estimate_usd: 9900 }
    })
    await flushPromises()

    expect(getUsage).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$2,400.00')
    expect(wrapper.emitted('reauth-status')).toBeUndefined()
    wrapper.unmount()
  })

  it('treats structured unauthenticated usage as reauth evidence and clears it after a successful refresh', async () => {
    getUsage
      .mockResolvedValueOnce({
        needs_reauth: false,
        error_code: 'unauthenticated',
        error: 'upstream authentication failed'
      })
      .mockResolvedValueOnce({
        seven_day: { utilization: 25, weekly_estimate_usd: 2400 }
      })
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 9106, platform: 'openai', type: 'oauth' }),
        manualRefreshToken: 0
      },
      global: { stubs: { UsageProgressBar: billingUsageProgressBarStub, OpenAIQuotaResetCell: true } }
    })

    await flushPromises()
    expect(wrapper.emitted('reauth-status')).toEqual([[
      { accountId: 9106, needsReauth: true }
    ]])

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(wrapper.emitted('reauth-status')).toEqual([
      [{ accountId: 9106, needsReauth: true }],
      [{ accountId: 9106, needsReauth: false }]
    ])
    wrapper.unmount()
  })

  it('relays quota reset reauth evidence while ignoring another account id', async () => {
    getUsage.mockResolvedValue({
      seven_day: { utilization: 25, weekly_estimate_usd: 2400 }
    })
    const wrapper = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 9107, platform: 'openai', type: 'oauth' }) },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          OpenAIQuotaResetCell: {
            name: 'OpenAIQuotaResetCell',
            emits: ['reauth-status'],
            template: '<div><slot name="pre-actions" /></div>'
          }
        }
      }
    })

    await flushPromises()
    const quotaCell = wrapper.findComponent({ name: 'OpenAIQuotaResetCell' })
    quotaCell.vm.$emit('reauth-status', { accountId: 9999, needsReauth: true })
    await flushPromises()
    expect(wrapper.emitted('reauth-status')).toBeUndefined()

    quotaCell.vm.$emit('reauth-status', { accountId: 9107, needsReauth: true })
    await flushPromises()
    quotaCell.vm.$emit('reauth-status', { accountId: 9107, needsReauth: false })
    await flushPromises()
    expect(wrapper.emitted('reauth-status')).toEqual([
      [{ accountId: 9107, needsReauth: true }],
      [{ accountId: 9107, needsReauth: false }]
    ])
    wrapper.unmount()
  })

  it('a successful quota-cell refresh clears stale reauth fields loaded by usage', async () => {
    getUsage.mockResolvedValue({
      needs_reauth: true,
      error_code: 'unauthenticated',
      error: 'upstream authentication failed'
    })
    const wrapper = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 9109, platform: 'openai', type: 'oauth' }) },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          OpenAIQuotaResetCell: {
            name: 'OpenAIQuotaResetCell',
            emits: ['reauth-status'],
            template: '<div><slot name="pre-actions" /></div>'
          }
        }
      }
    })

    await flushPromises()
    expect(wrapper.emitted('reauth-status')).toEqual([[
      { accountId: 9109, needsReauth: true }
    ]])

    wrapper.findComponent({ name: 'OpenAIQuotaResetCell' }).vm.$emit('reauth-status', {
      accountId: 9109,
      needsReauth: false
    })
    await flushPromises()

    expect(wrapper.emitted('reauth-status')).toEqual([
      [{ accountId: 9109, needsReauth: true }],
      [{ accountId: 9109, needsReauth: false }]
    ])
    const recoveredUsage = (wrapper.vm as unknown as {
      usageInfo: { needs_reauth?: boolean; error_code?: string; error?: string } | null
    }).usageInfo
    expect(recoveredUsage).toMatchObject({ needs_reauth: false })
    expect(recoveredUsage?.error_code).toBeUndefined()
    expect(recoveredUsage?.error).toBeUndefined()
    wrapper.unmount()
  })

  it('quota confirmation rejects a stale in-flight usage 401 without blocking later refreshes', async () => {
    let resolveStaleUsage!: (value: unknown) => void
    getUsage
      .mockResolvedValueOnce({
        needs_reauth: true,
        error_code: 'unauthenticated',
        error: 'first upstream 401',
        seven_day: { utilization: 25, weekly_estimate_usd: 2400 }
      })
      .mockImplementationOnce(() => new Promise(resolve => { resolveStaleUsage = resolve }))
      .mockResolvedValueOnce({
        seven_day: { utilization: 26, weekly_estimate_usd: 2400 }
      })
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 9110, platform: 'openai', type: 'oauth' }),
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          OpenAIQuotaResetCell: {
            name: 'OpenAIQuotaResetCell',
            emits: ['reauth-status'],
            template: '<div><slot name="pre-actions" /></div>'
          }
        }
      }
    })

    await flushPromises()
    const quotaCell = wrapper.findComponent({ name: 'OpenAIQuotaResetCell' })
    await wrapper.setProps({ manualRefreshToken: 1 })
    quotaCell.vm.$emit('reauth-status', { accountId: 9110, needsReauth: false })
    await flushPromises()

    resolveStaleUsage({
      needs_reauth: true,
      error_code: 'unauthenticated',
      error: 'late upstream 401',
      seven_day: { utilization: 99, weekly_estimate_usd: 9900 }
    })
    await flushPromises()
    expect(wrapper.emitted('reauth-status')).toEqual([
      [{ accountId: 9110, needsReauth: true }],
      [{ accountId: 9110, needsReauth: false }]
    ])

    await wrapper.setProps({ manualRefreshToken: 2 })
    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(3)
    expect(getUsage).toHaveBeenLastCalledWith(9110, undefined, true)
    expect(wrapper.emitted('reauth-status')).toHaveLength(2)
    wrapper.unmount()
  })

  it('clears quota reset evidence after a current forced usage refresh succeeds', async () => {
    getUsage
      .mockResolvedValueOnce({ seven_day: { utilization: 25, weekly_estimate_usd: 2400 } })
      .mockResolvedValueOnce({ seven_day: { utilization: 26, weekly_estimate_usd: 2400 } })
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 9108, platform: 'openai', type: 'oauth' }),
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          OpenAIQuotaResetCell: {
            name: 'OpenAIQuotaResetCell',
            emits: ['reauth-status'],
            template: '<div><slot name="pre-actions" /></div>'
          }
        }
      }
    })

    await flushPromises()
    wrapper.findComponent({ name: 'OpenAIQuotaResetCell' }).vm.$emit('reauth-status', {
      accountId: 9108,
      needsReauth: true
    })
    await flushPromises()
    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    expect(getUsage).toHaveBeenLastCalledWith(9108, undefined, true)
    expect(wrapper.emitted('reauth-status')).toEqual([
      [{ accountId: 9108, needsReauth: true }],
      [{ accountId: 9108, needsReauth: false }]
    ])
    wrapper.unmount()
  })

  it.each(['antigravity', 'gemini', 'grok'] as const)('does not actively probe unsupported remote %s usage', async platform => {
    const wrapper = mount(AccountUsageCell, {
      props: { account: makeAccount({ id: 9105, platform, type: 'oauth' }), readOnly: true },
      global: { stubs: { UsageProgressBar: true, AccountQuotaInfo: true, GrokQuotaProbeCell: true } }
    })
    await flushPromises()
    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(getUsage).not.toHaveBeenCalled()
    expect(wrapper.findComponent({ name: 'GrokQuotaProbeCell' }).exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders eligible Ollama Cloud state inside the unified usage cell', () => {
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 9001,
          platform: 'openai',
          type: 'apikey',
          ollama_cloud_usage: {
            account_id: 9001,
            eligible: true,
            configured: true,
            auto_refresh_enabled: true,
            encryption_key_configured: true,
            snapshot: {
              status: 'ok',
              last_attempt_at: '2026-07-23T00:00:00Z',
              next_refresh_at: '2026-07-23T01:00:00Z',
              data: {
                five_hour: { used_percent: 12 },
                seven_day: { used_percent: 34 }
              }
            }
          }
        })
      },
      global: {
        stubs: {
          OllamaCloudUsageCell: {
            props: ['account'],
            template: '<div data-test="embedded-ollama">{{ account.ollama_cloud_usage.snapshot.data.five_hour.used_percent }}</div>'
          },
          UsageProgressBar: true,
          AccountQuotaInfo: true
        }
      }
    })

    expect(wrapper.get('[data-test="embedded-ollama"]').text()).toBe('12')
    expect(getUsage).not.toHaveBeenCalled()
  })

  it('Antigravity 图片用量会聚合新旧 image 模型', async () => {
    getUsage.mockResolvedValue({
      antigravity_quota: {
        'gemini-2.5-flash-image': {
          utilization: 45,
          reset_time: '2026-03-01T11:00:00Z'
        },
        'gemini-3.1-flash-image': {
          utilization: 20,
          reset_time: '2026-03-01T10:00:00Z'
        },
        'gemini-3-pro-image': {
          utilization: 70,
          reset_time: '2026-03-01T09:00:00Z'
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 1001,
          platform: 'antigravity',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'color'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}</div>'
          },
          AccountQuotaInfo: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.usageWindow.gemini3Image|70|2026-03-01T09:00:00Z')
  })

  it('Antigravity 会显示 AI Credits 余额信息', async () => {
    getUsage.mockResolvedValue({
      ai_credits: [
        {
          credit_type: 'GOOGLE_ONE_AI',
          amount: 25,
          minimum_balance: 5
        }
      ]
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 1002,
          platform: 'antigravity',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          AccountQuotaInfo: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.aiCreditsBalance')
    expect(wrapper.text()).toContain('25')
  })


  it('OpenAI OAuth 快照已过期时首屏会重新请求 usage', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 15,
        resets_at: '2026-03-08T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 3,
          tokens: 300,
          cost: 0.03,
          standard_cost: 0.03,
          user_cost: 0.03
        }
      },
      seven_day: {
        utilization: 77,
        resets_at: '2026-03-13T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 3,
          tokens: 300,
          cost: 0.03,
          standard_cost: 0.03,
          user_cost: 0.03
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2000,
          platform: 'openai',
          type: 'oauth',
          extra: {
            codex_usage_updated_at: '2026-03-07T00:00:00Z',
            codex_5h_used_percent: 12,
            codex_5h_reset_at: '2026-03-08T12:00:00Z',
            codex_7d_used_percent: 34,
            codex_7d_reset_at: '2026-03-13T12:00:00Z'
          }
        })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'windowStats', 'color'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>'
          },
          AccountQuotaInfo: true
        }
      }
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2000)
    expect(wrapper.text()).toContain('5h|15|300')
    expect(wrapper.text()).toContain('7d|77|300')
  })

  it('OpenAI OAuth 有 codex 快照时仍然使用 /usage API 数据渲染', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 18,
        resets_at: '2099-03-07T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09
        }
      },
      seven_day: {
        utilization: 36,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2001,
          platform: 'openai',
          type: 'oauth',
          extra: {
            codex_usage_updated_at: '2099-03-07T10:00:00Z',
            codex_5h_used_percent: 12,
            codex_5h_reset_at: '2099-03-07T12:00:00Z',
            codex_7d_used_percent: 34,
            codex_7d_reset_at: '2099-03-13T12:00:00Z'
          }
        })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'windowStats', 'color'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>'
          },
          AccountQuotaInfo: true
        }
      }
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(2001)
    // 单一数据源：始终使用 /usage API 返回值，忽略 codex 快照
    expect(wrapper.text()).toContain('5h|18|900')
    expect(wrapper.text()).toContain('7d|36|900')
  })

  it('OpenAI OAuth 有现成快照时，手动刷新信号会触发 usage 重拉', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 18,
        resets_at: '2099-03-07T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09
        }
      },
      seven_day: {
        utilization: 36,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3600,
        window_stats: {
          requests: 9,
          tokens: 900,
          cost: 0.09,
          standard_cost: 0.09,
          user_cost: 0.09
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2010,
          platform: 'openai',
          type: 'oauth',
          extra: {
            codex_usage_updated_at: '2099-03-07T10:00:00Z',
            codex_5h_used_percent: 12,
            codex_5h_reset_at: '2099-03-07T12:00:00Z',
            codex_7d_used_percent: 34,
            codex_7d_reset_at: '2099-03-13T12:00:00Z'
          },
          rate_limit_reset_at: null
        }),
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'windowStats', 'color'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>'
          },
          AccountQuotaInfo: true
        }
      }
    })

    await flushPromises()
    // mount 时已经拉取一次
    expect(getUsage).toHaveBeenCalledTimes(1)

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    // 手动刷新再拉一次
    expect(getUsage).toHaveBeenCalledTimes(2)
    expect(getUsage).toHaveBeenNthCalledWith(2, 2010, undefined, true)
    // 单一数据源：始终使用 /usage API 值
    expect(wrapper.text()).toContain('5h|18|900')
  })

  it('OpenAI OAuth 在无 codex 快照时会回退显示 usage 接口，并隐藏利用率为 0 的 5h', async () => {
	getUsage.mockResolvedValue({
	  five_hour: {
	    utilization: 0,
	    resets_at: null,
	    remaining_seconds: 0,
	    window_stats: {
	      requests: 2,
	      tokens: 27700,
	      cost: 0.06,
	      standard_cost: 0.06,
	      user_cost: 0.06
	    }
	  },
	  seven_day: {
	    utilization: 0,
	    resets_at: null,
	    remaining_seconds: 0,
	    window_stats: {
	      requests: 2,
	      tokens: 27700,
	      cost: 0.06,
	      standard_cost: 0.06,
	      user_cost: 0.06
	    }
	  }
	})

		const wrapper = mount(AccountUsageCell, {
		  props: {
		    account: makeAccount({
		      id: 2002,
		      platform: 'openai',
		      type: 'oauth',
		      extra: {}
		    })
		  },
	  global: {
	    stubs: {
	      UsageProgressBar: {
	        props: ['label', 'utilization', 'resetsAt', 'windowStats', 'color'],
	        template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>'
	      },
	      AccountQuotaInfo: true
	    }
	  }
	})

	await flushPromises()

	expect(getUsage).toHaveBeenCalledWith(2002)
    expect(wrapper.text()).not.toContain('5h|0|27700')
	expect(wrapper.text()).toContain('7d|0|27700')
  })

  it('OpenAI OAuth 在行数据刷新后会重拉 usage，并在 5h 从 0% 恢复为正数时重新显示', async () => {
	getUsage
	  .mockResolvedValueOnce({
	    five_hour: {
	      utilization: 0,
	      resets_at: null,
	      remaining_seconds: 0,
	      window_stats: {
	        requests: 1,
	        tokens: 100,
	        cost: 0.01,
	        standard_cost: 0.01,
	        user_cost: 0.01
	      }
	    },
	    seven_day: null
	  })
	  .mockResolvedValueOnce({
	    five_hour: {
	      utilization: 25,
	      resets_at: null,
	      remaining_seconds: 0,
	      window_stats: {
	        requests: 2,
	        tokens: 200,
	        cost: 0.02,
	        standard_cost: 0.02,
	        user_cost: 0.02
	      }
	    },
	    seven_day: null
	  })

		const wrapper = mount(AccountUsageCell, {
		  props: {
		    account: makeAccount({
		      id: 2003,
		      platform: 'openai',
		      type: 'oauth',
		      updated_at: '2026-03-07T10:00:00Z',
		      extra: {}
		    })
		  },
	  global: {
	    stubs: {
	      UsageProgressBar: {
	        props: ['label', 'utilization', 'resetsAt', 'windowStats', 'color'],
	        template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>'
	      },
	      AccountQuotaInfo: true
	    }
	  }
	})

	await flushPromises()
    expect(wrapper.text()).not.toContain('5h|0|100')
	expect(getUsage).toHaveBeenCalledTimes(1)

	await wrapper.setProps({
	  account: {
	    id: 2003,
	    platform: 'openai',
	    type: 'oauth',
	    updated_at: '2026-03-07T10:01:00Z',
	    extra: {}
	  }
	})

	await flushPromises()
	expect(getUsage).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('5h|25|200')
  })

  it('OpenAI 重置响应更新账号行后会重新拉取 usage', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 0,
        resets_at: null,
        remaining_seconds: 0
      },
      seven_day: null
    })
    const account = makeAccount({
      id: 2004,
      platform: 'openai',
      type: 'oauth',
      updated_at: '2026-03-07T10:00:00Z',
      extra: {}
    })
    const wrapper = mount(AccountUsageCell, {
      props: { account },
      global: {
        stubs: {
          UsageProgressBar: true,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: {
            props: ['account'],
            emits: ['account-updated'],
            template: '<button data-test="quota-reset-result" @click="$emit(\'account-updated\', { ...account, updated_at: \'2026-03-07T10:01:00Z\' })" />'
          }
        }
      }
    })

    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="quota-reset-result"]').trigger('click')
    const updatedAccount = wrapper.emitted<Account[]>('account-updated')?.[0]?.[0]
    expect(updatedAccount?.updated_at).toBe('2026-03-07T10:01:00Z')

    await wrapper.setProps({ account: updatedAccount as Account })
    await flushPromises()

    expect(getUsage).toHaveBeenCalledTimes(2)
  })

  it('OpenAI OAuth 已限额时显示 /usage API 返回的限额数据', async () => {
	getUsage.mockResolvedValue({
	  five_hour: {
	    utilization: 100,
	    resets_at: '2026-03-07T12:00:00Z',
	    remaining_seconds: 3600,
	    window_stats: {
	      requests: 211,
	      tokens: 106540000,
	      cost: 38.13,
	      standard_cost: 38.13,
	      user_cost: 38.13
	    }
	  },
	  seven_day: {
	    utilization: 100,
	    resets_at: '2026-03-13T12:00:00Z',
	    remaining_seconds: 3600,
	    window_stats: {
	      requests: 211,
	      tokens: 106540000,
	      cost: 38.13,
	      standard_cost: 38.13,
	      user_cost: 38.13
	    }
	  }
	})

		const wrapper = mount(AccountUsageCell, {
		  props: {
		    account: makeAccount({
		      id: 2004,
		      platform: 'openai',
		      type: 'oauth',
		      rate_limit_reset_at: '2099-03-07T12:00:00Z',
		      extra: {
		        codex_5h_used_percent: 0,
		        codex_7d_used_percent: 0
		      }
		    })
		  },
	  global: {
	    stubs: {
	      UsageProgressBar: {
	        props: ['label', 'utilization', 'resetsAt', 'windowStats', 'color'],
	        template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ windowStats?.tokens }}</div>'
	      },
	      AccountQuotaInfo: true
	    }
	  }
	})

	await flushPromises()

  expect(getUsage).toHaveBeenCalledWith(2004)
  expect(wrapper.text()).toContain('5h|100|106540000')
  expect(wrapper.text()).toContain('7d|100|106540000')
  })

  it('OpenAI OAuth 保留原版用量布局并以两位小数显示冻结周额度', async () => {
    getUsage.mockResolvedValue({
      five_hour: null,
      seven_day: {
        utilization: 6,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3600,
        weekly_estimate_usd: 260 / 0.09,
        window_stats: {
          requests: 1500,
          tokens: 1_000_000,
          cost: 178.6,
          standard_cost: 178.6,
          user_cost: 42.37
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2101,
          platform: 'openai',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()

    const originalUsage = wrapper.get('[data-test="openai-oauth-original-usage"]')
    expect(originalUsage.classes()).toContain('space-y-1')
    const usageLayout = wrapper.get('[data-test="openai-oauth-usage-layout"]')
    expect(usageLayout.classes()).toContain('inline-flex')
    expect(usageLayout.classes()).toContain('max-w-full')
    expect(usageLayout.classes()).not.toContain('w-[32rem]')
    expect(wrapper.get('[data-window="7d"]').attributes('data-wide')).toBe('true')
    const estimate = wrapper.get('[data-test="oauth-weekly-estimate"]')
    expect(estimate.classes()).toContain('flex-row')
    expect(estimate.classes()).toContain('sm:flex-col')
    expect(estimate.classes()).toContain('items-center')
    expect(estimate.classes()).toContain('text-center')
    expect(estimate.classes()).toContain('w-full')
    expect(estimate.classes()).toContain('overflow-hidden')
    expect(estimate.classes()).toContain('whitespace-nowrap')
    expect(wrapper.get('[data-test="stub-footer"]').findComponent({ name: 'OpenAIQuotaResetCell' }).exists()).toBe(true)
    expect(wrapper.get('[data-test="stub-aside"]').find('[data-test="oauth-weekly-estimate"]').exists()).toBe(true)
    expect(estimate.text()).toContain('usage.weeklyEstimate')
    expect(estimate.text()).toContain('$2,888.89')
    expect(estimate.get('span:last-child').classes()).toContain('text-emerald-600')
    expect(estimate.get('span:last-child').classes()).toContain('dark:text-emerald-400')
  })

  it('OpenAI OAuth 只展示后端在新整数百分点冻结后的周额度', async () => {
    getUsage
      .mockResolvedValueOnce({
        seven_day: {
          utilization: 12,
          resets_at: '2099-03-13T12:00:00Z',
          remaining_seconds: 3600,
          weekly_estimate_usd: 3000,
          window_stats: {
            requests: 3000,
            tokens: 400_000_000,
            cost: 360,
            standard_cost: 360,
            user_cost: 80
          }
        }
      })
      .mockResolvedValueOnce({
        seven_day: {
          utilization: 13,
          resets_at: '2099-03-13T12:00:00Z',
          remaining_seconds: 3500,
          weekly_estimate_usd: 5000,
          window_stats: {
            requests: 3464,
            tokens: 426_000_000,
            cost: 413.92,
            standard_cost: 413.92,
            user_cost: 96.09
          }
        }
      })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 2104, platform: 'openai', type: 'oauth', extra: {} }),
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()
    expect(getUsage).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$3,000.00')

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    expect(getUsage).toHaveBeenCalledTimes(2)
    expect(getUsage).toHaveBeenNthCalledWith(2, 2104, undefined, true)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$5,000.00')
  })

  it('OpenAI OAuth 强制刷新失败时保留上一份完整周额度快照', async () => {
    getUsage
      .mockResolvedValueOnce({
        seven_day: {
          utilization: 13,
          resets_at: '2099-03-13T12:00:00Z',
          remaining_seconds: 3500,
          weekly_estimate_usd: 3184,
          window_stats: {
            requests: 3464,
            tokens: 426_000_000,
            cost: 413.92,
            standard_cost: 413.92,
            user_cost: 96.09
          }
        }
      })
      .mockRejectedValueOnce(new Error('forced quota probe failed'))

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 2106, platform: 'openai', type: 'oauth', extra: {} }),
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$3,184.00')

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    expect(getUsage).toHaveBeenNthCalledWith(2, 2106, undefined, true)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$3,184.00')
  })

  it('OpenAI OAuth 后端尚未返回冻结额度时显示统计中', async () => {
    getUsage.mockResolvedValue({
      seven_day: {
        utilization: 37,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3500,
        window_stats: {
          requests: 10,
          tokens: 1000,
          cost: 8,
          standard_cost: 8,
          user_cost: 1
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 2107, platform: 'openai', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('usage.weeklyEstimatePending')
  })

  it('OpenAI OAuth 达到 100% 时按账号已用精确显示两位小数', async () => {
    getUsage.mockResolvedValue({
      seven_day: {
        utilization: 100,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3500,
        weekly_estimate_usd: 9999,
        window_stats: {
          requests: 100,
          tokens: 1000,
          cost: 108.05,
          standard_cost: 108.05,
          user_cost: 20
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 2108, platform: 'openai', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()
    const estimate = wrapper.get('[data-test="oauth-weekly-estimate"]').text()
    expect(estimate).toContain('$108.05')
    expect(estimate).not.toContain('usage.weeklyEstimatePending')
  })

  it('OpenAI OAuth 100% 缺少估算字段时回退到账号已用且不显示统计中', async () => {
    getUsage.mockResolvedValue({
      seven_day: {
        utilization: 100,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 0,
        window_stats: {
          requests: 1000,
          tokens: 1_000_000,
          cost: 2892.37,
          standard_cost: 2892.37,
          user_cost: 600
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 2109, platform: 'openai', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()
    const estimate = wrapper.get('[data-test="oauth-weekly-estimate"]').text()
    expect(estimate).toContain('$2,892.37')
    expect(estimate).not.toContain('usage.weeklyEstimatePending')
  })

  it('OpenAI OAuth 并发刷新时忽略较晚返回的旧请求', async () => {
    let resolveInitial: ((value: any) => void) | undefined
    const initialRequest = new Promise((resolve) => {
      resolveInitial = resolve
    })
    getUsage
      .mockReturnValueOnce(initialRequest)
      .mockResolvedValueOnce({
        seven_day: {
          utilization: 13,
          resets_at: '2099-03-13T12:00:00Z',
          remaining_seconds: 3500,
          weekly_estimate_usd: 3184,
          window_stats: {
            requests: 3464,
            tokens: 426_000_000,
            cost: 413.92,
            standard_cost: 413.92,
            user_cost: 96.09
          }
        }
      })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 2105, platform: 'openai', type: 'oauth', extra: {} }),
        manualRefreshToken: 0
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$3,184')

    resolveInitial?.({
      seven_day: {
        utilization: 12,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3600,
        weekly_estimate_usd: 3000,
        window_stats: {
          requests: 3000,
          tokens: 400_000_000,
          cost: 360,
          standard_cost: 360,
          user_cost: 80
        }
      }
    })
    await flushPromises()

    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$3,184')
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).not.toContain('$3,000')
  })

  it('OpenAI OAuth 缺少 5h 限额时智能隐藏 5h 并保留 7d 与周额度', async () => {
    getUsage.mockResolvedValue({
      seven_day: {
        utilization: 25,
        resets_at: '2099-03-13T12:00:00Z',
        remaining_seconds: 3600,
        weekly_estimate_usd: 100,
        window_stats: {
          requests: 50,
          tokens: 50_000,
          cost: 25,
          standard_cost: 25,
          user_cost: 12.5
        }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2103,
          platform: 'openai',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.find('[data-window="5h"]').exists()).toBe(false)
    expect(wrapper.get('[data-window="7d"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="oauth-weekly-estimate"]').text()).toContain('$100')
  })

  it('OpenAI OAuth 仅在最近 12 小时有调用和计费时高亮', async () => {
    getUsage.mockResolvedValue({
      seven_day: {
        utilization: 25,
        resets_at: null,
        remaining_seconds: 0,
        window_stats: {
          requests: 1500,
          tokens: 50_000,
          cost: 25,
          standard_cost: 25,
          user_cost: 12.5
        }
      }
    })

    const recentWrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2110,
          platform: 'openai',
          type: 'oauth',
          last_used_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })
    await flushPromises()
    expect(recentWrapper.get('[data-window="7d"]').attributes('data-highlight-billing')).toBe('true')

    getUsage.mockResolvedValue({
      seven_day: {
        utilization: 25,
        resets_at: null,
        remaining_seconds: 0,
        window_stats: {
          requests: 1500,
          tokens: 50_000,
          cost: 25,
          standard_cost: 25,
          user_cost: 12.5
        }
      }
    })
    const staleWrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2111,
          platform: 'openai',
          type: 'oauth',
          last_used_at: new Date(Date.now() - 13 * 60 * 60 * 1000).toISOString(),
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })
    await flushPromises()
    expect(staleWrapper.get('[data-window="7d"]').attributes('data-highlight-billing')).toBe('false')
  })

  it('OpenAI OAuth 的 5h/7d 用户扣费点击会发出对应的精确窗口', async () => {
    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 10,
        resets_at: null,
        remaining_seconds: 0,
        window_stats: {
          requests: 5,
          tokens: 500,
          cost: 5,
          standard_cost: 5,
          user_cost: 2
        }
      },
      seven_day: {
        utilization: 20,
        resets_at: null,
        remaining_seconds: 0,
        window_stats: {
          requests: 20,
          tokens: 2000,
          cost: 20,
          standard_cost: 20,
          user_cost: 8
        }
      }
    })

    const account = makeAccount({
      id: 2104,
      platform: 'openai',
      type: 'oauth',
      extra: {}
    })
    const wrapper = mount(AccountUsageCell, {
      props: { account },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          OpenAIQuotaResetCell: true
        }
      }
    })

    await flushPromises()
    await wrapper.get('[data-window="5h"]').trigger('click')
    await wrapper.get('[data-window="7d"]').trigger('click')

    const events = wrapper.emitted<{
      account: Account
      windowLabel: string
      startTime: string
      endTime: string
    }[]>('open-billing-details')
    expect(events).toHaveLength(2)
    expect(events?.[0]?.[0].account).toEqual(account)
    expect(events?.[0]?.[0].windowLabel).toBe('5h')
    expect(
      new Date(events?.[0]?.[0].endTime ?? 0).getTime() -
      new Date(events?.[0]?.[0].startTime ?? 0).getTime()
    ).toBe(5 * 60 * 60 * 1000)
    expect(events?.[1]?.[0].account).toEqual(account)
    expect(events?.[1]?.[0].windowLabel).toBe('7d')
    expect(
      new Date(events?.[1]?.[0].endTime ?? 0).getTime() -
      new Date(events?.[1]?.[0].startTime ?? 0).getTime()
    ).toBe(7 * 24 * 60 * 60 * 1000)
  })

  it('API Key 和 setup-token 账号都支持用户扣费钻取', async () => {
    const apiKeyWrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2105,
          platform: 'anthropic',
          type: 'apikey'
        }),
        todayStats: {
          requests: 1,
          tokens: 100,
          cost: 1,
          standard_cost: 1,
          user_cost: 1
        }
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true
        }
      }
    })

    await apiKeyWrapper.get('[data-test="today-user-billing"]').trigger('click')
    const apiKeyEvents = apiKeyWrapper.emitted('open-billing-details')
    expect(apiKeyEvents).toHaveLength(1)
    expect(apiKeyEvents?.[0]?.[0]).toMatchObject({ account: expect.objectContaining({ id: 2105 }), windowLabel: '1d' })

    getUsage.mockResolvedValue({
      five_hour: {
        utilization: 10,
        resets_at: null,
        remaining_seconds: 0,
        window_stats: {
          requests: 1,
          tokens: 100,
          cost: 1,
          standard_cost: 1,
          user_cost: 1
        }
      },
      seven_day: null
    })
    const setupTokenWrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 2106,
          platform: 'anthropic',
          type: 'setup-token',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: billingUsageProgressBarStub,
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    const setupTokenUsage = setupTokenWrapper.get('[data-window="5h"]')
    await setupTokenUsage.trigger('click')
    const setupTokenEvents = setupTokenWrapper.emitted('open-billing-details')
    expect(setupTokenEvents).toHaveLength(1)
    expect(setupTokenEvents?.[0]?.[0]).toMatchObject({ account: expect.objectContaining({ id: 2106 }), windowLabel: '5h' })
  })

  it('Key 账号统一显示中文请求、账号计费与人民币用户扣费', async () => {
		const wrapper = mount(AccountUsageCell, {
		  props: {
		    account: makeAccount({
		      id: 3001,
		      platform: 'anthropic',
		      type: 'apikey'
		    }),
		    todayStats: {
		      requests: 1_000_000,
		      tokens: 1_000_000_000,
		      cost: 12.345,
		      standard_cost: 12.345,
		      user_cost: 6.789
		    }
		  },
		  global: {
		    stubs: {
		      UsageProgressBar: true,
		      AccountQuotaInfo: true
		    }
		  }
		})

		await flushPromises()

      expect(wrapper.text()).toContain('1000000 usage.requestCountUnit')
      expect(wrapper.text()).toContain('1.0B')
      expect(wrapper.text()).toContain('usage.accountBilled $12.35')
      expect(wrapper.text()).toContain('usage.userBilled ¥6.79')
      expect(wrapper.text()).not.toContain('A $')
      expect(wrapper.text()).not.toContain('U $')

      const accountBadge = wrapper.get('[title="usage.accountBilled"]')
      const userBadge = wrapper.get('[data-test="today-user-billing"]')
      expect(accountBadge.classes()).toContain('text-gray-500')
      expect(userBadge.classes()).toContain('text-gray-500')
      await userBadge.trigger('click')
      expect(wrapper.emitted('open-billing-details')).toHaveLength(1)
  })

  it('非 OAuth 账号最近 12 小时有调用和计费时也使用红黄强调', async () => {
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 3002,
          platform: 'anthropic',
          type: 'apikey',
          last_used_at: new Date(Date.now() - 30 * 60 * 1000).toISOString()
        }),
        todayStats: {
          requests: 1500,
          tokens: 1_000_000,
          cost: 12.345,
          standard_cost: 12.345,
          user_cost: 6.789
        }
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          AccountQuotaInfo: true
        }
      }
    })

    await flushPromises()

    const billed = wrapper.get('[title="usage.accountBilled"]')
    const deducted = wrapper.get('[data-test="today-user-billing"]')
    expect(billed.classes()).toContain('text-red-600')
    expect(deducted.classes()).toContain('text-amber-600')
    expect(wrapper.find('[data-test="oauth-weekly-estimate"]').exists()).toBe(false)
  })

  it('Grok OAuth 会展示本地 user billed 用量并把耗尽配额显示为 0% 剩余', async () => {
    getUsage.mockResolvedValue({
      grok_local_usage: {
        requests: 4,
        tokens: 1200,
        cost: 0.12,
        standard_cost: 0.12,
        user_cost: 0.34
      },
      grok_request_quota: {
        limit: 10,
        remaining: -2,
        reset_at: '2026-07-09T16:00:00Z'
      },
      grok_quota_snapshot_state: 'observed'
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 3861,
          platform: 'grok',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'color'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(getUsage).toHaveBeenCalledWith(3861)
    expect(wrapper.text()).toContain('4 usage.requestCountUnit')
    expect(wrapper.text()).toContain('1.2K')
    expect(wrapper.text()).toContain('usage.accountBilled $0.12')
    expect(wrapper.text()).toContain('usage.userBilled ¥0.34')
    expect(wrapper.text()).toContain('admin.accounts.usageWindow.grokRequests|0|2026-07-09T16:00:00Z')

    expect(wrapper.get('[title="usage.accountBilled"]').exists()).toBe(true)
    const userBilling = wrapper.get('[data-test="grok-user-billing"]')
    await userBilling.trigger('click')
    expect(wrapper.emitted('open-billing-details')).toHaveLength(1)
  })

  it('Grok OAuth 配额条按剩余容量显示 100% 满格和 25% 低量', async () => {
    getUsage.mockResolvedValue({
      grok_request_quota: {
        limit: 100,
        remaining: 100,
        reset_at: '2026-07-09T16:00:00Z'
      },
      grok_token_quota: {
        limit: 1000,
        remaining: 250,
        reset_at: '2026-07-09T16:00:00Z'
      },
      grok_quota_snapshot_state: 'observed'
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 4073,
          platform: 'grok',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'color', 'remainingCapacity'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ remainingCapacity }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.usageWindow.grokRequests|100|true')
    expect(wrapper.text()).toContain('admin.accounts.usageWindow.grokTokens|25|true')
  })

  it('Grok OAuth uses the official weekly billing percentage when available', async () => {
    getUsage.mockResolvedValue({
      grok_billing: {
        period_type: 'weekly',
        usage_percent: 37,
        period_end: '2026-07-16T03:25:00Z',
        plan: 'SuperGrok'
      },
      grok_local_usage: {
        requests: 5,
        tokens: 2_200_000,
        cost: 4.42,
        standard_cost: 4.42,
        user_cost: 0.44
      },
      grok_request_quota: { limit: 100, remaining: 100 },
      grok_token_quota: { limit: 2_000_000, remaining: 2_000_000 }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4201, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'remainingCapacity'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}|{{ remainingCapacity }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('7d|37|2026-07-16T03:25:00Z')
    expect(wrapper.text()).not.toContain('admin.accounts.usageWindow.grokRequests|')
    expect(wrapper.text()).not.toContain('admin.accounts.usageWindow.grokTokens|')
    expect(wrapper.text()).not.toContain('2M|')
  })

  it.each([
    { tokens: 0, expected: 0, compact: '0' },
    { tokens: 500_000, expected: 50, compact: '500.0K' },
    { tokens: 1_000_000, expected: 100, compact: '1.0M' },
    { tokens: 1_100_000, expected: 100, compact: '1.1M' }
  ])('Grok Free derives its 1M quota from local tokens: $tokens -> $expected%', async ({ tokens, expected, compact }) => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      grok_billing: {
        period_type: 'weekly',
        usage_percent: null,
        plan: ''
      },
      grok_local_usage_24h: {
        requests: 5,
        tokens,
        cost: 0,
        standard_cost: 0,
        user_cost: 0
      },
      grok_request_quota: { limit: 100, remaining: 100 },
      grok_token_quota: { limit: 1_000_000, remaining: 1_000_000 }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4300 + expected, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain(`24h|${expected}`)
    expect(wrapper.findAll('span').filter((node) => node.text() === compact)).toHaveLength(1)
    expect(wrapper.findAll('.usage-bar')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('admin.accounts.usageWindow.grokRequests|')
    expect(wrapper.text()).not.toContain('admin.accounts.usageWindow.grokTokens|')
  })

  it('Grok Free uses rolling 24h usage instead of today-only usage', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      grok_billing: { period_type: 'weekly', usage_percent: null, plan: '' },
      grok_local_usage: {
        requests: 2,
        tokens: 250_000,
        cost: 0,
        standard_cost: 0
      },
      grok_local_usage_24h: {
        requests: 12,
        tokens: 750_000,
        cost: 0,
        standard_cost: 0
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4398, platform: 'grok', type: 'oauth', extra: {} }),
        todayStats: {
          requests: 2,
          tokens: 200_000,
          cost: 0,
          standard_cost: 0
        }
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'title'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ title }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('24h|75|admin.accounts.usageWindow.grokFreeQuota24hHint')
    expect(wrapper.text()).toContain('750.0K')
    expect(wrapper.text()).not.toContain('7d|')
    expect(wrapper.text()).not.toContain('200.0K')
    expect(wrapper.text()).not.toContain('250.0K')
  })

  it('Grok Free does not substitute today stats when rolling 24h usage is unavailable', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      grok_billing: { period_type: 'weekly', usage_percent: null, plan: '' },
      grok_local_usage: {
        requests: 1,
        tokens: 250_000,
        cost: 0,
        standard_cost: 0,
        user_cost: 0
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4399, platform: 'grok', type: 'oauth', extra: {} }),
        todayStats: {
          requests: 4,
          tokens: 1_000_000,
          cost: 0,
          standard_cost: 0,
          user_cost: 0
        }
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.findAll('.usage-bar')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('24h|')
    expect(wrapper.text()).not.toContain('1.0M')
    expect(wrapper.text()).not.toContain('250.0K')
  })

  it('Grok paid plans are not mistaken for Free when weekly usage is temporarily missing', async () => {
    getUsage.mockResolvedValue({
      grok_billing: {
        period_type: 'weekly',
        usage_percent: null,
        plan: 'SuperGrok Heavy'
      },
      grok_entitlement_status: 'free',
      grok_local_usage: {
        requests: 2,
        tokens: 2_000_000,
        cost: 1,
        standard_cost: 1
      },
      grok_token_quota: { limit: 1_000, remaining: 250 }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4401, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.usageWindow.grokTokens|25')
    expect(wrapper.text()).not.toContain('2M|')
  })

  it('Grok custom paid monthly limits override stale Free entitlement', async () => {
    getUsage.mockResolvedValue({
      grok_billing: {
        period_type: 'weekly',
        usage_percent: null,
        monthly_limit_cents: 25_000,
        plan: ''
      },
      grok_entitlement_status: 'free',
      grok_local_usage: {
        requests: 2,
        tokens: 2_000_000,
        cost: 1,
        standard_cost: 1
      },
      grok_token_quota: { limit: 1_000, remaining: 250 }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4402, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.usageWindow.grokTokens|25')
    expect(wrapper.text()).not.toContain('2M|')
  })

  it('Grok credential Free tier keeps the 1M fallback when billing is unavailable', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      subscription_tier: 'FREE',
      grok_local_usage_24h: {
        requests: 3,
        tokens: 1_000_000,
        cost: 0,
        standard_cost: 0
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4403, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('24h|100')
  })

  it('Grok paid manual probes keep the weekly/local summary when 24h usage is returned', async () => {
    getUsage.mockResolvedValue({
      grok_quota_snapshot_state: 'no_headers',
      error: 'stale error',
      error_code: 'quota_unknown'
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4501, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}|{{ resetsAt }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: {
            emits: ['probed'],
            template: `<button class="probe" @click="$emit('probed', {
              source: 'hybrid_probe',
              billing: { period_type: 'weekly', usage_percent: 42, period_end: '2026-07-17T00:00:00Z' },
              snapshot: {
                headers_observed: true,
                updated_at: '2026-07-13T00:00:00Z',
                entitlement_status: 'ACTIVE',
                requests: { limit: 100, remaining: 20 }
              },
              local_usage_24h: { requests: 3, tokens: 750000, cost: 0.75, standard_cost: 0.75, user_cost: 0.25 },
              local_usage_7d: { requests: 4, tokens: 1000000, cost: 1, standard_cost: 1, user_cost: 0.5 },
              local_usage_monthly: { requests: 7, tokens: 1500000, cost: 2, standard_cost: 2, user_cost: 1 },
              status_code: 200,
              headers_observed: true,
              reset_supported: false,
              fetched_at: 1
            })">probe</button>`
          }
        }
      }
    })

    await flushPromises()
    await wrapper.get('.probe').trigger('click')

    expect(wrapper.text()).toContain('7d|42|2026-07-17T00:00:00Z')
    expect(wrapper.text()).toContain('1.0M')
    expect(wrapper.text()).not.toContain('750.0K')
    expect(wrapper.text()).toContain('ACTIVE')
    expect(wrapper.text()).not.toContain('stale error')
  })

  it('Grok successful probes immediately clear stale forbidden state', async () => {
    getUsage.mockResolvedValue({
      is_forbidden: true,
      forbidden_reason: 'stale forbidden response',
      forbidden_type: 'validation',
      validation_url: 'https://example.com/verify',
      needs_verify: true,
      is_banned: true,
      grok_entitlement_status: 'forbidden',
      grok_quota_snapshot_state: 'no_headers',
      error: 'stale forbidden response',
      error_code: 'forbidden'
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4503, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()
    expect(wrapper.text()).toContain('forbidden')

    const setupState = wrapper.vm.$.setupState as {
      handleGrokProbed: (result: Record<string, unknown>) => void
      usageInfo: Record<string, unknown> | null
    }
    setupState.handleGrokProbed({
      source: 'active_probe',
      snapshot: {
        headers_observed: false,
        updated_at: '2026-07-18T00:00:00Z',
        status_code: 200
      },
      status_code: 200,
      headers_observed: false,
      reset_supported: false,
      fetched_at: 1
    })
    await wrapper.vm.$nextTick()

    expect(setupState.usageInfo).toMatchObject({
      is_forbidden: false,
      needs_verify: false,
      is_banned: false,
      grok_last_status_code: 200
    })
    expect(setupState.usageInfo?.forbidden_reason).toBeUndefined()
    expect(setupState.usageInfo?.forbidden_type).toBeUndefined()
    expect(setupState.usageInfo?.validation_url).toBeUndefined()
    expect(setupState.usageInfo?.grok_entitlement_status).toBeUndefined()
    expect(wrapper.text()).not.toContain('admin.accounts.forbidden')
  })

  it('Grok successful probes preserve the entitlement reported by the latest snapshot', async () => {
    getUsage.mockResolvedValue({
      is_forbidden: true,
      grok_entitlement_status: 'forbidden'
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4504, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    const setupState = wrapper.vm.$.setupState as {
      handleGrokProbed: (result: Record<string, unknown>) => void
      usageInfo: Record<string, unknown> | null
    }
    setupState.handleGrokProbed({
      source: 'active_probe',
      snapshot: {
        headers_observed: true,
        updated_at: '2026-07-18T00:00:00Z',
        entitlement_status: 'ACTIVE',
        status_code: 200
      },
      status_code: 200,
      headers_observed: true,
      reset_supported: false,
      fetched_at: 1
    })
    await wrapper.vm.$nextTick()

    expect(setupState.usageInfo?.grok_entitlement_status).toBe('ACTIVE')
    expect(wrapper.text()).toContain('ACTIVE')
    expect(wrapper.text()).not.toContain('admin.accounts.forbidden')
  })

  it('Grok billing-only success does not clear an active-probe forbidden state', async () => {
    getUsage.mockResolvedValue({
      is_forbidden: true,
      forbidden_type: 'forbidden',
      needs_verify: true,
      is_banned: true,
      grok_entitlement_status: 'forbidden'
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4505, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: true,
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    const setupState = wrapper.vm.$.setupState as {
      handleGrokProbed: (result: Record<string, unknown>) => void
      usageInfo: Record<string, unknown> | null
    }
    setupState.handleGrokProbed({
      source: 'billing_probe',
      billing: {
        period_type: 'weekly',
        usage_percent: 10,
        plan: 'SuperGrok'
      },
      status_code: 200,
      headers_observed: false,
      reset_supported: false,
      fetched_at: 1
    })
    await wrapper.vm.$nextTick()

    expect(setupState.usageInfo).toMatchObject({
      is_forbidden: true,
      forbidden_type: 'forbidden',
      needs_verify: true,
      is_banned: true,
      grok_entitlement_status: 'forbidden'
    })
    expect(wrapper.text()).toContain('forbidden')
  })

  it('Grok Free manual probes merge rolling 24h usage', async () => {
    getUsage.mockResolvedValue({
      grok_free_token_limit: 1_000_000,
      subscription_tier: 'FREE',
      grok_quota_snapshot_state: 'no_headers'
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({ id: 4502, platform: 'grok', type: 'oauth', extra: {} })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: {
            emits: ['probed'],
            template: `<button class="probe" @click="$emit('probed', {
              source: 'hybrid_probe',
              billing: { period_type: 'weekly', usage_percent: null, plan: '' },
              local_usage_24h: { requests: 12, tokens: 750000, cost: 0, standard_cost: 0 },
              headers_observed: false,
              reset_supported: false,
              fetched_at: 1
            })">probe</button>`
          }
        }
      }
    })

    await flushPromises()
    await wrapper.get('.probe').trigger('click')

    expect(wrapper.text()).toContain('24h|75')
    expect(wrapper.text()).toContain('750.0K')
    expect(wrapper.text()).not.toContain('7d|')
  })

  it('Key 账号在 today stats loading 时显示骨架屏', async () => {
		const wrapper = mount(AccountUsageCell, {
		  props: {
		    account: makeAccount({
		      id: 3002,
		      platform: 'anthropic',
		      type: 'apikey'
		    }),
		    todayStats: null,
		    todayStatsLoading: true
		  },
		  global: {
		    stubs: {
		      UsageProgressBar: true,
		      AccountQuotaInfo: true
		    }
		  }
		})

		await flushPromises()

		expect(wrapper.findAll('.animate-pulse').length).toBeGreaterThan(0)
  })

  it('Key 账号在无 today stats 且无配额时显示兜底短横线', async () => {
		const wrapper = mount(AccountUsageCell, {
		  props: {
		    account: makeAccount({
		      id: 3003,
		      platform: 'anthropic',
		      type: 'apikey',
		      quota_limit: 0,
		      quota_daily_limit: 0,
		      quota_weekly_limit: 0
		    }),
		    todayStats: null,
		    todayStatsLoading: false
		  },
		  global: {
		    stubs: {
		      UsageProgressBar: true,
		      AccountQuotaInfo: true
		    }
		  }
		})

		await flushPromises()

		expect(wrapper.text().trim()).toBe('-')
  })

  it('Vertex 账号会在 Gemini 用量窗口里展示 today stats 徽章', async () => {
		const wrapper = mount(AccountUsageCell, {
		  props: {
		    account: makeAccount({
		      id: 4001,
		      platform: 'gemini',
		      type: 'service_account',
          credentials: {
            tier_id: 'vertex',
            project_id: 'vertex-proj',
            client_email: 'svc@vertex-proj.iam.gserviceaccount.com',
            location: 'global'
          },
		      extra: {}
		    }),
		    todayStats: {
		      requests: 0,
		      tokens: 0,
		      cost: 0,
		      standard_cost: 0,
		      user_cost: 0
		    }
		  },
		  global: {
		    stubs: {
		      UsageProgressBar: true,
		      AccountQuotaInfo: true
		    }
		  }
		})

		await flushPromises()

      expect(wrapper.text()).toContain('0 usage.requestCountUnit')
      expect(wrapper.text()).toContain('0')
      expect(wrapper.text()).toContain('usage.accountBilled $0.00')
      expect(wrapper.text()).toContain('usage.userBilled ¥0.00')
  })

  it('Anthropic OAuth 会渲染 7d F (Fable) 进度条，且 7d S 逻辑保留', async () => {
    getUsage.mockResolvedValue({
      source: 'passive',
      five_hour: {
        utilization: 41,
        resets_at: '2026-07-03T10:00:00Z',
        remaining_seconds: 3600
      },
      seven_day: {
        utilization: 56,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000
      },
      seven_day_sonnet: {
        utilization: 30,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000
      },
      seven_day_fable: {
        utilization: 100,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 3001,
          platform: 'anthropic',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'color'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('5h|41')
    expect(wrapper.text()).toContain('7d|56')
    expect(wrapper.text()).toContain('7d S|30')
    expect(wrapper.text()).toContain('7d F|100')
  })

  it('Anthropic OAuth 无 Fable 数据时不渲染 7d F 进度条', async () => {
    getUsage.mockResolvedValue({
      source: 'passive',
      five_hour: {
        utilization: 41,
        resets_at: '2026-07-03T10:00:00Z',
        remaining_seconds: 3600
      },
      seven_day: {
        utilization: 56,
        resets_at: '2026-07-06T22:00:00Z',
        remaining_seconds: 300000
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeAccount({
          id: 3002,
          platform: 'anthropic',
          type: 'oauth',
          extra: {}
        })
      },
      global: {
        stubs: {
          UsageProgressBar: {
            props: ['label', 'utilization', 'resetsAt', 'color'],
            template: '<div class="usage-bar">{{ label }}|{{ utilization }}</div>'
          },
          AccountQuotaInfo: true,
          GrokQuotaProbeCell: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('5h|41')
    expect(wrapper.text()).toContain('7d|56')
    expect(wrapper.text()).not.toContain('7d S')
    expect(wrapper.text()).not.toContain('7d F')
  })
})
