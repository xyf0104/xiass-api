import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createPinia } from 'pinia'
import BatchOpenAIOAuthModal from '../BatchOpenAIOAuthModal.vue'
import { batchOAuthAPI, type BatchOAuthTask } from '@/api/admin/openaiBatchOAuth'
import { isAdsPowerHelperAvailable } from '@/utils/adspowerHelper'

vi.mock('@/api/admin/openaiBatchOAuth', () => ({ batchOAuthAPI: { list: vi.fn(), create: vi.fn(), remove: vi.fn(), sms: vi.fn(), cancel: vi.fn(), complete: vi.fn(), restart: vi.fn(), launchAdsPower: vi.fn() } }))
vi.mock('@/utils/adspowerHelper', async importOriginal => ({
  ...(await importOriginal<typeof import('@/utils/adspowerHelper')>()),
  isAdsPowerHelperAvailable: vi.fn(),
}))
vi.mock('@/api/client', () => ({ apiClient: { get: vi.fn(async () => ({ data: { items: [{ id: 4, name: '号池分组1', proxy_id: 9 }] } })) } }))
const SelectStub = defineComponent({ props: ['modelValue', 'options'], emits: ['update:modelValue'], template: `<select :value="modelValue ?? ''" @change="$emit('update:modelValue', $event.target.value === '' ? null : Number.isNaN(Number($event.target.value)) ? $event.target.value : Number($event.target.value))"><option v-for="o in options" :value="o.value ?? ''">{{ o.label }}</option></select>` })
const base = { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' }
const confirm = { props: ['show', 'title', 'message'], emits: ['cancel', 'confirm'], template: '<div v-if="show" data-testid="confirmation"><h3>{{title}}</h3><p>{{message}}</p><slot/><button data-testid="confirm" @click="$emit(\'confirm\')">确认</button></div>' }
let wrapper: VueWrapper
function task(stage = 'login'): BatchOAuthTask { return { task_id: 'task-00000000000001', email: 'person@example.test', login_method: 'password', status: 'running', stage, restart_count: 0, requires_sms_confirmation: stage === 'phone_required', created_at: '', expires_at: '' } }
async function render(props: Record<string, unknown> = {}) {
  wrapper = mount(BatchOpenAIOAuthModal, { props: { show: true, groups: [], proxies: [], ...props }, global: { plugins: [createPinia()], stubs: { BaseDialog: base, ConfirmDialog: confirm, Select: SelectStub, ProxySelector: { props: ['modelValue'], template: '<span data-testid="proxy">{{modelValue}}</span>' }, GroupSelector: true, Icon: true } } })
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  vi.mocked(batchOAuthAPI.list).mockResolvedValue({ items: [], max_concurrency: 3, max_restarts: 2 })
  vi.mocked(batchOAuthAPI.create).mockResolvedValue(task())
  vi.mocked(isAdsPowerHelperAvailable).mockResolvedValue(true)
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })
describe('batch OAuth modal', () => {
  it('parses privately, snapshots pool proxy and clears pasted credentials after starting', async () => {
    await render()
    await wrapper.get('[data-testid="batch-credentials-password"]').setValue('person@example.test----MyPrivatePassword----JBSWY3DPEHPK3PXP')
    expect(wrapper.text()).toContain('已识别 1 个账号')
    expect(wrapper.text()).not.toContain('MyPrivatePassword')
    await wrapper.findAll('select')[0].setValue(4)
    expect(wrapper.get('[data-testid="proxy"]').text()).toBe('9')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    expect(batchOAuthAPI.create).toHaveBeenCalledWith(expect.objectContaining({
      email: 'person@example.test',
      login_method: 'password',
      password: 'MyPrivatePassword',
      totp_secret: 'JBSWY3DPEHPK3PXP',
      pool_id: 4,
      proxy_id: 9,
      concurrency: 1,
      priority: 2,
      codex_fingerprint_mode: 'off',
    }))
    expect(wrapper.find('[data-testid="batch-credentials-password"]').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('MyPrivatePassword')
  })
  it('creates AdsPower tasks explicitly and opens the task-bound helper URL', async () => {
    vi.mocked(batchOAuthAPI.create).mockResolvedValue({ ...task('external_browser'), browser_mode: 'adspower' })
    vi.mocked(batchOAuthAPI.launchAdsPower).mockResolvedValue({ helper_url: 'http://127.0.0.1:34987/launch?ticket=fixed', expires_at: '' })
    const popup = { opener: window, location: { href: 'about:blank' }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    await render()
    expect(wrapper.get('[data-testid="batch-browser-server"]').text()).toContain('内置浏览器授权')
    expect(wrapper.get('[data-testid="batch-browser-adspower"]').text()).toContain('Ads 指纹浏览器授权')
    await wrapper.get('[data-testid="batch-browser-adspower"]').trigger('click')
    await wrapper.get('[data-testid="batch-credentials-password"]').setValue('person@example.test----MyPrivatePassword----JBSWY3DPEHPK3PXP')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()

    expect(batchOAuthAPI.create).toHaveBeenCalledWith(expect.objectContaining({ browser_mode: 'adspower' }))
    await wrapper.get('[data-testid="launch-adspower-person@example.test"]').trigger('click')
    await flushPromises()
    expect(batchOAuthAPI.launchAdsPower).toHaveBeenCalledWith('task-00000000000001')
    expect(popup.location.href).toBe('http://127.0.0.1:34987/launch?ticket=fixed')
  })
  it('accepts a workbench-controlled browser mode without rendering a second selector', async () => {
    await render({ browserMode: 'adspower', showBrowserModeSelector: false })
    expect(wrapper.find('[data-testid="batch-browser-server"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="batch-browser-adspower"]').exists()).toBe(false)
    await wrapper.get('[data-testid="batch-credentials-password"]').setValue('person@example.test----MyPrivatePassword----JBSWY3DPEHPK3PXP')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()

    expect(batchOAuthAPI.create).toHaveBeenCalledWith(expect.objectContaining({ browser_mode: 'adspower' }))
  })
  it('shows an existing account as skipped without announcing a new account', async () => {
    vi.mocked(batchOAuthAPI.create).mockResolvedValue({
      ...task(),
      status: 'completed',
      stage: 'completed',
      reason: 'account_already_exists',
      account_id: 77,
    })
    await render()
    await wrapper.get('[data-testid="batch-credentials-password"]').setValue('existing@example.test----MyPrivatePassword----JBSWY3DPEHPK3PXP')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('已存在，已跳过')
    expect(wrapper.text()).toContain('已存在跳过 1 个')
    expect(wrapper.text()).not.toContain('失败 1')
    expect(wrapper.emitted('created')).toBeUndefined()
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
  it('leaves AdsPower phone and SMS actions to the resident helper', async () => {
    vi.mocked(batchOAuthAPI.list).mockResolvedValue({
      items: [{ ...task('phone_required'), browser_mode: 'adspower', requires_sms_confirmation: false }],
      max_concurrency: 3,
      max_restarts: 2,
    })
    await render()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(batchOAuthAPI.sms).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('提交手机号')
    expect(wrapper.findAll('button').some(button => button.text().includes('领取号码'))).toBe(false)
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
    expect(wrapper.text()).toContain('成功 0 · 已跳过 0 · 失败 2')
    expect(wrapper.text()).toContain('所选出口代理无法从授权浏览器连接')
    expect(wrapper.findAll('[data-testid="oauth-row-one@example.test"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('重新授权所选')
    await wrapper.findAll('button').find(button => button.text().includes('清除失败记录'))!.trigger('click')
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(batchOAuthAPI.remove).toHaveBeenCalledTimes(2)
  })
  it('shows precise retry reasons for OpenAI route and expired-session failures', async () => {
    vi.mocked(batchOAuthAPI.list).mockResolvedValue({ items: [
      { ...task('totp'), task_id: 'route-error-000001', email: 'route@example.test', status: 'failed', reason: 'openai_route_error', restart_count: 1 },
      { ...task('callback_waiting'), task_id: 'expired-state-0001', email: 'state@example.test', status: 'failed', reason: 'oauth_session_expired', restart_count: 1 },
    ], max_concurrency: 3, max_restarts: 2 })
    await render()
    expect(wrapper.text()).toContain('OpenAI 登录页临时返回 Route Error')
    expect(wrapper.text()).toContain('OpenAI 登录会话已失效（invalid_state）')
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
    await wrapper.get('[data-testid="batch-credentials-password"]').setValue('person@example.test----MyPrivatePassword----JBSWY3DPEHPK3PXP')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    const retryButton = wrapper.get('[data-testid="oauth-row-person@example.test"]').findAll('button').find(button => button.text().includes('重新授权'))!
    await retryButton.trigger('click')
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(batchOAuthAPI.restart).toHaveBeenCalledWith(failed.task_id, {
      login_method: 'password',
      password: 'MyPrivatePassword',
      totp_secret: 'JBSWY3DPEHPK3PXP'
    })
  })
  it('shows the final success and failed-account summary', async () => {
    vi.mocked(batchOAuthAPI.list).mockResolvedValue({
      items: [
        { ...task('opening'), task_id: 'failed-0000000001', email: 'failed@example.test', status: 'failed', reason: 'proxy_unavailable', restart_count: 2 },
        { ...task('completed'), task_id: 'skipped-000000001', email: 'existing@example.test', status: 'completed', reason: 'account_already_exists', account_id: 8 },
        { ...task('completed'), task_id: 'completed-0000001', email: 'done@example.test', status: 'completed', account_id: 9 },
      ],
      max_concurrency: 3,
      max_restarts: 2,
    })
    await render()
    expect(wrapper.text()).toContain('本批次已结束：成功 1 个，已存在跳过 1 个，失败 1 个')
    expect(wrapper.text()).toContain('失败账号：failed@example.test')
    expect(wrapper.findAll('[data-testid^="oauth-row-"]').map(row => row.attributes('data-testid'))).toEqual([
      'oauth-row-done@example.test',
      'oauth-row-existing@example.test',
      'oauth-row-failed@example.test',
    ])
  })
  it('disables start with invalid 2FA or malformed rows instead of partially importing', async () => {
    await render()
    await wrapper.get('[data-testid="batch-credentials-password"]').setValue('person@example.test----password----INVALID!!\nmalformed')
    expect(wrapper.get('[data-testid="batch-start"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('第 1、2 行')
    expect(batchOAuthAPI.create).not.toHaveBeenCalled()
  })
  it('keeps email-code login separate and retains failed email plus token only in modal memory', async () => {
    const token = 'a'.repeat(64)
    const failed: BatchOAuthTask = {
      ...task('email_code_waiting'),
      login_method: 'email_code',
      status: 'failed',
      reason: 'email_code_access_denied',
      restart_count: 0,
    }
    vi.mocked(batchOAuthAPI.create).mockResolvedValue(failed)
    await render()

    await wrapper.get('[data-testid="batch-mode-email-code"]').trigger('click')
    expect(wrapper.find('[data-testid="batch-credentials-password"]').exists()).toBe(false)
    await wrapper.get('[data-testid="batch-credentials-email-code"]').setValue(
      `gpt-0 https://ic.g-c.cc person@example.test ${token} Plus 美国洛杉矶-3`
    )
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()

    expect(batchOAuthAPI.create).toHaveBeenCalledWith(expect.objectContaining({
      email: 'person@example.test',
      login_method: 'email_code',
      email_code_token: token,
    }))
    expect(wrapper.text()).toContain('邮箱与 64 位 Token 不匹配')
    expect(wrapper.get('textarea[aria-label="失败邮箱验证码账号"]').attributes('value')).toBe(`person@example.test\t${token}`)
    expect(wrapper.text()).not.toContain('密码与 2FA 已识别')
    expect(batchOAuthAPI.restart).not.toHaveBeenCalled()
  })
})
