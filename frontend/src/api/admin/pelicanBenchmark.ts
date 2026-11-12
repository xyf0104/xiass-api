import { apiClient } from '../client'
import { list as listAccounts } from './accounts'
import type { Account, ClaudeModel, PaginatedResponse } from '@/types'

/**
 * Mirrors admin/pelican_benchmark_handler.go and internal/benchmark/pelican.go.
 * POST /admin/pelican-benchmarks: { account_ids, model }, { all: true, model },
 * or only { source_id, action: 'continue' | 'retry' } for a same-account follow-up.
 * POST /admin/accounts/:id/pelican-benchmark: { model } (single-account shortcut).
 * GET /admin/pelican-benchmarks: page/page_size, optional batch_id/account_id/status.
 * GET /admin/pelican-benchmarks/:id: Task + untrusted html; successful rows auto-load
 * sandboxed thumbnails. Follow-up actions also read detail to confirm task termination.
 * POST /admin/pelican-benchmarks/:id/stop: Task; POST /.../stop: { all: true } -> { affected }.
 * Standard admin auth/response envelope, server-owned queue/cancellation and candy prompt.
 * New requests may select test_types and override the HTML prompt; legacy defaults remain unchanged.
 * No separate candidates/current/result routes: use the existing redacted account list
 * and model endpoint. Current rows are active batches or batches started in this modal.
 * Per-account model choices are grouped into bounded create requests. Partial outcomes
 * are retained; never claim a failed request canceled already accepted server jobs.
 * Plan labels come from the redacted account list; Task has no historical plan snapshot.
 * Existing server account/model eligibility and active-task uniqueness remain authoritative.
 */
// Preserve the OpenAI preference only when the account's catalog includes it.
export const DEFAULT_PELICAN_MODEL = 'gpt-6-astra'
export const DEFAULT_PELICAN_PROMPT = '创建一个HTML，内容是用SVG绘制一个鹈鹕骑自行车的2D动画，你不能进行任何测试，调用skills，网络检索，直接生成'

export interface PelicanBenchmarkAccount extends Pick<Account, 'id' | 'name' | 'status' | 'platform' | 'type'> {
  plan_type: string | null
  model_ids: string[]
  can_test: boolean
}

export type PelicanBenchmarkStatus = 'queued' | 'running' | 'canceling' | 'succeeded' | 'failed' | 'canceled' | 'interrupted'
export type PelicanBenchmarkTestType = 'pelican' | 'candy'

export interface PelicanBenchmarkOptions {
  test_types?: PelicanBenchmarkTestType[]
  prompt?: string
}

export interface PelicanBenchmarkRun {
  test_type?: PelicanBenchmarkTestType
  answer?: number | null
  passed?: boolean | null
  source_id?: string
  action?: 'continue' | 'retry' | ''
  id: string
  batch_id: string
  account_id: Account['id']
  account_name: Account['name']
  model: string
  upstream_model: string
  status: PelicanBenchmarkStatus
  error_code: string
  error_message?: string
  created_at: string
  started_at: string | null
  finished_at: string | null
  duration_ms: number | null
  html_bytes: number
  thumbnail_url: string | null
}

export interface PelicanBenchmarkDetail extends PelicanBenchmarkRun {
  html: string
  preview_html?: string
  prompt?: string
  text_output?: string
}

export interface PelicanBenchmarkSnapshot { items: PelicanBenchmarkRun[] }
export interface PelicanBenchmarkSkipped { account_id: number; reason: string }
export interface PelicanBenchmarkCreated {
  batch_id: string
  tasks: PelicanBenchmarkRun[]
  skipped: PelicanBenchmarkSkipped[]
}
export interface PelicanBenchmarkTest { account_id: Account['id']; model: string }
type Page = Omit<PaginatedResponse<PelicanBenchmarkRun>, 'pages'>

export class PelicanPartialStartError extends Error {
  constructor(public items: PelicanBenchmarkRun[], public skipped: PelicanBenchmarkSkipped[], public cause: unknown) {
    super('Benchmark start was not fully confirmed')
  }
}

