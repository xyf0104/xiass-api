import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  getPelicanAccounts, getPelicanCurrent, getPelicanHistory, getPelicanModels, getPelicanResult,
  startPelicanTests, restartPelicanTest, stopAllPelicanTests, stopPelicanTests, PelicanPartialStartError,
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
      { ...first, id: 5, platform: 'anthropic', credentials: {} },
      { ...first, id: 6, credentials: {} },
      { ...first, id: 7, type: 'apikey', credentials: {} },
      { ...first, id: 8, platform: 'antigravity', credentials: { plan_type: 'free' } },
      { ...first, id: 9, platform: 'grok', credentials: { plan_type: 'abnormal' } },
      { ...first, id: 10, platform: 'gemini', credentials: {} },
      { ...first, id: 11, credentials: { plan_type: 'abnormal' } },
      { ...first, id: 12, extra: { synthetic_ui_test: true } }
    ], pages: 2 })
    const signal = new AbortController().signal
    const result = await getPelicanAccounts(signal)
    expect(list).toHaveBeenCalledTimes(2)
    expect(list).toHaveBeenNthCalledWith(2, 2, 200, { sort_by: 'id', sort_order: 'asc', include_scheduler_score: '0' }, { signal })
    expect(result.items.map(account => account.id)).toEqual([1, 2, 5, 7, 8, 9, 10])
    expect(result.items[0]).toEqual({ id: 1, name: 'one', status: 'active', platform: 'openai', type: 'oauth', plan_type: 'pro', model_ids: [], can_test: true })
    expect(result.items.every(account => account.model_ids.length === 0)).toBe(true)
    expect(JSON.stringify(result)).not.toContain('credentials')
    expect(get).not.toHaveBeenCalled()
  })

  it.each([
    ['anthropic', 'oauth'], ['anthropic', 'setup-token'], ['anthropic', 'apikey'], ['anthropic', 'bedrock'], ['anthropic', 'service_account'],
    ['antigravity', 'oauth'], ['antigravity', 'apikey'], ['antigravity', 'upstream'],
    ['gemini', 'oauth'], ['gemini', 'apikey'], ['gemini', 'service_account'],
    ['grok', 'oauth'], ['grok', 'apikey'], ['kimi', 'apikey'], ['zhipu', 'apikey'],
    ['deepseek', 'apikey'], ['minimax', 'apikey'], ['opencode_go', 'apikey']
  ])('includes supported %s %s accounts without applying OpenAI plan eligibility', async (platform, type) => {
    list.mockResolvedValue({ items: [{ id: 1, name: 'account', platform, type, status: 'active', credentials: {} }], pages: 1 })
    expect((await getPelicanAccounts()).items).toEqual([
      { id: 1, name: 'account', platform, type, status: 'active', plan_type: null, model_ids: [], can_test: true }
    ])
  })

  it('excludes unsupported authentication, credential shadows and synthetic accounts on other platforms', async () => {
    const account = { id: 1, name: 'account', platform: 'anthropic', type: 'oauth', status: 'active' }
    list.mockResolvedValue({ items: [
      { ...account, type: 'upstream' }, { ...account, id: 2, type: 'unsupported' },
      { ...account, id: 3, parent_account_id: 1 }, { ...account, id: 4, extra: { synthetic_ui_test: true } }
    ], pages: 1 })
    expect((await getPelicanAccounts()).items).toEqual([])
  })

  it.each([
    ['openai', 'setup-token'], ['openai', 'service_account'], ['grok', 'upstream'], ['grok', 'bedrock'],
    ['gemini', 'setup-token'], ['antigravity', 'service_account'], ['deepseek', 'oauth'], ['unknown', 'apikey']
  ])('does not advertise unsupported %s %s combinations', async (platform, type) => {
    list.mockResolvedValue({ items: [{ id: 1, name: 'account', platform, type, status: 'active', credentials: { plan_type: 'pro' } }], pages: 1 })
    expect((await getPelicanAccounts()).items).toEqual([])
  })

  it('does not start or continue paginated account reads after cancellation', async () => {
    const controller = new AbortController()
    list.mockImplementation(async () => {
      controller.abort()
      return { items: [{ id: 1, platform: 'anthropic', type: 'oauth' }], pages: 20 }
    })
    await expect(getPelicanAccounts(controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(list).toHaveBeenCalledTimes(1)
    await expect(getPelicanAccounts(controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(list).toHaveBeenCalledTimes(1)
  })

  it('uses the existing account model endpoint', async () => {
    const signal = new AbortController().signal
    get.mockResolvedValue({ data: [{ id: 'gemini-3.7-flash-high' }, { id: 'claude-sonnet-4-5' }, { id: 'gemini-3.7-flash-high' }, { id: ' ' }] })
    expect(await getPelicanModels(7, signal)).toEqual(['gemini-3.7-flash-high', 'claude-sonnet-4-5'])
    expect(get).toHaveBeenCalledWith('/admin/accounts/7/models', { signal })
  })

  it('filters dedicated non-text model families while preserving multimodal text and custom aliases', async () => {
    const excluded = [
      'gpt-image-2', 'openai/GPT-IMAGE-1', 'dall-e-3', 'chatgpt-image-latest',
      'grok-imagine', 'xai/grok-imagine-image', 'grok-imagine-video', 'grok-2-image-1212', 'imagine-image', 'grok-video-1.5',
      'models/gemini-2.5-flash-image-preview', 'gemini-3-pro-image', 'gemini-3.1-flash-image-preview',
      'imagen-4.0-generate-001', 'veo-3.1-generate-preview', 'sora-2',
      'text-embedding-3-large', 'gemini-embedding-001', 'embedding-001', 'textembedding-gecko@003',
      'multimodalembedding@001', 'rerank-v3.5', 'omni-moderation-latest',
      'whisper-1', 'tts-1-hd', 'gpt-4o-transcribe', 'gpt-4o-mini-transcribe-2025-12-15',
      'gpt-4o-mini-tts', 'gemini-2.5-flash-preview-tts', 'gemini-2.5-pro-preview-tts',
      'gemini-2.5-flash-native-audio-preview-12-2025', 'gemini-2.5-flash-preview-native-audio-dialog'
    ]
    const retained = [
      'gpt-6-astra', 'gpt-4o', 'gpt-audio', 'gpt-4o-audio-preview', 'gemini-2.5-flash', 'gemini-3.7-flash-high',
      'claude-sonnet-4-5@20250929', 'us.anthropic.claude-sonnet-4-5-20250929-v1:0',
      'vision-model', 'vendor/image-understanding', 'my-image-captioner', 'custom-audio-reasoning',
      'vendor/custom+reasoning', 'gemini-3-pro', 'grok-4', 'Qwen/Qwen2.5-VL-72B-Instruct'
    ]
    get.mockResolvedValue({ data: [...excluded, ...retained].map(id => ({ id })) })
    expect(await getPelicanModels(7)).toEqual(retained)
    get.mockResolvedValue({ data: excluded.map(id => ({ id })) })
    expect(await getPelicanModels(7)).toEqual([])
  })

  it.each([[], null])('returns no invented default for an empty model catalog %s', async data => {
    get.mockResolvedValue({ data })
    expect(await getPelicanModels(7)).toEqual([])
  })

  it('propagates model errors without returning stale or default models', async () => {
    get.mockResolvedValueOnce({ data: [{ id: 'grok-4' }] }).mockRejectedValueOnce(new Error('unavailable'))
    expect(await getPelicanModels(7)).toEqual(['grok-4'])
    await expect(getPelicanModels(7)).rejects.toThrow('unavailable')
  })

  it.each(['continue', 'retry'] as const)('posts only source_id and action for %s', async action => {
    post.mockResolvedValue({ data: { batch_id: 'batch-1', tasks: [task()], skipped: [] } })
    expect(await restartPelicanTest('source', action)).toEqual({ items: [task()], skipped: [] })
    expect(post).toHaveBeenCalledExactlyOnceWith('/admin/pelican-benchmarks', { source_id: 'source', action }, { signal: undefined })
  })

  it('passes the success filter to backend pagination', async () => {
    get.mockResolvedValue({ data: { items: [], total: 45, page: 2, page_size: 20 } })
    expect((await getPelicanHistory(2, undefined, 'succeeded')).pages).toBe(3)
    expect(get).toHaveBeenCalledWith('/admin/pelican-benchmarks', { params: { page: 2, page_size: 20, status: 'succeeded' }, signal: undefined })
  })

  it('dispatches stops in parallel even while the first request is pending', async () => {
    let finish!: (value: unknown) => void
    post.mockReturnValueOnce(new Promise(resolve => { finish = resolve })).mockResolvedValueOnce({ data: task({ id: 'two' }) })
    const result = stopPelicanTests(['one', 'two'])
    expect(post).toHaveBeenCalledTimes(2)
    finish({ data: task({ id: 'one' }) })
    expect((await result).items.map(item => item.id)).toEqual(['one', 'two'])
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

  it('submits a mixed-platform selection through the same endpoint without changing the server prompt', async () => {
    post.mockResolvedValue({ data: { batch_id: 'mixed', tasks: [], skipped: [] } })
    await startPelicanTests([
      { account_id: 1, model: 'gpt-6-astra' }, { account_id: 2, model: 'claude-sonnet-4-5' },
      { account_id: 3, model: 'gemini-3.7-flash-high' }, { account_id: 4, model: 'grok-4' },
      { account_id: 5, model: 'claude-sonnet-4-5' }
    ])
    expect(post.mock.calls.map(([url, body]) => [url, body])).toEqual([
      ['/admin/pelican-benchmarks', { account_ids: [1], model: 'gpt-6-astra' }],
      ['/admin/pelican-benchmarks', { account_ids: [2, 5], model: 'claude-sonnet-4-5' }],
      ['/admin/pelican-benchmarks', { account_ids: [3], model: 'gemini-3.7-flash-high' }],
      ['/admin/pelican-benchmarks', { account_ids: [4], model: 'grok-4' }]
    ])
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
    expect(get).toHaveBeenCalledWith('/admin/pelican-benchmarks', { params: { page: 2, page_size: 20, status: 'succeeded' }, signal: undefined })
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
