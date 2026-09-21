import { describe, expect, it, vi } from 'vitest'
import { accountsAPI } from '@/api/admin/accounts'

const apiClient = vi.hoisted(() => ({ put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient }))

describe('admin account Codex ticket capture defaults API', () => {
  it('saves explicit proxy IDs and returns the server-normalized list', async () => {
    apiClient.put.mockResolvedValueOnce({ data: { proxy_ids: [7, 9] } })

    await expect(accountsAPI.setCodexTicketCaptureProxies(41, [7, 9])).resolves.toEqual([7, 9])
    expect(apiClient.put).toHaveBeenCalledWith(
      '/admin/accounts/41/codex-ticket/capture-proxies',
      { proxy_ids: [7, 9] }
    )
  })

  it('sends an empty list to restore legacy business defaults', async () => {
    apiClient.put.mockResolvedValueOnce({ data: { proxy_ids: [] } })

    await expect(accountsAPI.setCodexTicketCaptureProxies(41, [])).resolves.toEqual([])
    expect(apiClient.put).toHaveBeenCalledWith(
      '/admin/accounts/41/codex-ticket/capture-proxies',
      { proxy_ids: [] }
    )
  })
})
