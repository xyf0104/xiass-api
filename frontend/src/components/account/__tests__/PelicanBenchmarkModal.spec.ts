import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import PelicanBenchmarkModal from '../PelicanBenchmarkModal.vue'
import { PelicanPartialStartError, type PelicanBenchmarkAccount, type PelicanBenchmarkRun } from '@/api/admin/pelicanBenchmark'
import zh from '@/i18n/locales/zh/admin/accounts'

const api = vi.hoisted(() => ({
  getPelicanAccounts: vi.fn(), getPelicanCurrent: vi.fn(), getPelicanHistory: vi.fn(),
  getPelicanResult: vi.fn(), getPelicanModels: vi.fn(), startPelicanTests: vi.fn(), restartPelicanTest: vi.fn(), stopPelicanTests: vi.fn(), stopAllPelicanTests: vi.fn()
}))
vi.mock('@/api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn() } }))
vi.mock('@/api/admin/pelicanBenchmark', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api/admin/pelicanBenchmark')>()), ...api,
  DEFAULT_PELICAN_MODEL: 'gpt-6-astra', PelicanPartialStartError: class extends Error {
  constructor(public items: PelicanBenchmarkRun[], public skipped: unknown[], public cause: unknown) { super('Partial start') }
} }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({
  locale: { value: 'zh-CN' },
  t: (key: string, params?: Record<string, unknown>) => {
    const parts = key.replace('admin.accounts.', '').split('.')
    let value: unknown = zh.accounts
    for (const part of parts) value = (value as Record<string, unknown>)?.[part]
    return typeof value === 'string' ? value.replace(/\{(\w+)\}/g, (_, name) => String(params?.[name] ?? name)) : key
  }
}) }))

