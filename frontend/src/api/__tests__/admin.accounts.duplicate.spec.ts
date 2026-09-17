import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { post }
}))

import { duplicate } from '@/api/admin/accounts'

describe('admin account duplicate API', () => {
  beforeEach(() => {
    sessionStorage.clear()
    post.mockReset()
    post.mockResolvedValue({ data: { id: 43, name: 'primary (Copy)' } })
    vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue('11111111-1111-4111-8111-111111111111')
  })

  it('sends a stable idempotency key with the duplicate request', async () => {
    const account = await duplicate(42)

    expect(post).toHaveBeenCalledWith('/admin/accounts/42/duplicate', undefined, {
      headers: {
        'Idempotency-Key': 'account-duplicate-42-11111111-1111-4111-8111-111111111111'
      }
    })
    expect(account).toEqual({ id: 43, name: 'primary (Copy)' })
  })

  it('reuses the operation key after an ambiguous failed request', async () => {
    post.mockRejectedValueOnce(new Error('network timeout'))
    await expect(duplicate(99)).rejects.toThrow('network timeout')

    post.mockResolvedValueOnce({ data: { id: 100, name: 'retry (Copy)' } })
    await duplicate(99)

    expect(post).toHaveBeenCalledTimes(2)
    const firstHeaders = post.mock.calls[0][2].headers
    const secondHeaders = post.mock.calls[1][2].headers
    expect(secondHeaders).toEqual(firstHeaders)
  })

  it('reuses the operation key after a page reload', async () => {
    post.mockRejectedValueOnce(new Error('network timeout'))
    await expect(duplicate(77)).rejects.toThrow('network timeout')
    const firstHeaders = post.mock.calls[0][2].headers

    vi.resetModules()
    post.mockResolvedValueOnce({ data: { id: 78, name: 'reload (Copy)' } })
    const { duplicate: duplicateAfterReload } = await import('@/api/admin/accounts')
    await duplicateAfterReload(77)

    expect(post).toHaveBeenCalledTimes(2)
    expect(post.mock.calls[1][2].headers).toEqual(firstHeaders)
    expect(sessionStorage.length).toBe(0)
  })

  it('uses a fresh operation key after each successful copy', async () => {
    vi.spyOn(globalThis.crypto, 'randomUUID')
      .mockReturnValueOnce('11111111-1111-4111-8111-111111111111')
      .mockReturnValueOnce('22222222-2222-4222-8222-222222222222')
    post
      .mockResolvedValueOnce({ data: { id: 43, name: 'primary (Copy)' } })
      .mockResolvedValueOnce({ data: { id: 44, name: 'primary (Copy)' } })

    await duplicate(42)
    await duplicate(42)

    expect(post).toHaveBeenCalledTimes(2)
    expect(post.mock.calls[0][2].headers).toEqual({
      'Idempotency-Key': 'account-duplicate-42-11111111-1111-4111-8111-111111111111'
    })
    expect(post.mock.calls[1][2].headers).toEqual({
      'Idempotency-Key': 'account-duplicate-42-22222222-2222-4222-8222-222222222222'
    })
    expect(sessionStorage.length).toBe(0)
  })
})
