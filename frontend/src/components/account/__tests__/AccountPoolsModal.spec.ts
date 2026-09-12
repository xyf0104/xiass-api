import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import AccountPoolsModal from '../AccountPoolsModal.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { AccountPool } from '@/api/admin/accountPools'
import type { Account, Proxy } from '@/types'

const { list, create, rename, del, assign, setProxy, getAccounts } = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), rename: vi.fn(), del: vi.fn(), assign: vi.fn(), setProxy: vi.fn(), getAccounts: vi.fn()
}))
vi.mock('@/api/admin/accountPools', () => ({
  accountPoolsAPI: { list, create, rename, delete: del, assign, setProxy }, getAccountPoolAccounts: getAccounts
}))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { testProxy: vi.fn() } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const proxies = [
  { id: 9, name: '节点九', protocol: 'socks5', host: 'nine.example.test', port: 1080 },
  { id: 10, name: '节点十', protocol: 'http', host: 'ten.example.test', port: 8080 }
] as Proxy[]
let pools: AccountPool[]
let accounts: Account[]
let wrapper: VueWrapper | undefined

function row(id: number, plan: string, proxyId: number | null = 9): Account {
  return { id, name: `${plan}账号`, platform: 'openai', type: 'oauth', proxy_id: proxyId,
    execution_node_id: 'node-a', credentials: { plan_type: plan } } as Account
}

function element(testId: string) {
  const target = document.querySelector(`[data-testid="${testId}"]`)
  if (!target) throw new Error(`Missing element: ${testId}`)
  return new DOMWrapper(target)
}

function confirmDialog() {
  const dialogs = document.querySelectorAll('[role="dialog"]')
  expect(dialogs).toHaveLength(2)
  return new DOMWrapper(dialogs[1])
}

async function confirm(cancel = false) {
  const button = confirmDialog().findAll('button').find(button => button.text() === (cancel ? '取消' : '确认'))
  if (!button) throw new Error('Missing confirmation button')
  await button.trigger('click')
  await flushPromises()
}

async function open(show = true) {
  wrapper = mount(AccountPoolsModal, { attachTo: document.body, props: { show, proxies }, global: { stubs: { Icon: true } } })
  await flushPromises()
  return wrapper
}

