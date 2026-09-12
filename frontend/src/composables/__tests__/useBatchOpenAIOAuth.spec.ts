import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { batchOAuthAPI, type BatchOAuthTask, type BatchOAuthConfig } from '@/api/admin/openaiBatchOAuth'
import { useBatchOpenAIOAuth } from '../useBatchOpenAIOAuth'
import { parseAccountCredentials } from '@/features/token-converter/accountCredentials'

vi.mock('@/api/admin/openaiBatchOAuth', () => ({ batchOAuthAPI: { list: vi.fn(), create: vi.fn(), complete: vi.fn(), cancel: vi.fn(), restart: vi.fn(), sms: vi.fn() } }))
const settings: BatchOAuthConfig = { group_ids: [4, 9], proxy_id: 3, pool_id: 2, concurrency: 3, priority: 1, codex_fingerprint_mode: 'off' }
const credentials = parseAccountCredentials(Array.from({ length: 5 }, (_, i) => `person${i}@example.test----password-${i}----JBSWY3DPEHPK3PXP`).join('\n')).rows
function task(id: string, email = 'person0@example.test', status: BatchOAuthTask['status'] = 'running'): BatchOAuthTask {
  return { task_id: id, email, status, stage: 'login', restart_count: 0, requires_sms_confirmation: false, created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 600000).toISOString() }
}
let scope: EffectScope
let server: BatchOAuthTask[]
let created: ReturnType<typeof vi.fn>
async function setup() {
  scope = effectScope()
  const controller = scope.run(() => useBatchOpenAIOAuth(created))!
  await flushPromises()
  return controller
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  server = []
  created = vi.fn()
  vi.mocked(batchOAuthAPI.list).mockImplementation(async () => ({ items: server.map(t => ({ ...t })), max_concurrency: 3, max_restarts: 2 }))
  vi.mocked(batchOAuthAPI.create).mockImplementation(async input => {
    const result = task(input.idempotency_key, input.email)
    server.push(result)
    return result
  })
  vi.mocked(batchOAuthAPI.cancel).mockImplementation(async id => ({ ...server.find(t => t.task_id === id)!, status: 'canceled', stage: 'canceled' }))
  vi.mocked(batchOAuthAPI.restart).mockImplementation(async id => {
    const index = server.findIndex(t => t.task_id === id)
    const result = { ...server[index], status: 'running' as const, stage: 'login', reason: undefined, restart_count: server[index].restart_count + 1 }
    if (index >= 0) server[index] = result
    return result
  })
})
afterEach(() => { scope?.stop(); vi.useRealTimers() })

describe('batch OAuth orchestration', () => {
  it('starts only three accounts and advances one slot after completion', async () => {
    const c = await setup()
    c.start(credentials, settings)
    await flushPromises()
    expect(batchOAuthAPI.create).toHaveBeenCalledTimes(3)
    expect(c.activeCount.value).toBe(3)
    expect(c.pendingCount.value).toBe(2)
    server[0] = { ...server[0], status: 'completed', account_id: 55 }
    await c.refresh()
    expect(batchOAuthAPI.create).toHaveBeenCalledTimes(4)
    expect(c.activeCount.value).toBe(3)
    expect(created).toHaveBeenCalledTimes(1)
    expect(c.hasSecret(c.rows.value[0])).toBe(false)
  })
  it('sends exact shared config and separate idempotency keys without exposing passwords in UI rows', async () => {
    const c = await setup()
    const config = { ...settings, group_ids: [4, 9] }
    c.start(credentials, config)
    config.group_ids.push(999)
    await flushPromises()
    for (const [input] of vi.mocked(batchOAuthAPI.create).mock.calls) expect(input).toMatchObject(settings)
    expect(new Set(vi.mocked(batchOAuthAPI.create).mock.calls.map(([input]) => input.idempotency_key)).size).toBe(3)
    expect(JSON.stringify(c.rows.value)).not.toContain('password-')
    expect(JSON.stringify(c.rows.value)).not.toContain('JBSWY3')
  })
  it('automatically acquires an independent number and routes each SMS check to its own task', async () => {
    server = [{ ...task('first'), stage: 'phone_required' }, { ...task('second', 'other@example.test'), stage: 'sms_waiting' }]
    vi.mocked(batchOAuthAPI.sms).mockImplementation(async (id, action) => {
      const current = server.find(t => t.task_id === id)!
      if (id === 'first' && action === 'check') return { task: current, sms: null }
      const result = { ...current, stage: 'sms_waiting' }
      return { task: result, sms: { status: 'waiting', number: id === 'first' ? '+12025550101' : '+12025550102', expires_at: '' } }
    })
    await setup()
    expect(batchOAuthAPI.sms).toHaveBeenNthCalledWith(1, 'first', 'check')
    expect(batchOAuthAPI.sms).toHaveBeenNthCalledWith(2, 'first', 'acquire')
    expect(batchOAuthAPI.sms).toHaveBeenNthCalledWith(3, 'second', 'check')
    expect(batchOAuthAPI.create).not.toHaveBeenCalled()
  })
  it('keeps ambiguous starts occupying slots and retries with the original idempotency key', async () => {
    vi.mocked(batchOAuthAPI.create).mockRejectedValueOnce(new Error('connection lost'))
    const c = await setup()
    c.start(credentials, settings)
    await flushPromises()
    const row = c.rows.value[0]
    expect(row.localStatus).toBe('uncertain')
    const key = vi.mocked(batchOAuthAPI.create).mock.calls[0][0].idempotency_key
    await c.retry(row)
    expect(vi.mocked(batchOAuthAPI.create).mock.calls[3][0].idempotency_key).toBe(key)
    expect(c.activeCount.value).toBe(3)
  })
  it('stops pending work without creating it', async () => {
    const c = await setup()
    c.start(credentials, settings)
    await flushPromises()
    await c.cancelAll()
    expect(batchOAuthAPI.create).toHaveBeenCalledTimes(3)
    expect(batchOAuthAPI.cancel).toHaveBeenCalledTimes(3)
    expect(c.hasWork.value).toBe(false)
  })
  it('cleans and restarts a blocked temporary OAuth task once without adding another row', async () => {
    server = [{ ...task('blocked', 'person@example.test', 'blocked'), reason: 'captcha_required' }]
    vi.mocked(batchOAuthAPI.restart).mockImplementation(async id => {
      server[0] = { ...server[0], task_id: id, restart_count: 1 }
      return server[0]
    })
    const c = await setup()
    expect(batchOAuthAPI.restart).toHaveBeenCalledExactlyOnceWith('blocked', undefined)
    expect(c.rows.value).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(10000)
    expect(batchOAuthAPI.restart).toHaveBeenCalledTimes(1)
  })
  it('stops polling and clears secrets on dispose, including a late request result', async () => {
    const c = await setup()
    c.start(credentials, settings)
    await flushPromises()
    const row = c.rows.value[0]
    expect(c.hasSecret(row)).toBe(true)
    scope.stop()
    expect(c.hasSecret(row)).toBe(false)
    const calls = vi.mocked(batchOAuthAPI.list).mock.calls.length
    await vi.advanceTimersByTimeAsync(60000)
    expect(batchOAuthAPI.list).toHaveBeenCalledTimes(calls)
  })
  it('does not start any accounts until the initial server status is known', async () => {
    vi.mocked(batchOAuthAPI.list).mockRejectedValueOnce(new Error('offline'))
    const c = await setup()
    c.start(credentials, settings)
    expect(batchOAuthAPI.create).not.toHaveBeenCalled()
    expect(c.started.value).toBe(false)
  })
})
