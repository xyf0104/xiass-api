import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { batchOAuthAPI, type BatchOAuthTask, type BatchOAuthConfig } from '@/api/admin/openaiBatchOAuth'
import { batchTaskWillAutoRestart, useBatchOpenAIOAuth } from '../useBatchOpenAIOAuth'
import { parseAccountCredentials } from '@/features/token-converter/accountCredentials'
import { isAdsPowerHelperAvailable } from '@/utils/adspowerHelper'

vi.mock('@/api/admin/openaiBatchOAuth', () => ({ batchOAuthAPI: { list: vi.fn(), create: vi.fn(), complete: vi.fn(), cancel: vi.fn(), restart: vi.fn(), remove: vi.fn(), sms: vi.fn(), launchAdsPower: vi.fn() } }))
vi.mock('@/utils/adspowerHelper', async importOriginal => ({
  ...(await importOriginal<typeof import('@/utils/adspowerHelper')>()),
  isAdsPowerHelperAvailable: vi.fn(),
}))
const settings: BatchOAuthConfig = { group_ids: [4, 9], proxy_id: 3, pool_id: 2, concurrency: 3, priority: 1, codex_fingerprint_mode: 'off' }
const credentials = parseAccountCredentials(Array.from({ length: 5 }, (_, i) => `person${i}@example.test----password-${i}----JBSWY3DPEHPK3PXP`).join('\n')).rows.map(row => ({
  account: row.account,
  login: { login_method: 'password' as const, password: row.password, totp_secret: row.twoFactor },
}))
function task(id: string, email = 'person0@example.test', status: BatchOAuthTask['status'] = 'running'): BatchOAuthTask {
  return { task_id: id, email, login_method: 'password', status, stage: 'login', restart_count: 0, requires_sms_confirmation: false, created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 600000).toISOString() }
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
  vi.mocked(isAdsPowerHelperAvailable).mockResolvedValue(true)
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
afterEach(() => { scope?.stop(); vi.restoreAllMocks(); vi.useRealTimers() })

