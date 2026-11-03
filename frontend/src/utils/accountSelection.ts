import type { AccountPlatform, AccountType } from '@/types'

interface AccountSelectionRow {
  id: number
  platform?: AccountPlatform
  type?: AccountType
}

interface AccountListPage {
  items: AccountSelectionRow[]
  total: number
  pages?: number
}

type AccountPageFetcher = (
  page: number,
  pageSize: number,
  filters: Record<string, unknown>
) => Promise<AccountListPage>

const SELECT_ALL_PAGE_SIZE = 1000

async function fetchAllAccountRows(
  fetchPage: AccountPageFetcher,
  filters: Record<string, unknown>
): Promise<AccountSelectionRow[]> {
  const requestFilters = {
    ...filters,
    lite: '1',
    include_scheduler_score: '0'
  }
  const firstPage = await fetchPage(1, SELECT_ALL_PAGE_SIZE, requestFilters)
  const pageCount = Math.max(
    firstPage.pages ?? 0,
    Math.ceil(firstPage.total / SELECT_ALL_PAGE_SIZE)
  )
  const rows = [...firstPage.items]

  for (let page = 2; page <= pageCount; page++) {
    const result = await fetchPage(page, SELECT_ALL_PAGE_SIZE, requestFilters)
    rows.push(...result.items)
  }

  const rowsByID = new Map(rows.map(account => [account.id, account]))
  if (rowsByID.size !== firstPage.total) {
    throw new Error('账号列表结果不完整')
  }
  return Array.from(rowsByID.values())
}

export interface AccountSelectionSnapshot {
  accounts: Array<{
    id: number
    platform: AccountPlatform
    type: AccountType
  }>
  ids: number[]
  selectedPlatforms: AccountPlatform[]
  selectedTypes: AccountType[]
}

export async function fetchAllAccountSelection(
  fetchPage: AccountPageFetcher,
  filters: Record<string, unknown>
): Promise<AccountSelectionSnapshot> {
  const rows = await fetchAllAccountRows(fetchPage, filters)
  if (rows.some(account => !account.platform || !account.type)) {
    throw new Error('账号列表元数据不完整')
  }

  const accounts = rows.map(account => ({
    id: account.id,
    platform: account.platform as AccountPlatform,
    type: account.type as AccountType,
  }))

  return {
    accounts,
    ids: accounts.map(account => account.id),
    selectedPlatforms: Array.from(new Set(accounts.map(account => account.platform))),
    selectedTypes: Array.from(new Set(accounts.map(account => account.type)))
  }
}

export async function fetchAllAccountIds(
  fetchPage: AccountPageFetcher,
  filters: Record<string, unknown>
): Promise<number[]> {
  return (await fetchAllAccountRows(fetchPage, filters)).map(account => account.id)
}
