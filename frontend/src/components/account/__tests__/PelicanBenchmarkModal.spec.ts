import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import PelicanBenchmarkModal from '../PelicanBenchmarkModal.vue'
import { PelicanPartialStartError, type PelicanBenchmarkAccount, type PelicanBenchmarkRun } from '@/api/admin/pelicanBenchmark'
import zh from '@/i18n/locales/zh/admin/accounts'

const api = vi.hoisted(() => ({
  getPelicanAccounts: vi.fn(), getPelicanCurrent: vi.fn(), getPelicanHistory: vi.fn(),
  getPelicanResult: vi.fn(), getPelicanModels: vi.fn(), startPelicanTests: vi.fn(), restartPelicanTest: vi.fn(), stopPelicanTests: vi.fn(), stopAllPelicanTests: vi.fn()
}))
const nodes = vi.hoisted(() => ({ getStatus: vi.fn() }))
vi.mock('@/api/admin/executionNodes', () => nodes)
vi.mock('@/api/admin/pelicanBenchmark', () => ({ ...api, DEFAULT_PELICAN_MODEL: 'gpt-6-astra', PelicanPartialStartError: class extends Error {
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
  return { id, name: `账号-${id}`, status: 'active', plan_type: 'pro', model_ids: ['gpt-6-astra', 'gpt-5.6-luna'], can_test: true, ...overrides }
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
  const promise = new Promise<T>(r => { resolve = r })
  return { promise, resolve }
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
    nodes.getStatus.mockReset().mockResolvedValue({ runtime: { node_id: 'api' }, nodes: [{ node_id: 'api', is_local: true }, { node_id: 'api2', is_local: false }] })
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
    api.getPelicanAccounts.mockResolvedValue({ items: [account(), account(2), account(3, { can_test: false }), account(4, { model_ids: ['gpt-5.6-luna'] })] })
    api.getPelicanCurrent.mockResolvedValue({ items: [run('run-2', { account_id: 2 })] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="start-2"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="start-3"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="start-4"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="model-4"]').setValue('gpt-5.6-luna')
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
    expect(wrapper.get('[data-testid="run-run-1"] .run-status').text()).toBe('排队中')
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

  it('loads completed HTML automatically, retaining scripts inside an opaque CSP-first sandbox', async () => {
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
    const srcdoc = frame.attributes('srcdoc')
    expect(srcdoc).toMatch(/^<meta http-equiv="Content-Security-Policy"/)
    for (const directive of ["default-src 'none'", "script-src 'unsafe-inline'", "style-src 'unsafe-inline'", 'img-src data:', 'font-src data:', "connect-src 'none'", "form-action 'none'", "base-uri 'none'"]) expect(srcdoc).toContain(directive)
    expect(srcdoc).toContain('<script>document.body.dataset.animated="yes"</script>')
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

  it('loads history on demand with independent pagination and no background polling', async () => {
    api.getPelicanHistory.mockImplementation((page: number) => Promise.resolve({ items: [run(`page-${page}`, { status: 'succeeded', html_bytes: 120 })], total: 21, page, page_size: 20, pages: 2 }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenCalledWith(1, expect.any(AbortSignal), undefined)
    await wrapper.get('[data-testid="history-next"]').trigger('click')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenCalledWith(2, expect.any(AbortSignal), undefined)
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

  it('loads model choices on demand and keeps the selection across account refresh', async () => {
    const wrapper = render()
    await flushPromises()
    expect(api.getPelicanModels).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="model-1"]').trigger('focusin')
    await flushPromises()
    expect(api.getPelicanModels).toHaveBeenCalledWith(1, expect.any(AbortSignal))
    await wrapper.get('[data-testid="model-1"]').setValue('gpt-5.6-luna')
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('[data-testid="model-1"]').element as HTMLSelectElement).value).toBe('gpt-5.6-luna')
    await wrapper.get('[data-testid="model-1"]').trigger('focusin')
    expect(api.getPelicanModels).toHaveBeenCalledTimes(1)
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
    api.getPelicanCurrent.mockResolvedValue({ items: [run(), run('done', { status: 'succeeded', duration_ms: 1234 })] })
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

  it('uses real node snapshots on all tabs and never invents ownership', async () => {
    api.getPelicanAccounts.mockResolvedValue({ items: [account(1, { execution_node_id: 'api' }), account(2, { execution_node_id: 'api2' }), account(3)] })
    const items = [run('old', { execution_node_id: '' }), run('snapshot', { account_id: 2, execution_node_id: 'worker-east' }), run('unknown', { account_id: 3, execution_node_id: '' })]
    api.getPelicanCurrent.mockResolvedValue({ items })
    api.getPelicanHistory.mockResolvedValue({ items, page: 1, page_size: 20, total: 3, pages: 1 })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="account-1"] [data-testid="node-badge"]').text()).toBe('本机')
    expect(wrapper.get('[data-testid="account-2"] [data-testid="node-badge"]').text()).toBe('api2')
    expect(wrapper.get('[data-testid="account-3"] [data-testid="node-badge"]').text()).toBe('--')
    for (const tab of ['current', 'history']) {
      await wrapper.get(`[data-testid="tab-${tab}"]`).trigger('click')
      await flushPromises()
      expect(wrapper.get('[data-testid="run-old"] [data-testid="node-badge"]').text()).toBe('本机')
      expect(wrapper.get('[data-testid="run-snapshot"] [data-testid="node-badge"]').text()).toBe('worker-east')
      expect(wrapper.get('[data-testid="run-unknown"] [data-testid="node-badge"]').text()).toBe('--')
    }
  })

  it('filters current results locally but requests filtered history pages from the backend', async () => {
    api.getPelicanCurrent.mockResolvedValue({ items: [run(), run('done', { status: 'succeeded' })] })
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
    await wrapper.get('[data-testid="result-filter"]').setValue('succeeded')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenLastCalledWith(1, expect.any(AbortSignal), 'succeeded')
    await wrapper.get('[data-testid="history-next"]').trigger('click')
    await flushPromises()
    expect(api.getPelicanHistory).toHaveBeenLastCalledWith(2, expect.any(AbortSignal), 'succeeded')
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

  it('stops an active successor before retrying its completed source, leaving other accounts alone', async () => {
    const completed = run('source', { status: 'failed' })
    api.getPelicanCurrent.mockResolvedValue({ items: [completed, run('successor'), run('other', { account_id: 2 })] })
    api.getPelicanResult.mockResolvedValue(completed)
    api.stopPelicanTests.mockResolvedValue({ items: [run('successor', { status: 'canceled' })] })
    api.restartPelicanTest.mockResolvedValue({ items: [run('new')] })
    const wrapper = render()
    await flushPromises()
    await currentTab(wrapper)
    await wrapper.get('[data-testid="retry-source"]').trigger('click')
    await flushPromises()
    expect(api.stopPelicanTests).toHaveBeenCalledExactlyOnceWith(['successor'], expect.any(AbortSignal))
    expect(api.restartPelicanTest).toHaveBeenCalledExactlyOnceWith('source', 'retry', expect.any(AbortSignal))
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

  it('ignores a late unfiltered history response after the filter changes', async () => {
    const pending = deferred<{ items: PelicanBenchmarkRun[]; page: number; page_size: number; total: number; pages: number }>()
    api.getPelicanHistory.mockReturnValueOnce(pending.promise).mockResolvedValue({ items: [run('passed', { status: 'succeeded' })], page: 1, page_size: 20, total: 1, pages: 1 })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="tab-history"]').trigger('click')
    await wrapper.get('[data-testid="result-filter"]').setValue('succeeded')
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
    if (error_code !== 'raw secret error') expect(wrapper.get('[data-testid="run-run-1"] p').text()).toBe(zh.accounts.pelicanBenchmark.errors[error_code as keyof typeof zh.accounts.pelicanBenchmark.errors])
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
