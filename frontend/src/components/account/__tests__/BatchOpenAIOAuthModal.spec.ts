import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import BatchOpenAIOAuthModal from '../BatchOpenAIOAuthModal.vue'
import { batchOAuthAPI, type BatchOAuthTask } from '@/api/admin/openaiBatchOAuth'

vi.mock('@/api/admin/openaiBatchOAuth', () => ({ batchOAuthAPI: { list: vi.fn(), create: vi.fn(), remove: vi.fn(), sms: vi.fn(), cancel: vi.fn(), complete: vi.fn(), restart: vi.fn() } }))
vi.mock('@/api/client', () => ({ apiClient: { get: vi.fn(async () => ({ data: { items: [{ id: 4, name: '号池分组1', proxy_id: 9 }] } })) } }))
const SelectStub = defineComponent({ props: ['modelValue', 'options'], emits: ['update:modelValue'], template: `<select :value="modelValue ?? ''" @change="$emit('update:modelValue', $event.target.value === '' ? null : Number.isNaN(Number($event.target.value)) ? $event.target.value : Number($event.target.value))"><option v-for="o in options" :value="o.value ?? ''">{{ o.label }}</option></select>` })
const base = { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' }
const confirm = { props: ['show', 'title', 'message'], emits: ['cancel', 'confirm'], template: '<div v-if="show" data-testid="confirmation"><h3>{{title}}</h3><p>{{message}}</p><slot/><button data-testid="confirm" @click="$emit(\'confirm\')">确认</button></div>' }
let wrapper: VueWrapper
function task(stage = 'login'): BatchOAuthTask { return { task_id: 'task-00000000000001', email: 'person@example.test', status: 'running', stage, restart_count: 0, requires_sms_confirmation: stage === 'phone_required', created_at: '', expires_at: '' } }
async function render() {
  wrapper = mount(BatchOpenAIOAuthModal, { props: { show: true, groups: [], proxies: [] }, global: { stubs: { BaseDialog: base, ConfirmDialog: confirm, Select: SelectStub, ProxySelector: { props: ['modelValue'], template: '<span data-testid="proxy">{{modelValue}}</span>' }, GroupSelector: true, Icon: true } } })
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  vi.mocked(batchOAuthAPI.list).mockResolvedValue({ items: [], max_concurrency: 3, max_restarts: 2 })
  vi.mocked(batchOAuthAPI.create).mockResolvedValue(task())
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })
describe('batch OAuth modal', () => {
  it('parses privately, snapshots pool proxy and clears pasted credentials after starting', async () => {
    await render()
    await wrapper.get('[data-testid="batch-credentials"]').setValue('person@example.test----MyPrivatePassword----JBSWY3DPEHPK3PXP')
    expect(wrapper.text()).toContain('已识别 1 个账号')
    expect(wrapper.text()).not.toContain('MyPrivatePassword')
    await wrapper.findAll('select')[0].setValue(4)
    expect(wrapper.get('[data-testid="proxy"]').text()).toBe('9')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    expect(batchOAuthAPI.create).toHaveBeenCalledWith(expect.objectContaining({ email: 'person@example.test', pool_id: 4, proxy_id: 9, concurrency: 3, priority: 1, codex_fingerprint_mode: 'off' }))
    expect(wrapper.find('[data-testid="batch-credentials"]').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('MyPrivatePassword')
  })
  it('automatically claims a number for the isolated batch workflow', async () => {
    vi.mocked(batchOAuthAPI.list).mockResolvedValue({ items: [task('phone_required')], max_concurrency: 3, max_restarts: 2 })
    vi.mocked(batchOAuthAPI.sms).mockImplementation(async (_id, action) => action === 'check'
      ? { task: task('phone_required'), sms: null }
      : { task: task('sms_waiting'), sms: { number: '+12025550123', status: 'waiting', expires_at: '' } })
    await render()
    await flushPromises()
    expect(batchOAuthAPI.sms).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(batchOAuthAPI.sms).toHaveBeenNthCalledWith(1, 'task-00000000000001', 'check')
    expect(batchOAuthAPI.sms).toHaveBeenNthCalledWith(2, 'task-00000000000001', 'acquire')
    expect(wrapper.find('[data-testid="confirmation"]').exists()).toBe(false)
  })
  it('deduplicates old failed attempts and deletes finished records', async () => {
    const failed = (id: string, email: string): BatchOAuthTask => ({ ...task('opening'), task_id: id, email, status: 'failed', reason: 'proxy_unavailable', restart_count: 1 })
    vi.mocked(batchOAuthAPI.list).mockResolvedValue({ items: [
      failed('failed-old-000001', 'one@example.test'),
      failed('failed-new-000001', 'one@example.test'),
      failed('failed-new-000002', 'two@example.test')
    ], max_concurrency: 3, max_restarts: 2 })
    vi.mocked(batchOAuthAPI.remove).mockResolvedValue({ task_id: 'deleted' })
    await render()
    expect(wrapper.text()).toContain('成功 0 · 失败 2')
    expect(wrapper.text()).toContain('所选出口代理无法从授权浏览器连接')
    expect(wrapper.findAll('[data-testid="oauth-row-one@example.test"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('重新授权所选')
    await wrapper.findAll('button').find(button => button.text().includes('清除失败记录'))!.trigger('click')
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(batchOAuthAPI.remove).toHaveBeenCalledTimes(2)
  })
  it('resends only the current modal memory credentials when retrying', async () => {
    const failed: BatchOAuthTask = { ...task('opening'), status: 'failed', reason: 'proxy_unavailable', restart_count: 1 }
    vi.mocked(batchOAuthAPI.list)
      .mockResolvedValueOnce({ items: [], max_concurrency: 3, max_restarts: 2 })
      .mockResolvedValueOnce({ items: [], max_concurrency: 3, max_restarts: 2 })
      .mockResolvedValue({ items: [failed], max_concurrency: 3, max_restarts: 2 })
    vi.mocked(batchOAuthAPI.create).mockResolvedValue(failed)
    vi.mocked(batchOAuthAPI.restart).mockResolvedValue({ ...failed, status: 'running', stage: 'opening', reason: undefined, restart_count: 2 })
    await render()
    await wrapper.get('[data-testid="batch-credentials"]').setValue('person@example.test----MyPrivatePassword----JBSWY3DPEHPK3PXP')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    const retryButton = wrapper.get('[data-testid="oauth-row-person@example.test"]').findAll('button').find(button => button.text().includes('重新授权'))!
    await retryButton.trigger('click')
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(batchOAuthAPI.restart).toHaveBeenCalledWith(failed.task_id, {
      password: 'MyPrivatePassword',
      totp_secret: 'JBSWY3DPEHPK3PXP'
    })
  })
  it('shows the final success and failed-account summary', async () => {
    vi.mocked(batchOAuthAPI.list).mockResolvedValue({
      items: [
        { ...task('completed'), task_id: 'completed-0000001', email: 'done@example.test', status: 'completed', account_id: 9 },
        { ...task('opening'), task_id: 'failed-0000000001', email: 'failed@example.test', status: 'failed', reason: 'proxy_unavailable', restart_count: 2 },
      ],
      max_concurrency: 3,
      max_restarts: 2,
    })
    await render()
    expect(wrapper.text()).toContain('本批次已结束：成功 1 个，失败 1 个')
    expect(wrapper.text()).toContain('失败账号：failed@example.test')
  })
  it('disables start with invalid 2FA or malformed rows instead of partially importing', async () => {
    await render()
    await wrapper.get('[data-testid="batch-credentials"]').setValue('person@example.test----password----INVALID!!\nmalformed')
    expect(wrapper.get('[data-testid="batch-start"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('第 1、2 行')
    expect(batchOAuthAPI.create).not.toHaveBeenCalled()
  })
})