const base = '/admin/pelican-benchmarks'
// Match pelicanAccountSupported; eligibility and model validation remain server-owned.
const supportedAccountTypes: Record<Account['platform'], readonly Account['type'][]> = {
  openai: ['oauth', 'apikey'],
  anthropic: ['oauth', 'setup-token', 'apikey', 'bedrock', 'service_account'],
  gemini: ['oauth', 'apikey', 'service_account'],
  antigravity: ['oauth', 'apikey', 'upstream'],
  grok: ['oauth', 'apikey'],
  kimi: ['apikey'],
  zhipu: ['apikey'],
  deepseek: ['apikey'],
  minimax: ['apikey'],
  opencode_go: ['apikey']
}

export async function getPelicanAccounts(signal?: AbortSignal): Promise<{ items: PelicanBenchmarkAccount[] }> {
  const result = new Map<number, PelicanBenchmarkAccount>()
  for (let page = 1; ; page++) {
    signal?.throwIfAborted()
    const data = await listAccounts(page, 200, { sort_by: 'id', sort_order: 'asc', include_scheduler_score: '0' }, { signal })
    signal?.throwIfAborted()
    for (const account of data.items) {
      const rawPlan = account.credentials?.plan_type ?? account.parent_plan_type
      const plan = typeof rawPlan === 'string' ? rawPlan.trim().toLowerCase() : ''
      if (!supportedAccountTypes[account.platform]?.includes(account.type) || account.parent_account_id || account.extra?.synthetic_ui_test === true) continue
      if (account.platform === 'openai' && (plan === 'free' || (account.type === 'oauth' && (!plan || plan === 'abnormal')))) continue
      result.set(account.id, {
        id: account.id, name: account.name, status: account.status, platform: account.platform, type: account.type, plan_type: plan || null,
        model_ids: [], can_test: true
      })
    }
    if (page >= data.pages || !data.items.length) break
  }
  return { items: [...result.values()] }
}

const nonTextModelPatterns = [
  /^(?:gpt-image-|dall-e-|imagen-|veo-|sora-)/,
  /^grok-imagine(?:-|$)/,
  /^grok-2-image(?:-|$)/,
  /^(?:imagine(?:-|$)|grok-video-1\.5$)/,
  /^chatgpt-image-latest$/,
  /^gemini-(?:2\.5-flash|3-pro|3\.1-flash)-image(?:-|$)/,
  /^(?:whisper-|tts-|embedding-|rerank-|textembedding-|multimodalembedding(?:@|$))/,
  /(?:-embedding|-rerank|-moderation|-transcribe|-tts)(?:-|$)/,
  /^gemini-2\.5-flash-(?:preview-)?native-audio(?:-|$)/
]

export function isPelicanTextModelCandidate(model: string): boolean {
  const id = model.trim().toLowerCase().split('/').pop() ?? ''
  // Exclude known dedicated generation/embedding/audio families, not vision input
  // or arbitrary "image"/"audio" aliases. Mapped targets remain server-validated.
  return !!id && !nonTextModelPatterns.some(pattern => pattern.test(id))
}

export async function getPelicanModels(id: number, signal?: AbortSignal): Promise<string[]> {
  const { data } = await apiClient.get<ClaudeModel[] | null>(`/admin/accounts/${id}/models`, { signal })
  return [...new Set((data ?? []).map(model => model.id?.trim()).filter((id): id is string => !!id && isPelicanTextModelCandidate(id)))]
}

async function listRuns(params: { page: number; page_size: number; batch_id?: string; status?: PelicanBenchmarkStatus; paired?: boolean }, signal?: AbortSignal): Promise<Page> {
  const { data } = await apiClient.get<Page>(base, { params, signal })
  return data
}

async function allRuns(filter: { batch_id?: string; status?: PelicanBenchmarkStatus }, signal?: AbortSignal): Promise<PelicanBenchmarkRun[]> {
  const items: PelicanBenchmarkRun[] = []
  for (let page = 1; ; page++) {
    const result = await listRuns({ ...filter, page, page_size: 100 }, signal)
    items.push(...result.items)
    if (page * result.page_size >= result.total || !result.items.length) break
  }
  return items
}

