import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CodexTicketStatusModal from '../CodexTicketStatusModal.vue'
import type { Account } from '@/types'

const api = vi.hoisted(() => ({
  setCodexTicketEnabled: vi.fn(),
  refreshCodexTicket: vi.fn()
}))
const proxyAPI = vi.hoisted(() => ({
  getAll: vi.fn(),
  getSubscriptions: vi.fn()
}))

vi.mock('@/api/admin/accounts', () => ({ accountsAPI: api }))
vi.mock('@/api/admin/proxies', () => ({ proxiesAPI: proxyAPI }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const account = (): Account => ({
  id: 41,
  name: 'business-account',
  platform: 'openai',
  type: 'oauth',
  codex_ticket_enabled: false,
  codex_turn_tickets: [{
    model: 'gpt-6-astra', ready: false, remaining_seconds: 0, blocked: false, fallback: false
  }],
  proxy_id: null,
  concurrency: 1,
  priority: 1,
  status: 'active'
} as Account)

const proxies = [
  { id: 7, name: 'Subscription JP', protocol: 'socks5', host: '127.0.0.1', port: 17001, username: null, status: 'active', expires_at: null, fallback_mode: 'none', expiry_warn_days: 7, created_at: '', updated_at: '' },
  { id: 9, name: 'Manual US', protocol: 'http', host: '127.0.0.1', port: 17002, username: null, status: 'active', expires_at: null, fallback_mode: 'none', expiry_warn_days: 7, created_at: '', updated_at: '' }
]

describe('CodexTicketStatusModal', () => {
  beforeEach(() => {
    api.setCodexTicketEnabled.mockReset().mockResolvedValue(true)
    api.refreshCodexTicket.mockReset().mockResolvedValue([{
      model: 'gpt-6-astra', ready: true, remaining_seconds: 3600, blocked: false, fallback: false
    }])
    proxyAPI.getAll.mockReset().mockResolvedValue(proxies)
    proxyAPI.getSubscriptions.mockReset().mockResolvedValue({
      agent_available: true,
      sources: [{ id: 'source-jp', name: 'Japan', source_kind: 'url', configured: true }],
      nodes: [{ id: 'node-jp', source_id: 'source-jp', source_name: 'Japan', name: 'JP', protocol: 'vless', selected: true, proxy_id: 7 }]
    })
  })

  it('keeps capture off by default and requires an explicit account opt-in before refresh', async () => {
    const wrapper = mount(CodexTicketStatusModal, {
      props: { show: true, account: account() },
      global: {
        stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } }
      }
    })

    const refreshButton = wrapper.findAll('button.btn-secondary')
      .find(button => button.text().includes('codexTicket.refresh'))
    expect(refreshButton).toBeDefined()
    expect(refreshButton!.attributes('disabled')).toBeDefined()
    expect(api.refreshCodexTicket).not.toHaveBeenCalled()

    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(api.setCodexTicketEnabled).toHaveBeenCalledWith(41, true)
    expect(wrapper.emitted('updated')?.at(-1)?.[0]).toMatchObject({ enabled: true })
    expect(refreshButton!.attributes('disabled')).toBeUndefined()

    await refreshButton!.trigger('click')
    await flushPromises()
    expect(api.refreshCodexTicket).toHaveBeenCalledWith(41, 'gpt-6-astra')
  })

  it('passes only explicitly selected proxy IDs for a custom capture', async () => {
    const enabledAccount = { ...account(), codex_ticket_enabled: true }
    const wrapper = mount(CodexTicketStatusModal, {
      props: { show: true, account: enabledAccount },
      global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, Icon: true } }
    })
    await flushPromises()

    await wrapper.findAll('button[role="radio"]')[2].trigger('click')
    const proxyCheckboxes = wrapper.findAll('input[type="checkbox"]')
    await proxyCheckboxes[0].setValue(true)
    const refreshButton = wrapper.findAll('button.btn-secondary').find(button => button.text().includes('codexTicket.refresh'))!
    await refreshButton.trigger('click')
    await flushPromises()

    expect(api.refreshCodexTicket).toHaveBeenCalledWith(41, 'gpt-6-astra', [7])
    expect(wrapper.text()).toContain('Japan')
  })

  it('disables refresh for an empty explicit selection and ignores a late proxy response from another account', async () => {
    let resolveFirst: ((value: typeof proxies) => void) | undefined
    proxyAPI.getAll
      .mockImplementationOnce(() => new Promise<typeof proxies>(resolve => { resolveFirst = resolve }))
      .mockResolvedValueOnce([{ ...proxies[1], id: 12, name: 'Second Account Proxy' }])
    const wrapper = mount(CodexTicketStatusModal, {
      props: { show: true, account: { ...account(), codex_ticket_enabled: true } },
      global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, Icon: true } }
    })
    await wrapper.setProps({ account: { ...account(), id: 42, name: 'second', codex_ticket_enabled: true } })
    await flushPromises()
    resolveFirst?.(proxies)
    await flushPromises()

    await wrapper.findAll('button[role="radio"]')[2].trigger('click')
    expect(wrapper.text()).toContain('Second Account Proxy')
    expect(wrapper.text()).not.toContain('Subscription JP')
    const refreshButton = wrapper.findAll('button.btn-secondary').find(button => button.text().includes('codexTicket.refresh'))!
    expect(refreshButton.attributes('disabled')).toBeDefined()
  })
})
