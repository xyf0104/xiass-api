import { enableAutoUnmount, flushPromises, mount, shallowMount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, reactive } from 'vue'
import EditAccountModal from '../EditAccountModal.vue'
import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'
import type { Account } from '@/types'
import type { SyncUpstreamModelsResult } from '@/api/admin/accounts'

const { getAvailableModels, syncUpstreamModels, updateAccount, showError, showWarning } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(), syncUpstreamModels: vi.fn(), updateAccount: vi.fn(), showError: vi.fn(), showWarning: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels, syncUpstreamModels, update: updateAccount,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false })
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) }
  }
}))
vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: { getAvailableModels, syncUpstreamModels },
  getAntigravityDefaultModelMapping: vi.fn()
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showInfo: vi.fn(), showSuccess: vi.fn(), showWarning })
}))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const models = (...ids: string[]): SyncUpstreamModelsResult => ({ models: ids })
const catalogWarnings = [
  { code: 'upstream_model_list_mapping_fallback', message: 'Mapping fallback' },
  { code: 'upstream_model_metadata_partial', message: 'Partial metadata' },
  { code: 'upstream_model_metadata_incomplete', message: 'Incomplete metadata' },
  { code: 'upstream_model_metadata_too_large', message: 'Metadata too large' },
  { code: 'future_warning', message: 'Upstream qualification' }
]
const warningMessages = [
  'admin.accounts.syncUpstreamModelsMappingFallback',
  'admin.accounts.syncUpstreamModelsMetadataPartial',
  'admin.accounts.syncUpstreamModelsMetadataIncomplete',
  'admin.accounts.syncUpstreamModelsMetadataTooLarge',
  'Upstream qualification'
]
function account(overrides: Partial<Account> = {}): Account {
  return {
    id: 42, name: 'Antigravity Pro', platform: 'antigravity', type: 'oauth', status: 'active',
    notes: '', credentials: { model_mapping: { 'gemini-2.5-flash': 'gemini-2.5-flash' } },
    extra: {}, proxy_id: null, concurrency: 1, priority: 1, rate_multiplier: 1,
    group_ids: [], expires_at: null, auto_pause_on_expired: false, ...overrides
  } as Account
}

interface EditState {
  antigravityModelMappings: { from: string; to: string }[]
  antigravityPresetMappings: { from: string; to: string }[]
  antigravityAccountCatalog: { models: string[] | null; loading: boolean; error: string }
  loadAntigravityAccountModels: (refresh?: boolean) => Promise<string[] | null>
  syncAntigravityUpstreamModels: () => Promise<void>
  addAntigravityPresetMapping: (from: string, to: string) => void
  handleClose: () => void
}
const editState = (wrapper: VueWrapper) => wrapper.vm as unknown as EditState
function mountEditor(selected = account(), show = true) {
  return shallowMount(EditAccountModal, {
    props: { show, account: selected, groups: [], proxies: [] },
    global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } } }
  })
}
const suggestionIds = (wrapper: VueWrapper) => wrapper.findAll('[data-testid="antigravity-model-suggestions"] option').map(option => option.attributes('value'))

enableAutoUnmount(afterEach)
beforeEach(() => {
  syncUpstreamModels.mockReset().mockResolvedValue(models('gemini-2.5-flash', 'gemini-3.7-flash-low'))
  getAvailableModels.mockReset()
  updateAccount.mockReset().mockImplementation(async (_id, data) => ({ ...account(), ...data }))
  showError.mockReset()
  showWarning.mockReset()
})

