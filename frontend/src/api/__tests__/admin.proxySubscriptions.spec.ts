import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put } }))

import { applySubscriptions, getSubscriptions, previewSubscriptions, refreshSubscriptions } from '@/api/admin/proxies'
import { refreshCodexTicket } from '@/api/admin/accounts'

describe('proxy subscription and Codex Ticket API contracts', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
  })

  it('keeps preview and apply on the same server snapshot', async () => {
    const overview = { agent_available: true, sources: [], nodes: [], preview_id: 'preview-1' }
    get.mockResolvedValueOnce({ data: overview })
    post.mockResolvedValueOnce({ data: overview }).mockResolvedValueOnce({ data: overview })
    put.mockResolvedValueOnce({ data: overview })
    const sources = [{ id: '', name: 'One', url: 'https://example.test/sub' }]

    await expect(getSubscriptions()).resolves.toEqual(overview)
    await expect(previewSubscriptions(sources)).resolves.toEqual(overview)
    await expect(applySubscriptions([{ ...sources[0], id: 'server-source-id' }], ['node-1'], 'preview-1')).resolves.toEqual(overview)
    await expect(refreshSubscriptions()).resolves.toEqual(overview)

    expect(get).toHaveBeenCalledWith('/admin/proxies/subscriptions')
    expect(post).toHaveBeenNthCalledWith(1, '/admin/proxies/subscriptions/preview', { sources }, { timeout: 180_000 })
    expect(put).toHaveBeenCalledWith('/admin/proxies/subscriptions', {
      sources: [{ ...sources[0], id: 'server-source-id' }],
      selected_node_ids: ['node-1'],
      preview_id: 'preview-1'
    }, { timeout: 180_000 })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/proxies/subscriptions/refresh', undefined, { timeout: 180_000 })
  })

  it('omits proxy_ids for legacy capture and scales the timeout for explicit selections', async () => {
    post.mockResolvedValue({ data: { statuses: [] } })
    const twentyProxyIds = Array.from({ length: 20 }, (_, index) => index + 1)
    const fortyProxyIds = Array.from({ length: 40 }, (_, index) => index + 1)

    await refreshCodexTicket(41, 'gpt-5.6-terra')
    await refreshCodexTicket(41, 'gpt-5.6-terra', twentyProxyIds)
    await refreshCodexTicket(41, 'gpt-5.6-terra', fortyProxyIds)

    expect(post).toHaveBeenNthCalledWith(1, '/admin/accounts/41/codex-ticket/refresh', { model: 'gpt-5.6-terra' }, { timeout: 180_000 })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/accounts/41/codex-ticket/refresh', {
      model: 'gpt-5.6-terra',
      proxy_ids: twentyProxyIds
    }, { timeout: 180_000 })
    expect(post).toHaveBeenNthCalledWith(3, '/admin/accounts/41/codex-ticket/refresh', {
      model: 'gpt-5.6-terra',
      proxy_ids: fortyProxyIds
    }, { timeout: 280_000 })
  })
})