async function selectAccount(id: number) {
  await element(`pool-account-${id}`).get('input').setValue(true)
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

describe('AccountPoolsModal', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    pools = [
      { id: 7, name: '号池分组1', proxy_id: 9, account_ids: [1], account_count: 1 },
      { id: 8, name: '其他号池', proxy_id: 10, account_ids: [3], account_count: 1 }
    ]
    accounts = [row(1, 'pro'), row(2, 'plus', null), row(3, 'team', 10)]
    list.mockImplementation(async () => ({ items: structuredClone(pools) }))
    getAccounts.mockImplementation(async () => structuredClone(accounts))
    create.mockImplementation(async input => {
      const pool = { ...input, id: 9, account_ids: [], account_count: 0 }
      pools.push(pool)
      return pool
    })
    rename.mockImplementation(async (id, name) => {
      const pool = pools.find(pool => pool.id === id)!
      pool.name = name
      return pool
    })
    assign.mockImplementation(async (id, ids: number[], remove: boolean) => {
      const target = pools.find(pool => pool.id === id)!
      for (const pool of pools) {
        pool.account_ids = pool.account_ids.filter(accountId => !ids.includes(accountId))
        if (pool.id === id && !remove) pool.account_ids.push(...ids)
        pool.account_count = pool.account_ids.length
      }
      if (!remove) accounts.filter(account => ids.includes(account.id)).forEach(account => { account.proxy_id = target.proxy_id })
      return target
    })
    setProxy.mockImplementation(async (id, proxyId) => {
      const pool = pools.find(pool => pool.id === id)!
      pool.proxy_id = proxyId
      accounts.filter(account => pool.account_ids.includes(account.id)).forEach(account => { account.proxy_id = proxyId })
      return pool
    })
    del.mockImplementation(async id => { pools = pools.filter(pool => pool.id !== id) })
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('does not fetch while hidden; opens with the complete independent account list', async () => {
    await open(false)
    expect(list).not.toHaveBeenCalled()
    await wrapper!.setProps({ show: true })
    await flushPromises()
    expect(getAccounts).toHaveBeenCalledExactlyOnceWith(expect.any(AbortSignal))
    expect(element('pool-account-1').text()).toContain('pro')
    expect(element('pool-account-2').text()).toContain('plus')
    expect(element('pool-account-3').text()).toContain('team')
    expect(element('pool-account-3').text()).toContain('节点十')
    expect(element('pool-account-3').text()).toContain('其他号池')
    expect(element('pool-account-1').text()).toContain('所属服务器：node-a')
    expect(element('pool-account-1').text()).toContain('代理出口：节点九')
  })

  it('shows 本机 for missing server IDs and preserves api/api2 server names independently of proxies', async () => {
    accounts[0].execution_node_id = undefined
    accounts[1].execution_node_id = 'api'
    accounts[2].execution_node_id = 'api2'
    await open()
    expect(element('pool-account-1').text()).toContain('所属服务器：本机')
    expect(element('pool-account-2').text()).toContain('所属服务器：api')
    expect(element('pool-account-3').text()).toContain('所属服务器：api2')
    expect(element('pool-account-3').text()).toContain('代理出口：节点十')
  })

  it('disables shadow/read-only rows and excludes them from select-all and mutation payloads', async () => {
    accounts.push(
      { ...row(4, 'shadow'), parent_account_id: 1 },
      { ...row(5, 'flagged shadow'), credential_shadow: true } as Account,
      { ...row(6, 'read only'), read_only: true } as Account,
      { ...row(7, 'extra shadow'), extra: { credential_shadow: true } },
      { ...row(8, 'extra read only'), extra: { read_only: true } }
    )
    await open()
    for (const id of [4, 5, 6, 7, 8]) {
      expect(element(`pool-account-${id}`).get('input').attributes('disabled')).toBeDefined()
      expect(element(`pool-account-${id}`).text()).toContain('不可选择')
    }
    await element('pool-select-all').setValue(true)
    expect(element('pool-add').text()).toContain('(2)')
    await element('pool-add').trigger('click')
    await confirm()
    expect(assign).toHaveBeenCalledExactlyOnceWith(7, [2, 3], false)
    await element('pool-search').setValue('shadow')
    expect(element('pool-select-all').attributes('disabled')).toBeDefined()
  })

  it('creates an empty pool using 号池分组1 and explicit null, then allows custom renaming', async () => {
    pools = []
    await open()
    expect((element('pool-name').element as HTMLInputElement).value).toBe('号池分组1')
    expect(document.body.textContent).toContain('暂无号池')
    await element('pool-save').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledExactlyOnceWith({ name: '号池分组1', proxy_id: null })
    expect(wrapper!.emitted('updated')).toHaveLength(1)
    expect(document.body.textContent).toContain('当前号池暂无账号')
    await element('pool-name').setValue('  自定义混合池  ')
    await element('pool-save').trigger('submit')
    await flushPromises()
    expect(rename).toHaveBeenCalledExactlyOnceWith(9, '自定义混合池')
    expect(setProxy).not.toHaveBeenCalled()
    expect(wrapper!.emitted('updated')).toHaveLength(2)
  })

  it('chooses the next free default name and uses the chosen proxy for a new pool', async () => {
    await open()
    await element('pool-new').trigger('click')
    expect((element('pool-name').element as HTMLInputElement).value).toBe('号池分组2')
    wrapper!.getComponent(ProxySelector).vm.$emit('update:modelValue', 10)
    await nextTick()
    expect(setProxy).not.toHaveBeenCalled()
    await element('pool-save').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledWith({ name: '号池分组2', proxy_id: 10 })
  })

  it('rejects blank and duplicate names locally and exposes server duplicate-name errors without losing the draft', async () => {
    await open()
    await element('pool-name').setValue('   ')
    expect(element('pool-save').attributes('disabled')).toBeDefined()
    await element('pool-name').setValue('其他号池')
    await element('pool-save').trigger('submit')
    expect(rename).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('号池名称已存在')
    rename.mockRejectedValueOnce({ reason: 'ACCOUNT_POOL_NAME_TAKEN' })
    await element('pool-name').setValue('远端重复名称')
    await element('pool-save').trigger('submit')
    await flushPromises()
    expect(document.body.textContent).toContain('号池名称已存在')
    expect((element('pool-name').element as HTMLInputElement).value).toBe('远端重复名称')
    expect(wrapper!.emitted('updated')).toBeUndefined()
  })

  it('filters by current pool and search, clearing hidden selections and resetting on pool changes', async () => {
    await open()
    await element('pool-select-all').setValue(true)
    expect(element('pool-add').text()).toContain('(2)')
    await new DOMWrapper(document.querySelector('input[value="pool"]')!).setValue(true)
    expect(document.querySelectorAll('[data-testid^="pool-account-"]')).toHaveLength(1)
    expect(element('pool-add').attributes('disabled')).toBeDefined()
    await new DOMWrapper(document.querySelector('input[value="all"]')!).setValue(true)
    await element('pool-search').setValue('节点十')
    expect(document.querySelectorAll('[data-testid^="pool-account-"]')).toHaveLength(1)
    expect(element('pool-account-3').text()).toContain('team')
    await selectAccount(3)
    await element('pool-select').setValue('8')
    expect(element('pool-remove').attributes('disabled')).toBeDefined()
    await element('pool-search').setValue('no-match')
    expect(document.body.textContent).toContain('没有匹配的账号')
  })

  it('confirms multi-account moves and proxy inheritance, with cancellation making no request', async () => {
    const nativeConfirm = vi.spyOn(window, 'confirm')
    await open()
    await selectAccount(2)
    await selectAccount(3)
    await element('pool-add').trigger('click')
    expect(assign).not.toHaveBeenCalled()
    expect(confirmDialog().text()).toContain('2 个账号')
    expect(confirmDialog().text()).toContain('代理覆盖为节点九')
    await confirm(true)
    expect(assign).not.toHaveBeenCalled()
    await element('pool-add').trigger('click')
    await confirm()
    expect(assign).toHaveBeenCalledExactlyOnceWith(7, [2, 3], false)
    expect(element('pool-account-2').text()).toContain('节点九')
    expect(element('pool-account-3').text()).toContain('号池分组1')
    expect(pools[1].account_count).toBe(0)
    expect(setProxy).not.toHaveBeenCalled()
    expect(nativeConfirm).not.toHaveBeenCalled()
    expect(wrapper!.emitted('updated')).toHaveLength(1)
  })

  it('requires confirmation before clearing the proxy via the real ProxySelector, updating every member', async () => {
    pools[0].account_ids = [1, 2]
    pools[0].account_count = 2
    await open()
    await selectAccount(1)
    await wrapper!.getComponent(ProxySelector).get('button.select-trigger').trigger('click')
    await nextTick()
    await new DOMWrapper(document.querySelector('.select-option')!).trigger('click')
    expect(setProxy).not.toHaveBeenCalled()
    expect(confirmDialog().text()).toContain('全部 2 个现有成员')
    expect(confirmDialog().text()).toContain('直连（无代理）')
    await confirm()
    expect(setProxy).toHaveBeenCalledExactlyOnceWith(7, null)
    expect(element('pool-account-1').text()).toContain('直连（无代理）')
    expect(element('pool-account-2').text()).toContain('直连（无代理）')
    expect(assign).not.toHaveBeenCalled()
  })

  it('keeps the existing proxy when proxy confirmation is canceled', async () => {
    await open()
    wrapper!.getComponent(ProxySelector).vm.$emit('update:modelValue', 10)
    await nextTick()
    await confirm(true)
    expect(setProxy).not.toHaveBeenCalled()
    expect(wrapper!.getComponent(ProxySelector).props('modelValue')).toBe(9)
  })

  it('confirms removal of only selected current members and leaves their proxy intact', async () => {
    await open()
    await element('pool-select-all').setValue(true)
    await element('pool-remove').trigger('click')
    expect(assign).not.toHaveBeenCalled()
    expect(confirmDialog().text()).toContain('1 个成员')
    expect(confirmDialog().text()).toContain('当前代理保持不变')
    await confirm()
    expect(assign).toHaveBeenCalledExactlyOnceWith(7, [1], true)
    expect(element('pool-account-1').text()).toContain('未分配')
    expect(element('pool-account-1').text()).toContain('节点九')
    expect(setProxy).not.toHaveBeenCalled()
  })

  it('confirms deletion, retains accounts and proxies, and handles deleting the last pool', async () => {
    pools = [pools[0]]
    await open()
    await element('pool-delete').trigger('click')
    expect(del).not.toHaveBeenCalled()
    expect(confirmDialog().text()).toContain('不会删除账号')
    await confirm(true)
    expect(del).not.toHaveBeenCalled()
    await element('pool-delete').trigger('click')
    await confirm()
    expect(del).toHaveBeenCalledExactlyOnceWith(7)
    expect(accounts).toHaveLength(3)
    expect(accounts[0].proxy_id).toBe(9)
    expect(document.body.textContent).toContain('暂无号池')
    expect(wrapper!.emitted('updated')).toHaveLength(1)
  })

  it('disables duplicate submissions and closing while a confirmed mutation is pending', async () => {
    const pending = deferred<AccountPool>()
    assign.mockReturnValueOnce(pending.promise)
    await open()
    await selectAccount(2)
    await element('pool-add').trigger('click')
    const button = confirmDialog().findAll('button').find(button => button.text() === '确认')!
    await button.trigger('click')
    await button.trigger('click')
    expect(assign).toHaveBeenCalledTimes(1)
    expect(wrapper!.getComponent(ConfirmDialog).props('show')).toBe(false)
    expect(document.querySelectorAll('fieldset:disabled')).toHaveLength(2)
    expect(wrapper!.getComponent(ProxySelector).props('disabled')).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(wrapper!.emitted('close')).toBeUndefined()
    pending.resolve(pools[0])
    await flushPromises()
    expect(document.querySelectorAll('fieldset:disabled')).toHaveLength(0)
    expect(wrapper!.emitted('updated')).toHaveLength(1)
  })

  it('retries failed loading without exposing a partial actionable account list', async () => {
    getAccounts.mockRejectedValueOnce(new Error('第二页读取失败'))
    await open()
    expect(document.body.textContent).toContain('第二页读取失败')
    expect(document.querySelector('fieldset:disabled')).not.toBeNull()
    expect(document.querySelectorAll('[data-testid^="pool-account-"]')).toHaveLength(0)
    await element('pool-retry').trigger('click')
    await flushPromises()
    expect(getAccounts).toHaveBeenCalledTimes(2)
    expect(document.querySelectorAll('[data-testid^="pool-account-"]')).toHaveLength(3)
    expect(document.querySelector('[role="alert"]')).toBeNull()
  })

  it('retains selections after mutation failure and requires confirmation again on retry', async () => {
    assign.mockRejectedValueOnce(new Error('暂时不可用'))
    await open()
    await selectAccount(2)
    await element('pool-add').trigger('click')
    await confirm()
    expect(document.body.textContent).toContain('暂时不可用')
    expect(wrapper!.emitted('updated')).toBeUndefined()
    expect(element('pool-account-2').get('input').element).toHaveProperty('checked', true)
    await element('pool-add').trigger('click')
    expect(assign).toHaveBeenCalledTimes(1)
    await confirm()
    expect(assign).toHaveBeenCalledTimes(2)
    expect(wrapper!.emitted('updated')).toHaveLength(1)
  })

  it('reports successful mutation separately from a failed refresh and retries reads without replaying writes', async () => {
    await open()
    list.mockRejectedValueOnce(new Error('刷新失败'))
    await element('pool-name').setValue('已保存名称')
    await element('pool-save').trigger('submit')
    await flushPromises()
    expect(wrapper!.emitted('updated')).toHaveLength(1)
    expect(document.body.textContent).toContain('刷新失败')
    await element('pool-retry').trigger('click')
    await flushPromises()
    expect(rename).toHaveBeenCalledTimes(1)
    expect((element('pool-name').element as HTMLInputElement).value).toBe('已保存名称')
  })

  it('aborts hidden loads and ignores stale completions after reopening', async () => {
    const stale = deferred<Account[]>()
    getAccounts.mockReturnValueOnce(stale.promise)
    await open()
    const signal = getAccounts.mock.calls[0][0] as AbortSignal
    await wrapper!.setProps({ show: false })
    expect(signal.aborted).toBe(true)
    await wrapper!.setProps({ show: true })
    await flushPromises()
    stale.resolve([row(99, 'stale')])
    await flushPromises()
    expect(document.querySelector('[data-testid="pool-account-99"]')).toBeNull()
    expect(document.querySelectorAll('[data-testid^="pool-account-"]')).toHaveLength(3)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(wrapper!.emitted('close')).toHaveLength(1)
  })
})
