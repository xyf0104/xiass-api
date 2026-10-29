import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/smsReceiver', () => ({
  getStatus: vi.fn(),
  addCardKeys: vi.fn(),
  clearCardKeys: vi.fn(),
  redeem: vi.fn(),
  resume: vi.fn(),
  check: vi.fn(),
  change: vi.fn(),
  cancel: vi.fn(),
  memberSMSReceiverAPI: {
    getStatus: vi.fn(),
    redeem: vi.fn(),
    resume: vi.fn(),
    check: vi.fn(),
    change: vi.fn(),
    cancel: vi.fn()
  }
}))

import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import { usePixlabSMSReceiver } from '@/composables/usePixlabSMSReceiver'

const ACTIVE_SESSION_STORAGE_KEY = 'xiass-api:pixlab-sms:active-session-id'

describe('usePixlabSMSReceiver', () => {
  let receiver: ReturnType<typeof usePixlabSMSReceiver>

  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    receiver = usePixlabSMSReceiver()
  })

  afterEach(() => {
    receiver.stop()
    vi.useRealTimers()
  })

  it('refreshes queue status without calling a session endpoint when no session exists', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 4, active_count: 0 })

    await expect(receiver.refresh()).resolves.toBe('unavailable')

    expect(smsReceiverAPI.getStatus).toHaveBeenCalledTimes(1)
    expect(smsReceiverAPI.resume).not.toHaveBeenCalled()
    expect(smsReceiverAPI.check).not.toHaveBeenCalled()
    expect(receiver.queuedKeyCount.value).toBe(4)
    expect(receiver.activeSessionCount.value).toBe(0)
    expect(receiver.phase.value).toBe('idle')
  })

  it('refreshes an active session and applies the latest phone, status, and code', async () => {
    receiver.stop()
    localStorage.setItem(ACTIVE_SESSION_STORAGE_KEY, 'server-session-refresh')
    receiver = usePixlabSMSReceiver()
    vi.mocked(smsReceiverAPI.check).mockResolvedValue({
      status: 'RECEIVED',
      number: '27749433060',
      country: '南非',
      code: '318204',
      queued_count: 3
    })

    await expect(receiver.refresh()).resolves.toBe('received')

    expect(smsReceiverAPI.check).toHaveBeenCalledWith('server-session-refresh')
    expect(receiver.phoneForCopy.value).toBe('+27749433060')
    expect(receiver.region.value).toBe('南非')
    expect(receiver.code.value).toBe('318204')
    expect(receiver.phase.value).toBe('received')
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBeNull()
  })

  it('clears a stale phone and code while preserving an active pending-number session', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-pending-number',
      status: 'WAITING',
      number: '12025550123',
      country: '美国',
      queued_count: 0,
    })
    vi.mocked(smsReceiverAPI.check).mockResolvedValue({
      session_id: 'server-session-pending-number',
      status: 'WAITING',
      number_pending: true,
      poll_seconds: 2,
      queued_count: 0,
    })

    await receiver.start()
    expect(receiver.phoneForCopy.value).toBe('+12025550123')
    receiver.code.value = '654321'

    await receiver.refresh()

    expect(receiver.hasActiveSession.value).toBe(true)
    expect(receiver.phase.value).toBe('waiting')
    expect(receiver.phoneForCopy.value).toBe('')
    expect(receiver.phoneDisplay.value).toBe('--')
    expect(receiver.localPhoneNumber.value).toBe('--')
    expect(receiver.code.value).toBe('--')
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBe('server-session-pending-number')
  })

  it('uses a valid provider poll interval and honors local stop without dropping the session', async () => {
    vi.useFakeTimers()
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-poll-interval',
      status: 'WAITING',
      number_pending: true,
      poll_seconds: 2,
      queued_count: 0,
    })
    vi.mocked(smsReceiverAPI.check).mockResolvedValue({
      session_id: 'server-session-poll-interval',
      status: 'WAITING',
      number_pending: true,
      local_stop: true,
      queued_count: 0,
    })

    await receiver.start()
    await vi.advanceTimersByTimeAsync(1_999)
    expect(smsReceiverAPI.check).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(smsReceiverAPI.check).toHaveBeenCalledTimes(1)
    expect(receiver.hasActiveSession.value).toBe(true)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(smsReceiverAPI.check).toHaveBeenCalledTimes(1)
  })

  it('deduplicates concurrent refreshes for the same active session', async () => {
    receiver.stop()
    localStorage.setItem(ACTIVE_SESSION_STORAGE_KEY, 'server-session-concurrent')
    receiver = usePixlabSMSReceiver()
    let resolveCheck: ((value: Awaited<ReturnType<typeof smsReceiverAPI.check>>) => void) | undefined
    vi.mocked(smsReceiverAPI.check).mockImplementation(() => new Promise((resolve) => {
      resolveCheck = resolve
    }))

    const first = receiver.refresh()
    const second = receiver.refresh()

    expect(smsReceiverAPI.check).toHaveBeenCalledTimes(1)
    resolveCheck?.({
      session_id: 'server-session-concurrent',
      status: 'WAITING',
      number: '27749433060',
      country: '南非',
      queued_count: 2,
      expires_at: '2026-08-14T00:15:00.000Z'
    })

    await expect(Promise.all([first, second])).resolves.toEqual(['waiting', 'waiting'])
    expect(smsReceiverAPI.check).toHaveBeenCalledTimes(1)
  })

  it('uses the server expiry timestamp and refreshes an expired session only once', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-14T00:05:00.000Z'))
    vi.mocked(smsReceiverAPI.getStatus)
      .mockResolvedValueOnce({ queued_count: 1, active_count: 0 })
      .mockResolvedValueOnce({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-expiry',
      status: 'WAITING',
      number: '27749433060',
      country: '南非',
      queued_count: 0,
      expires_at: '2026-08-14T00:00:05.000Z',
      server_time: '2026-08-14T00:00:00.000Z'
    })
    vi.mocked(smsReceiverAPI.check).mockResolvedValue({
      status: 'EXPIRED',
      queued_count: 1
    })

    await receiver.start()
    expect(receiver.sessionExpiresAt.value).toBe('2026-08-14T00:00:05.000Z')
    expect(receiver.sessionExpiryText.value).toBe('00:05')

    await vi.advanceTimersByTimeAsync(5_000)

    expect(smsReceiverAPI.check).toHaveBeenCalledTimes(1)
    expect(smsReceiverAPI.getStatus).toHaveBeenCalledTimes(2)
    expect(receiver.phase.value).toBe('expired')
    expect(receiver.hasActiveSession.value).toBe(false)
    expect(receiver.sessionExpiresAt.value).toBe('')
    expect(receiver.queuedKeyCount.value).toBe(1)

    await vi.advanceTimersByTimeAsync(10_000)
    expect(smsReceiverAPI.check).toHaveBeenCalledTimes(1)
  })

  it('retries expiry synchronization after a transient network failure', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-14T00:00:00.000Z'))
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-retry',
      status: 'WAITING',
      number: '27749433060',
      country: '南非',
      queued_count: 0,
      expires_at: '2026-08-14T00:00:02.000Z',
      server_time: '2026-08-14T00:00:00.000Z'
    })
    vi.mocked(smsReceiverAPI.check).mockRejectedValueOnce(new Error('network unavailable'))
    vi.mocked(smsReceiverAPI.resume).mockResolvedValueOnce({
      status: 'EXPIRED',
      queued_count: 1,
      server_time: '2026-08-14T00:00:07.000Z'
    })

    await receiver.start()
    await vi.advanceTimersByTimeAsync(2_000)

    expect(smsReceiverAPI.check).toHaveBeenCalledTimes(1)
    expect(receiver.phase.value).toBe('error')
    expect(receiver.hasActiveSession.value).toBe(true)

    await vi.advanceTimersByTimeAsync(4_000)
    expect(smsReceiverAPI.resume).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1_000)
    expect(smsReceiverAPI.resume).toHaveBeenCalledTimes(1)
    expect(receiver.phase.value).toBe('expired')
    expect(receiver.hasActiveSession.value).toBe(false)
  })

  it('rejects non-RFC3339 expiry values instead of treating numeric timestamps as valid', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-invalid-expiry',
      status: 'WAITING',
      queued_count: 0,
      expires_at: 1_786_665_600 as unknown as string,
      server_time: '2026-08-14T00:00:00.000Z'
    })

    await receiver.start()

    expect(receiver.sessionExpiresAt.value).toBe('')
    expect(receiver.sessionExpiryText.value).toBe('00:00')
  })

  it('stores submitted card keys on the server and keeps only an opaque session locally', async () => {
    vi.mocked(smsReceiverAPI.addCardKeys).mockResolvedValue({ added_count: 2, queued_count: 2 })
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 2, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-a',
      status: 'WAITING',
      number: '8613812345678',
      country: '中国',
      queued_count: 1
    })

    await expect(receiver.appendCardKeys('CARD-A\nCARD-B')).resolves.toBe(2)
    await expect(receiver.start()).resolves.toBe('waiting')

    expect(smsReceiverAPI.addCardKeys).toHaveBeenCalledWith('CARD-A\nCARD-B')
    expect(smsReceiverAPI.redeem).toHaveBeenCalledTimes(1)
    expect(receiver.phoneDisplay.value).toBe('+86 13812345678')
    expect(receiver.phoneForCopy.value).toBe('+8613812345678')
    expect(receiver.countryCallingCode.value).toBe('86')
    expect(receiver.localPhoneNumber.value).toBe('13812345678')
    expect(receiver.queuedKeyCount.value).toBe(1)
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBe('server-session-a')
    expect(JSON.stringify(localStorage)).not.toContain('CARD-A')
    expect(JSON.stringify(localStorage)).not.toContain('CARD-B')
  })

  it('removes the opaque local session after the server reports a verification code', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-b',
      status: 'RECEIVED',
      number: '12025550123',
      country: '美国',
      code: '654321',
      queued_count: 0
    })

    await expect(receiver.start()).resolves.toBe('received')

    expect(receiver.code.value).toBe('654321')
    expect(receiver.phase.value).toBe('received')
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBeNull()
    expect(receiver.queuedKeyCount.value).toBe(0)
  })

  it('expands country codes to a complete region name and keeps the full international number copyable', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-region',
      status: 'WAITING',
      number: '12025550123',
      country: 'US',
      queued_count: 0
    })

    await receiver.start()

    expect(receiver.countryCallingCode.value).toBe('1')
    expect(receiver.localPhoneNumber.value).toBe('2025550123')
    expect(receiver.phoneForCopy.value).toBe('+12025550123')
    expect(receiver.region.value).toBe('美国')
    expect(receiver.countryFlag.value).toBe('🇺🇸')
  })

  it('separates Colombian country code and renders its full region with flag', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-colombia',
      status: 'WAITING',
      number: '573242390811',
      country: '🌎 哥伦',
      queued_count: 0
    })

    await receiver.start()

    expect(receiver.countryCallingCode.value).toBe('57')
    expect(receiver.localPhoneNumber.value).toBe('3242390811')
    expect(receiver.phoneForCopy.value).toBe('+573242390811')
    expect(receiver.region.value).toBe('哥伦比亚')
    expect(receiver.countryFlag.value).toBe('🇨🇴')
  })

  it('formats a South African number as +27 plus the local number for copying', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-south-africa',
      status: 'WAITING',
      number: '27749433060',
      country: '南非',
      queued_count: 0
    })

    await receiver.start()

    expect(receiver.countryCallingCode.value).toBe('27')
    expect(receiver.localPhoneNumber.value).toBe('749433060')
    expect(receiver.phoneForCopy.value).toBe('+27749433060')
    expect(receiver.region.value).toBe('南非')
    expect(receiver.countryFlag.value).toBe('🇿🇦')
  })

  it('uses one server-side change request and replaces the opaque session ID', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 2, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-old',
      status: 'WAITING',
      number: '8613812345678',
      queued_count: 1
    })
    vi.mocked(smsReceiverAPI.change).mockResolvedValue({
      session_id: 'server-session-new',
      status: 'WAITING',
      number: '8613912345678',
      queued_count: 0
    })

    await receiver.start()
    await expect(receiver.changeNumber()).resolves.toBe('waiting')

    expect(smsReceiverAPI.change).toHaveBeenCalledWith('server-session-old')
    expect(smsReceiverAPI.cancel).not.toHaveBeenCalled()
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBe('server-session-new')
    expect(receiver.phoneForCopy.value).toBe('+8613912345678')
    expect(receiver.queuedKeyCount.value).toBe(0)
  })

  it('clears the old phone and code when a confirmed rotation returns a pending number', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 2, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-rotation-old',
      status: 'WAITING',
      number: '8613812345678',
      queued_count: 1
    })
    vi.mocked(smsReceiverAPI.change).mockResolvedValue({
      session_id: 'server-session-rotation-new',
      status: 'WAITING',
      number_pending: true,
      poll_seconds: 2,
      queued_count: 0
    })

    await receiver.start()
    receiver.code.value = '654321'
    await expect(receiver.changeNumber()).resolves.toBe('waiting')

    expect(receiver.hasActiveSession.value).toBe(true)
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBe('server-session-rotation-new')
    expect(receiver.phoneForCopy.value).toBe('')
    expect(receiver.phoneDisplay.value).toBe('--')
    expect(receiver.code.value).toBe('--')
  })

  it('keeps the active member session price snapshot when queue status has a newer price', async () => {
    receiver.stop()
    receiver = usePixlabSMSReceiver('member')
    vi.mocked(smsReceiverAPI.memberSMSReceiverAPI.getStatus)
      .mockResolvedValueOnce({ queued_count: 2, active_count: 0, available: true, fee_amount: 3 })
      .mockResolvedValueOnce({ queued_count: 2, active_count: 1, available: true, fee_amount: 4 })
    vi.mocked(smsReceiverAPI.memberSMSReceiverAPI.redeem).mockResolvedValue({
      session_id: 'member-price-snapshot',
      status: 'WAITING',
      number: '27749433060',
      country: '南非',
      queued_count: 1,
      fee_amount: 2,
      charge_state: 'held'
    })

    await expect(receiver.start()).resolves.toBe('waiting')
    expect(receiver.feeAmount.value).toBe(2)

    await receiver.refreshQueueStatus()
    expect(receiver.feeAmount.value).toBe(2)
  })

  it('clears a locally stored session when the server retires a limit-reached card', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 2, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-limit',
      status: 'WAITING',
      number: '8613812345678',
      queued_count: 1
    })
    vi.mocked(smsReceiverAPI.check).mockResolvedValue({
      status: 'EXHAUSTED',
      queued_count: 1
    })

    await receiver.start()
    await expect(receiver.refresh()).resolves.toBe('expired')

    expect(receiver.phase.value).toBe('expired')
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBeNull()
    expect(receiver.queuedKeyCount.value).toBe(1)
  })
})
