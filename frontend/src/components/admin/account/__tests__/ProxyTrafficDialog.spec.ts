import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ProxyTrafficDialog from '../ProxyTrafficDialog.vue'
import zh from '@/i18n/locales/zh/admin/accounts'
import type { Account } from '@/types'

const { getTraffic } = vi.hoisted(() => ({ getTraffic: vi.fn() }))
vi.mock('@/api/admin/proxies', () => ({ getTraffic }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, values: Record<string, unknown> = {}) => {
  if (key.startsWith('common.')) return key
  const name = key.split('.').pop() as keyof typeof zh.accounts.traffic
  return (zh.accounts.traffic[name] ?? key).replace(/\{(\w+)\}/g, (_, id: string) => String(values[id] ?? ''))
} }) }))
const fixture = () => ({
  started_at: '2026-11-20T00:00:00Z', period_start: '2026-10-22T00:00:00+08:00', period_end: '2026-11-20T10:00:00+08:00',
  last_saved_at: '2026-11-20T02:00:00Z', persistence_ok: true, days: 30,
  nodes: [
    { id: 'jp', name: '[Subscription] Subscription 1 / Japan HY2', protocol: 'hysteria2', proxy_ids: [84, 163], started_at: '2026-11-20T00:00:00Z', upload_bytes: 1e9, download_bytes: 2e9, total_bytes: 3e9 },
    { id: 'sg', name: 'Singapore', protocol: 'socks5', proxy_ids: [168], started_at: '2026-11-20T00:00:00Z', upload_bytes: 1e9, download_bytes: 1e9, total_bytes: 2e9 }
  ]
})
function render(account?: Account) {
  return mount(ProxyTrafficDialog, { props: { account }, global: {
    stubs: { BaseDialog: { template: '<section><slot /></section>' }, Icon: true }
  } })
}
describe('ProxyTrafficDialog', () => {
  beforeEach(() => { getTraffic.mockReset(); getTraffic.mockResolvedValue(fixture()) })
  it('loads only on open, deduplicates aliases and filters names without fetching again', async () => {
    const wrapper = render({ name: 'Account', proxy_id: 84, proxy_bindings: [{ proxy_id: 163 }] } as Account)
    await flushPromises()
    expect(getTraffic).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="traffic-total"]').text()).toBe('3.00 GB')
    await wrapper.findAll('button').find(b => b.text() === '全部节点')!.trigger('click')
    expect(wrapper.get('[data-testid="traffic-total"]').text()).toBe('5.00 GB')
    await wrapper.get('input[type="search"]').setValue('Japan')
    expect(wrapper.get('[data-testid="traffic-total"]').text()).toBe('3.00 GB')
    expect(getTraffic).toHaveBeenCalledTimes(1)
    await wrapper.get('input[type="number"]').setValue('0.5')
    expect(wrapper.text()).toContain('1.5000')
    expect(wrapper.text()).toContain('不能补算')
    wrapper.unmount()
  })
  it('changes period, exposes persistence failures, and does not display stale totals on failure', async () => {
    const wrapper = render()
    await flushPromises()
    getTraffic.mockResolvedValueOnce({ ...fixture(), persistence_ok: false, days: 7 })
    await wrapper.findAll('button').find(b => b.text() === '近 7 天')!.trigger('click')
    await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(7, expect.any(AbortSignal))
    expect(wrapper.get('[role="alert"]').text()).toContain('保存失败')
    getTraffic.mockRejectedValueOnce(new Error('unavailable'))
    await wrapper.findAll('button').find(b => b.text() === '今日')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('暂时无法')
    expect(wrapper.find('[data-testid="traffic-total"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