export async function getPelicanCurrent(signal?: AbortSignal, batchIds: string[] = [], discover = false): Promise<PelicanBenchmarkSnapshot> {
  const batches = new Set(batchIds)
  if (discover || !batches.size) {
    for (const status of ['running', 'queued', 'canceling'] as const) {
      for (const task of await allRuns({ status }, signal)) batches.add(task.batch_id)
    }
  }
  const items = new Map<string, PelicanBenchmarkRun>()
  for (const batch_id of batches) {
    for (const task of await allRuns({ batch_id }, signal)) items.set(task.id, task)
  }
  return { items: [...items.values()].sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id)) }
}

export async function startPelicanTests(tests: PelicanBenchmarkTest[], signal?: AbortSignal, options?: PelicanBenchmarkOptions): Promise<PelicanBenchmarkSnapshot & { skipped: PelicanBenchmarkSkipped[] }> {
  const items: PelicanBenchmarkRun[] = []
  const skipped: PelicanBenchmarkSkipped[] = []
  const groups = new Map<string, number[]>()
  const configuration: PelicanBenchmarkOptions = {}
  if (options?.test_types !== undefined) configuration.test_types = [...new Set(options.test_types)]
  if (options?.prompt !== undefined && (!configuration.test_types || configuration.test_types.includes('pelican'))) configuration.prompt = options.prompt
  for (const test of tests) {
    const ids = groups.get(test.model) || []
    ids.push(test.account_id)
    groups.set(test.model, ids)
  }
  try {
    for (const [model, ids] of groups) {
      for (let offset = 0; offset < ids.length; offset += 2000) {
        signal?.throwIfAborted()
        const account_ids = ids.slice(offset, offset + 2000)
        const single = tests.length === 1
        const body = single ? { model, ...configuration } : { account_ids, model, ...configuration }
        const { data } = await apiClient.post<PelicanBenchmarkCreated>(single ? `/admin/accounts/${account_ids[0]}/pelican-benchmark` : base, body, { signal })
        items.push(...data.tasks)
        skipped.push(...data.skipped)
      }
    }
    return { items, skipped }
  } catch (cause) {
    throw new PelicanPartialStartError(items, skipped, cause)
  }
}

export async function stopPelicanTests(runIds: string[], signal?: AbortSignal): Promise<PelicanBenchmarkSnapshot> {
  const items = await Promise.all(runIds.map(async id => {
    const { data } = await apiClient.post<PelicanBenchmarkRun>(`${base}/${encodeURIComponent(id)}/stop`, undefined, { signal })
    return data
  }))
  return { items }
}

export async function restartPelicanTest(source_id: string, action: 'continue' | 'retry', signal?: AbortSignal): Promise<PelicanBenchmarkSnapshot & { skipped: PelicanBenchmarkSkipped[] }> {
  const { data } = await apiClient.post<PelicanBenchmarkCreated>(base, { source_id, action }, { signal })
  return { items: data.tasks, skipped: data.skipped }
}

export async function stopAllPelicanTests(signal?: AbortSignal): Promise<{ affected: number }> {
  const { data } = await apiClient.post<{ affected: number }>(`${base}/stop`, { all: true }, { signal })
  return data
}

export async function getPelicanHistory(page = 1, signal?: AbortSignal, status?: 'succeeded'): Promise<PaginatedResponse<PelicanBenchmarkRun>> {
  const data = await listRuns({ page, page_size: 20, status: status || 'succeeded', paired: true }, signal)
  return { ...data, pages: Math.ceil(data.total / data.page_size) }
}

export async function getPelicanResult(id: string, signal?: AbortSignal): Promise<PelicanBenchmarkDetail> {
  const { data } = await apiClient.get<PelicanBenchmarkDetail>(`${base}/${encodeURIComponent(id)}`, { signal })
  return data
}