function account(id = 1, overrides: Partial<PelicanBenchmarkAccount> = {}): PelicanBenchmarkAccount {
  return { id, name: `账号-${id}`, status: 'active', platform: 'openai', type: 'oauth', plan_type: 'pro', model_ids: [], can_test: true, ...overrides }
}
function run(id = 'run-1', overrides: Partial<PelicanBenchmarkRun> = {}): PelicanBenchmarkRun {
  return {
    id, batch_id: 'batch-1', account_id: 1, account_name: '账号-1', model: 'gpt-6-astra', upstream_model: 'gpt-6-astra', status: 'running',
    created_at: '2026-09-09T12:00:00Z', started_at: '2026-09-09T12:00:01Z', finished_at: null,
    duration_ms: 1234, html_bytes: 0, error_code: '', thumbnail_url: null, ...overrides
  }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const wrappers: VueWrapper[] = []
function render(show = true) {
  const wrapper = mount(PelicanBenchmarkModal, {
    props: { show },
    global: { stubs: {
      BaseDialog: { props: ['show', 'title'], emits: ['close'], template: '<div v-if="show"><h2>{{ title }}</h2><button data-testid="close" @click="$emit(\'close\')">close</button><slot /></div>' },
      Select: { props: ['modelValue', 'options', 'disabled', 'ariaLabel'], emits: ['update:modelValue'], template: '<select :value="modelValue" :disabled="disabled" :aria-label="ariaLabel" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :value="option.value" :disabled="option.disabled">{{ option.label }}</option></select>' },
      Icon: { props: ['name'], template: '<i :data-icon="name" />' }
    } }
  })
  wrappers.push(wrapper)
  return wrapper
}
async function currentTab(wrapper: VueWrapper) {
  await wrapper.get('[data-testid="tab-current"]').trigger('click')
  await flushPromises()
}
async function setDocumentHidden(hidden: boolean) {
  Object.defineProperty(document, 'hidden', { configurable: true, value: hidden })
  document.dispatchEvent(new Event('visibilitychange'))
  await flushPromises()
}

describe('PelicanBenchmarkModal', () => {
  it('retries in the same account row and renders upstream HTTP details as text', async () => {
    const failed = run('failed', { status: 'failed', error_code: 'upstream_http_503', error_message: 'Capacity exhausted <script>unsafe()</script>' })
    api.getPelicanCurrent.mockResolvedValue({ items: [failed] })
    api.getPelicanResult.mockResolvedValue(failed)
    const successor = run('successor', { status: 'queued', source_id: failed.id, created_at: '2026-09-09T12:01:00Z' })
    api.restartPelicanTest.mockImplementation(async () => {
      api.getPelicanCurrent.mockResolvedValue({ items: [failed, successor] })
      return { items: [successor] }
    })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.text()).toContain('HTTP 503')
    expect(wrapper.text()).toContain('Capacity exhausted')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.get('[data-testid="retry-failed"]').classes()).toContain('btn-primary')
    await wrapper.get('[data-testid="retry-failed"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid^="run-"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="run-successor"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="run-failed"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('排队中')
  })

  it('history never renders unsuccessful records even from a stale response', async () => {
    api.getPelicanHistory.mockResolvedValue({ items: [run('good', { status: 'succeeded' }), run('bad', { status: 'failed' })], total: 1, page: 1, pages: 1 })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenCalledWith(1, expect.any(AbortSignal), 'succeeded')
    expect(wrapper.find('[data-testid="run-bad"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="run-good"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="result-filter"]').exists()).toBe(false)
  })
  it.each(['queued', 'running', 'canceling', 'succeeded', 'canceled', 'failed', 'interrupted'] as const)('shows followup controls only for actionable %s state', async status => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('state', { status })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.find('[data-testid="continue-state"]').exists()).toBe(status === 'canceled')
    expect(wrapper.find('[data-testid="retry-state"]').exists()).toBe(status === 'failed' || status === 'interrupted')
  })

  it('unmounts generated previews on close and does not stop server jobs', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('preview', { status: 'succeeded', html_bytes: 120 })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(true)
    await wrapper.get('[data-testid="close"]').trigger('click')
    expect(wrapper.find('iframe').exists()).toBe(false)
    await wrapper.setProps({ show: false })
    const reads = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(reads)
    expect(api.stopPelicanTests).not.toHaveBeenCalled()
    expect(api.stopAllPelicanTests).not.toHaveBeenCalled()
  })

  beforeEach(() => {
    vi.useFakeTimers()
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    for (const mock of Object.values(api)) mock.mockReset()
    api.getPelicanAccounts.mockResolvedValue({ items: [account(), account(2, { plan_type: 'team' }), account(3, { plan_type: 'plus' })] })
    api.getPelicanCurrent.mockResolvedValue({ items: [] })
    api.getPelicanHistory.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    api.startPelicanTests.mockResolvedValue({ items: [run()] })
    api.stopPelicanTests.mockResolvedValue({ items: [run('run-1', { status: 'canceled' })] })
    api.getPelicanModels.mockResolvedValue(['gpt-6-astra', 'gpt-5.6-luna'])
    api.stopAllPelicanTests.mockResolvedValue({ affected: 2 })
    api.getPelicanResult.mockImplementation((id: string) => Promise.resolve({ ...run(id, { status: 'succeeded', html_bytes: 120 }), html: '<html><body><svg><circle r="10" /></svg><script>document.body.dataset.animated="yes"</script></body></html>' }))
  })
  afterEach(() => {
    for (const wrapper of wrappers.splice(0)) wrapper.unmount()
    vi.useRealTimers()
  })

  it('does not fetch or poll while closed; opens with native Chinese tabs and independent default models', async () => {
    const wrapper = render(false)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanAccounts).not.toHaveBeenCalled()
    expect(api.getPelicanCurrent).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(x => x.text())).toEqual(['新建测试', '当前测试', '测试记录'])
    expect(wrapper.text()).toContain('PRO')
    expect(wrapper.text()).toContain('TEAM')
    expect(wrapper.text()).toContain('PLUS')
    for (const select of wrapper.findAll('select')) expect((select.element as HTMLSelectElement).value).toBe('gpt-6-astra')
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(api.getPelicanResult).not.toHaveBeenCalled()
    expect(api.getPelicanHistory).not.toHaveBeenCalled()
  })

  it('starts only the requested account with its model and prevents duplicate clicks', async () => {
    const pending = deferred<{ items: PelicanBenchmarkRun[] }>()
    api.startPelicanTests.mockReturnValue(pending.promise)
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="model-2"]').setValue('gpt-5.6-luna')
    await wrapper.get('[data-testid="start-2"]').trigger('click')
    await wrapper.get('[data-testid="start-all"]').trigger('click')
    expect(api.startPelicanTests).toHaveBeenCalledTimes(1)
    expect(api.startPelicanTests).toHaveBeenCalledWith([{ account_id: 2, model: 'gpt-5.6-luna' }], expect.any(AbortSignal))
    pending.resolve({ items: [run('run-2', { account_id: 2 })] })
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-current"]').attributes('aria-selected')).toBe('true')
  })

  it('starts all eligible accounts, excludes busy or unavailable accounts, and never substitutes an unsupported default', async () => {
    api.getPelicanAccounts.mockResolvedValue({ items: [account(), account(2), account(3, { can_test: false }), account(4)] })
    api.getPelicanModels.mockImplementation(async (id: number) => id === 4 ? ['gpt-5.6-luna'] : ['gpt-6-astra'])
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-2', { account_id: 2 })] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="start-2"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="start-3"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="start-4"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="model-4"]').findAll('option').map(option => option.text())).toEqual(['gpt-5.6-luna'])
    expect(api.getPelicanModels.mock.calls.map(call => call[0])).toEqual([1, 2, 4])
    await wrapper.get('[data-testid="start-all"]').trigger('click')
    expect(api.startPelicanTests.mock.calls[0][0]).toEqual([{ account_id: 1, model: 'gpt-6-astra' }, { account_id: 4, model: 'gpt-5.6-luna' }])
  })

  it('stops a single run or all stoppable runs, without canceling completed results', async () => {
    const items = [run(), run('queued', { status: 'queued' }), run('done', { status: 'succeeded', html_bytes: 120 }), run('canceling', { status: 'canceling' })]
    api.getPelicanCurrent.mockResolvedValue({ items })
    api.stopPelicanTests.mockResolvedValue({ items })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get('[data-testid="stop-run-1"]').trigger('click')
    await flushPromises()
    expect(api.stopPelicanTests.mock.calls[0][0]).toEqual(['run-1'])
    await wrapper.get('[data-testid="stop-all"]').trigger('click')
    await flushPromises()
    expect(api.stopAllPelicanTests).toHaveBeenCalledWith(expect.any(AbortSignal))
  })

  it.each(['queued', 'running', 'canceling'] as const)('polls %s tasks every three seconds only while the current modal tab is visible', async status => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('done', { status: 'succeeded', html_bytes: 120 }), run('run-1', { status })] })
    const wrapper = render()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(1)
    await currentTab(wrapper)
    const calls = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(2999)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 1)
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 2)

    for (const tab of ['history', 'new']) {
      await wrapper.get(`[data-testid="tab-${tab}"]`).trigger('click')
      await flushPromises()
      const tabCalls = api.getPelicanCurrent.mock.calls.length
      await vi.advanceTimersByTimeAsync(60_000)
      await setDocumentHidden(true)
      await setDocumentHidden(false)
      await vi.advanceTimersByTimeAsync(60_000)
      expect(api.getPelicanCurrent).toHaveBeenCalledTimes(tabCalls)
      await currentTab(wrapper)
      expect(api.getPelicanCurrent).toHaveBeenCalledTimes(tabCalls + 1)
      await vi.advanceTimersByTimeAsync(3000)
      expect(api.getPelicanCurrent).toHaveBeenCalledTimes(tabCalls + 2)
    }

    await wrapper.setProps({ show: false })
    const closedCalls = api.getPelicanCurrent.mock.calls.length
    const previewCalls = api.getPelicanResult.mock.calls.length
    await setDocumentHidden(true)
    await setDocumentHidden(false)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(closedCalls)
    expect(api.getPelicanResult).toHaveBeenCalledTimes(previewCalls)
    expect(wrapper.find('iframe').exists()).toBe(false)
  })

  it.each(['succeeded', 'failed', 'canceled', 'interrupted'] as const)('does not poll %s tasks or refresh them on visibility changes', async status => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    const calls = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(60_000)
    await setDocumentHidden(true)
    await setDocumentHidden(false)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls)
    expect(api.getPelicanResult).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="run-run-1"]').isVisible()).toBe(true)
  })

  it.each(['succeeded', 'failed', 'canceled', 'interrupted'] as const)('stops polling when a running task becomes %s without removing it', async status => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run()] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    const calls = api.getPelicanCurrent.mock.calls.length
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status })] })
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 1)
    expect(wrapper.get('[data-testid="run-run-1"] .run-status').text()).toBe(zh.accounts.pelicanBenchmark.status[status])
    await setDocumentHidden(true)
    await setDocumentHidden(false)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 1)
    expect(wrapper.get('[data-testid="run-run-1"]').isVisible()).toBe(true)
    expect(api.getPelicanResult).not.toHaveBeenCalled()
  })

  it.each(['queued', 'running', 'canceling'] as const)('pauses %s polling in background tabs, resumes when visible, and cancels in-flight reads when closed', async status => {
    const items = [run('run-1', { status })]
    api.getPelicanCurrent.mockResolvedValue({ items })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await setDocumentHidden(true)
    const calls = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls)
    await setDocumentHidden(false)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 1)
    const pending = deferred<{ items: PelicanBenchmarkRun[] }>()
    api.getPelicanCurrent.mockReturnValue(pending.promise)
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 2)
    const signal = api.getPelicanCurrent.mock.calls[api.getPelicanCurrent.mock.calls.length - 1][0] as AbortSignal
    await wrapper.setProps({ show: false })
    expect(signal.aborted).toBe(true)
    pending.resolve({ items })
    await flushPromises()
    const closedCalls = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(closedCalls)
  })

  it('keeps queued and running tasks compact, automatically showing a thumbnail on success', async () => {
    api.startPelicanTests.mockResolvedValue({ items: [run('run-1', { status: 'queued', started_at: null, duration_ms: null })] })
    const wrapper = render()
    await flushPromises()
    api.getPelicanCurrent.mockResolvedValueOnce({ items: [run()] })
      .mockResolvedValue({ items: [run('run-1', { status: 'succeeded', finished_at: '2026-09-09T12:00:05Z', html_bytes: 120 })] })
    await wrapper.get('[data-testid="start-1"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-current"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-testid="run-run-1"]').isVisible()).toBe(true)
    expect(wrapper.get('[data-testid="run-run-1"] .run-status').text()).toBe('正在启动')
    expect(wrapper.find('[data-testid="details-run-1"]').exists()).toBe(false)
    expect(api.getPelicanResult).not.toHaveBeenCalled()
    expect(wrapper.find('iframe').exists()).toBe(false)

    const calls = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 1)
    expect(api.getPelicanCurrent).toHaveBeenLastCalledWith(expect.any(AbortSignal), ['batch-1'], false)
    expect(wrapper.get('[data-testid="run-run-1"]').isVisible()).toBe(true)
    expect(wrapper.get('[data-testid="run-run-1"] .run-status').text()).toBe('测试中')
    expect(wrapper.find('[data-testid="details-run-1"]').exists()).toBe(false)
    expect(api.getPelicanResult).not.toHaveBeenCalled()
    expect(wrapper.find('iframe').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 2)
    expect(api.getPelicanCurrent).toHaveBeenLastCalledWith(expect.any(AbortSignal), ['batch-1'], false)
    expect(wrapper.get('[data-testid="run-run-1"] .run-status').text()).toBe('已完成')
    expect(wrapper.find('[data-testid="stop-run-1"]').exists()).toBe(false)
    await flushPromises()
    expect(wrapper.find('[data-testid="details-run-1"]').exists()).toBe(false)
    const preview = wrapper.get<HTMLIFrameElement>('[data-testid="result-preview"]')
    const frameWindow = { postMessage: vi.fn() }
    Object.defineProperty(preview.element, 'contentWindow', { value: frameWindow, configurable: true })
    await preview.trigger('load')
    const accepted = new MessageEvent('message', {
      origin: 'null',
      data: { type: 'xiass-pelican-render-accepted' }
    })
    Object.defineProperty(accepted, 'source', { value: frameWindow })
    window.dispatchEvent(accepted)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 2)
    expect(wrapper.get('[data-testid="tab-current"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-testid="run-run-1"]').isVisible()).toBe(true)
    expect(api.getPelicanHistory).not.toHaveBeenCalled()
    expect(api.getPelicanResult).toHaveBeenCalledExactlyOnceWith('run-1', expect.any(AbortSignal))
    expect(wrapper.get('[data-testid="result-preview"]').isVisible()).toBe(true)
  })

  it('never overlaps polling requests and stops automatic retries after an error', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run()] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    const pending = deferred<{ items: PelicanBenchmarkRun[] }>()
    api.getPelicanCurrent.mockReturnValueOnce(pending.promise)
    const calls = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(30_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls + 1)
    pending.resolve({ items: [run()] })
    await flushPromises()
    api.getPelicanCurrent.mockRejectedValue(new Error('offline'))
    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.get('[role="alert"]').text()).toContain('加载测试数据失败')
    const failedCalls = api.getPelicanCurrent.mock.calls.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(failedCalls)
  })

  it('loads completed HTML automatically, retaining scripts inside an isolated preview document', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: 'succeeded', html_bytes: 120 }), run('failed', { status: 'failed' })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(api.getPelicanResult).toHaveBeenCalledExactlyOnceWith('run-1', expect.any(AbortSignal))
    expect(wrapper.find('iframe').exists()).toBe(true)
    expect(wrapper.find('[data-testid="details-failed"]').exists()).toBe(false)
    const frame = wrapper.get('iframe')
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('src')).toBe('/api/v1/pelican-preview')
    expect(frame.attributes('srcdoc')).toBeUndefined()
    const postMessage = vi.fn()
    Object.defineProperty(frame.element, 'contentWindow', { value: { postMessage }, configurable: true })
    await frame.trigger('load')
    expect(postMessage).toHaveBeenCalledWith({ type: 'xiass-pelican-render', html: expect.stringContaining('<script>document.body.dataset.animated="yes"</script>') }, '*')
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.setProps({ show: false })
    expect(wrapper.find('iframe').exists()).toBe(false)
  })

  it('ignores late result requests after switching tabs', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: 'succeeded', html_bytes: 120 })] })
    const pending = deferred<{ id: string; html: string }>()
    api.getPelicanResult.mockReturnValue(pending.promise)
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    const signal = api.getPelicanResult.mock.calls[0][1] as AbortSignal
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    pending.resolve({ id: 'run-1', html: '<html>late result</html>' })
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect(wrapper.find('iframe').exists()).toBe(false)
  })

  it('reloads result frames when history is explicitly refreshed without starting model calls', async () => {
    const item = run('saved', { status: 'succeeded', html_bytes: 100 })
    api.getPelicanHistory.mockResolvedValue({ items: [item], total: 1, page: 1, pages: 1 })
    api.getPelicanResult.mockResolvedValue({ ...item, html: '<html>saved scene</html>' })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await flushPromises()
    const firstFrame = wrapper.get('iframe').element
    expect(api.getPelicanResult).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('iframe').element).not.toBe(firstFrame)
    expect(api.getPelicanResult).toHaveBeenCalledTimes(2)
    expect(api.startPelicanTests).not.toHaveBeenCalled()
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
  })

  it('loads history on demand with independent pagination and no background polling', async () => {
    api.getPelicanHistory.mockImplementation((page: number) => Promise.resolve({ items: [run(`page-${page}`, { status: 'succeeded', html_bytes: 120 })], total: 21, page, page_size: 20, pages: 2 }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenCalledWith(1, expect.any(AbortSignal), 'succeeded')
    await wrapper.get('[data-testid="history-next"]').trigger('click')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenCalledWith(2, expect.any(AbortSignal), 'succeeded')
    expect(wrapper.get('[data-testid="history-next"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="run-page-2"]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanHistory).toHaveBeenCalledTimes(2)
    expect(api.getPelicanResult).toHaveBeenCalledTimes(2)
  })

  it('fails closed when the backend is missing instead of displaying simulated success', async () => {
    api.getPelicanAccounts.mockRejectedValue({ status: 404 })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('服务尚未接入')
    expect(wrapper.get('[data-testid="start-all"]').attributes('disabled')).toBeDefined()
    expect(api.startPelicanTests).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getPelicanAccounts).toHaveBeenCalledTimes(1)
  })

  it('preserves accepted tasks and stop controls after a partially failed batch', async () => {
    api.startPelicanTests.mockRejectedValue(new PelicanPartialStartError([run()], [], { status: 503 }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="start-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('部分测试已提交')
    expect(wrapper.get('[data-testid="run-run-1"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="stop-run-1"]').attributes('disabled')).toBeUndefined()
  })

  it('shows skipped outcomes without fabricating successful tasks', async () => {
    api.startPelicanTests.mockResolvedValue({ items: [], skipped: [{ account_id: 1, reason: 'model_not_supported' }] })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="start-1"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain('已跳过 1 个')
    expect(wrapper.find('[data-testid="run-run-1"]').exists()).toBe(false)
  })

  it('loads each account catalog once and keeps the selection only after refresh revalidates it', async () => {
    const wrapper = render()
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledTimes(3)
    await wrapper.get('[data-testid="model-1"]').trigger('focusin')
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledWith(1, expect.any(AbortSignal))
    await wrapper.get('[data-testid="model-1"]').setValue('gpt-5.6-luna')
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('[data-testid="model-1"]').element as HTMLSelectElement).value).toBe('gpt-5.6-luna')
    await wrapper.get('[data-testid="model-1"]').trigger('focusin')
    expect(api.getPelicanModels).toHaveBeenCalledTimes(6)
  })

  it.each(['row', 'selected', 'all'] as const)('starts mixed-platform accounts with their own catalog using %s controls', async mode => {
    const accounts = [account(), account(2, { platform: 'anthropic', plan_type: null }),
      account(3, { platform: 'anthropic', type: 'apikey', plan_type: null }),
      account(4, { platform: 'antigravity', plan_type: 'free' }),
      account(5, { platform: 'gemini', type: 'service_account', plan_type: null }),
      account(6, { platform: 'grok', plan_type: null }), account(7, { platform: 'deepseek', type: 'apikey', plan_type: null })]
    const catalogs: Record<number, string[]> = {
      1: ['gpt-5.6-luna', 'gpt-6-astra'], 2: ['claude-sonnet-4-5'], 3: ['claude-opus-4-5'],
      4: ['claude-sonnet-4-5', 'gemini-3.7-flash-high'], 5: ['gemini-2.5-pro'], 6: ['grok-4'], 7: ['deepseek-chat']
    }
    api.getPelicanAccounts.mockResolvedValue({ items: accounts })
    api.getPelicanModels.mockImplementation(async (id: number) => catalogs[id])
    api.startPelicanTests.mockResolvedValue({ items: [] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('[data-testid^="account-"]')).toHaveLength(accounts.length)
    for (const account of accounts) {
      const select = wrapper.get(`[data-testid="model-${account.id}"]`)
      expect(select.findAll('option').map(option => option.text())).toEqual(catalogs[account.id])
      expect((select.element as HTMLSelectElement).value).toBe(account.id === 1 ? 'gpt-6-astra' : catalogs[account.id][0])
    }
    await wrapper.get('[data-testid="model-4"]').setValue('gemini-3.7-flash-high')
    if (mode === 'row') await wrapper.get('[data-testid="start-4"]').trigger('click')
    else if (mode === 'selected') {
      await wrapper.get('[data-testid="select-2"]').setValue(true)
      await wrapper.get('[data-testid="select-6"]').setValue(true)
      await wrapper.get('[data-testid="start-selected"]').trigger('click')
    } else await wrapper.get('[data-testid="start-all"]').trigger('click')
    const ids = mode === 'row' ? [4] : mode === 'selected' ? [2, 6] : accounts.map(account => account.id)
    expect(api.startPelicanTests).toHaveBeenCalledExactlyOnceWith(ids.map(account_id => ({
      account_id, model: account_id === 1 ? 'gpt-6-astra' : account_id === 4 ? 'gemini-3.7-flash-high' : catalogs[account_id][0]
    })), expect.any(AbortSignal))
  })

  it('limits model reads to four, deduplicates focus events, and keeps ready rows usable while the queue drains', async () => {
    const pending = Array.from({ length: 12 }, () => deferred<string[]>())
    api.getPelicanAccounts.mockResolvedValue({ items: pending.map((_, index) => account(index + 1, { platform: 'antigravity' })) })
    api.getPelicanModels.mockImplementation((id: number) => pending[id - 1].promise)
    api.startPelicanTests.mockResolvedValue({ items: [] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('[data-testid^="account-"]')).toHaveLength(12)
    expect(api.getPelicanModels.mock.calls.map(call => call[0])).toEqual([1, 2, 3, 4])
    expect(wrapper.get('[data-testid="start-all"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="start-1"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="model-1"]').findAll('option')).toHaveLength(0)
    await wrapper.get('[data-testid="model-1"]').trigger('focusin')
    await wrapper.get('[data-testid="model-8"]').trigger('pointerdown')
    expect(api.getPelicanModels).toHaveBeenCalledTimes(4)
    pending[0].resolve(['gemini-3.7-flash-high'])
    await flushPromises()
    expect(api.getPelicanModels.mock.calls.map(call => call[0])).toEqual([1, 2, 3, 4, 5])
    expect(wrapper.get('[data-testid="start-1"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="start-all"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="select-all"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="start-1"]').trigger('click')
    await flushPromises()
    expect(api.startPelicanTests).toHaveBeenCalledWith([{ account_id: 1, model: 'gemini-3.7-flash-high' }], expect.any(AbortSignal))
    pending[1].reject(new Error('private catalog failure'))
    await flushPromises()
    expect(api.getPelicanModels.mock.calls.map(call => call[0])).toEqual([1, 2, 3, 4, 5, 6])
    expect(wrapper.get('[data-testid="start-2"]').attributes('disabled')).toBeDefined()
    for (const item of pending.slice(2)) item.resolve(['claude-sonnet-4-5'])
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledTimes(12)
    expect(wrapper.get('[data-testid="start-all"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('private catalog failure')
  })

  it.each(['empty', 'error'] as const)('clears old models when refresh returns %s and never starts with the previous choice', async outcome => {
    api.getPelicanAccounts.mockResolvedValue({ items: [account(1, { platform: 'antigravity' })] })
    api.getPelicanModels.mockResolvedValueOnce(['claude-sonnet-4-5', 'gemini-3.7-flash-high'])
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="model-1"]').setValue('gemini-3.7-flash-high')
    const pending = deferred<string[]>()
    api.getPelicanModels.mockReturnValueOnce(pending.promise)
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="model-1"]').findAll('option')).toHaveLength(0)
    expect(wrapper.get('[data-testid="start-1"]').attributes('disabled')).toBeDefined()
    if (outcome === 'empty') pending.resolve([])
    else pending.reject(new Error('private upstream response'))
    await flushPromises()
    const select = wrapper.get('[data-testid="model-1"]')
    expect(select.findAll('option')).toHaveLength(0)
    expect((select.element as HTMLSelectElement).value).toBe('')
    await wrapper.get('[data-testid="start-1"]').trigger('click')
    await wrapper.get('[data-testid="start-all"]').trigger('click')
    expect(api.startPelicanTests).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('gpt-6-astra')
    expect(wrapper.text()).not.toContain('private upstream response')
    api.getPelicanModels.mockResolvedValueOnce(['gemini-3.7-flash-low'])
    if (outcome === 'error') await select.trigger('focusin')
    else await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('[data-testid="model-1"]').element as HTMLSelectElement).value).toBe('gemini-3.7-flash-low')
    expect(wrapper.get('[data-testid="start-1"]').attributes('disabled')).toBeUndefined()
  })

  it('replaces a removed selection using only the new supported catalog', async () => {
    api.getPelicanAccounts.mockResolvedValue({ items: [account()] })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="model-1"]').setValue('gpt-5.6-luna')
    api.getPelicanModels.mockResolvedValueOnce(['custom-supported-model'])
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="model-1"]').findAll('option').map(option => option.text())).toEqual(['custom-supported-model'])
    await wrapper.get('[data-testid="start-1"]').trigger('click')
    expect(api.startPelicanTests).toHaveBeenCalledWith([{ account_id: 1, model: 'custom-supported-model' }], expect.any(AbortSignal))
  })

  it('starts only text candidates and disables a dedicated-model-only catalog without a default fallback', async () => {
    api.getPelicanAccounts.mockResolvedValue({ items: [
      account(1, { platform: 'antigravity' }), account(2, { platform: 'gemini' })
    ] })
    api.getPelicanModels.mockImplementation(async (id: number) => id === 1
      ? ['gemini-3-pro-image', 'gemini-3.7-flash-high', 'vendor/image-understanding']
      : ['text-embedding-3-large', 'gemini-2.5-flash-native-audio-preview-12-2025', 'gemini-2.5-flash-preview-tts'])
    api.startPelicanTests.mockResolvedValue({ items: [] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="model-1"]').findAll('option').map(option => option.text()))
      .toEqual(['gemini-3.7-flash-high', 'vendor/image-understanding'])
    expect((wrapper.get('[data-testid="model-1"]').element as HTMLSelectElement).value).toBe('gemini-3.7-flash-high')
    expect(wrapper.get('[data-testid="model-2"]').findAll('option')).toHaveLength(0)
    expect(wrapper.get('[data-testid="start-2"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="select-2"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="select-all"]').setValue(true)
    expect((wrapper.get('[data-testid="select-2"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('[data-testid="start-all"]').trigger('click')
    await flushPromises()
    expect(api.startPelicanTests).toHaveBeenCalledExactlyOnceWith([{ account_id: 1, model: 'gemini-3.7-flash-high' }], expect.any(AbortSignal))
    api.getPelicanModels.mockResolvedValue(['gpt-image-2', 'whisper-1'])
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="model-1"]').findAll('option')).toHaveLength(0)
    expect(wrapper.get('[data-testid="start-all"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="start-selected"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain('gpt-6-astra')
  })

  it('ignores a late model response from before a refresh, including when the same account ID changes platform', async () => {
    api.getPelicanAccounts.mockResolvedValueOnce({ items: [account()] })
      .mockResolvedValueOnce({ items: [account(1, { platform: 'grok', plan_type: null })] })
    const pending = deferred<string[]>()
    api.getPelicanModels.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(['grok-4'])
    const wrapper = render()
    await flushPromises()
    const signal = api.getPelicanModels.mock.calls[0][1] as AbortSignal
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    pending.resolve(['gpt-6-astra'])
    await flushPromises()
    expect(wrapper.get('[data-testid="model-1"]').findAll('option').map(option => option.text())).toEqual(['grok-4'])
    await wrapper.get('[data-testid="start-1"]').trigger('click')
    expect(api.startPelicanTests).toHaveBeenCalledWith([{ account_id: 1, model: 'grok-4' }], expect.any(AbortSignal))
  })

  it.each(['close', 'hide', 'unmount', 'history', 'current'] as const)('cancels active and queued catalog reads on %s', async action => {
    const pending = deferred<string[]>()
    api.getPelicanAccounts.mockResolvedValue({ items: Array.from({ length: 12 }, (_, index) => account(index + 1)) })
    api.getPelicanModels.mockReturnValue(pending.promise)
    const wrapper = render()
    await flushPromises()
    const signals = api.getPelicanModels.mock.calls.map(call => call[1] as AbortSignal)
    expect(signals).toHaveLength(4)
    if (action === 'hide') await wrapper.setProps({ show: false })
    else if (action === 'unmount') { wrapper.unmount(); wrappers.splice(wrappers.indexOf(wrapper), 1) }
    else await wrapper.get(`[data-testid="${action === 'close' ? 'close' : `tab-${action}`}"]`).trigger('click')
    expect(signals.every(signal => signal.aborted)).toBe(true)
    pending.resolve(['stale-model'])
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledTimes(4)
    expect(api.startPelicanTests).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('stale-model')
  })

  it('keeps the reopened queue bounded when old requests finish late', async () => {
    const pending = Array.from({ length: 9 }, () => deferred<string[]>())
    api.getPelicanAccounts.mockResolvedValue({ items: Array.from({ length: 8 }, (_, index) => account(index + 1)) })
    let next = 0
    api.getPelicanModels.mockImplementation(() => pending[next++].promise)
    const wrapper = render()
    await flushPromises()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledTimes(8)
    for (const item of pending.slice(0, 4)) item.resolve(['stale-model'])
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledTimes(8)
    expect(wrapper.get('[data-testid="start-1"]').attributes('disabled')).toBeDefined()
    pending[4].resolve(['fresh-model'])
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledTimes(9)
    expect(wrapper.get('[data-testid="model-1"]').findAll('option').map(option => option.text())).toEqual(['fresh-model'])
    expect(wrapper.text()).not.toContain('stale-model')
  })

  it('does not let a late running poll overwrite a successful stop', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run()] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    const pending = deferred<{ items: PelicanBenchmarkRun[] }>()
    api.getPelicanCurrent.mockReturnValueOnce(pending.promise)
    await vi.advanceTimersByTimeAsync(3000)
    await wrapper.get('[data-testid="stop-run-1"]').trigger('click')
    await flushPromises()
    pending.resolve({ items: [run()] })
    await flushPromises()
    expect(wrapper.get('[data-testid="run-run-1"]').text()).toContain('已停止')
    expect(wrapper.find('[data-testid="stop-run-1"]').exists()).toBe(false)
  })

  it('rejects details whose returned status is not successful', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: 'succeeded', html_bytes: 100 })] })
    api.getPelicanResult.mockResolvedValue({ ...run(), html: '<html>not completed</html>' })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.get('[data-testid="result-thumbnail"]').text()).toContain('结果预览加载失败')
  })

  it('supports keyboard tab navigation', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="tab-new"]').trigger('keydown', { key: 'End' })
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-history"]').attributes('aria-selected')).toBe('true')
    await wrapper.get('[data-testid="tab-history"]').trigger('keydown', { key: 'ArrowRight' })
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-new"]').attributes('tabindex')).toBe('0')
  })

  it('orders plans Pro, Pro Lite, Plus, Team before other accounts', async () => {
    api.getPelicanAccounts.mockResolvedValue({ items: [account(1, { plan_type: 'team' }), account(2, { plan_type: 'plus' }), account(3, { plan_type: 'pro_lite' }), account(4), account(5, { plan_type: null })] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('[data-testid^="account-"]').map(row => row.attributes('data-testid'))).toEqual(['account-4', 'account-3', 'account-2', 'account-1', 'account-5'])
  })

  it('ticks running durations every second without a network poll and freezes completed durations', async () => {
    vi.setSystemTime(new Date('2026-09-09T12:00:02Z'))
    api.getPelicanCurrent.mockResolvedValue({ items: [run(), run('done', { account_id: 2, status: 'succeeded', duration_ms: 1234 })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    const calls = api.getPelicanCurrent.mock.calls.length
    expect(wrapper.get('[data-testid="run-run-1"] .run-duration').text()).toBe('1.0 秒')
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('[data-testid="run-run-1"] .run-duration').text()).toBe('2.0 秒')
    expect(wrapper.get('[data-testid="run-done"] .run-duration').text()).toBe('1.2 秒')
    expect(api.getPelicanCurrent).toHaveBeenCalledTimes(calls)
  })

  it('shows multiple completed previews together while other accounts keep running', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('a', { status: 'succeeded', html_bytes: 100 }), run('b', { account_id: 2, status: 'succeeded', html_bytes: 100 }), run('c', { account_id: 3 })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.findAll('iframe')).toHaveLength(2)
    expect(wrapper.find('[data-testid="stop-c"]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getPelicanResult).toHaveBeenCalledTimes(2)
  })

  it('uses the same compact rows, sandboxed thumbnails and history for non-OpenAI results', async () => {
    const accounts = [account(1, { platform: 'anthropic' }), account(2, { platform: 'antigravity' }), account(3, { platform: 'grok' })]
    const items = ['claude-sonnet-4-5', 'gemini-3.7-flash-high', 'grok-4'].map((model, index) =>
      run(`platform-${index}`, { account_id: index + 1, model, upstream_model: model, status: 'succeeded', html_bytes: 120 }))
    api.getPelicanAccounts.mockResolvedValue({ items: accounts })
    api.getPelicanModels.mockImplementation(async (id: number) => [items[id - 1].model])
    api.getPelicanCurrent.mockResolvedValue({ items })
    api.getPelicanHistory.mockResolvedValue({ items, total: 3, page: 1, page_size: 20, pages: 1 })
    const wrapper = render()
    await flushPromises()
    for (const tab of ['current', 'history']) {
      await wrapper.get(`[data-testid="tab-${tab}"]`).trigger('click')
      await flushPromises()
      expect(wrapper.findAll('[data-testid^="run-"]')).toHaveLength(3)
      expect(wrapper.findAll('iframe')).toHaveLength(3)
      for (const item of items) {
        const row = wrapper.get(`[data-testid="run-${item.id}"]`)
        expect(row.get('.benchmark-row').classes()).toContain('grid')
        expect(row.get('.model-select').text()).toBe(item.model)
        expect(row.get('iframe').attributes('sandbox')).toBe('allow-scripts')
        expect(row.get('iframe').attributes('referrerpolicy')).toBe('no-referrer')
        expect(row.get('iframe').attributes('src')).toBe('/api/v1/pelican-preview')
        expect(row.get('iframe').attributes('srcdoc')).toBeUndefined()
      }
    }
  })

  it.each(['continue', 'retry'] as const)('keeps non-OpenAI %s on the existing source task contract', async action => {
    const source = run('antigravity-source', { model: 'gemini-3.7-flash-high', upstream_model: 'gemini-3.7-flash-tiered', status: action === 'continue' ? 'canceled' : 'failed' })
    api.getPelicanAccounts.mockResolvedValue({ items: [account(1, { platform: 'antigravity', plan_type: null })] })
    api.getPelicanModels.mockResolvedValue(['gemini-3.7-flash-high'])
    api.getPelicanCurrent.mockResolvedValue({ items: [source] })
    api.getPelicanResult.mockResolvedValue(source)
    api.restartPelicanTest.mockResolvedValue({ items: [run('next', { source_id: source.id, model: source.model, status: 'queued' })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get(`[data-testid="${action}-${source.id}"]`).trigger('click')
    await flushPromises()
    expect(api.restartPelicanTest).toHaveBeenCalledExactlyOnceWith(source.id, action, expect.any(AbortSignal))
    expect(api.startPelicanTests).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="run-next"] .model-select').text()).toBe(source.model)
    expect(wrapper.findAll('[data-testid^="run-"]')).toHaveLength(1)
  })

  it.each(['continue', 'retry'] as const)('offers %s for an incomplete non-OpenAI result without discarding its partial output', async action => {
    const source = run('partial', { model: 'claude-fable-5-2', status: 'failed', error_code: 'upstream_incomplete', html_bytes: 1700 })
    api.getPelicanCurrent.mockResolvedValue({ items: [source] })
    api.getPelicanResult.mockResolvedValue(source)
    api.restartPelicanTest.mockResolvedValue({ items: [run('next', { source_id: source.id, model: source.model, status: 'queued' })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.find('[data-testid="continue-partial"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="retry-partial"]').exists()).toBe(true)
    expect(wrapper.find('iframe').exists()).toBe(false)
    await wrapper.get(`[data-testid="${action}-partial"]`).trigger('click')
    await flushPromises()
    expect(api.restartPelicanTest).toHaveBeenCalledExactlyOnceWith(source.id, action, expect.any(AbortSignal))
    expect(api.startPelicanTests).not.toHaveBeenCalled()
  })

  it.each(['html_too_large', 'invalid_html', 'upstream_http_401'])('does not offer continuation for nonrecoverable %s output', async error_code => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('partial', { status: 'failed', error_code, html_bytes: 1700 })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.find('[data-testid="continue-partial"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="retry-partial"]').exists()).toBe(true)
  })

  it('explains a confirmed output limit and offers continuation', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('limited', {
      status: 'failed', error_code: 'upstream_incomplete', html_bytes: 1700,
      error_message: 'cause=output_limit; protocol=gemini; finishReason=MAX_TOKENS; terminal=received'
    })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.text()).toContain('本次生成达到模型输出上限')
    expect(wrapper.text()).not.toContain('cause=')
    expect(wrapper.find('[data-testid="continue-limited"]').exists()).toBe(true)
  })

  it('selects multiple accounts and select-all excludes unavailable accounts', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="select-1"]').setValue(true)
    await wrapper.get('[data-testid="select-3"]').setValue(true)
    expect((wrapper.get('[data-testid="select-all"]').element as HTMLInputElement).indeterminate).toBe(true)
    await wrapper.get('[data-testid="start-selected"]').trigger('click')
    expect(api.startPelicanTests.mock.calls[0][0].map((item: { account_id: number }) => item.account_id)).toEqual([1, 3])
  })

  it('selects and clears all startable accounts without selecting active accounts', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run()] })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="select-all"]').setValue(true)
    expect((wrapper.get('[data-testid="select-1"]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.get('[data-testid="select-2"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-testid="select-all"]').setValue(false)
    expect(wrapper.get('[data-testid="start-selected"]').attributes('disabled')).toBeDefined()
  })

  it('does not display legacy node metadata on account or result rows', async () => {
    api.getPelicanAccounts.mockResolvedValue({ items: [account(1), account(2), account(3)] })
    const items = [run('old'), run('snapshot', { account_id: 2 }), run('unknown', { account_id: 3 })]
    api.getPelicanCurrent.mockResolvedValue({ items })
    api.getPelicanHistory.mockResolvedValue({ items: items.map(item => ({ ...item, status: 'succeeded' })), page: 1, page_size: 20, total: 3, pages: 1 })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).not.toContain('node-badge')
    expect(wrapper.text()).not.toContain('api2')
    for (const tab of ['current', 'history']) {
      await wrapper.get(`[data-testid="tab-${tab}"]`).trigger('click')
      await flushPromises()
      expect(wrapper.text()).not.toContain('node-badge')
      expect(wrapper.text()).not.toContain('worker-east')
    }
  })

  it('filters current results locally but requests filtered history pages from the backend', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run(), run('done', { account_id: 2, status: 'succeeded' })] })
    api.getPelicanHistory.mockImplementation((page: number) => Promise.resolve({ items: [run(`page-${page}`, { status: 'succeeded' })], total: 41, page, page_size: 20, pages: 3 }))
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get('[data-testid="result-filter"]').setValue('succeeded')
    expect(wrapper.find('[data-testid="run-run-1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="run-done"]').exists()).toBe(true)
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="history-next"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="result-filter"]').exists()).toBe(false)
    expect(api.getPelicanHistory).toHaveBeenLastCalledWith(2, expect.any(AbortSignal), 'succeeded')
    await wrapper.get('[data-testid="history-next"]').trigger('click')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenLastCalledWith(3, expect.any(AbortSignal), 'succeeded')
  })

  it.each(['continue', 'retry'] as const)('%s waits for every same-account active task to exit before creating a successor', async action => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: action === 'continue' ? 'canceled' : 'failed' }), run('other', { account_id: 2 })] })
    api.getPelicanResult.mockResolvedValue(run())
    api.stopPelicanTests.mockResolvedValue({ items: [run('run-1', { status: 'canceling' })] })
    api.restartPelicanTest.mockResolvedValue({ items: [run('successor')] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get(`[data-testid="${action}-run-1"]`).trigger('click')
    await flushPromises()
    expect(api.stopPelicanTests).toHaveBeenCalledWith(['run-1'], expect.any(AbortSignal))
    expect(wrapper.text()).toContain('等待完全退出')
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
    api.getPelicanResult.mockResolvedValue(run('run-1', { status: 'canceled' }))
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.restartPelicanTest).toHaveBeenCalledExactlyOnceWith('run-1', action, expect.any(AbortSignal))
  })

  it('does not create a successor if the modal closes while waiting for cancellation', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: 'failed' })] })
    api.getPelicanResult.mockResolvedValue(run('run-1', { status: 'canceling' }))
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get('[data-testid="retry-run-1"]').trigger('click')
    await flushPromises()
    expect(api.stopPelicanTests).not.toHaveBeenCalled()
    await wrapper.setProps({ show: false })
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
  })

  it('shows only the active successor and never offers retry on its old source', async () => {
    const completed = run('source', { status: 'failed' })
    api.getPelicanCurrent.mockResolvedValue({ items: [completed, run('successor'), run('other', { account_id: 2 })] })
    api.getPelicanResult.mockResolvedValue(completed)
    api.stopPelicanTests.mockResolvedValue({ items: [run('successor', { status: 'canceled' })] })
    api.restartPelicanTest.mockResolvedValue({ items: [run('new')] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.find('[data-testid="run-source"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="run-successor"]').exists()).toBe(true)
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
  })

  it('never starts after a stop failure or an unconfirmed terminal poll', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: 'failed' })] })
    api.getPelicanResult.mockResolvedValue(run())
    api.stopPelicanTests.mockRejectedValue(new Error('private raw upstream error'))
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get('[data-testid="retry-run-1"]').trigger('click')
    await flushPromises()
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('private raw upstream error')
    api.stopPelicanTests.mockResolvedValue({ items: [run('run-1', { status: 'canceling' })] })
    await wrapper.get('[data-testid="retry-run-1"]').trigger('click')
    await flushPromises()
    api.getPelicanResult.mockRejectedValue(new Error('unconfirmed'))
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
  })

  it('ignores a late history response after leaving and returning to history', async () => {
    const pending = deferred<{ items: PelicanBenchmarkRun[]; page: number; page_size: number; total: number; pages: number }>()
    api.getPelicanHistory.mockReturnValueOnce(pending.promise).mockResolvedValue({ items: [run('passed', { status: 'succeeded' })], page: 1, page_size: 20, total: 1, pages: 1 })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await wrapper.get('[data-testid="tab-new"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await flushPromises()
    pending.resolve({ items: [run('stale')], page: 2, page_size: 20, total: 41, pages: 3 })
    await flushPromises()
    expect(wrapper.find('[data-testid="run-stale"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="run-passed"]').exists()).toBe(true)
  })

  it.each(['upstream_client_upgrade_required', 'continuation_source_unavailable', 'invalid_continuation_source', 'upstream_http_400', 'upstream_http_401', 'upstream_http_403', 'upstream_http_404', 'upstream_http_408', 'upstream_http_429', 'upstream_http_500', 'upstream_http_502', 'upstream_http_503', 'upstream_http_504', 'upstream_network_error', 'upstream_stream_error', 'upstream_incomplete', 'upstream_failed', 'raw secret error'])('shows safe localized errors for %s', async error_code => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: 'failed', error_code })] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    expect(wrapper.text()).not.toContain(error_code)
    expect(wrapper.get('[data-testid="run-run-1"] p').text()).not.toContain('admin.accounts.')
    if (error_code !== 'raw secret error') expect(wrapper.get('[data-testid="run-run-1"] p').text()).toContain(zh.accounts.pelicanBenchmark.errors[error_code as keyof typeof zh.accounts.pelicanBenchmark.errors])
  })

  it.each(['continue', 'retry'] as const)('%s releases busy after 30 seconds without creating a task and keeps stop usable', async action => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: action === 'continue' ? 'canceled' : 'failed' }), run('active', { account_id: 2 })] })
    api.getPelicanResult.mockResolvedValue(run('run-1', { status: 'canceling' }))
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get(`[data-testid="${action}-run-1"]`).trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(29_999)
    expect(wrapper.get('[data-testid="stop-active"]').attributes('disabled')).toBeDefined()
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(wrapper.get('[role="alert"]').text()).toContain('未创建新测试')
    expect(wrapper.get('[data-testid="stop-active"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="stop-all"]').attributes('disabled')).toBeUndefined()
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="stop-active"]').trigger('click')
    await flushPromises()
    expect(api.stopPelicanTests).toHaveBeenCalledWith(['active'], expect.any(AbortSignal))
  })

  it('bounds a stalled detail request and ignores its late terminal response', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-1', { status: 'failed' }), run('active', { account_id: 2 })] })
    const pending = deferred<PelicanBenchmarkRun>()
    api.getPelicanResult.mockReturnValue(pending.promise)
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get('[data-testid="retry-run-1"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(wrapper.get('[role="alert"]').text()).toContain('30 秒')
    expect(wrapper.get('[data-testid="stop-active"]').attributes('disabled')).toBeUndefined()
    pending.resolve(run('run-1', { status: 'canceled' }))
    await flushPromises()
    expect(api.restartPelicanTest).not.toHaveBeenCalled()
    expect(api.stopPelicanTests).not.toHaveBeenCalled()
  })
})
