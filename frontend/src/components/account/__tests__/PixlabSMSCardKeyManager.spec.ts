import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import PixlabSMSCardKeyManager from '../PixlabSMSCardKeyManager.vue'

const { showError, showInfo, showSuccess } = vi.hoisted(() => ({
  showError: vi.fn(), showInfo: vi.fn(), showSuccess: vi.fn(),
}))

vi.mock('@/stores', () => ({ useAppStore: () => ({ showError, showInfo, showSuccess }) }))
vi.mock('@/api/admin/smsReceiver', () => ({
  listCardKeys: vi.fn(),
  addCardKeys: vi.fn(),
  getProviderConfig: vi.fn(),
  updateProviderConfig: vi.fn(),
  clearCardKeys: vi.fn(),
  deleteCardKey: vi.fn(),
  reorderCardKeys: vi.fn(),
}))

enableAutoUnmount(afterEach)
afterEach(() => vi.restoreAllMocks())

let serverKeys: smsReceiverAPI.SMSReceiverCardKey[]

function card(id: number, status: string, claimCount: number, provider?: smsReceiverAPI.SMSReceiverProvider): smsReceiverAPI.SMSReceiverCardKey {
  return { id, card_key: `test-card-${id}`, status, claim_count: claimCount, queue_rank: id, provider, created_at: '2026-10-29T00:00:00Z' }
}

