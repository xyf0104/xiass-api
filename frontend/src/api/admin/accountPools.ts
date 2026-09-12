import { apiClient } from '../client'
import { list as listAccounts } from './accounts'
import type { Account } from '@/types'

export interface AccountPool {
  id: number
  name: string
  proxy_id: number | null
  account_ids: number[]
  account_count: number
}

const path = '/admin/account-pools'

export const accountPoolsAPI = {
  async list(signal?: AbortSignal): Promise<{ items: AccountPool[] }> {
    const { data } = await apiClient.get<{ items: AccountPool[] | null }>(path, { signal })
    return { items: (data.items ?? []).map(pool => ({ ...pool, account_ids: pool.account_ids ?? [] })) }
  },
  async create(input: { name: string; proxy_id: number | null }): Promise<AccountPool> {
    return (await apiClient.post<AccountPool>(path, input)).data
  },
  async rename(id: number, name: string): Promise<AccountPool> {
    return (await apiClient.put<AccountPool>(`${path}/${id}`, { name })).data
  },
  async delete(id: number): Promise<void> {
    await apiClient.delete(`${path}/${id}`)
  },
  async assign(id: number, accountIds: number[], remove = false): Promise<AccountPool> {
    return (await apiClient.post<AccountPool>(`${path}/${id}/accounts`, {
      account_ids: accountIds,
      remove
    })).data
  },
  async setProxy(id: number, proxyId: number | null): Promise<AccountPool> {
    return (await apiClient.put<AccountPool>(`${path}/${id}/proxy`, { proxy_id: proxyId })).data
  }
}

/** Read complete, redacted account rows independently of the parent table's filters. */
export async function getAccountPoolAccounts(signal?: AbortSignal): Promise<Account[]> {
  const accounts = new Map<number, Account>()
  let pages = 1
  for (let page = 1; page <= pages; page++) {
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    const result = await listAccounts(page, 200, {
      sort_by: 'id', sort_order: 'asc', include_scheduler_score: '0'
    }, { signal })
    pages = result.pages ?? Math.ceil(result.total / (result.page_size || 200))
    const previousSize = accounts.size
    for (const account of result.items) accounts.set(account.id, account)
    if ((result.items.length === 0 && page < pages) || (result.items.length > 0 && accounts.size === previousSize)) {
      throw new Error('账号分页数据不完整，请刷新重试。')
    }
  }
  return [...accounts.values()]
}
