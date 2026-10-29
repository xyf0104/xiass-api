import { beforeEach, describe, expect, it, vi } from 'vitest'
import { addCardKeys, getProviderConfig, updateProviderConfig } from '../smsReceiver'

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put } }))

describe('SMS receiver provider API', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('reads only the Giema configured flag', async () => {
    get.mockResolvedValue({ data: { giema_api_key_configured: true, api_key: 'must-not-be-used' } })

    await expect(getProviderConfig()).resolves.toEqual({ giema_api_key_configured: true })
    expect(get).toHaveBeenCalledExactlyOnceWith('/admin/settings/sms-receiver/providers')
  })

  it('saves a Giema key without exposing it in the response contract', async () => {
    put.mockResolvedValue({ data: { giema_api_key_configured: true, api_key: 'must-not-be-used' } })

    await expect(updateProviderConfig('giema-secret')).resolves.toEqual({ giema_api_key_configured: true })
    expect(put).toHaveBeenCalledExactlyOnceWith('/admin/settings/sms-receiver/providers', { api_key: 'giema-secret' })
  })

  it('keeps the existing Pixlab payload backward compatible and forwards an explicit provider only when selected', async () => {
    post.mockResolvedValue({ data: { added_count: 1, queued_count: 1 } })

    await addCardKeys('pixlab-card')
    await addCardKeys('giema-card', 'giema')

    expect(post).toHaveBeenNthCalledWith(1, '/admin/settings/sms-receiver/card-keys', { card_keys: 'pixlab-card' })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/settings/sms-receiver/card-keys', { card_keys: 'giema-card', provider: 'giema' })
  })
})
