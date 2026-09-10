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
 * Standard admin auth/response envelope, server-owned prompt/queue/cancellation.
 * No separate candidates/current/result routes: use the existing redacted account list
 * and model endpoint. Current rows are active batches or batches started in this modal.
 * Per-account model choices are grouped into bounded create requests. Partial outcomes
 * are retained; never claim a failed request canceled already accepted server jobs.
 * Plan labels come from the redacted account list; Task has no historical plan snapshot.
 * Existing server account/model eligibility and active-task uniqueness remain authoritative.
 */
export const DEFAULT_PELICAN_MODEL = 'gpt-6-astra'

export interface PelicanBenchmarkAccount extends Pick<Account, 'id' | 'name' | 'status'> {
  execution_node_id?: string
  plan_type: string | null
  model_ids: string[]
  can_test: boolean
}

export type PelicanBenchmarkStatus = 'queued' | 'running' | 'canceling' | 'succeeded' | 'failed' | 'canceled' | 'interrupted'

export interface PelicanBenchmarkRun {
  execution_node_id?: string
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
  created_at: string
  started_at: string | null
  finished_at: string | null
  duration_ms: number | null
  html_bytes: number
  thumbnail_url: string | null
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

export async function getPelicanAccounts(signal?: AbortSignal): Promise<{ items: PelicanBenchmarkAccount[] }> {
  const result = new Map<number, PelicanBenchmarkAccount>()
  for (let page = 1; ; page++) {
    const data = await listAccounts(page, 200, { platform: 'openai', sort_by: 'id', sort_order: 'asc', include_scheduler_score: '0' }, { signal })
    for (const account of data.items) {
      const rawPlan = account.credentials?.plan_type ?? account.parent_plan_type
      const plan = typeof rawPlan === 'string' ? rawPlan.trim().toLowerCase() : ''
      if (account.platform !== 'openai' || !['oauth', 'apikey'].includes(account.type) || account.parent_account_id || account.extra?.synthetic_ui_test === true) continue
      if (plan === 'free' || (account.type === 'oauth' && (!plan || plan === 'abnormal'))) continue
      result.set(account.id, {
        id: account.id, name: account.name, status: account.status, execution_node_id: account.execution_node_id, plan_type: plan || null,
        model_ids: [DEFAULT_PELICAN_MODEL], can_test: true
      })
    }
    if (page >= data.pages || !data.items.length) break
  }
  return { items: [...result.values()] }
}

export async function getPelicanModels(id: number, signal?: AbortSignal): Promise<string[]> {
  const { data } = await apiClient.get<ClaudeModel[]>(`/admin/accounts/${id}/models`, { signal })
  return data.map(model => model.id)
}

async function listRuns(params: { page: number; page_size: number; batch_id?: string; status?: PelicanBenchmarkStatus }, signal?: AbortSignal): Promise<Page> {
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

export async function startPelicanTests(tests: PelicanBenchmarkTest[], signal?: AbortSignal): Promise<PelicanBenchmarkSnapshot & { skipped: PelicanBenchmarkSkipped[] }> {
  const items: PelicanBenchmarkRun[] = []
  const skipped: PelicanBenchmarkSkipped[] = []
  const groups = new Map<string, number[]>()
  for (const test of tests) {
    const ids = groups.get(test.model) || []
    ids.push(test.account_id)
    groups.set(test.model, ids)
  }
  try {
    for (const [model, ids] of groups) {
      for (let offset = 0; offset < ids.length; offset += 2000) {
        const account_ids = ids.slice(offset, offset + 2000)
        const single = tests.length === 1
        const { data } = await apiClient.post<PelicanBenchmarkCreated>(single ? `/admin/accounts/${account_ids[0]}/pelican-benchmark` : base, single ? { model } : { account_ids, model }, { signal })
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
  const data = await listRuns({ page, page_size: 20, ...(status ? { status } : {}) }, signal)
  return { ...data, pages: Math.ceil(data.total / data.page_size) }
}

export async function getPelicanResult(id: string, signal?: AbortSignal): Promise<PelicanBenchmarkRun & { html: string }> {
  const { data } = await apiClient.get<PelicanBenchmarkRun & { html: string }>(`${base}/${encodeURIComponent(id)}`, { signal })
  return data
}