async function render() {
  const wrapper = mount(PixlabSMSCardKeyManager, { global: { stubs: { Icon: true } } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.resetAllMocks()
  serverKeys = [card(1, 'queued', 12), card(2, 'active', 6, 'giema'), card(3, 'exhausted', 8)]
  vi.mocked(smsReceiverAPI.getProviderConfig).mockResolvedValue({ giema_api_key_configured: false })
  vi.mocked(smsReceiverAPI.listCardKeys).mockImplementation(async () => ({
    card_keys: serverKeys.map(key => ({ ...key })),
    queued_count: serverKeys.filter(key => key.status === 'queued').length,
    active_count: serverKeys.filter(key => key.status === 'active').length,
  }))
})

describe('PixlabSMSCardKeyManager', () => {
  it('shows local historical claim attempts without a five-claim cap or changing server status', async () => {
    const wrapper = await render()

    expect(wrapper.text()).toContain('本地历史领号尝试')
    expect(wrapper.text()).toContain('次数不代表服务商总额度或剩余次数')
    expect(wrapper.text()).not.toMatch(/\/\s*5/)
    expect(wrapper.get('[data-testid="sms-card-key-1"]').text()).toContain('12 次')
    expect(wrapper.get('[data-testid="sms-card-key-1"]').text()).toContain('待用')
    expect(wrapper.get('[data-testid="sms-card-key-2"]').text()).toContain('6 次')
    expect(wrapper.get('[data-testid="sms-card-key-2"]').text()).toContain('使用中')
    expect(smsReceiverAPI.addCardKeys).not.toHaveBeenCalled()
  })

  it('keeps Giema disabled until configured and leaves the secret field empty on load', async () => {
    const wrapper = await render()

    expect(wrapper.get('[data-testid="sms-provider"]').element.value).toBe('pixlab')
    expect(wrapper.get('[data-testid="sms-provider"] option[value="giema"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="giema-config"]').exists()).toBe(false)
    await wrapper.findAll('button').find(button => button.text() === '配置 Giema')!.trigger('click')
    expect(wrapper.get('[data-testid="giema-config-status"]').text()).toContain('未配置')
    expect(wrapper.get('[data-testid="giema-api-key"]').element.value).toBe('')
    expect(JSON.stringify(wrapper.html())).not.toContain('secret')
  })

  it('saves the Giema key through the provider endpoint, clears the field, and never echoes the secret', async () => {
    vi.mocked(smsReceiverAPI.updateProviderConfig).mockResolvedValue({ giema_api_key_configured: true })
    const wrapper = await render()
    await wrapper.findAll('button').find(button => button.text() === '配置 Giema')!.trigger('click')
    await wrapper.get('[data-testid="giema-api-key"]').setValue('giema-secret-value')
    await wrapper.get('[data-testid="save-giema-key"]').trigger('click')
    await flushPromises()

    expect(smsReceiverAPI.updateProviderConfig).toHaveBeenCalledExactlyOnceWith('giema-secret-value')
    expect(wrapper.get('[data-testid="giema-api-key"]').element.value).toBe('')
    expect(wrapper.text()).not.toContain('giema-secret-value')
    expect(JSON.stringify(localStorage)).not.toContain('giema-secret-value')
    expect(showSuccess).toHaveBeenCalledWith('Giema API 密钥已保存。')
    expect(wrapper.get('[data-testid="sms-provider"] option[value="giema"]').attributes('disabled')).toBeUndefined()
  })

  it('shows legacy cards as Pixlab and forwards an explicit Giema provider when adding cards', async () => {
    vi.mocked(smsReceiverAPI.getProviderConfig).mockResolvedValue({ giema_api_key_configured: true })
    vi.mocked(smsReceiverAPI.addCardKeys).mockResolvedValue({ added_count: 2, queued_count: 3 })
    const wrapper = await render()
    expect(wrapper.get('[data-testid="sms-card-key-1"] [data-testid="card-provider"]').text()).toBe('Pixlab')
    expect(wrapper.get('[data-testid="sms-card-key-2"] [data-testid="card-provider"]').text()).toBe('Giema')
    await wrapper.get('[data-testid="sms-provider"]').setValue('giema')
    await wrapper.get('textarea').setValue('giema-card-a\ngiema-card-b')
    await wrapper.findAll('button').find(button => button.text() === '加密保存卡密')!.trigger('click')
    await flushPromises()

    expect(smsReceiverAPI.addCardKeys).toHaveBeenCalledExactlyOnceWith('giema-card-a\ngiema-card-b', 'giema')
    expect(wrapper.text()).not.toContain('giema-card-a')
  })

  it('labels exhausted cards as retired without claiming that provider credit is depleted', async () => {
    const wrapper = await render()
    const retired = wrapper.get('[data-testid="sms-card-key-3"]')

    expect(retired.text()).toContain('已停用')
    expect(retired.text()).toContain('8 次')
    expect(retired.text()).not.toContain('已耗尽')
    expect(wrapper.text()).toContain('可能因服务商拒绝或不可用，不代表服务商总额度已耗尽')
    expect(wrapper.text()).toContain('重新添加已停用的卡密可使其重新入队，在下次领取时向服务商再次核验')
    expect(wrapper.text()).toContain('待用或使用中的重复卡密仍会忽略')
    expect(smsReceiverAPI.addCardKeys).not.toHaveBeenCalled()
    expect(smsReceiverAPI.deleteCardKey).not.toHaveBeenCalled()
  })

  it('shows the server-requeued card with its preserved claim history after explicit reimport', async () => {
    vi.mocked(smsReceiverAPI.addCardKeys).mockImplementation(async () => {
      serverKeys[2] = { ...serverKeys[2], status: 'queued' }
      return { added_count: 1, queued_count: 2 }
    })
    const wrapper = await render()
    const input = 'test-card-3\ntest-card-1\ntest-card-2'
    await wrapper.get('textarea').setValue(input)
    await wrapper.findAll('button').find(button => button.text() === '加密保存卡密')!.trigger('click')
    await flushPromises()

    expect(smsReceiverAPI.addCardKeys).toHaveBeenCalledExactlyOnceWith(input, undefined)
    expect(smsReceiverAPI.listCardKeys).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="sms-card-key-3"]').text()).toContain('待用')
    expect(wrapper.get('[data-testid="sms-card-key-3"]').text()).toContain('8 次')
    expect(wrapper.get('[data-testid="sms-card-key-2"]').text()).toContain('使用中')
    expect(wrapper.get('textarea').element.value).toBe('')
    expect(showSuccess).toHaveBeenCalledExactlyOnceWith('已加入待用队列 1 个接码卡密。')
    expect(showError).not.toHaveBeenCalled()
  })

  it('explains ignored queued and active duplicates when the server reports no additions', async () => {
    vi.mocked(smsReceiverAPI.addCardKeys).mockResolvedValue({ added_count: 0, queued_count: 1 })
    const wrapper = await render()
    await wrapper.get('textarea').setValue('test-card-1\ntest-card-2')
    await wrapper.findAll('button').find(button => button.text() === '加密保存卡密')!.trigger('click')
    await flushPromises()

    expect(showInfo).toHaveBeenCalledExactlyOnceWith('没有可新增或重新入队的卡密，待用或使用中的重复卡密已忽略。')
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-card-key-1"]').text()).toContain('12 次')
    expect(wrapper.get('[data-testid="sms-card-key-2"]').text()).toContain('使用中')
  })

  it('refreshes retired card status without automatically reimporting or deleting it', async () => {
    const wrapper = await render()
    await wrapper.findAll('button').find(button => button.text() === '刷新列表')!.trigger('click')
    await flushPromises()

    expect(smsReceiverAPI.listCardKeys).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="sms-card-key-3"]').text()).toContain('已停用')
    expect(smsReceiverAPI.addCardKeys).not.toHaveBeenCalled()
    expect(smsReceiverAPI.deleteCardKey).not.toHaveBeenCalled()
    expect(smsReceiverAPI.clearCardKeys).not.toHaveBeenCalled()
    expect(smsReceiverAPI.reorderCardKeys).not.toHaveBeenCalled()
  })

  it('preserves the existing confirmation gates for clearing the queue and deleting active cards', async () => {
    const confirmation = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const wrapper = await render()
    await wrapper.findAll('button').find(button => button.text() === '清空待用卡密')!.trigger('click')
    await wrapper.get('[data-testid="sms-card-key-2"] [aria-label="删除此卡密"]').trigger('click')

    expect(confirmation).toHaveBeenNthCalledWith(1, '确定要清空全部待用卡密吗？进行中的接码会话不会被影响。')
    expect(confirmation).toHaveBeenNthCalledWith(2, '此卡密正在使用。删除后会终止该本地接码会话，确定继续吗？')
    expect(smsReceiverAPI.clearCardKeys).not.toHaveBeenCalled()
    expect(smsReceiverAPI.deleteCardKey).not.toHaveBeenCalled()
  })
})
