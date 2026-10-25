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
    api.previewSubscriptions.mockImplementation(async (submitted) => ({
      agent_available: true,
      preview_id: 'preview-1',
      sources: [{ id: 'server-source-1', client_ref: submitted[0].client_ref, name: 'Subscription 1', source_kind: 'url', configured: false }],
      nodes: [{ id: 'node-1', source_id: 'server-source-1', source_name: 'Subscription 1', name: 'Node A', protocol: 'vless', selected: true }]
    }))
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

  it('coalesces a re-entered saved URL into the existing source after preview', async () => {
    api.getSubscriptions.mockResolvedValue({
      agent_available: true,
      sources: [{ id: 'saved-source', name: 'Saved', source_kind: 'url', configured: true, url: 'https://saved.test/…' }],
      nodes: []
    })
    api.previewSubscriptions.mockImplementation(async (submitted) => ({
      agent_available: true,
      preview_id: 'coalesced-preview',
      sources: [{ id: 'saved-source', client_ref: submitted.find(source => source.url)?.client_ref, name: 'Saved', source_kind: 'url', configured: true, url: 'https://saved.test/…' }],
      nodes: []
    }))
    api.applySubscriptions.mockResolvedValue(emptyOverview)
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    await wrapper.get('#subscription-url-batch').setValue('https://saved.test/subscription')
    await wrapper.findAll('button').find(button => button.text().includes('addUrls'))!.trigger('click')
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()

    expect(wrapper.findAll('input[type="url"]')).toHaveLength(1)
    expect((wrapper.get('input[type="url"]').element as HTMLInputElement).value).toBe('https://saved.test/subscription')
    await wrapper.findAll('button').find(button => button.text().includes('applyCount'))!.trigger('click')
    await wrapper.get('[data-test="confirm-button"]').trigger('click')
    await flushPromises()
    expect(api.applySubscriptions).toHaveBeenCalledWith(
      [expect.objectContaining({ id: 'saved-source', url: 'https://saved.test/subscription' })],
      [],
      'coalesced-preview'
    )
  })

  it('correlates multiple masked and new sources only by server ID or client reference', async () => {
    api.getSubscriptions.mockResolvedValue({
      agent_available: true,
      sources: [
        { id: 'source-a', name: 'A', source_kind: 'url', configured: true, url: 'https://a.test/…' },
        { id: 'source-b', name: 'B', source_kind: 'url', configured: true, url: 'https://b.test/…' }
      ],
      nodes: []
    })
    api.previewSubscriptions.mockImplementation(async (submitted) => {
      const added = submitted.find(source => source.url === 'https://c.test/sub')!
      const duplicateB = submitted.find(source => source.url === 'https://b.test/private')!
      const savedA = submitted.find(source => source.id === 'source-a')!
      return {
        agent_available: true,
        preview_id: 'multi-preview',
        sources: [
          { id: 'source-c', client_ref: added.client_ref, name: 'C', source_kind: 'url', configured: true },
          { id: 'source-b', client_ref: duplicateB.client_ref, name: 'B', source_kind: 'url', configured: true, url: 'https://b.test/…' },
          { id: 'source-a', client_ref: savedA.client_ref, name: 'A', source_kind: 'url', configured: true, url: 'https://a.test/…' }
        ],
        nodes: []
      }
    })
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    await wrapper.get('#subscription-url-batch').setValue('https://b.test/private\nhttps://c.test/sub')
    await wrapper.findAll('button').find(button => button.text().includes('addUrls'))!.trigger('click')
    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.preview'))!.trigger('click')
    await flushPromises()

    const values = wrapper.findAll('input[type="url"]').map(input => (input.element as HTMLInputElement).value)
    expect(values).toEqual(['https://c.test/sub', 'https://b.test/private', ''])
  })

  it('hydrates the saved refresh error without replacing visible nodes', async () => {
    const savedNodes = [{ id: 'saved-node', source_id: 'saved-source', source_name: 'Saved', name: 'Node A', protocol: 'vless', selected: true }]
    api.getSubscriptions
      .mockResolvedValueOnce({ agent_available: true, sources: [{ id: 'saved-source', name: 'Saved', source_kind: 'url', configured: true, url: 'https://saved.test/…' }], nodes: savedNodes })
      .mockResolvedValueOnce({ agent_available: true, sources: [{ id: 'saved-source', name: 'Saved', source_kind: 'url', configured: true, url: 'https://saved.test/…' }], nodes: savedNodes, last_error: 'Subscription refresh failed; saved nodes retained' })
    api.refreshSubscriptions.mockRejectedValue(new Error('subscription provider offline'))
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.refresh'))!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Subscription refresh failed; saved nodes retained')
    expect(wrapper.text()).toContain('Node A')
    expect(api.getSubscriptions).toHaveBeenCalledTimes(2)
  })

  it('opens a fresh preview when a saved selected node disappears', async () => {
    const savedSource = { id: 'saved-source', name: 'Saved', source_kind: 'url', configured: true, url: 'https://saved.test/…' }
    const savedNode = { id: 'old-node', source_id: 'saved-source', source_name: 'Saved', name: 'Old node', protocol: 'vless', selected: true, proxy_id: 41 }
    api.getSubscriptions
      .mockResolvedValueOnce({ agent_available: true, sources: [savedSource], nodes: [savedNode] })
      .mockResolvedValueOnce({ agent_available: true, sources: [savedSource], nodes: [savedNode], last_error: 'A selected node disappeared from its subscription; saved nodes retained' })
    api.refreshSubscriptions.mockRejectedValue({ reason: 'PROXY_NODE_MISSING', message: 'A selected subscription node is unavailable; preview and select again' })
    api.previewSubscriptions.mockImplementation(async (submitted) => ({
      agent_available: true,
      preview_id: 'replacement-preview',
      sources: [{ ...savedSource, client_ref: submitted[0].client_ref }],
      nodes: [
        { ...savedNode, missing: true },
        { id: 'new-node', source_id: 'saved-source', source_name: 'Saved', name: 'New node', protocol: 'vless', selected: false }
      ]
    }))
    api.applySubscriptions.mockResolvedValue({ agent_available: true, sources: [savedSource], nodes: [] })
    const wrapper = mount(SubscriptionProxyPanel, { global })
    await flushPromises()

    await wrapper.findAll('button').find(button => button.text().includes('subscriptions.refresh'))!.trigger('click')
    await flushPromises()

    expect(api.previewSubscriptions).toHaveBeenCalledWith([expect.objectContaining({ id: 'saved-source', url: '' })])
    expect(wrapper.text()).toContain('Old node')
    expect(wrapper.text()).toContain('New node')
    const nodeCheckboxes = wrapper.findAll('tbody input[type="checkbox"]')
    expect(nodeCheckboxes).toHaveLength(2)
    expect(nodeCheckboxes[0].attributes('disabled')).toBeDefined()
    expect(nodeCheckboxes[1].attributes('disabled')).toBeUndefined()
    expect((nodeCheckboxes[1].element as HTMLInputElement).checked).toBe(false)
    expect(api.applySubscriptions).not.toHaveBeenCalled()
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
