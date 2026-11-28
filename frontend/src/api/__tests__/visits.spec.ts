import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { recordPageVisit } from '../visits'

describe('recordPageVisit', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset().mockResolvedValue({ status: 204 })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('reports anonymous page navigation without query secrets', async () => {
    await recordPageVisit('/pricing?token=secret#section')
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/visits', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ path: '/pricing' }),
      headers: { 'Content-Type': 'application/json' },
      keepalive: true,
    }))
  })

  it('uses the current token to identify a signed-in visitor', async () => {
    localStorage.setItem('auth_token', 'verified-token')
    await recordPageVisit('/dashboard')
    expect(fetchMock.mock.calls[0][1].headers.Authorization).toBe('Bearer verified-token')
  })

  it('records an anonymous visit when the stored session has expired', async () => {
    localStorage.setItem('auth_token', 'expired-token')
    fetchMock.mockResolvedValueOnce({ status: 401 })
    await recordPageVisit('/home')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls[1][1].headers).toEqual({ 'Content-Type': 'application/json' })
    expect(localStorage.getItem('auth_token')).toBe('expired-token')
  })

  it('leaves navigation usable on network failure', async () => {
    fetchMock.mockRejectedValueOnce(new Error('offline'))
    await expect(recordPageVisit('/home')).resolves.toBeUndefined()
  })
})
