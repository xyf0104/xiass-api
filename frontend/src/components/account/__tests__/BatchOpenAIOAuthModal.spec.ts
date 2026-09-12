import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import BatchOpenAIOAuthModal from '../BatchOpenAIOAuthModal.vue'
import { batchOAuthAPI, type BatchOAuthTask } from '@/api/admin/openaiBatchOAuth'

vi.mock('@/api/admin/openaiBatchOAuth', () => ({ batchOAuthAPI: { list: vi.fn(), create: vi.fn(), sms: vi.fn(), cancel: vi.fn(), complete: vi.fn(), restart: vi.fn() } }))
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
    expect(batchOAuthAPI.sms).toHaveBeenNthCalledWith(1, 'task-00000000000001', 'check')
    expect(batchOAuthAPI.sms).toHaveBeenNthCalledWith(2, 'task-00000000000001', 'acquire')
    expect(wrapper.find('[data-testid="confirmation"]').exists()).toBe(false)
  })
  it('disables start with invalid 2FA or malformed rows instead of partially importing', async () => {
    await render()
    await wrapper.get('[data-testid="batch-credentials"]').setValue('person@example.test----password----INVALID!!\nmalformed')
    expect(wrapper.get('[data-testid="batch-start"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('第 1、2 行')
    expect(batchOAuthAPI.create).not.toHaveBeenCalled()
  })
})