describe('EditAccountModal account-specific model suggestions', () => {
  it('loads once per opening and filters presets and suggestions without changing stored mappings', async () => {
    const existing = account({ credentials: { model_mapping: {
      'gemini-4-argon-high': 'gemini-4-argon-high',
      'my-custom-alias': 'custom-upstream-target'
    } } })
    const wrapper = mountEditor(existing, false)
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(syncUpstreamModels).toHaveBeenCalledExactlyOnceWith(42)
    expect(suggestionIds(wrapper)).toEqual(['gemini-2.5-flash', 'gemini-3.7-flash-low'])
    expect(editState(wrapper).antigravityPresetMappings).toEqual(expect.arrayContaining([
      expect.objectContaining({ from: 'gemini-3.7-flash-low', to: 'gemini-3.7-flash-low' })
    ]))
    expect(editState(wrapper).antigravityPresetMappings.every(preset => ['gemini-2.5-flash', 'gemini-3.7-flash-low'].includes(preset.to))).toBe(true)
    expect(wrapper.findAll('[data-testid="antigravity-model-preset"]').some(button => /Argon|3.7 Flash High|3.7 Flash Medium/.test(button.text()))).toBe(false)
    expect(editState(wrapper).antigravityModelMappings).toEqual([
      { from: 'gemini-4-argon-high', to: 'gemini-4-argon-high' },
      { from: 'my-custom-alias', to: 'custom-upstream-target' }
    ])
    editState(wrapper).addAntigravityPresetMapping('gemini-4-carbon-high', 'gemini-4-carbon-high')
    expect(editState(wrapper).antigravityModelMappings).toHaveLength(2)

    await wrapper.setProps({ account: { ...existing, name: 'Refreshed name' } })
    await editState(wrapper).loadAntigravityAccountModels()
    expect(syncUpstreamModels).toHaveBeenCalledTimes(1)
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(syncUpstreamModels).toHaveBeenCalledTimes(2)
  })

  it('discovers A+B from the live catalog when only A is mapped and adds B only on explicit sync', async () => {
    const pending = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountEditor()
    const first = editState(wrapper).loadAntigravityAccountModels()
    const second = editState(wrapper).loadAntigravityAccountModels(true)
    expect(first).toBe(second)
    expect(syncUpstreamModels).toHaveBeenCalledTimes(1)
    expect(editState(wrapper).antigravityModelMappings).toHaveLength(1)
    pending.resolve(models('gemini-2.5-flash', 'gemini-3.7-flash-low'))
    await first
    await nextTick()
    expect(suggestionIds(wrapper)).toEqual(['gemini-2.5-flash', 'gemini-3.7-flash-low'])
    expect(editState(wrapper).antigravityPresetMappings).toContainEqual(expect.objectContaining({
      from: 'gemini-3.7-flash-low', to: 'gemini-3.7-flash-low'
    }))
    expect(editState(wrapper).antigravityModelMappings).toHaveLength(1)

    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.syncUpstreamModels')!.trigger('click')
    await flushPromises()
    expect(editState(wrapper).antigravityModelMappings).toEqual([
      { from: 'gemini-2.5-flash', to: 'gemini-2.5-flash' },
      { from: 'gemini-3.7-flash-low', to: 'gemini-3.7-flash-low' }
    ])
    expect(getAvailableModels).not.toHaveBeenCalled()
    expect(syncUpstreamModels).toHaveBeenCalledTimes(2)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccount.mock.calls[0][1].credentials.model_mapping).toEqual({
      'gemini-2.5-flash': 'gemini-2.5-flash', 'gemini-3.7-flash-low': 'gemini-3.7-flash-low'
    })
  })

  it('allows the user to add a newly discovered preset without another catalog request', async () => {
    const wrapper = mountEditor()
    await flushPromises()
    const preset = wrapper.findAll('[data-testid="antigravity-model-preset"]').find(button => button.text().includes('3.7 Flash Low'))!
    await preset.trigger('click')
    expect(editState(wrapper).antigravityModelMappings).toContainEqual({ from: 'gemini-3.7-flash-low', to: 'gemini-3.7-flash-low' })
    expect(syncUpstreamModels).toHaveBeenCalledTimes(1)
  })

  it('shares the automatic request with explicit sync and reports all warnings exactly once', async () => {
    const pending = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountEditor(account({ credentials: { model_mapping: { 'gemini-4-argon-high': 'gemini-4-argon-high' } } }))
    const sync = editState(wrapper).syncAntigravityUpstreamModels()
    pending.resolve({ ...models('new-model'), warnings: catalogWarnings })
    await sync
    await nextTick()
    expect(syncUpstreamModels).toHaveBeenCalledExactlyOnceWith(42)
    expect(showWarning.mock.calls.map(([message]) => message)).toEqual(warningMessages)
    expect(wrapper.find('[data-testid="unavailable-antigravity-mapping"]').exists()).toBe(false)
    expect(editState(wrapper).antigravityModelMappings).toContainEqual({ from: 'new-model', to: 'new-model' })
    await editState(wrapper).loadAntigravityAccountModels()
    expect(showWarning).toHaveBeenCalledTimes(catalogWarnings.length)
  })

  it('reports warnings even when the returned catalog is empty', async () => {
    syncUpstreamModels.mockResolvedValueOnce({ models: [], warnings: catalogWarnings })
    const wrapper = mountEditor()
    await flushPromises()
    expect(suggestionIds(wrapper)).toEqual([])
    expect(showWarning.mock.calls.map(([message]) => message)).toEqual(warningMessages)
    expect(wrapper.find('[data-testid="unavailable-antigravity-mapping"]').exists()).toBe(false)
  })

  it('marks only unchanged known identity mappings unavailable after a successful catalog response', async () => {
    const pending = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountEditor(account({ credentials: { model_mapping: {
      'gemini-4-argon-high': 'gemini-4-argon-high',
      'my-alias': 'gemini-4-argon-high',
      'claude-*': 'claude-sonnet-4-6',
      'unknown-custom-model': 'unknown-custom-model'
    } } }))
    expect(wrapper.find('[data-testid="unavailable-antigravity-mapping"]').exists()).toBe(false)
    pending.resolve(models('gemini-3.7-flash-low'))
    await flushPromises()
    expect(wrapper.findAll('[data-testid="unavailable-antigravity-mapping"]')).toHaveLength(1)
    expect(editState(wrapper).antigravityModelMappings).toHaveLength(4)
    syncUpstreamModels.mockRejectedValueOnce({ status: 502, message: 'Catalog unavailable' })
    await editState(wrapper).loadAntigravityAccountModels(true)
    await nextTick()
    expect(wrapper.find('[data-testid="unavailable-antigravity-mapping"]').exists()).toBe(false)
  })

  it('does not restore global presets on empty/error responses and supports retry', async () => {
    syncUpstreamModels.mockResolvedValueOnce(models())
    const wrapper = mountEditor()
    await flushPromises()
    expect(suggestionIds(wrapper)).toEqual([])
    expect(editState(wrapper).antigravityPresetMappings).toEqual([])
    syncUpstreamModels.mockRejectedValueOnce({ status: 502, message: 'Catalog unavailable' })
    await editState(wrapper).loadAntigravityAccountModels(true)
    await nextTick()
    expect(wrapper.get('[role="alert"]').text()).toBe('Catalog unavailable')
    expect(showError).toHaveBeenCalledExactlyOnceWith('Catalog unavailable')
    expect(editState(wrapper).antigravityModelMappings).toHaveLength(1)
    expect(suggestionIds(wrapper)).toEqual([])
    expect(editState(wrapper).antigravityPresetMappings).toEqual([])
    await wrapper.get('[data-testid="retry-antigravity-models"]').trigger('click')
    await flushPromises()
    expect(suggestionIds(wrapper)).toContain('gemini-3.7-flash-low')
    expect(editState(wrapper).antigravityModelMappings).toHaveLength(1)
  })

  it.each(['success', 'error'] as const)('ignores stale catalog %s when an account ID changes in place', async (outcome) => {
    const old = deferred<SyncUpstreamModelsResult>()
    const current = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const selected = reactive(account())
    const wrapper = mountEditor(selected)
    selected.id = 43
    await nextTick()
    expect(syncUpstreamModels).toHaveBeenLastCalledWith(43)
    if (outcome === 'success') old.resolve(models('ultra-only-model'))
    else old.reject({ message: 'Old account failure' })
    await flushPromises()
    expect(editState(wrapper).antigravityAccountCatalog.loading).toBe(true)
    expect(suggestionIds(wrapper)).toEqual([])
    expect(showError).not.toHaveBeenCalled()
    current.resolve(models('pro-model'))
    await flushPromises()
    expect(suggestionIds(wrapper)).toEqual(['pro-model'])
  })

  it.each(['close', 'hide', 'unmount'] as const)('invalidates late catalog and sync results on %s', async (action) => {
    const pending = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountEditor()
    const vm = editState(wrapper)
    const sync = vm.syncAntigravityUpstreamModels()
    if (action === 'close') vm.handleClose()
    else if (action === 'hide') await wrapper.setProps({ show: false })
    else wrapper.unmount()
    pending.resolve({ ...models('stale-ultra-model'), warnings: catalogWarnings })
    await sync
    expect(vm.antigravityAccountCatalog.models).toBeNull()
    expect(vm.antigravityModelMappings).toEqual([{ from: 'gemini-2.5-flash', to: 'gemini-2.5-flash' }])
    expect(showError).not.toHaveBeenCalled()
    expect(showWarning).not.toHaveBeenCalled()
  })

  it('ignores an earlier response after reopening the same account', async () => {
    const old = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(old.promise)
    const wrapper = mountEditor()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    old.resolve(models('stale-model'))
    await flushPromises()
    expect(suggestionIds(wrapper)).toEqual(['gemini-2.5-flash', 'gemini-3.7-flash-low'])
  })

  it.each(['openai', 'anthropic', 'gemini', 'grok'] as const)('makes no availability request for %s accounts', async (platform) => {
    mountEditor(account({ platform }))
    await flushPromises()
    expect(syncUpstreamModels).not.toHaveBeenCalled()
  })

  it.each(['oauth', 'apikey'] as const)('preserves untouched raw %s mappings even when availability fails', async (type) => {
    const savedMapping = {
      'gemini-3.7-flash': 'gemini-3.7-flash-tiered',
      'gemini-4-argon-high': 'gemini-4-argon-high',
      'my-private-alias': 'my-private-upstream'
    }
    syncUpstreamModels.mockRejectedValueOnce({ status: 502, message: 'Catalog failed' })
    const selected = account({ type, credentials: { model_mapping: savedMapping, model_whitelist: ['legacy-model'] }, credentials_status: { has_api_key: true } })
    const wrapper = mountEditor(selected)
    await flushPromises()
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccount).toHaveBeenCalledTimes(1)
    const payload = updateAccount.mock.calls[0][1]
    expect(payload.credentials.model_mapping).toEqual(savedMapping)
    expect(payload.credentials.model_whitelist).toEqual(['legacy-model'])
    expect(selected.credentials.model_mapping).toEqual(savedMapping)
  })

  it.each([
    { model_whitelist: ['gemini-3.7-flash', 'my-custom-model'] },
    { model_mapping: {} },
    {}
  ])('preserves untouched legacy/empty configuration %j on save', async (credentials) => {
    const wrapper = mountEditor(account({ credentials }))
    await flushPromises()
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    const saved = updateAccount.mock.calls[0][1].credentials
    for (const key of ['model_mapping', 'model_whitelist'] as const) {
      if (Object.prototype.hasOwnProperty.call(credentials, key)) expect(saved[key]).toEqual(credentials[key])
      else expect(saved).not.toHaveProperty(key)
    }
  })

  it('allows explicit custom edits while retaining other unavailable mappings', async () => {
    const wrapper = mountEditor(account({ credentials: { model_mapping: {
      'my-custom-alias': 'old-custom-target',
      'gemini-4-argon-high': 'gemini-4-argon-high'
    } } }))
    await flushPromises()
    const target = wrapper.findAll<HTMLInputElement>('input[list]').find(input => input.element.value === 'old-custom-target')!
    await target.setValue('new-custom-target')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccount.mock.calls[0][1].credentials.model_mapping).toEqual({
      'my-custom-alias': 'new-custom-target', 'gemini-4-argon-high': 'gemini-4-argon-high'
    })
  })
})

