import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  getPelicanAccounts, getPelicanCurrent, getPelicanHistory, getPelicanModels, getPelicanResult,
  startPelicanTests, stopAllPelicanTests, stopPelicanTests, PelicanPartialStartError,
  type PelicanBenchmarkRun
} from '@/api/admin/pelicanBenchmark'

const { get, post, list } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), list: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))
vi.mock('@/api/admin/accounts', () => ({ list }))

const task = (overrides: Partial<PelicanBenchmarkRun> = {}): PelicanBenchmarkRun => ({
  id: 'task-1', batch_id: 'batch-1', account_id: 1, account_name: 'PRO account', model: 'gpt-6-astra', upstream_model: 'gpt-6-astra',
  status: 'running', error_code: '', created_at: '2026-09-09T12:00:00Z', started_at: null, finished_at: null, duration_ms: null, html_bytes: 0, thumbnail_url: null,
  ...overrides
})

describe('Pelican benchmark backend contract', () => {
  beforeEach(() => { get.mockReset(); post.mockReset(); list.mockReset() })

  it('uses the complete independent redacted account list, never lite, export, or table filters', async () => {
    const first = { id: 1, name: 'one', platform: 'openai', type: 'oauth', status: 'active', credentials: { plan_type: 'PRO', arbitrary: 'not-for-ui' } }
    list.mockResolvedValueOnce({ items: [first], pages: 2 }).mockResolvedValueOnce({ items: [
      { ...first, id: 2, credentials: { plan_type: 'team' } },
      { ...first, id: 3, credentials: { plan_type: 'free' } },
      { ...first, id: 4, parent_account_id: 1 },
      { ...first, id: 5, platform: 'anthropic' },
      { ...first, id: 6, credentials: {} },
      { ...first, id: 7, type: 'apikey', credentials: {} }
    ], pages: 2 })
    const signal = new AbortController().signal
    const result = await getPelicanAccounts(signal)
    expect(list).toHaveBeenCalledTimes(2)
    expect(list).toHaveBeenNthCalledWith(2, 2, 200, { platform: 'openai', sort_by: 'id', sort_order: 'asc', include_scheduler_score: '0' }, { signal })
    expect(result.items.map(account => account.id)).toEqual([1, 2, 7])
    expect(result.items[0]).toEqual({ id: 1, name: 'one', status: 'active', plan_type: 'pro', model_ids: ['gpt-6-astra'], can_test: true })
    expect(JSON.stringify(result)).not.toContain('credentials')
    expect(get).not.toHaveBeenCalled()
  })

  it('uses the existing account model endpoint', async () => {
    get.mockResolvedValue({ data: [{ id: 'gpt-6-astra', display_name: 'Astra' }] })
    expect(await getPelicanModels(7)).toEqual(['gpt-6-astra'])
    expect(get).toHaveBeenCalledWith('/admin/accounts/7/models', { signal: undefined })
  })

  it('creates a single task using the actual shortcut and server default prompt', async () => {
    post.mockResolvedValue({ data: { batch_id: 'batch-1', tasks: [task()], skipped: [] } })
    const signal = new AbortController().signal
    expect(await startPelicanTests([{ account_id: 1, model: 'gpt-6-astra' }], signal)).toEqual({ items: [task()], skipped: [] })
    expect(post).toHaveBeenCalledExactlyOnceWith('/admin/accounts/1/pelican-benchmark', { model: 'gpt-6-astra' }, { signal })
  })

  it('groups per-account models into the server account_ids/model contract and retains skipped outcomes', async () => {
    post.mockResolvedValueOnce({ data: { batch_id: 'batch-1', tasks: [task()], skipped: [{ account_id: 3, reason: 'account_access_denied' }] } })
      .mockResolvedValueOnce({ data: { batch_id: 'batch-2', tasks: [task({ id: 'task-2', batch_id: 'batch-2', account_id: 2, model: 'gpt-5.6-luna' })], skipped: [] } })
    const result = await startPelicanTests([{ account_id: 1, model: 'gpt-6-astra' }, { account_id: 2, model: 'gpt-5.6-luna' }, { account_id: 3, model: 'gpt-6-astra' }])
    expect(post).toHaveBeenNthCalledWith(1, '/admin/pelican-benchmarks', { account_ids: [1, 3], model: 'gpt-6-astra' }, { signal: undefined })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/pelican-benchmarks', { account_ids: [2], model: 'gpt-5.6-luna' }, { signal: undefined })
    expect(result.items).toHaveLength(2)
    expect(result.skipped).toEqual([{ account_id: 3, reason: 'account_access_denied' }])
  })

  it('preserves confirmed tasks when a later model group fails', async () => {
    post.mockResolvedValueOnce({ data: { batch_id: 'batch-1', tasks: [task()], skipped: [] } }).mockRejectedValueOnce({ status: 503 })
    const outcome = await startPelicanTests([{ account_id: 1, model: 'gpt-6-astra' }, { account_id: 2, model: 'gpt-5.6-luna' }]).catch(error => error)
    expect(outcome).toBeInstanceOf(PelicanPartialStartError)
    expect(outcome.items).toEqual([task()])
    expect(outcome.cause).toEqual({ status: 503 })
  })

  it('respects the 2000-account create limit without silently truncating selections', async () => {
    post.mockResolvedValue({ data: { batch_id: 'batch-1', tasks: [], skipped: [] } })
    await startPelicanTests(Array.from({ length: 2001 }, (_, index) => ({ account_id: index + 1, model: 'gpt-6-astra' })))
    expect(post).toHaveBeenCalledTimes(2)
    expect(post.mock.calls[0][1].account_ids).toHaveLength(2000)
    expect(post.mock.calls[1][1].account_ids).toEqual([2001])
  })

  it('discovers active batches using actual status filters and keeps their completed siblings', async () => {
    get.mockImplementation(async (_url, { params }) => ({ data: {
      items: params.status === 'running' ? [task()] : params.batch_id ? [task(), task({ id: 'done', status: 'succeeded', html_bytes: 100 })] : [],
      total: params.batch_id ? 2 : params.status === 'running' ? 1 : 0, page: 1, page_size: 100
    } }))
    const result = await getPelicanCurrent()
    expect(result.items).toHaveLength(2)
    expect(get.mock.calls.map(call => call[0])).toEqual(Array(4).fill('/admin/pelican-benchmarks'))
    expect(get.mock.calls.slice(0, 3).map(call => call[1].params.status)).toEqual(['running', 'queued', 'canceling'])
    expect(get.mock.calls[3][1].params.batch_id).toBe('batch-1')
  })

  it('fetches all pages of known batches without fetching HTML or rediscovering each poll', async () => {
    get.mockImplementation(async (_url, { params }) => ({ data: { items: [task({ id: `task-${params.page}` })], total: 101, page: params.page, page_size: 100 } }))
    const result = await getPelicanCurrent(undefined, ['batch-1', 'batch-1'])
    expect(result.items).toHaveLength(2)
    expect(get).toHaveBeenCalledTimes(2)
    expect(get.mock.calls[1][1].params).toEqual({ batch_id: 'batch-1', page: 2, page_size: 100 })
  })

  it('derives history pages from the backend total rather than assuming a pages field exists', async () => {
    get.mockResolvedValue({ data: { items: [task()], total: 21, page: 2, page_size: 20 } })
    expect((await getPelicanHistory(2)).pages).toBe(2)
    expect(get).toHaveBeenCalledWith('/admin/pelican-benchmarks', { params: { page: 2, page_size: 20 }, signal: undefined })
  })

  it('uses actual detail and single/all stop endpoints, with no invented result or stop-ID-list route', async () => {
    get.mockResolvedValue({ data: { ...task({ status: 'succeeded' }), html: '<html></html>' } })
    await getPelicanResult('task/1')
    expect(get).toHaveBeenCalledWith('/admin/pelican-benchmarks/task%2F1', { signal: undefined })
    post.mockResolvedValueOnce({ data: task({ status: 'canceling' }) }).mockResolvedValueOnce({ data: { affected: 1 } })
    expect((await stopPelicanTests(['task-1'])).items[0].status).toBe('canceling')
    expect(post).toHaveBeenNthCalledWith(1, '/admin/pelican-benchmarks/task-1/stop', undefined, { signal: undefined })
    expect(await stopAllPelicanTests()).toEqual({ affected: 1 })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/pelican-benchmarks/stop', { all: true }, { signal: undefined })
  })
})
