import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SubscriptionProxyPanel from '../SubscriptionProxyPanel.vue'

const api = vi.hoisted(() => ({
  getSubscriptions: vi.fn(),
  previewSubscriptions: vi.fn(),
  applySubscriptions: vi.fn(),
  refreshSubscriptions: vi.fn()
}))
const store = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn(), showInfo: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { proxies: api } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => store }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const emptyOverview = { agent_available: true, sources: [], nodes: [] }
const global = {
  stubs: {
    Icon: true,
    ConfirmDialog: {
      props: ['show'],
      emits: ['confirm', 'cancel'],
      template: '<div v-if="show" data-test="confirm"><button data-test="confirm-button" @click="$emit(\'confirm\')">confirm</button></div>'
    }
  }
}

describe('SubscriptionProxyPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.getSubscriptions.mockResolvedValue(emptyOverview)
    api.refreshSubscriptions.mockResolvedValue(emptyOverview)
  })

  it('backfills server source IDs and applies the exact preview snapshot ID', async () => {
    api.previewSubscriptions.mockResolvedValue({
      agent_available: true,
      preview_id: 'preview-1',
      sources: [{ id: 'server-source-1', name: 'Subscription 1', source_kind: 'url', configured: false }],
      nodes: [{ id: 'node-1', source_id: 'server-source-1', source_name: 'Subscription 1', name: 'Node A', protocol: 'vless', selected: true }]
    })
    api.applySubscriptions.mockResolvedValue({
      agent_available: true,
      sources: [{ id: 'server-source-1', name: 'Subscription 1', source_kind: 'url', configured: true, url: 'https://example.test/…' }],
      nodes: [{ id: 'node-1', source_id: 'server-source-1', source_name: 'Subscription 1', name: 'Node A', protocol: 'vless', selected: true, proxy_id: 88 }]
    })
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    await wrapper.get('#subscription-url-batch').setValue('https://example.test/sub\nhttps://example.test/sub')
    await wrapper.findAll('button').find(button => button.text().includes('addUrls'))!.trigger('click')
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()

    expect(api.previewSubscriptions).toHaveBeenCalledWith([expect.objectContaining({ id: '', url: 'https://example.test/sub' })])
    expect(wrapper.get('input[type="url"]').element).toHaveProperty('value', 'https://example.test/sub')

    await wrapper.findAll('button').find(button => button.text().includes('applyCount'))!.trigger('click')
    await flushPromises()
    expect(api.applySubscriptions).toHaveBeenCalledWith(
      [expect.objectContaining({ id: 'server-source-1', url: 'https://example.test/sub' })],
      ['node-1'],
      'preview-1'
    )
  })

  it('invalidates apply after a source edit and preserves drafts during refresh', async () => {
    api.getSubscriptions.mockResolvedValue({
      agent_available: true,
      sources: [{ id: 'source-1', name: 'Saved', source_kind: 'url', configured: true, url: 'https://saved.test/…' }],
      nodes: []
    })
    api.previewSubscriptions.mockResolvedValue({
      agent_available: true,
      preview_id: 'preview-2',
      sources: [{ id: 'source-1', name: 'Draft', source_kind: 'url', configured: true, url: 'https://saved.test/…' }],
      nodes: []
    })
    api.refreshSubscriptions.mockResolvedValue({ agent_available: true, sources: [], nodes: [], updated_at: '2026-09-20T00:00:00Z' })
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    const nameInput = wrapper.findAll('input').find(input => input.attributes('placeholder')?.includes('defaultName'))!
    await nameInput.setValue('Draft')
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()
    await nameInput.setValue('Unsaved change')
    expect(wrapper.findAll('button').find(button => button.text().includes('applyCount'))!.attributes('disabled')).toBeDefined()

    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.refresh'))!.trigger('click')
    await flushPromises()
    expect((nameInput.element as HTMLInputElement).value).toBe('Unsaved change')
  })

  it('adds pasted URI, Base64, or Clash text as an input source', async () => {
    api.previewSubscriptions.mockResolvedValue({
      agent_available: true,
      preview_id: 'input-preview',
      sources: [{ id: 'input-source-1', name: 'Subscription 1', source_kind: 'input', configured: false }],
      nodes: []
    })
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    await wrapper.get('#subscription-content-paste').setValue('vless://node@example.test:443')
    await wrapper.findAll('button').find(button => button.text().includes('addContent'))!.trigger('click')
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()

    expect(api.previewSubscriptions).toHaveBeenCalledWith([
      expect.objectContaining({ id: '', input: 'vless://node@example.test:443', url: undefined })
    ])
  })

  it('uses the site confirmation dialog before applying an empty source list', async () => {
    api.previewSubscriptions.mockResolvedValue({ agent_available: true, preview_id: 'empty-preview', sources: [], nodes: [] })
    api.applySubscriptions.mockResolvedValue(emptyOverview)
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('applyCount'))!.trigger('click')
    expect(wrapper.find('[data-test="confirm"]').exists()).toBe(true)
    await wrapper.get('[data-test="confirm-button"]').trigger('click')
    await flushPromises()
    expect(api.applySubscriptions).toHaveBeenCalledWith([], [], 'empty-preview')
  })

  it('requires confirmation for source TLS opt-in and invalidates the preview when disabled', async () => {
    api.previewSubscriptions.mockImplementation(async (sources) => ({ ...emptyOverview, preview_id: 'tls-preview', sources }))
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()
    await wrapper.get('#subscription-url-batch').setValue('https://example.test/sub#label')
    await wrapper.findAll('button').find(button => button.text().includes('addUrls'))!.trigger('click')
    const checkbox = wrapper.get('[data-test="allow-insecure-tls"]')
    expect((checkbox.element as HTMLInputElement).checked).toBe(false)
    await checkbox.setValue(true)
    expect((checkbox.element as HTMLInputElement).checked).toBe(false)
    expect(api.previewSubscriptions).not.toHaveBeenCalled()
    await wrapper.get('[data-test="confirm-button"]').trigger('click')
    expect((checkbox.element as HTMLInputElement).checked).toBe(true)
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()
    expect(api.previewSubscriptions).toHaveBeenCalledWith([expect.objectContaining({ allow_insecure_tls: true })])
    await checkbox.setValue(false)
    expect(wrapper.findAll('button').find(button => button.text().includes('applyCount'))!.attributes('disabled')).toBeDefined()
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()
    expect(api.previewSubscriptions).toHaveBeenLastCalledWith([expect.objectContaining({ allow_insecure_tls: false })])
  })

  it.each([
    [{ reason: 'INVALID_SUBSCRIPTION_URL', message: 'Use an HTTPS subscription URL' }, 'invalidUrl'],
    [{ message: 'node 1: insecure TLS is not allowed; fix the node certificate' }, 'insecureTLSRejected']
  ])('localizes subscription validation errors without discarding drafts', async (error, key) => {
    api.previewSubscriptions.mockRejectedValue(error)
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()
    await wrapper.get('#subscription-url-batch').setValue('https://example.test/sub#label')
    await wrapper.findAll('button').find(button => button.text().includes('addUrls'))!.trigger('click')
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()
    expect(store.showError).toHaveBeenCalledWith(`admin.proxies.subscriptions.${key}`)
    expect((wrapper.get('input[type="url"]').element as HTMLInputElement).value).toBe('https://example.test/sub#label')
  })
})
