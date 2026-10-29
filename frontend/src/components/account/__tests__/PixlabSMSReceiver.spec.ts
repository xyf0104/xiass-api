import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { Ref } from 'vue'
import PixlabSMSReceiver from '../PixlabSMSReceiver.vue'
import { usePixlabSMSReceiver } from '@/composables/usePixlabSMSReceiver'

enableAutoUnmount(afterEach)
afterEach(() => vi.useRealTimers())

const SMSReceiverActionDialogStub = {
  props: ['show'],
  emits: ['cancel', 'confirm'],
  template: `
    <div v-if="show" data-testid="sms-confirm-dialog">
      <button type="button" data-testid="confirm-sms-receiver-action" @click="$emit('confirm')">confirm</button>
      <button type="button" data-testid="cancel-sms-receiver-action" @click="$emit('cancel')">cancel</button>
    </div>
  `,
}

const { receiverMocks, receiverState, copyToClipboardMock } = vi.hoisted(() => ({
  receiverMocks: {
    start: vi.fn(),
    stop: vi.fn(),
    refresh: vi.fn(),
    changeNumber: vi.fn(),
    cancel: vi.fn(),
    refreshQueueStatus: vi.fn(),
    appendCardKeys: vi.fn(),
    clearQueuedCardKeys: vi.fn()
  },
  receiverState: {
    phase: 'idle',
    canChangeNumber: false,
    hasActiveSession: false,
    code: '--',
  },
  copyToClipboardMock: vi.fn().mockResolvedValue(true)
}))

vi.mock('@/composables/usePixlabSMSReceiver', async () => {
  const { ref } = await import('vue')
  return {
    usePixlabSMSReceiver: vi.fn(() => ({
      phase: ref(receiverState.phase),
      phoneDisplay: ref('--'),
      phoneForCopy: ref('+27749433060'),
      countryCallingCode: ref('27'),
      localPhoneNumber: ref('749433060'),
      region: ref('南非'),
      countryFlag: ref('🇿🇦'),
      code: ref(receiverState.code),
      queuedKeyCount: ref(3),
      statusText: ref('等待取号'),
      statusClass: ref('text-cyan-600'),
      sessionExpiresAt: ref('2026-08-14T00:15:00.000Z'),
      sessionExpiryText: ref('12:34'),
      hasActiveSession: ref(receiverState.hasActiveSession),
      canRefresh: ref(true),
      canChangeNumber: ref(receiverState.canChangeNumber),
      canCancel: ref(true),
      isRefreshing: ref(false),
      isChangingNumber: ref(false),
      isCancelling: ref(false),
      ...receiverMocks
    }))
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showInfo: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: copyToClipboardMock })
}))

