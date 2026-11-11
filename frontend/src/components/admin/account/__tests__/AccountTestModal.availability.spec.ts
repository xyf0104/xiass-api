import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick, reactive } from 'vue'
import AdminAccountTestModal from '../AccountTestModal.vue'
import AccountTestModal from '@/components/account/AccountTestModal.vue'
import type { Account, ClaudeModel } from '@/types'

const { getAvailableModels, showError } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const SelectStub = defineComponent({
  props: ['modelValue', 'options', 'valueKey', 'labelKey', 'disabled'],
  emits: ['update:modelValue'],
  template: `<select :value="modelValue" :disabled="disabled" @change="$emit('update:modelValue', $event.target.value)">
    <option v-for="option in options" :key="option[valueKey || 'value']" :value="option[valueKey || 'value']">
      {{ option[labelKey || 'label'] }}
    </option>
  </select>`
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function model(id: string): ClaudeModel {
  return { id, display_name: id, type: 'model', created_at: '2026-11-11' }
}

function account(id = 42, overrides: Partial<Account> = {}): Account {
  return { id, name: `Account ${id}`, platform: 'antigravity', type: 'oauth', status: 'active', ...overrides } as Account
}

function streamResponse(read = vi.fn().mockResolvedValue({ done: true })) {
  return { ok: true, body: { getReader: () => ({ read }) } } as unknown as Response
}

function streamChunk(...events: Record<string, unknown>[]) {
  return {
    done: false,
    value: new TextEncoder().encode(events.map((event) => `data: ${JSON.stringify(event)}\n`).join(''))
  }
}

interface ModalState {
  availableModels: ClaudeModel[]
  selectedModelId: string
  loadingModels: boolean
  modelLoadError: string
  status: string
  errorMessage: string
  streamingContent: string
  outputLines: unknown[]
  generatedImages: unknown[]
  startTest: () => Promise<void>
}

const state = (wrapper: VueWrapper) => wrapper.vm as unknown as ModalState
const startButton = (wrapper: VueWrapper) => wrapper.findAll('button').find((button) => /admin.accounts.(startTest|testing|retry)/.test(button.text()))!
const retryModelsButton = (wrapper: VueWrapper) => wrapper.findAll('button').find((button) => button.text() === 'common.retry')!
const modelIds = (wrapper: VueWrapper) => state(wrapper).availableModels.map(({ id }) => id)

enableAutoUnmount(afterEach)

describe.each([
  ['admin', AdminAccountTestModal],
  ['shared', AccountTestModal]
] as const)('%s account test model availability', (_name, component) => {
  function mountModal(selectedAccount: Account | null = account(), show = true) {
    return mount(component, {
      props: { show, account: selectedAccount },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
          Select: SelectStub,
          TextArea: { props: ['modelValue'], template: '<textarea :value="modelValue" />' },
          Icon: true
        }
      }
    })
  }

  beforeEach(() => {
    getAvailableModels.mockReset().mockResolvedValue([model('gemini-3.7-flash-low')])
    showError.mockReset()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(streamResponse()))
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('loads an initially open Pro account without adding catalog models or sibling tiers', async () => {
    const models = [model('gemini-3.7-flash-low'), model('custom-sonnet-alias'), model('gemini-3.8-flash-low')]
    getAvailableModels.mockResolvedValue(models)
    const wrapper = mountModal(account(42, { extra: { subscription_tier: 'PRO' } }))
    await flushPromises()

    expect(getAvailableModels).toHaveBeenCalledExactlyOnceWith(42)
    expect(state(wrapper).availableModels).toHaveLength(models.length)
    expect(state(wrapper).availableModels).toEqual(expect.arrayContaining(models))
    expect(state(wrapper).selectedModelId).toBe('custom-sonnet-alias')
    expect(startButton(wrapper).attributes('disabled')).toBeUndefined()

    wrapper.findComponent(SelectStub).vm.$emit('update:modelValue', 'gemini-4-argon-high')
    await nextTick()
    expect(startButton(wrapper).attributes('disabled')).toBeDefined()
    await state(wrapper).startTest()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('normalizes only returned 3.7 aliases, preserving explicit public metadata', async () => {
    const low = { ...model('gemini-3.7-flash-low'), display_name: 'Account-specific Low' }
    getAvailableModels.mockResolvedValue([
      model('gemini-3.7-flash-tiered'), model('gemini-3.7-flash'), low,
      model('gemini-3.8-flash-tiered'), model('gemini-4-argon-low')
    ])
    const wrapper = mountModal()
    await flushPromises()

    expect(modelIds(wrapper).sort()).toEqual([
      'gemini-3.7-flash-high', 'gemini-3.7-flash-medium', 'gemini-3.7-flash-low',
      'gemini-3.8-flash-tiered', 'gemini-4-argon-low'
    ].sort())
    expect(state(wrapper).availableModels.find(({ id }) => id === low.id)).toEqual(low)
    expect(state(wrapper).availableModels.find(({ id }) => id === 'gemini-3.7-flash-high')?.display_name).toBe('Gemini 3.7 Flash High')
  })

  it.each(['gemini', 'antigravity'] as const)('preserves %s API-key image prompts and thumbnails', async (platform) => {
    getAvailableModels.mockResolvedValue([model('gemini-2.5-pro'), model('gemini-3.1-flash-image')])
    const read = vi.fn()
      .mockResolvedValueOnce(streamChunk(
        { type: 'image', image_url: 'data:image/png;base64,QUJD', mime_type: 'image/png' },
        { type: 'test_complete', success: true }
      ))
      .mockResolvedValue({ done: true })
    vi.mocked(fetch).mockResolvedValue(streamResponse(read))
    const wrapper = mountModal(account(1, { platform, type: 'apikey' }))
    await flushPromises()

    expect(state(wrapper).selectedModelId).toBe('gemini-3.1-flash-image')
    await state(wrapper).startTest()
    await nextTick()

    const [url, request] = vi.mocked(fetch).mock.calls[0]
    expect(String(url)).toContain('/admin/accounts/1/test')
    expect(JSON.parse(request!.body as string)).toMatchObject({
      model_id: 'gemini-3.1-flash-image', prompt: 'admin.accounts.imagePromptDefault'
    })
    expect(wrapper.get('img[alt="test-image-1"]').attributes('src')).toBe('data:image/png;base64,QUJD')
  })

  it('preserves Gemini aliases and Claude Sonnet default selection', async () => {
    getAvailableModels.mockResolvedValueOnce([model('gemini-3.7-flash')])
    const wrapper = mountModal(account(1, { platform: 'gemini' }))
    await flushPromises()
    expect(modelIds(wrapper)).toEqual(['gemini-3.7-flash'])

    getAvailableModels.mockResolvedValueOnce([model('claude-opus-4-6'), model('claude-sonnet-4-6')])
    await wrapper.setProps({ account: account(2, { platform: 'anthropic' }) })
    await flushPromises()
    expect(state(wrapper).selectedModelId).toBe('claude-sonnet-4-6')
    expect(wrapper.find('textarea').exists()).toBe(false)
  })

  it('loads when an account arrives while open and clears when it is removed', async () => {
    const wrapper = mountModal(null)
    await flushPromises()
    expect(getAvailableModels).not.toHaveBeenCalled()
    await wrapper.setProps({ account: account(7) })
    await flushPromises()
    expect(getAvailableModels).toHaveBeenCalledExactlyOnceWith(7)
    await wrapper.setProps({ account: null })
    expect(modelIds(wrapper)).toEqual([])
    expect(state(wrapper).selectedModelId).toBe('')
    expect(startButton(wrapper).attributes('disabled')).toBeDefined()
  })

  it('reloads an ID changed in place while open and immediately discards the old selection', async () => {
    const selectedAccount = reactive(account())
    const wrapper = mountModal(selectedAccount)
    await flushPromises()
    const pending = deferred<ClaudeModel[]>()
    getAvailableModels.mockReturnValueOnce(pending.promise)
    selectedAccount.id = 43
    await nextTick()

    expect(getAvailableModels).toHaveBeenLastCalledWith(43)
    expect(state(wrapper).loadingModels).toBe(true)
    expect(modelIds(wrapper)).toEqual([])
    expect(state(wrapper).selectedModelId).toBe('')
    expect(wrapper.get('select').attributes('disabled')).toBeDefined()
    expect(startButton(wrapper).attributes('disabled')).toBeDefined()
    await state(wrapper).startTest()
    expect(fetch).not.toHaveBeenCalled()

    pending.resolve([model('gemini-2.5-flash')])
    await flushPromises()
    expect(modelIds(wrapper)).toEqual(['gemini-2.5-flash'])
    expect(state(wrapper).selectedModelId).toBe('gemini-2.5-flash')
    await state(wrapper).startTest()
    expect(String(vi.mocked(fetch).mock.calls[0][0])).toContain('/admin/accounts/43/test')
  })

  it('retains a valid selection across same-account status refreshes', async () => {
    getAvailableModels.mockResolvedValue([model('gemini-3.7-flash-low'), model('gemini-3.7-flash-medium')])
    const wrapper = mountModal()
    await flushPromises()
    wrapper.findComponent(SelectStub).vm.$emit('update:modelValue', 'gemini-3.7-flash-medium')
    await wrapper.setProps({ account: account(42, { name: 'Refreshed account' }) })
    expect(getAvailableModels).toHaveBeenCalledTimes(1)
    expect(state(wrapper).selectedModelId).toBe('gemini-3.7-flash-medium')
  })

  it.each(['empty', 'error'] as const)('clears old models on %s and retries loading without starting a test', async (result) => {
    const wrapper = mountModal()
    await flushPromises()
    if (result === 'empty') getAvailableModels.mockResolvedValueOnce([])
    else getAvailableModels.mockRejectedValueOnce({ status: 502, message: 'Account catalog unavailable' })
    await wrapper.setProps({ account: account(43) })
    await flushPromises()

    expect(modelIds(wrapper)).toEqual([])
    expect(state(wrapper).selectedModelId).toBe('')
    expect(state(wrapper).loadingModels).toBe(false)
    expect(startButton(wrapper).attributes('disabled')).toBeDefined()
    if (result === 'error') {
      expect(wrapper.get('[role="alert"]').text()).toBe('Account catalog unavailable')
      expect(showError).toHaveBeenCalledExactlyOnceWith('Account catalog unavailable')
    }
    await state(wrapper).startTest()
    expect(fetch).not.toHaveBeenCalled()

    const pending = deferred<ClaudeModel[]>()
    getAvailableModels.mockReturnValueOnce(pending.promise)
    await retryModelsButton(wrapper).trigger('click')
    expect(state(wrapper).loadingModels).toBe(true)
    expect(state(wrapper).modelLoadError).toBe('')
    pending.resolve([model('gemini-2.5-flash')])
    await flushPromises()
    expect(getAvailableModels).toHaveBeenCalledTimes(3)
    expect(state(wrapper).selectedModelId).toBe('gemini-2.5-flash')
    expect(startButton(wrapper).attributes('disabled')).toBeUndefined()
    expect(fetch).not.toHaveBeenCalled()
  })

  it.each(['success', 'error'] as const)('ignores a stale %s without ending the current loading state', async (result) => {
    const old = deferred<ClaudeModel[]>()
    const current = deferred<ClaudeModel[]>()
    getAvailableModels.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = mountModal()
    await wrapper.setProps({ account: account(43) })
    if (result === 'success') old.resolve([model('ultra-only-model')])
    else old.reject({ status: 502, message: 'Old account failed' })
    await flushPromises()

    expect(modelIds(wrapper)).toEqual([])
    expect(state(wrapper).loadingModels).toBe(true)
    expect(state(wrapper).selectedModelId).toBe('')
    expect(showError).not.toHaveBeenCalled()

    current.resolve([model('pro-only-model')])
    await flushPromises()
    expect(modelIds(wrapper)).toEqual(['pro-only-model'])
    expect(state(wrapper).loadingModels).toBe(false)
  })

  it.each(['success', 'error'] as const)('ignores late %s from a previous opening of the same account', async (result) => {
    const old = deferred<ClaudeModel[]>()
    getAvailableModels.mockReturnValueOnce(old.promise)
    const wrapper = mountModal(account(), false)
    expect(getAvailableModels).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true })
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    if (result === 'success') old.resolve([model('stale-model')])
    else old.reject({ status: 502, message: 'Old opening failed' })
    await flushPromises()

    expect(modelIds(wrapper)).toEqual(['gemini-3.7-flash-low'])
    expect(state(wrapper).selectedModelId).toBe('gemini-3.7-flash-low')
    expect(state(wrapper).modelLoadError).toBe('')
    expect(showError).not.toHaveBeenCalled()
  })

  it.each(['close-button', 'hide', 'unmount'] as const)('invalidates pending model results on %s', async (action) => {
    const pending = deferred<ClaudeModel[]>()
    getAvailableModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountModal()
    const vm = state(wrapper)
    if (action === 'close-button') {
      await wrapper.findAll('button').find((button) => button.text() === 'common.close')!.trigger('click')
      expect(wrapper.emitted('close')).toHaveLength(1)
    } else if (action === 'hide') await wrapper.setProps({ show: false })
    else wrapper.unmount()
    pending.resolve([model('stale-model')])
    await flushPromises()

    expect(vm.availableModels).toEqual([])
    expect(vm.selectedModelId).toBe('')
    expect(vm.loadingModels).toBe(false)
    expect(showError).not.toHaveBeenCalled()
    await vm.startTest()
    expect(fetch).not.toHaveBeenCalled()
  })

  it.each(['success', 'abort', 'error'] as const)('ignores a late test HTTP %s after switching accounts', async (result) => {
    const old = deferred<Response>()
    const current = deferred<Response>()
    vi.mocked(fetch).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = mountModal()
    await flushPromises()
    const oldTest = state(wrapper).startTest()
    const oldSignal = vi.mocked(fetch).mock.calls[0][1]!.signal!
    await wrapper.setProps({ account: account(43) })
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    expect(state(wrapper).status).toBe('idle')
    const currentTest = state(wrapper).startTest()

    if (result === 'success') old.resolve(streamResponse(vi.fn().mockResolvedValueOnce(streamChunk({ type: 'error', error: 'Stale test failed' }))))
    else if (result === 'abort') old.reject(new DOMException('Aborted', 'AbortError'))
    else old.reject(new Error('Old network failure'))
    await oldTest
    expect(state(wrapper).status).toBe('connecting')
    expect(state(wrapper).errorMessage).toBe('')
    expect(wrapper.emitted('account-state-changed')).toBeUndefined()

    current.resolve(streamResponse(vi.fn().mockResolvedValueOnce(streamChunk({ type: 'test_complete', success: true })).mockResolvedValue({ done: true })))
    await currentTest
    expect(state(wrapper).status).toBe('success')
    if (component === AdminAccountTestModal) expect(wrapper.emitted('account-state-changed')).toEqual([[43]])
  })

  it.each(['switch', 'close-button', 'unmount'] as const)('discards pending SSE chunks after %s', async (action) => {
    const chunk = deferred<ReturnType<typeof streamChunk>>()
    const read = vi.fn().mockReturnValueOnce(chunk.promise).mockResolvedValue({ done: true })
    vi.mocked(fetch).mockResolvedValueOnce(streamResponse(read))
    const wrapper = mountModal()
    await flushPromises()
    const vm = state(wrapper)
    const pendingTest = vm.startTest()
    await flushPromises()
    expect(read).toHaveBeenCalledTimes(1)

    if (action === 'switch') await wrapper.setProps({ account: account(43) })
    else if (action === 'close-button') await wrapper.findAll('button').find((button) => button.text() === 'common.close')!.trigger('click')
    else wrapper.unmount()
    expect(vi.mocked(fetch).mock.calls[0][1]!.signal!.aborted).toBe(true)
    chunk.resolve(streamChunk(
      { type: 'content', text: 'Old account content' },
      { type: 'image', image_url: 'data:image/png;base64,QUJD' },
      { type: 'test_complete', success: true },
      { type: 'error', error: 'Old account failed' }
    ))
    await pendingTest
    expect(vm.status).toBe('idle')
    expect(vm.outputLines).toEqual([])
    expect(vm.streamingContent).toBe('')
    expect(vm.generatedImages).toEqual([])
    expect(vm.errorMessage).toBe('')
    expect(wrapper.emitted('account-state-changed')).toBeUndefined()
  })

  it('blocks duplicate starts and preserves the existing test error retry', async () => {
    const first = deferred<Response>()
    vi.mocked(fetch).mockReturnValueOnce(first.promise)
    const wrapper = mountModal()
    await flushPromises()
    const pendingTest = state(wrapper).startTest()
    await state(wrapper).startTest()
    expect(fetch).toHaveBeenCalledTimes(1)
    first.reject(new Error('Test temporarily failed'))
    await pendingTest
    await nextTick()
    expect(state(wrapper).status).toBe('error')
    expect(startButton(wrapper).attributes('disabled')).toBeUndefined()

    vi.mocked(fetch).mockResolvedValueOnce(streamResponse(vi.fn().mockResolvedValueOnce(streamChunk({ type: 'test_complete', success: true })).mockResolvedValue({ done: true })))
    await startButton(wrapper).trigger('click')
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(state(wrapper).status).toBe('success')
    expect(state(wrapper).selectedModelId).toBe('gemini-3.7-flash-low')
  })
})
