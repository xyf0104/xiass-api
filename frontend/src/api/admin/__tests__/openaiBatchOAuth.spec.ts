import { beforeEach, describe, expect, it, vi } from 'vitest'
import { batchOAuthAPI } from '../openaiBatchOAuth'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))

describe('batch OAuth SMS confirmation', () => {
  const result = { task: { task_id: 'task-1' }, sms: null }

  beforeEach(() => {
    vi.resetAllMocks()
    get.mockResolvedValue({ data: result })
    post.mockResolvedValue({ data: result })
  })

  it.each(['acquire', 'change', 'cancel'] as const)('rejects %s without explicit confirmation before issuing a request', async action => {
    await expect(batchOAuthAPI.sms('task-1', action)).rejects.toThrow('请先在 XIASS 站内确认接码操作。')
    await expect(batchOAuthAPI.sms('task-1', action, false)).rejects.toThrow('请先在 XIASS 站内确认接码操作。')
    expect(post).not.toHaveBeenCalled()
    expect(get).not.toHaveBeenCalled()
  })

  it.each(['acquire', 'change', 'cancel'] as const)('forwards explicit confirmation for %s to the selected task', async action => {
    expect(await batchOAuthAPI.sms('task-1', action, true)).toEqual(result)
    expect(post).toHaveBeenCalledExactlyOnceWith(`/admin/openai/batch-oauth/tasks/task-1/sms/${action}`, { confirmed: true })
  })

  it('keeps checking an existing session as a GET without mutation confirmation', async () => {
    expect(await batchOAuthAPI.sms('task-1', 'check')).toEqual(result)
    expect(get).toHaveBeenCalledExactlyOnceWith('/admin/openai/batch-oauth/tasks/task-1/sms')
    expect(post).not.toHaveBeenCalled()
  })
})
