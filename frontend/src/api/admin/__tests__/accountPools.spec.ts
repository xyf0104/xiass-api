import { beforeEach, describe, expect, it, vi } from 'vitest'
import { accountPoolsAPI, getAccountPoolAccounts } from '../accountPools'

const { get, post, put, del, list } = vi.hoisted(() => ({
  get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn(), list: vi.fn()
}))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put, delete: del } }))
vi.mock('@/api/admin/accounts', () => ({ list }))

const pool = { id: 7, name: '混合号池', proxy_id: 9, account_ids: [1, 2], account_count: 2 }

describe('account pools API contract', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    post.mockResolvedValue({ data: pool })
    put.mockResolvedValue({ data: pool })
    del.mockResolvedValue({ data: { deleted: true } })
  })

  it('reads the items envelope and normalizes empty pools and membership arrays', async () => {
    const signal = new AbortController().signal
    get.mockResolvedValueOnce({ data: { items: [pool] } })
    expect(await accountPoolsAPI.list(signal)).toEqual({ items: [pool] })
    expect(get).toHaveBeenCalledWith('/admin/account-pools', { signal })
    get.mockResolvedValueOnce({ data: { items: null } })
    expect(await accountPoolsAPI.list()).toEqual({ items: [] })
    get.mockResolvedValueOnce({ data: { items: [{ ...pool, account_ids: null, account_count: 0 }] } })
    expect((await accountPoolsAPI.list()).items[0].account_ids).toEqual([])
  })

  it('creates named empty pools with an explicit direct proxy and renames without changing proxy', async () => {
    expect(await accountPoolsAPI.create({ name: '号池分组1', proxy_id: null })).toEqual(pool)
    expect(post).toHaveBeenCalledExactlyOnceWith('/admin/account-pools', { name: '号池分组1', proxy_id: null })
    expect(await accountPoolsAPI.rename(7, '自定义名称')).toEqual(pool)
    expect(put).toHaveBeenCalledExactlyOnceWith('/admin/account-pools/7', { name: '自定义名称' })
  })

  it('uses the member endpoint for add and remove, with no independent account proxy writes', async () => {
    await accountPoolsAPI.assign(7, [1, 2])
    await accountPoolsAPI.assign(7, [2], true)
    expect(post).toHaveBeenNthCalledWith(1, '/admin/account-pools/7/accounts', { account_ids: [1, 2], remove: false })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/account-pools/7/accounts', { account_ids: [2], remove: true })
    expect(put).not.toHaveBeenCalled()
  })

  it.each([null, 9])('writes proxy_id explicitly for %s to the pool-wide endpoint', async proxyId => {
    expect(await accountPoolsAPI.setProxy(7, proxyId)).toEqual(pool)
    expect(put).toHaveBeenCalledExactlyOnceWith('/admin/account-pools/7/proxy', { proxy_id: proxyId })
    expect(JSON.parse(JSON.stringify(put.mock.calls[0][1]))).toHaveProperty('proxy_id', proxyId)
  })

  it('deletes only the pool', async () => {
    await accountPoolsAPI.delete(7)
    expect(del).toHaveBeenCalledExactlyOnceWith('/admin/account-pools/7')
    expect(post).not.toHaveBeenCalled()
  })

  it('preserves server duplicate-name errors', async () => {
    const error = { reason: 'ACCOUNT_POOL_NAME_TAKEN', message: 'account pool name already exists' }
    post.mockRejectedValueOnce(error)
    await expect(accountPoolsAPI.create({ name: pool.name, proxy_id: null })).rejects.toBe(error)
  })

  it('fetches every full page, keeps pro/plus/team and other platforms, and never requests lite or secrets', async () => {
    const rows = ['pro', 'plus', 'team'].map((plan, index) => ({
      id: index + 1, name: plan, proxy_id: 9, platform: 'openai', type: 'oauth',
      credentials: { plan_type: plan }, execution_node_id: 'node-a'
    }))
    const other = { id: 4, name: 'Claude', platform: 'anthropic', type: 'apikey', proxy_id: null }
    list.mockResolvedValueOnce({ items: rows.slice(0, 2), pages: 2, total: 4, page_size: 2 })
      .mockResolvedValueOnce({ items: [rows[1], rows[2], other], pages: 2, total: 4, page_size: 2 })
    const signal = new AbortController().signal
    expect(await getAccountPoolAccounts(signal)).toEqual([...rows, other])
    expect(list).toHaveBeenCalledTimes(2)
    for (let page = 1; page <= 2; page++) {
      expect(list).toHaveBeenNthCalledWith(page, page, 200, { sort_by: 'id', sort_order: 'asc', include_scheduler_score: '0' }, { signal })
    }
    expect(get).not.toHaveBeenCalled()
  })

  it('uses the returned page size to calculate pages when pages is absent', async () => {
    list.mockResolvedValueOnce({ items: [{ id: 1 }], total: 2, page_size: 1 })
      .mockResolvedValueOnce({ items: [{ id: 2 }], total: 2, page_size: 1 })
    expect(await getAccountPoolAccounts()).toEqual([{ id: 1 }, { id: 2 }])
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('rejects a later-page failure instead of returning a partial selection list', async () => {
    list.mockResolvedValueOnce({ items: [{ id: 1 }], pages: 2 })
      .mockRejectedValueOnce(new Error('page unavailable'))
    await expect(getAccountPoolAccounts()).rejects.toThrow('page unavailable')
  })

  it('rejects non-progressing or missing intermediate pages', async () => {
    list.mockResolvedValue({ items: [{ id: 1 }], pages: 3 })
    await expect(getAccountPoolAccounts()).rejects.toThrow('账号分页数据不完整')
    expect(list).toHaveBeenCalledTimes(2)
    list.mockReset().mockResolvedValue({ items: [], pages: 2 })
    await expect(getAccountPoolAccounts()).rejects.toThrow('账号分页数据不完整')
  })

  it('accepts an empty account list and stops pagination after cancellation', async () => {
    list.mockResolvedValueOnce({ items: [], pages: 0, total: 0 })
    expect(await getAccountPoolAccounts()).toEqual([])
    const controller = new AbortController()
    list.mockImplementationOnce(async () => {
      controller.abort()
      return { items: [{ id: 1 }], pages: 2 }
    })
    await expect(getAccountPoolAccounts(controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(list).toHaveBeenCalledTimes(2)
  })
})