describe('batch OAuth orchestration', () => {
  function adsTasks() {
    vi.mocked(batchOAuthAPI.create).mockImplementation(async input => {
      const result = { ...task(input.idempotency_key, input.email), browser_mode: 'adspower' as const, stage: 'external_browser' }
      server.push(result)
      return result
    })
  }

  it('starts selected AdsPower tasks automatically from the original start click and only once per attempt', async () => {
    adsTasks()
    const popups = Array.from({ length: 3 }, () => ({ opener: window, closed: false, location: { href: 'about:blank' }, close: vi.fn() }))
    const open = vi.spyOn(window, 'open')
    for (const popup of popups) open.mockReturnValueOnce(popup as unknown as Window)
    vi.mocked(batchOAuthAPI.launchAdsPower).mockImplementation(async id => ({ helper_url: `http://127.0.0.1:34987/launch?ticket=${id}`, expires_at: '' }))
    const c = await setup()
    c.start(credentials, { ...settings, browser_mode: 'adspower' })
    expect(open).toHaveBeenCalledTimes(3)
    await flushPromises()
    await c.refresh()
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledTimes(3)
    for (let index = 0; index < 3; index++) {
      expect(popups[index].opener).toBeNull()
      expect(popups[index].location.href).toBe(`http://127.0.0.1:34987/launch?ticket=${server[index].task_id}`)
    }
    await c.refresh()
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledTimes(3)
    expect(c.activeCount.value).toBe(3)
    expect(c.pendingCount.value).toBe(2)
  })

  it('reuses a released AdsPower window for the fourth queued account', async () => {
    adsTasks()
    const popups = Array.from({ length: 3 }, () => ({ opener: window, closed: false, location: { href: 'about:blank' }, close: vi.fn() }))
    const open = vi.spyOn(window, 'open')
    for (const popup of popups) open.mockReturnValueOnce(popup as unknown as Window)
    vi.mocked(batchOAuthAPI.launchAdsPower).mockImplementation(async id => ({ helper_url: `http://127.0.0.1:34987/launch?ticket=${id}`, expires_at: '' }))
    const c = await setup()
    c.start(credentials, { ...settings, browser_mode: 'adspower' })
    await flushPromises()
    await c.refresh()
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledTimes(3)
    server[0] = { ...server[0], status: 'completed', stage: 'completed', account_id: 100 }
    await c.refresh()
    await flushPromises()
    await c.refresh()
    expect(batchOAuthAPI.create).toHaveBeenCalledTimes(4)
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledTimes(4)
    expect(popups[0].location.href).toContain(`/launch?ticket=${server[3].task_id}`)
    expect(open).toHaveBeenCalledTimes(3)
  })

  it('delivers paired AdsPower tasks remotely and closes unused local placeholders', async () => {
    adsTasks()
    const popup = { opener: window, closed: false, location: { href: 'about:blank' }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    vi.mocked(batchOAuthAPI.launchAdsPower).mockResolvedValue({ helper_url: 'http://127.0.0.1:34987/launch?ticket=queued', expires_at: '', delivery: 'queued' })
    const c = await setup()
    c.start(credentials.slice(0, 1), { ...settings, browser_mode: 'adspower' })
    await flushPromises()
    await c.refresh()
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledOnce()
    expect(popup.close).toHaveBeenCalledOnce()
    expect(popup.location.href).toBe('about:blank')
  })

  it('stops a local AdsPower task and exposes the installer state when the helper is unavailable', async () => {
    adsTasks()
    const popup = { opener: window, closed: false, location: { href: 'about:blank' }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    vi.mocked(isAdsPowerHelperAvailable).mockResolvedValue(false)
    vi.mocked(batchOAuthAPI.launchAdsPower).mockResolvedValue({ helper_url: 'http://127.0.0.1:34987/launch?ticket=missing', expires_at: '' })
    const c = await setup()
    c.start(credentials.slice(0, 1), { ...settings, browser_mode: 'adspower' })
    await flushPromises()
    await c.refresh()
    expect(batchOAuthAPI.cancel).toHaveBeenCalledOnce()
    expect(c.adsPowerHelperMissing.value).toBe(true)
    expect(c.rows.value[0].error).toContain('未能连接本机 XIASS AdsPower 助手')
    expect(popup.close).toHaveBeenCalled()
  })

  it('reuses an issued launch response after a blocked popup instead of consuming another launch ticket', async () => {
    adsTasks()
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    vi.mocked(batchOAuthAPI.launchAdsPower).mockResolvedValue({ helper_url: 'http://127.0.0.1:34987/launch?ticket=original', expires_at: '' })
    const c = await setup()
    c.start(credentials.slice(0, 1), { ...settings, browser_mode: 'adspower' })
    await flushPromises()
    await c.refresh()
    const row = c.rows.value[0]
    expect(row.error).toContain('浏览器阻止了助手窗口')
    await c.refresh()
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledOnce()
    const popup = { opener: window, closed: false, location: { href: 'about:blank' }, close: vi.fn() }
    open.mockReturnValue(popup as unknown as Window)
    await c.launchAdsPower(row)
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledOnce()
    expect(popup.location.href).toBe('http://127.0.0.1:34987/launch?ticket=original')
    expect(row.error).toBe('')
  })

  it('does not automatically launch historical AdsPower tasks without this browser session login material', async () => {
    server = [{ ...task('historical'), stage: 'external_browser', browser_mode: 'adspower' }]
    const c = await setup()
    await c.refresh()
    expect(batchOAuthAPI.launchAdsPower).not.toHaveBeenCalled()
  })

  it('keeps the original retry click window available for the new AdsPower attempt', async () => {
    adsTasks()
    const open = vi.spyOn(window, 'open')
    const initial = { opener: window, closed: false, location: { href: 'about:blank' }, close: vi.fn() }
    const retried = { opener: window, closed: false, location: { href: 'about:blank' }, close: vi.fn() }
    open.mockReturnValueOnce(initial as unknown as Window).mockReturnValueOnce(retried as unknown as Window)
    vi.mocked(batchOAuthAPI.launchAdsPower).mockResolvedValue({ helper_url: 'http://127.0.0.1:34987/launch?ticket=new', expires_at: '' })
    const c = await setup()
    c.start(credentials.slice(0, 1), { ...settings, browser_mode: 'adspower' })
    await flushPromises()
    await c.refresh()
    server[0] = { ...server[0], status: 'failed', stage: 'failed', reason: 'proxy_unavailable' }
    await c.refresh()
    vi.mocked(batchOAuthAPI.restart).mockImplementationOnce(async () => {
      server[0] = { ...server[0], status: 'running', stage: 'external_browser', reason: undefined, restart_count: 1 }
      return { ...server[0] }
    })
    c.retry(c.rows.value[0])
    await flushPromises()
    await c.refresh()
    expect(initial.close).not.toHaveBeenCalled()
    expect(initial.location.href).toContain('/launch?ticket=new')
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledTimes(2)
  })
  it('never automatically retries an account explicitly restricted by OpenAI', () => {
    expect(batchTaskWillAutoRestart({
      ...task('restricted', 'restricted@example.test', 'blocked'),
      stage: 'totp',
      reason: 'account_blocked',
    })).toBe(false)
  })

  it('never automatically retries an email-code account', () => {
    expect(batchTaskWillAutoRestart({
      ...task('email-code', 'mail@example.test', 'failed'),
      login_method: 'email_code',
      stage: 'email_code_waiting',
      reason: 'email_code_unavailable',
    })).toBe(false)
  })

  it('retries a transient OpenAI route failure for an email-code account', () => {
    expect(batchTaskWillAutoRestart({
      ...task('email-code-route', 'mail@example.test', 'failed'),
      login_method: 'email_code',
      stage: 'sms_submitting',
      reason: 'openai_route_error',
    })).toBe(true)
  })

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
    expect(vi.mocked(batchOAuthAPI.sms).mock.calls).toContainEqual(['second', 'check'])
    expect(vi.mocked(batchOAuthAPI.sms).mock.calls).not.toContainEqual(['first', 'acquire'])
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(vi.mocked(batchOAuthAPI.sms).mock.calls).toContainEqual(['first', 'check'])
    expect(vi.mocked(batchOAuthAPI.sms).mock.calls).toContainEqual(['first', 'acquire'])
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
  it('clears an ambiguous operation error after authoritative progress and completes the task', async () => {
    const c = await setup()
    c.start(credentials.slice(0, 1), settings)
    await flushPromises()
    const row = c.rows.value[0]
    server[0] = { ...server[0], status: 'failed', stage: 'password', reason: 'page_interaction_failed' }
    await c.refresh()
    vi.mocked(batchOAuthAPI.restart).mockImplementationOnce(async () => {
      server[0] = { ...server[0], status: 'running', stage: 'opening', reason: undefined, restart_count: 1 }
      throw new Error('reply lost after the server accepted restart')
    })
    c.retry(row)
    await flushPromises()
    expect(row.error).toBe('操作未确认，请刷新状态后重试。')

    server[0] = { ...server[0], status: 'ready', stage: 'callback_received' }
    vi.mocked(batchOAuthAPI.complete).mockImplementationOnce(async () => {
      server[0] = { ...server[0], status: 'completed', stage: 'completed', account_id: 777 }
      return { ...server[0] }
    })
    await c.refresh()

    expect(batchOAuthAPI.complete).toHaveBeenCalledWith(row.task?.task_id)
    expect(row.error).toBe('')
    expect(row.task?.status).toBe('completed')
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
  it('does not restart a historical blocked task after its login material was cleared', async () => {
    server = [{ ...task('blocked', 'person@example.test', 'blocked'), reason: 'captcha_required' }]
    vi.mocked(batchOAuthAPI.restart).mockImplementation(async id => {
      server[0] = { ...server[0], task_id: id, restart_count: 1 }
      return server[0]
    })
    const c = await setup()
    expect(batchOAuthAPI.restart).not.toHaveBeenCalled()
    expect(c.rows.value).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(9999)
    expect(batchOAuthAPI.restart).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(batchOAuthAPI.restart).not.toHaveBeenCalled()
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
  it('requires fresh login material before retrying historical tasks', async () => {
    const server = Array.from({ length: 4 }, (_, index): BatchOAuthTask => ({
      ...task(`retry-${index}`),
      email: `retry-${index}@example.test`,
      status: 'failed',
      stage: 'opening',
      reason: 'proxy_unavailable',
      restart_count: 1,
    }))
    vi.mocked(batchOAuthAPI.list).mockResolvedValue({ items: server, max_concurrency: 3, max_restarts: 2 })
    vi.mocked(batchOAuthAPI.restart).mockImplementation(async id => {
      const current = server.find(item => item.task_id === id)!
      current.status = 'running'
      current.reason = undefined
      current.restart_count = 2
      return { ...current }
    })
    const c = await setup()
    for (const row of c.rows.value) c.retry(row)
    await flushPromises()
    expect(batchOAuthAPI.restart).not.toHaveBeenCalled()
    expect(c.pendingCount.value).toBe(0)
    await c.cancelAll()
    expect(c.pendingCount.value).toBe(0)
    c.retry(c.rows.value[3])
    expect(c.pendingCount.value).toBe(0)
    expect(c.rows.value[3].error).toContain('登录信息已从浏览器内存清除')
    server[0].status = 'completed'
    server[0].account_id = 100
    await c.refresh()
    expect(batchOAuthAPI.restart).not.toHaveBeenCalled()
    expect(c.pendingCount.value).toBe(0)
  })
  it('does not start any accounts until the initial server status is known', async () => {
    vi.mocked(batchOAuthAPI.list).mockRejectedValueOnce(new Error('offline'))
    const c = await setup()
    c.start(credentials, settings)
    expect(batchOAuthAPI.create).not.toHaveBeenCalled()
    expect(c.started.value).toBe(false)
  })
})