interface SelectorState {
  fillRelated: () => Promise<void>
  syncUpstreamModels: () => Promise<void>
  availableOptions: { value: string }[]
  accountModelsLoading: boolean
}
const selectorState = (wrapper: VueWrapper) => wrapper.vm as unknown as SelectorState
function mountSelector(props: Record<string, unknown> = {}) {
  return mount(ModelWhitelistSelector, {
    props: { modelValue: ['saved-custom-model'], platform: 'antigravity', accountId: 42, ...props },
    global: { stubs: { ModelIcon: true, Icon: true, teleport: true } }
  })
}
const openSelector = (wrapper: VueWrapper) => wrapper.get('div.cursor-pointer').trigger('click')
const optionIds = (wrapper: VueWrapper) => selectorState(wrapper).availableOptions.map(option => option.value)

describe('ModelWhitelistSelector existing Antigravity accounts', () => {
  it('discovers B absent from the saved whitelist and appends it only after explicit sync', async () => {
    syncUpstreamModels.mockResolvedValue(models('model-A', 'model-B'))
    const wrapper = mountSelector({ modelValue: ['model-A'] })
    await openSelector(wrapper)
    await flushPromises()
    expect(optionIds(wrapper)).toEqual(['model-A', 'model-B'])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.syncUpstreamModels')!.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')).toEqual([[['model-A', 'model-B']]])
    expect(syncUpstreamModels).toHaveBeenCalledTimes(2)
    expect(getAvailableModels).not.toHaveBeenCalled()
  })

  it('coalesces opening and explicit sync and preserves warnings once per response', async () => {
    const pending = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountSelector()
    await openSelector(wrapper)
    const sync = selectorState(wrapper).syncUpstreamModels()
    pending.resolve({ ...models('new-model'), warnings: catalogWarnings })
    await sync
    expect(syncUpstreamModels).toHaveBeenCalledExactlyOnceWith(42)
    expect(showWarning.mock.calls.map(([message]) => message)).toEqual(warningMessages)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['saved-custom-model', 'new-model']]])
    await selectorState(wrapper).fillRelated()
    expect(showWarning).toHaveBeenCalledTimes(catalogWarnings.length)
  })

  it('preserves warnings from an empty explicit sync response', async () => {
    syncUpstreamModels.mockResolvedValueOnce({ models: [], warnings: catalogWarnings })
    const wrapper = mountSelector()
    await selectorState(wrapper).syncUpstreamModels()
    expect(showWarning.mock.calls.map(([message]) => message)).toEqual(warningMessages)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(optionIds(wrapper)).toEqual([])
  })

  it('loads lazily and reuses a successful catalog across search, reopening, and fill', async () => {
    const wrapper = mountSelector()
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    expect(optionIds(wrapper)).toEqual([])
    await openSelector(wrapper)
    await flushPromises()
    expect(syncUpstreamModels).toHaveBeenCalledExactlyOnceWith(42)
    expect(optionIds(wrapper)).toEqual(['gemini-2.5-flash', 'gemini-3.7-flash-low'])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('[data-testid="model-dropdown"] input').setValue('3.7')
    expect(wrapper.findAll('[data-testid="model-option"]')).toHaveLength(1)
    await openSelector(wrapper)
    await openSelector(wrapper)
    await selectorState(wrapper).fillRelated()
    expect(syncUpstreamModels).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['saved-custom-model', 'gemini-2.5-flash', 'gemini-3.7-flash-low']]])
  })

  it('coalesces filling and opening while a lazy catalog request is pending', async () => {
    const pending = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountSelector()
    const fill = selectorState(wrapper).fillRelated()
    await openSelector(wrapper)
    expect(syncUpstreamModels).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    pending.resolve(models('account-only-model'))
    await fill
    expect(wrapper.emitted('update:modelValue')).toEqual([[['saved-custom-model', 'account-only-model']]])
  })

  it('does not fall back or rewrite selection on failure; retry restores only the live suggestions', async () => {
    syncUpstreamModels.mockRejectedValueOnce({ status: 502, message: 'Catalog failed' })
    const wrapper = mountSelector()
    await openSelector(wrapper)
    await flushPromises()
    expect(optionIds(wrapper)).toEqual([])
    expect(wrapper.get('[role="alert"]').text()).toBe('Catalog failed')
    expect(wrapper.text()).toContain('saved-custom-model')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('[data-testid="retry-account-models"]').trigger('click')
    await flushPromises()
    expect(optionIds(wrapper)).toEqual(['gemini-2.5-flash', 'gemini-3.7-flash-low'])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    syncUpstreamModels.mockResolvedValueOnce(models())
    await selectorState(wrapper).syncUpstreamModels()
    expect(optionIds(wrapper)).toEqual([])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(getAvailableModels).not.toHaveBeenCalled()
  })

  it('normalizes only the returned 3.7 compatibility alias and never adds missing public tiers', async () => {
    syncUpstreamModels.mockResolvedValueOnce(models('gemini-3.7-flash', 'gemini-3.8-flash-low'))
    const wrapper = mountSelector()
    await selectorState(wrapper).syncUpstreamModels()
    expect(optionIds(wrapper)).toEqual(['gemini-3.7-flash-high', 'gemini-3.7-flash-medium', 'gemini-3.7-flash-low', 'gemini-3.8-flash-low'])
    expect(getAvailableModels).not.toHaveBeenCalled()
  })

  it.each(['success', 'error'] as const)('ignores obsolete %s and pending fill after switching account IDs', async (outcome) => {
    const old = deferred<SyncUpstreamModelsResult>()
    const current = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = mountSelector()
    await openSelector(wrapper)
    const fill = selectorState(wrapper).fillRelated()
    await wrapper.setProps({ accountId: 43, modelValue: ['new-account-custom-model'] })
    if (outcome === 'success') old.resolve(models('ultra-only-model'))
    else old.reject({ message: 'Old account failed' })
    await fill
    expect(optionIds(wrapper)).toEqual([])
    expect(selectorState(wrapper).accountModelsLoading).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(showError).not.toHaveBeenCalled()
    current.resolve(models('pro-only-model'))
    await flushPromises()
    expect(optionIds(wrapper)).toEqual(['pro-only-model'])
  })

  it('drops late errors and pending sync updates after unmounting', async () => {
    const pending = deferred<SyncUpstreamModelsResult>()
    syncUpstreamModels.mockReturnValueOnce(pending.promise)
    const wrapper = mountSelector()
    const sync = selectorState(wrapper).syncUpstreamModels()
    wrapper.unmount()
    pending.reject({ message: 'Late failure' })
    await sync
    expect(showError).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it.each([
    { platform: 'antigravity', accountId: undefined },
    { platform: 'openai' }, { platform: 'anthropic' }, { platform: 'gemini' }, { platform: 'grok' }
  ])('retains static creation/other-platform behavior for %j', async (props) => {
    const wrapper = mountSelector(props)
    await openSelector(wrapper)
    await selectorState(wrapper).fillRelated()
    expect(optionIds(wrapper).length).toBeGreaterThan(0)
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:modelValue')?.[0][0]).toContain('saved-custom-model')
  })
})
