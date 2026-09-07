import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpenAIAutoResetSettings from '../OpenAIAutoResetSettings.vue'

const { getConfig, setConfig } = vi.hoisted(() => ({ getConfig: vi.fn(), setConfig: vi.fn() }))
vi.mock('@/api/admin/openaiAutoReset', () => ({ getOpenAIAutoReset: getConfig, setOpenAIAutoReset: setConfig }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const off = { enabled: false, threshold_5h: 1, threshold_7d: 1, revision: '' }

describe('OpenAIAutoResetSettings', () => {
  beforeEach(() => { getConfig.mockReset().mockResolvedValue(off); setConfig.mockReset() })

  it('loads OFF and never enables from merely opening an account', async () => {
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    expect(wrapper.get('[data-testid="auto-reset-enabled"]').attributes('disabled')).toBeDefined()
    await flushPromises()
    expect(getConfig).toHaveBeenCalledWith(42)
    expect((wrapper.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false)
    expect(setConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('requires an explicit save and sends ratio thresholds through the dedicated API', async () => {
    setConfig.mockResolvedValue({ ...off, enabled: true, threshold_5h: .9, threshold_7d: .95, revision: 'new' })
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    await flushPromises()
    await wrapper.get('[data-testid="auto-reset-enabled"]').setValue(true)
    expect(setConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="auto-reset-5h"]').setValue('90')
    await wrapper.get('[data-testid="auto-reset-7d"]').setValue('95')
    await wrapper.get('[data-testid="auto-reset-save"]').trigger('click')
    await flushPromises()
    expect(setConfig).toHaveBeenCalledExactlyOnceWith(42, { enabled: true, threshold_5h: .9, threshold_7d: .95 })
    wrapper.unmount()
  })

  it('persists explicit OFF even when an uncertain attempt remains', async () => {
    getConfig.mockResolvedValue({ ...off, enabled: true, pending: true })
    setConfig.mockResolvedValue({ ...off, pending: true })
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    await flushPromises()
    await wrapper.get('[data-testid="auto-reset-enabled"]').setValue(false)
    await wrapper.get('[data-testid="auto-reset-save"]').trigger('click')
    await flushPromises()
    expect(setConfig).toHaveBeenCalledWith(42, { enabled: false, threshold_5h: 1, threshold_7d: 1 })
    expect(wrapper.text()).toContain('admin.accounts.autoResetCredit.pending')
    wrapper.unmount()
  })

  it('fails closed when this node cannot read the config', async () => {
    getConfig.mockRejectedValue(new Error('remote read only'))
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    await flushPromises()
    expect(wrapper.get('[data-testid="auto-reset-enabled"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="auto-reset-save"]').attributes('disabled')).toBeDefined()
    expect(setConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('rejects invalid thresholds without a request', async () => {
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    await flushPromises()
    await wrapper.get('[data-testid="auto-reset-enabled"]').setValue(true)
    await wrapper.get('[data-testid="auto-reset-5h"]').setValue('101')
    await wrapper.get('[data-testid="auto-reset-save"]').trigger('click')
    expect(setConfig).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('invalidThreshold')
    wrapper.unmount()
  })

  it('does not hydrate a reused editor from the previous account response', async () => {
    let resolveOld!: (value: typeof off) => void
    getConfig.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve })).mockResolvedValueOnce(off)
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    await wrapper.setProps({ accountId: 43 })
    await flushPromises()
    resolveOld({ ...off, enabled: true })
    await flushPromises()
    expect((wrapper.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false)
    expect(setConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('can turn OFF with invalid draft thresholds and preserves saved thresholds', async () => {
    getConfig.mockResolvedValue({ ...off, enabled: true, threshold_5h: .9, threshold_7d: .95 })
    setConfig.mockResolvedValue({ ...off, threshold_5h: .9, threshold_7d: .95 })
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    await flushPromises()
    await wrapper.get('[data-testid="auto-reset-5h"]').setValue('')
    await wrapper.get('[data-testid="auto-reset-7d"]').setValue('101')
    await wrapper.get('[data-testid="auto-reset-enabled"]').setValue(false)
    await wrapper.get('[data-testid="auto-reset-save"]').trigger('click')
    await flushPromises()
    expect(setConfig).toHaveBeenCalledExactlyOnceWith(42, { enabled: false, threshold_5h: .9, threshold_7d: .95 })
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not apply an old save response to another account', async () => {
    let resolveSave!: (value: typeof off) => void
    setConfig.mockImplementationOnce(() => new Promise(resolve => { resolveSave = resolve }))
    const wrapper = mount(OpenAIAutoResetSettings, { props: { accountId: 42 } })
    await flushPromises()
    await wrapper.get('[data-testid="auto-reset-enabled"]').setValue(true)
    await wrapper.get('[data-testid="auto-reset-save"]').trigger('click')
    await wrapper.setProps({ accountId: 43 })
    await flushPromises()
    resolveSave({ ...off, enabled: true })
    await flushPromises()
    expect((wrapper.get('[data-testid="auto-reset-enabled"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.text()).not.toContain('admin.accounts.autoResetCredit.saved')
    wrapper.unmount()
  })
})