describe('PixlabSMSReceiver', () => {
  beforeEach(() => {
    Object.values(receiverMocks).forEach((mock) => mock.mockReset())
    vi.mocked(usePixlabSMSReceiver).mockClear()
    receiverState.phase = 'idle'
    receiverState.canChangeNumber = false
    receiverState.hasActiveSession = false
    receiverState.code = '--'
    copyToClipboardMock.mockClear()
    receiverMocks.start.mockResolvedValue('waiting')
  })

  function mountReceiver(active = true, automationMode = false) {
    return mount(PixlabSMSReceiver, {
      props: { active, automationMode },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub,
        },
      },
    })
  }

  async function claimNumber(wrapper: ReturnType<typeof mountReceiver>) {
    await wrapper.get('[data-testid="request-sms-phone"]').trigger('click')
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
  }

  it('requires an explicit request before claiming a number for every OAuth flow', async () => {
    const wrapper = mountReceiver()

    await flushPromises()
    expect(receiverMocks.start).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="request-sms-phone"]').text()).toContain('获取手机号')

    await wrapper.get('[data-testid="request-sms-phone"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.start).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060']])
    expect(wrapper.find('[data-testid="request-sms-phone"]').exists()).toBe(false)
  })

  it('keeps the request button visible but disabled until an authorization link exists', async () => {
    const wrapper = mountReceiver(false)

    const requestButton = wrapper.get('[data-testid="request-sms-phone"]')
    expect(requestButton.attributes('disabled')).toBeDefined()
    expect(receiverMocks.start).not.toHaveBeenCalled()

    await wrapper.setProps({ active: true })
    expect(wrapper.get('[data-testid="request-sms-phone"]').attributes('disabled')).toBeUndefined()
    expect(receiverMocks.start).not.toHaveBeenCalled()
  })

  it('copies the complete international number while keeping country code and local number separated on screen', async () => {
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    expect(wrapper.text()).toContain('+27')
    expect(wrapper.text()).toContain('749433060')
	await wrapper.get('[title="点击手机号框复制完整国际号码"]').trigger('click')
    expect(copyToClipboardMock).toHaveBeenCalledWith('+27749433060', '手机号已复制')
  })

  it('renders the countdown supplied by the current server session', async () => {
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    const countdown = wrapper.get('[data-testid="sms-session-countdown"]')
    expect(countdown.text()).toContain('号码有效期')
    expect(countdown.text()).toContain('12:34')
    expect(countdown.get('time').attributes('datetime')).toBe('2026-08-14T00:15:00.000Z')
  })

  it('renders a received SMS code in six aligned copyable slots', async () => {
    receiverState.code = '581768'
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    const slots = wrapper.get('[data-testid="sms-code-slots"]')
    expect(slots.findAll(':scope > span').map((slot) => slot.text())).toEqual(['5', '8', '1', '7', '6', '8'])
    await slots.element.parentElement?.click()
    expect(copyToClipboardMock).toHaveBeenCalledWith('581768', '验证码已复制')
  })

  it('keeps a verification code visible when it arrives during cancellation', async () => {
    receiverMocks.cancel.mockResolvedValue('received')
    const wrapper = mountReceiver()
    await claimNumber(wrapper)
    const cancelButton = wrapper.findAll('button').find((button) => button.text().includes('取消'))
    expect(cancelButton).toBeDefined()
    await cancelButton!.trigger('click')
    await flushPromises()

    expect(receiverMocks.cancel).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('session-cancelled')).toBeUndefined()
    expect(wrapper.find('[data-testid="request-sms-phone"]').exists()).toBe(false)
  })

  it('requires an in-app confirmation before changing the current number', async () => {
    receiverState.canChangeNumber = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    const changeButton = wrapper.findAll('button').find((button) => button.text().includes('换号'))
    expect(changeButton).toBeDefined()

    await changeButton!.trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060'], ['+27749433060']])
  })

  it('opens the in-app replacement confirmation when the OAuth page rejects the active number', async () => {
    receiverState.canChangeNumber = true
    receiverState.hasActiveSession = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mount(PixlabSMSReceiver, {
      props: { active: true, replacementRequired: true },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub,
        },
      },
    })

    await flushPromises()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)
    await wrapper.get('[data-testid="cancel-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '换号')!.trigger('click')
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    // The mounted confirmed session is replayed once for recovery, then the
    // confirmed replacement is emitted once more to overwrite that number.
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060'], ['+27749433060']])
  })

  it('replays an existing confirmed number when manual recovery advances to the submit node', async () => {
    receiverState.hasActiveSession = true
    const wrapper = mount(PixlabSMSReceiver, {
      props: { active: true, submissionKey: 'sms_confirm' },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub,
        },
      },
    })

    await flushPromises()
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060']])

    await wrapper.setProps({ submissionKey: 'phone_submit' })
    await flushPromises()
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060'], ['+27749433060']])
  })

  it('emits cancellation only after a confirmed cancellation completes', async () => {
    receiverMocks.cancel.mockResolvedValue('expired')
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    const cancelButton = wrapper.findAll('button').find((button) => button.text().includes('取消'))
    expect(cancelButton).toBeDefined()
    await cancelButton!.trigger('click')
    await flushPromises()
    expect(receiverMocks.cancel).not.toHaveBeenCalled()
    expect(wrapper.emitted('session-cancelled')).toBeUndefined()

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('session-cancelled')).toEqual([[]])
  })

  it('cancels an automation-owned number immediately when the workflow resets', async () => {
    receiverState.hasActiveSession = true
    receiverMocks.cancel.mockResolvedValue('expired')
    const wrapper = mount(PixlabSMSReceiver, {
      props: { active: true, automationMode: true, automationCancelSignal: 0 },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub
        }
      }
    })

    await wrapper.setProps({ automationCancelSignal: 1 })
    await flushPromises()

    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
    expect(wrapper.emitted('session-cancelled')).toEqual([[]])
  })

  it('requires confirmation for a manual claim after an automated claim fails', async () => {
    receiverMocks.start.mockRejectedValueOnce(new Error('SMS unavailable'))
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-testid="request-sms-phone"]').trigger('click')
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)
    await wrapper.get('[data-testid="cancel-sms-receiver-action"]').trigger('click')
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)

    await claimNumber(wrapper)
    expect(receiverMocks.start).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060']])
  })

  it.each([
    { label: '换号', action: 'changeNumber' as const, outcome: 'waiting' },
    { label: '取消', action: 'cancel' as const, outcome: 'expired' },
  ])('requires confirmation for a manual $label click in automation mode', async ({ label, action, outcome }) => {
    receiverState.hasActiveSession = true
    receiverState.canChangeNumber = true
    receiverMocks[action].mockResolvedValue(outcome)
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    const button = wrapper.findAll('button').find(button => button.text() === label)!

    await button.trigger('click')
    await flushPromises()
    expect(receiverMocks[action]).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)
    await wrapper.get('[data-testid="cancel-sms-receiver-action"]').trigger('click')
    expect(receiverMocks[action]).not.toHaveBeenCalled()

    await button.trigger('click')
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks[action]).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
  })

  it('still claims automatically when the Team workflow enters the phone node', async () => {
    const wrapper = mountReceiver(false, true)
    expect(receiverMocks.start).not.toHaveBeenCalled()
    await wrapper.setProps({ active: true })
    await flushPromises()
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060']])
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
  })

  it('still replaces a rejected number automatically from the Team workflow node', async () => {
    receiverState.hasActiveSession = true
    receiverState.canChangeNumber = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    await wrapper.setProps({ replacementRequired: true })
    await flushPromises()
    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060'], ['+27749433060']])
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
  })

  it('still replaces a waiting Team number automatically after 120 seconds', async () => {
    vi.useFakeTimers()
    receiverState.phase = 'waiting'
    receiverState.hasActiveSession = true
    receiverState.canChangeNumber = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(119_999)
    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
  })

  it.each(['换号', '取消'])('defers timed replacement while the manual %s dialog is open and resumes after dismissal', async label => {
    vi.useFakeTimers()
    receiverState.phase = 'waiting'
    receiverState.hasActiveSession = true
    receiverState.canChangeNumber = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(119_000)
    await wrapper.findAll('button').find(button => button.text() === label)!.trigger('click')
    await vi.advanceTimersByTimeAsync(120_000)
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)
    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    expect(receiverMocks.cancel).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="cancel-sms-receiver-action"]').trigger('click')
    await vi.advanceTimersByTimeAsync(119_999)
    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    expect(receiverMocks.cancel).not.toHaveBeenCalled()
  })

  it.each(['submission', 'phone', 'session'])('invalidates a manual confirmation when its %s identity changes', async identity => {
    receiverState.hasActiveSession = true
    receiverState.canChangeNumber = true
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '换号')!.trigger('click')
    const dialog = wrapper.findComponent(SMSReceiverActionDialogStub)
    const receiver = vi.mocked(usePixlabSMSReceiver).mock.results.at(-1)!.value as { phoneForCopy: Ref<string>; sessionExpiresAt: Ref<string> }
    if (identity === 'submission') await wrapper.setProps({ submissionKey: 'new-phone-node' })
    else if (identity === 'phone') receiver.phoneForCopy.value = '+12025550123'
    else receiver.sessionExpiresAt.value = '2026-08-14T00:30:00.000Z'
    // A stale confirm event must also fail before the identity watcher runs.
    dialog.vm.$emit('confirm')
    await flushPromises()
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
  })

  it('keeps workflow reset cancellation immediate and invalidates an open manual dialog', async () => {
    receiverState.hasActiveSession = true
    receiverState.canChangeNumber = true
    receiverMocks.cancel.mockResolvedValue('expired')
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '换号')!.trigger('click')
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)
    await wrapper.setProps({ automationCancelSignal: 1 })
    await flushPromises()
    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
    expect(wrapper.emitted('session-cancelled')).toEqual([[]])
  })

  it('rechecks a Team rejection deferred by a manual dialog when it is dismissed', async () => {
    receiverState.hasActiveSession = true
    receiverState.canChangeNumber = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mountReceiver(true, true)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '取消')!.trigger('click')
    await wrapper.setProps({ replacementRequired: true })
    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="cancel-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    expect(receiverMocks.cancel).not.toHaveBeenCalled()
  })

})
