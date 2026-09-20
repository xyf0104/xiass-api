import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CodexTicketStatusModal from '../CodexTicketStatusModal.vue'
import type { Account } from '@/types'

const api = vi.hoisted(() => ({
  setCodexTicketEnabled: vi.fn(),
  refreshCodexTicket: vi.fn()
}))

vi.mock('@/api/admin/accounts', () => ({ accountsAPI: api }))
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

describe('CodexTicketStatusModal', () => {
  beforeEach(() => {
    api.setCodexTicketEnabled.mockReset().mockResolvedValue(true)
    api.refreshCodexTicket.mockReset().mockResolvedValue([{
      model: 'gpt-6-astra', ready: true, remaining_seconds: 3600, blocked: false, fallback: false
    }])
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

    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(api.setCodexTicketEnabled).toHaveBeenCalledWith(41, true)
    expect(wrapper.emitted('updated')?.at(-1)?.[0]).toMatchObject({ enabled: true })
    expect(refreshButton!.attributes('disabled')).toBeUndefined()

    await refreshButton!.trigger('click')
    await flushPromises()
    expect(api.refreshCodexTicket).toHaveBeenCalledWith(41, 'gpt-6-astra')
  })
})
