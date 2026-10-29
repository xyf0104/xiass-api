import { computed, ref } from 'vue'
import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import type { SMSReceiverSession } from '@/api/admin/smsReceiver'

type SMSPhase = 'idle' | 'starting' | 'waiting' | 'received' | 'expired' | 'unavailable' | 'error'
export type SMSReceiveOutcome = 'waiting' | 'received' | 'expired' | 'unavailable'
export type SMSReceiverScope = 'admin' | 'member'

const ACTIVE_SESSION_STORAGE_KEY = 'xiass-api:pixlab-sms:active-session-id'
const MEMBER_ACTIVE_SESSION_STORAGE_KEY = 'xiass-api:pixlab-sms:member-active-session-id'
const LEGACY_QUEUE_STORAGE_KEY = 'xiass-api:pixlab-sms:card-key-queue'
const LEGACY_ACTIVE_KEY_STORAGE_KEY = 'xiass-api:pixlab-sms:active-card-key'
const POLL_INTERVAL_MS = 5_000

const callingCodes = [
  ['1', '美国/加拿大'], ['7', '俄罗斯/哈萨克斯坦'], ['20', '埃及'], ['27', '南非'],
  ['30', '希腊'], ['31', '荷兰'], ['32', '比利时'], ['33', '法国'], ['34', '西班牙'],
  ['39', '意大利'], ['44', '英国'], ['49', '德国'], ['52', '墨西哥'], ['55', '巴西'], ['57', '哥伦比亚'],
  ['60', '马来西亚'], ['61', '澳大利亚'], ['62', '印度尼西亚'], ['63', '菲律宾'],
  ['65', '新加坡'], ['66', '泰国'], ['81', '日本'], ['82', '韩国'], ['84', '越南'],
  ['86', '中国'], ['90', '土耳其'], ['91', '印度'], ['92', '巴基斯坦'], ['93', '阿富汗'],
  ['94', '斯里兰卡'], ['95', '缅甸'], ['98', '伊朗'], ['212', '摩洛哥'], ['351', '葡萄牙'],
  ['352', '卢森堡'], ['353', '爱尔兰'], ['354', '冰岛'], ['358', '芬兰'], ['380', '乌克兰'],
  ['420', '捷克'], ['852', '香港'], ['853', '澳门'], ['855', '柬埔寨'], ['856', '老挝'],
  ['880', '孟加拉国'], ['886', '台湾'], ['960', '马尔代夫'], ['961', '黎巴嫩'], ['971', '阿联酋']
] as const

const countryFlags: Record<string, string> = {
  '美国': '🇺🇸', 'united states': '🇺🇸', 'us': '🇺🇸',
  '加拿大': '🇨🇦', 'canada': '🇨🇦',
  '英国': '🇬🇧', 'united kingdom': '🇬🇧', 'uk': '🇬🇧',
  '日本': '🇯🇵', 'japan': '🇯🇵',
  '韩国': '🇰🇷', 'south korea': '🇰🇷', 'korea': '🇰🇷',
  '中国': '🇨🇳', 'china': '🇨🇳',
  '香港': '🇭🇰', 'hong kong': '🇭🇰',
  '澳门': '🇲🇴', 'macau': '🇲🇴',
  '台湾': '🇹🇼', 'taiwan': '🇹🇼',
  '新加坡': '🇸🇬', 'singapore': '🇸🇬',
  '澳大利亚': '🇦🇺', 'australia': '🇦🇺',
  '马来西亚': '🇲🇾', 'malaysia': '🇲🇾',
  '泰国': '🇹🇭', 'thailand': '🇹🇭',
  '越南': '🇻🇳', 'vietnam': '🇻🇳',
  '菲律宾': '🇵🇭', 'philippines': '🇵🇭',
  '印度': '🇮🇳', 'india': '🇮🇳',
  '印度尼西亚': '🇮🇩', 'indonesia': '🇮🇩',
  '德国': '🇩🇪', 'germany': '🇩🇪',
  '法国': '🇫🇷', 'france': '🇫🇷',
  '西班牙': '🇪🇸', 'spain': '🇪🇸',
  '意大利': '🇮🇹', 'italy': '🇮🇹',
  '巴西': '🇧🇷', 'brazil': '🇧🇷',
  '墨西哥': '🇲🇽', 'mexico': '🇲🇽',
  '哥伦比亚': '🇨🇴', 'colombia': '🇨🇴', 'co': '🇨🇴', 'col': '🇨🇴',
  '俄罗斯': '🇷🇺', 'russia': '🇷🇺',
  '土耳其': '🇹🇷', 'turkey': '🇹🇷',
  '阿联酋': '🇦🇪', 'united arab emirates': '🇦🇪',
}

// Pixlab may return a country code, an English name, or a Chinese name. Keep
// the displayed region readable in Chinese instead of showing a raw `US`/`GB`.
const countryRegionAliases: Record<string, { name: string; flag: string }> = {
  'us': { name: '美国', flag: '🇺🇸' }, 'usa': { name: '美国', flag: '🇺🇸' }, 'united states': { name: '美国', flag: '🇺🇸' }, 'united states of america': { name: '美国', flag: '🇺🇸' },
  'ca': { name: '加拿大', flag: '🇨🇦' }, 'canada': { name: '加拿大', flag: '🇨🇦' },
  'gb': { name: '英国', flag: '🇬🇧' }, 'uk': { name: '英国', flag: '🇬🇧' }, 'united kingdom': { name: '英国', flag: '🇬🇧' },
  'jp': { name: '日本', flag: '🇯🇵' }, 'japan': { name: '日本', flag: '🇯🇵' },
  'kr': { name: '韩国', flag: '🇰🇷' }, 'south korea': { name: '韩国', flag: '🇰🇷' }, 'korea': { name: '韩国', flag: '🇰🇷' },
  'cn': { name: '中国', flag: '🇨🇳' }, 'china': { name: '中国', flag: '🇨🇳' },
  'hk': { name: '中国香港', flag: '🇭🇰' }, 'hong kong': { name: '中国香港', flag: '🇭🇰' },
  'mo': { name: '中国澳门', flag: '🇲🇴' }, 'macau': { name: '中国澳门', flag: '🇲🇴' },
  'tw': { name: '中国台湾', flag: '🇹🇼' }, 'taiwan': { name: '中国台湾', flag: '🇹🇼' },
  'sg': { name: '新加坡', flag: '🇸🇬' }, 'singapore': { name: '新加坡', flag: '🇸🇬' },
  'au': { name: '澳大利亚', flag: '🇦🇺' }, 'australia': { name: '澳大利亚', flag: '🇦🇺' },
  'my': { name: '马来西亚', flag: '🇲🇾' }, 'malaysia': { name: '马来西亚', flag: '🇲🇾' },
  'th': { name: '泰国', flag: '🇹🇭' }, 'thailand': { name: '泰国', flag: '🇹🇭' },
  'vn': { name: '越南', flag: '🇻🇳' }, 'vietnam': { name: '越南', flag: '🇻🇳' },
  'ph': { name: '菲律宾', flag: '🇵🇭' }, 'philippines': { name: '菲律宾', flag: '🇵🇭' },
  'id': { name: '印度尼西亚', flag: '🇮🇩' }, 'indonesia': { name: '印度尼西亚', flag: '🇮🇩' },
  'in': { name: '印度', flag: '🇮🇳' }, 'india': { name: '印度', flag: '🇮🇳' },
  'de': { name: '德国', flag: '🇩🇪' }, 'germany': { name: '德国', flag: '🇩🇪' },
  'fr': { name: '法国', flag: '🇫🇷' }, 'france': { name: '法国', flag: '🇫🇷' },
  'es': { name: '西班牙', flag: '🇪🇸' }, 'spain': { name: '西班牙', flag: '🇪🇸' },
  'it': { name: '意大利', flag: '🇮🇹' }, 'italy': { name: '意大利', flag: '🇮🇹' },
  'br': { name: '巴西', flag: '🇧🇷' }, 'brazil': { name: '巴西', flag: '🇧🇷' },
  'mx': { name: '墨西哥', flag: '🇲🇽' }, 'mexico': { name: '墨西哥', flag: '🇲🇽' },
  'co': { name: '哥伦比亚', flag: '🇨🇴' }, 'col': { name: '哥伦比亚', flag: '🇨🇴' }, 'colombia': { name: '哥伦比亚', flag: '🇨🇴' }, '哥伦': { name: '哥伦比亚', flag: '🇨🇴' }, '哥伦比亚': { name: '哥伦比亚', flag: '🇨🇴' },
  'ru': { name: '俄罗斯', flag: '🇷🇺' }, 'russia': { name: '俄罗斯', flag: '🇷🇺' },
  'tr': { name: '土耳其', flag: '🇹🇷' }, 'turkey': { name: '土耳其', flag: '🇹🇷' },
  'ae': { name: '阿拉伯联合酋长国', flag: '🇦🇪' }, 'united arab emirates': { name: '阿拉伯联合酋长国', flag: '🇦🇪' },
}

const callingCodeFlags: Record<string, string> = {
  '1': '🇺🇸', '7': '🇷🇺', '20': '🇪🇬', '27': '🇿🇦', '30': '🇬🇷',
  '31': '🇳🇱', '32': '🇧🇪', '33': '🇫🇷', '34': '🇪🇸', '39': '🇮🇹',
  '44': '🇬🇧', '49': '🇩🇪', '52': '🇲🇽', '55': '🇧🇷', '57': '🇨🇴', '60': '🇲🇾',
  '61': '🇦🇺', '62': '🇮🇩', '63': '🇵🇭', '65': '🇸🇬', '66': '🇹🇭',
  '81': '🇯🇵', '82': '🇰🇷', '84': '🇻🇳', '86': '🇨🇳', '90': '🇹🇷',
  '91': '🇮🇳', '92': '🇵🇰', '93': '🇦🇫', '94': '🇱🇰', '95': '🇲🇲',
  '98': '🇮🇷', '212': '🇲🇦', '351': '🇵🇹', '352': '🇱🇺', '353': '🇮🇪',
  '354': '🇮🇸', '358': '🇫🇮', '380': '🇺🇦', '420': '🇨🇿', '852': '🇭🇰',
  '853': '🇲🇴', '855': '🇰🇭', '856': '🇱🇦', '880': '🇧🇩', '886': '🇹🇼',
  '960': '🇲🇻', '961': '🇱🇧', '971': '🇦🇪',
}

function safeStorage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

// The browser stores only an opaque server session ID, never a card key.
function readActiveSession(scope: SMSReceiverScope): string {
	const key = scope === 'member' ? MEMBER_ACTIVE_SESSION_STORAGE_KEY : ACTIVE_SESSION_STORAGE_KEY
	return safeStorage()?.getItem(key)?.trim() ?? ''
}

function writeActiveSession(scope: SMSReceiverScope, value: string): void {
	const storage = safeStorage()
	if (!storage) return
	const key = scope === 'member' ? MEMBER_ACTIVE_SESSION_STORAGE_KEY : ACTIVE_SESSION_STORAGE_KEY
	if (value) storage.setItem(key, value)
	else storage.removeItem(key)
}

function readLegacyCardKeys(): string[] {
  const storage = safeStorage()
  if (!storage) return []
  const values: string[] = []
  const rawQueue = storage.getItem(LEGACY_QUEUE_STORAGE_KEY)
  if (rawQueue) {
    try {
      const parsed: unknown = JSON.parse(rawQueue)
      if (Array.isArray(parsed)) {
        values.push(...parsed.filter((item): item is string => typeof item === 'string' && item.trim().length > 0))
      }
    } catch {
      // A malformed legacy value is erased only after a successful migration attempt.
    }
  }
  const activeKey = storage.getItem(LEGACY_ACTIVE_KEY_STORAGE_KEY)?.trim()
  if (activeKey) values.push(activeKey)
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))]
}

function clearLegacyCardKeys(): void {
  const storage = safeStorage()
  storage?.removeItem(LEGACY_QUEUE_STORAGE_KEY)
  storage?.removeItem(LEGACY_ACTIVE_KEY_STORAGE_KEY)
}

function formatPhone(number: string, reportedRegion: string): {
  display: string
  copyValue: string
  region: string
  flag: string
  callingCode: string
  localNumber: string
} {
  const digits = number.replace(/\D/g, '')
  if (!digits) {
    return {
      display: '--', copyValue: '', region: reportedRegion || '--', flag: '', callingCode: '', localNumber: '--'
    }
  }

  // Some providers prefix their country label with an emoji such as `🌎`.
  // It is presentation noise, not part of the country identity.
  const normalizedRegion = reportedRegion.trim().toLowerCase().replace(/^[^a-z\u4e00-\u9fff]+/, '').trim()
  const reportedCountry = countryRegionAliases[normalizedRegion]

  const match = [...callingCodes]
    .sort(([left], [right]) => right.length - left.length)
    .find(([callingCode]) => digits.startsWith(callingCode) && digits.length > callingCode.length)

  if (match) {
    const [callingCode, fallbackRegion] = match
    const localNumber = digits.slice(callingCode.length)
    return {
      display: `+${callingCode} ${localNumber}`,
      copyValue: `+${callingCode}${localNumber}`,
      region: reportedCountry?.name || fallbackRegion,
      flag: reportedCountry?.flag || countryFlags[normalizedRegion] || callingCodeFlags[callingCode] || '',
      callingCode,
      localNumber
    }
  }

  return {
    display: `+${digits}`,
    copyValue: `+${digits}`,
    region: reportedCountry?.name || reportedRegion || '自动识别',
    flag: reportedCountry?.flag || countryFlags[normalizedRegion] || '',
    callingCode: '',
    localNumber: digits
  }
}

function errorReason(error: unknown): string {
  if (typeof error === 'object' && error !== null && 'reason' in error) {
    return String((error as { reason?: unknown }).reason || '')
  }
  return ''
}

export function usePixlabSMSReceiver(scope: SMSReceiverScope = 'admin') {
	const isMember = scope === 'member'
	const api = isMember ? smsReceiverAPI.memberSMSReceiverAPI : smsReceiverAPI
  const phase = ref<SMSPhase>('idle')
  const phoneDisplay = ref('--')
  const phoneForCopy = ref('')
  const countryCallingCode = ref('')
  const localPhoneNumber = ref('--')
  const region = ref('--')
  const countryFlag = ref('')
  const code = ref('--')
  const queuedKeyCount = ref(0)
	const activeSessionCount = ref(0)
	const activeSessionID = ref(readActiveSession(scope))
	const available = ref(false)
	const feeAmount = ref(0)
	const balance = ref<number | null>(null)
	const chargeState = ref<'held' | 'captured' | 'released' | ''>('')
  const actionAvailableAt = ref('')
  const sessionExpiresAtMs = ref<number | null>(null)
  const serverClockOffsetMs = ref(0)
  const currentTime = ref(Date.now())
  const isRefreshing = ref(false)
  const isChangingNumber = ref(false)
  const isCancelling = ref(false)
  let pollingTimer: number | undefined
  let pollingIntervalMs = POLL_INTERVAL_MS
  let clockTimer: number | undefined
  let sessionRefreshRequest: { sessionID: string; promise: Promise<SMSReceiveOutcome> } | undefined
  let expirySyncInFlight = false
  let nextExpirySyncAt = 0

  const statusText = computed(() => {
    const labels: Record<SMSPhase, string> = {
      idle: '等待取号',
      starting: '正在取号',
      waiting: '实时监听',
      received: '接收成功',
      expired: '已超时',
      unavailable: '暂无卡密',
      error: '连接失败'
    }
    return labels[phase.value]
  })

  const statusClass = computed(() => {
    if (phase.value === 'received') return 'text-emerald-600 dark:text-emerald-300'
    if (phase.value === 'expired' || phase.value === 'error') return 'text-red-600 dark:text-red-300'
    if (phase.value === 'unavailable') return 'text-amber-600 dark:text-amber-300'
    return 'text-blue-600 dark:text-blue-300'
  })

  const hasActiveSession = computed(() => Boolean(activeSessionID.value))
  const canRefresh = computed(() => !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && phase.value !== 'starting')
  const sessionExpiresAt = computed(() => sessionExpiresAtMs.value === null ? '' : new Date(sessionExpiresAtMs.value).toISOString())
  const sessionExpiryRemainingSeconds = computed(() => {
    if (!hasActiveSession.value || sessionExpiresAtMs.value === null) return 0
    return Math.max(0, Math.ceil((sessionExpiresAtMs.value - currentTime.value) / 1_000))
  })
  const sessionExpiryText = computed(() => {
    const seconds = sessionExpiryRemainingSeconds.value
    const minutes = Math.floor(seconds / 60)
    return `${minutes.toString().padStart(2, '0')}:${(seconds % 60).toString().padStart(2, '0')}`
  })
  const memberMutationRemainingSeconds = computed(() => {
    if (!isMember || !actionAvailableAt.value) return 0
    const availableAt = Date.parse(actionAvailableAt.value)
    if (!Number.isFinite(availableAt)) return 0
    return Math.max(0, Math.ceil((availableAt - currentTime.value) / 1_000))
  })
  const canMutateMemberSession = computed(() => !isMember || memberMutationRemainingSeconds.value === 0)
  const canChangeNumber = computed(() => hasActiveSession.value && canMutateMemberSession.value && !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && !['starting', 'received'].includes(phase.value))
  const canCancel = computed(() => hasActiveSession.value && canMutateMemberSession.value && !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && !['starting', 'received'].includes(phase.value))

  // Move queues produced by the short-lived browser-local implementation to
  // the server on first use. This is deliberately best-effort: a failed upload
  // leaves the legacy data intact so an operator can retry rather than lose it.
  async function migrateLegacyCardKeys(): Promise<number> {
    if (isMember) return 0
    const legacyKeys = readLegacyCardKeys()
    if (legacyKeys.length === 0) return 0
    const result = await smsReceiverAPI.addCardKeys(legacyKeys.join('\n'))
    queuedKeyCount.value = result.queued_count
    clearLegacyCardKeys()
    return result.added_count
  }

  async function refreshQueueStatus(): Promise<smsReceiverAPI.SMSReceiverQueueStatus> {
		const status = await api.getStatus()
		queuedKeyCount.value = status.queued_count
		activeSessionCount.value = status.active_count
		available.value = status.available === true
			// The queue status carries the administrator's current price, while an
			// active member session carries its immutable price snapshot. Do not let
			// a status refresh relabel an in-progress session after the price changes.
			if (typeof status.fee_amount === 'number' && (!isMember || !hasActiveSession.value)) {
				feeAmount.value = status.fee_amount
			}
		if (typeof status.balance === 'number') balance.value = status.balance
		return status
  }

  async function appendCardKeys(raw: string): Promise<number> {
    const result = await smsReceiverAPI.addCardKeys(raw)
    queuedKeyCount.value = result.queued_count
    return result.added_count
  }

  async function clearQueuedCardKeys(): Promise<number> {
    const result = await smsReceiverAPI.clearCardKeys()
    queuedKeyCount.value = result.queued_count
    return result.deleted_count
  }

  function stopPolling(): void {
    if (pollingTimer) clearInterval(pollingTimer)
    pollingTimer = undefined
  }

  function stop(): void {
    stopPolling()
    if (clockTimer) clearInterval(clockTimer)
    clockTimer = undefined
  }

  function startClockTicker(): void {
    const hasMemberCooldown = isMember && Boolean(actionAvailableAt.value) && memberMutationRemainingSeconds.value > 0
    const hasSessionExpiry = hasActiveSession.value && sessionExpiresAtMs.value !== null
    if ((!hasMemberCooldown && !hasSessionExpiry) || clockTimer) return
    clockTimer = window.setInterval(() => {
      currentTime.value = Date.now() + serverClockOffsetMs.value

      const sessionID = activeSessionID.value
      if (
        sessionID
        && sessionExpiresAtMs.value !== null
        && sessionExpiryRemainingSeconds.value === 0
        && !expirySyncInFlight
        && Date.now() >= nextExpirySyncAt
      ) {
        expirySyncInFlight = true
        nextExpirySyncAt = Date.now() + POLL_INTERVAL_MS
        void refreshExpiredSession(sessionID).finally(() => {
          expirySyncInFlight = false
        })
      }

      const keepMemberClock = isMember && memberMutationRemainingSeconds.value > 0
      const keepExpiryClock = Boolean(activeSessionID.value) && sessionExpiresAtMs.value !== null
      if (!keepMemberClock && !keepExpiryClock && clockTimer) {
        clearInterval(clockTimer)
        clockTimer = undefined
      }
    }, 1_000)
  }

  function parseServerTimestamp(value: string | null | undefined): number | null {
    if (typeof value !== 'string') return null
    const normalized = value.trim()
    if (!normalized) return null
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(normalized)) return null
    const parsed = Date.parse(normalized)
    return Number.isFinite(parsed) ? parsed : null
  }

  function applySessionExpiry(result: SMSReceiverSession, previousSessionID: string): void {
    const serverTime = parseServerTimestamp(result.server_time)
    if (serverTime !== null) serverClockOffsetMs.value = serverTime - Date.now()

    if (Object.prototype.hasOwnProperty.call(result, 'expires_at')) {
      sessionExpiresAtMs.value = parseServerTimestamp(result.expires_at)
      if (sessionExpiresAtMs.value !== null && sessionExpiresAtMs.value > Date.now() + serverClockOffsetMs.value) {
        nextExpirySyncAt = 0
      }
    } else if (result.session_id && result.session_id !== previousSessionID) {
      sessionExpiresAtMs.value = null
    }
    currentTime.value = Date.now() + serverClockOffsetMs.value
  }

  function clearActiveSession(): void {
    stop()
    activeSessionID.value = ''
		sessionExpiresAtMs.value = null
		actionAvailableAt.value = ''
		nextExpirySyncAt = 0
		writeActiveSession(scope, '')
  }

  function resetDisplay(nextPhase: SMSPhase): void {
    stop()
    phase.value = nextPhase
    phoneDisplay.value = nextPhase === 'unavailable' ? '暂无待用卡密' : '--'
    phoneForCopy.value = ''
    countryCallingCode.value = ''
    localPhoneNumber.value = '--'
    region.value = '--'
    countryFlag.value = ''
    code.value = '--'
    sessionExpiresAtMs.value = null
  }

  function clearPhoneDisplay(): void {
    phoneDisplay.value = '--'
    phoneForCopy.value = ''
    countryCallingCode.value = ''
    localPhoneNumber.value = '--'
    region.value = '--'
    countryFlag.value = ''
    code.value = '--'
  }

  function validPollIntervalMs(value: unknown): number | null {
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 1) return null
    return Math.max(1_000, Math.trunc(value * 1_000))
  }

	function applyPhone(result: SMSReceiverSession): void {
		if (result.number_pending === true) {
			clearPhoneDisplay()
			return
		}
    const rawNumber = result.number?.trim() ?? ''
    if (!rawNumber) return
    const formatted = formatPhone(rawNumber, result.country?.trim() ?? '')
    phoneDisplay.value = formatted.display
    phoneForCopy.value = formatted.copyValue
    countryCallingCode.value = formatted.callingCode
    localPhoneNumber.value = formatted.localNumber
    region.value = formatted.region
		countryFlag.value = formatted.flag
	}

	function applyBilling(result: SMSReceiverSession): void {
		if (typeof result.fee_amount === 'number') feeAmount.value = result.fee_amount
		if (typeof result.balance === 'number') balance.value = result.balance
		if (result.charge_state === 'held' || result.charge_state === 'captured' || result.charge_state === 'released') {
			chargeState.value = result.charge_state
		}
		actionAvailableAt.value = result.action_available_at || ''
	}

	function applyResponse(result: SMSReceiverSession): SMSReceiveOutcome {
		const previousSessionID = activeSessionID.value
		const isNewSession = Boolean(result.session_id && result.session_id !== previousSessionID)
		if (isNewSession) pollingIntervalMs = POLL_INTERVAL_MS
		const returnedPollIntervalMs = validPollIntervalMs(result.poll_seconds)
		if (returnedPollIntervalMs !== null) pollingIntervalMs = returnedPollIntervalMs
		queuedKeyCount.value = result.queued_count
		applyBilling(result)
    applyPhone(result)
    const status = (result.status || '').trim().toUpperCase()
    const incomingCode = result.code?.trim() ?? ''

    if (incomingCode && incomingCode !== '--') {
      code.value = incomingCode
      clearActiveSession()
      phase.value = 'received'
      return 'received'
    }

    if (['EXPIRED', 'TIMEOUT', 'CANCELLED', 'CANCELED', 'FAILED', 'ERROR', 'EXHAUSTED', 'RECEIVED', 'COMPLETED', 'USED'].includes(status)) {
      clearActiveSession()
      phase.value = 'expired'
      code.value = '--'
      return 'expired'
    }

		if (isNewSession) {
			// A confirmed claim/change starts a fresh SMS session. Clear the old
			// code before polling it so a Team OAuth workflow cannot submit a stale
			// verification code after a number replacement.
			code.value = '--'
		}
		if (result.session_id) {
			activeSessionID.value = result.session_id
			writeActiveSession(scope, result.session_id)
    }
    applySessionExpiry(result, previousSessionID)
    phase.value = 'waiting'
    startClockTicker()
		if (result.local_stop === true) stopPolling()
		else startPolling()
    return 'waiting'
  }

  async function refreshExpiredSession(sessionID: string): Promise<void> {
    stopPolling()
    if (activeSessionID.value === sessionID) {
      await refreshSession(sessionID, true).catch(() => undefined)
    }
    await refreshQueueStatus().catch(() => undefined)
  }

  function startPolling(): void {
    stopPolling()
    if (!activeSessionID.value) return
    pollingTimer = window.setInterval(() => {
      if (phase.value !== 'waiting') {
        stopPolling()
        return
      }
      void refresh(true).catch(() => undefined)
    }, pollingIntervalMs)
  }

  async function resumeActive(): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) {
      resetDisplay('unavailable')
      return 'unavailable'
    }
    activeSessionID.value = sessionID
    phase.value = 'starting'
    try {
		return await refreshSession(sessionID, true)
    } catch (error) {
      if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
      phase.value = 'error'
      throw error
    }
  }

  async function start(): Promise<SMSReceiveOutcome> {
    if (activeSessionID.value || readActiveSession(scope)) return resumeActive()

    await migrateLegacyCardKeys()
    await refreshQueueStatus()
		if ((!isMember && queuedKeyCount.value <= 0) || (isMember && !available.value)) {
      resetDisplay('unavailable')
      return 'unavailable'
    }

    phase.value = 'starting'
    phoneDisplay.value = '正在获取…'
    region.value = '--'
    code.value = '--'
    try {
		return applyResponse(await api.redeem())
    } catch (error) {
      phase.value = 'error'
      await refreshQueueStatus().catch(() => undefined)
      throw error
    }
  }

  async function refresh(silent = false): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) {
      if (!silent) isRefreshing.value = true
      try {
        await refreshQueueStatus()
        return 'unavailable'
      } finally {
        if (!silent) isRefreshing.value = false
      }
    }
    activeSessionID.value = sessionID
    return refreshSession(sessionID, silent)
  }

  function refreshSession(sessionID: string, silent: boolean): Promise<SMSReceiveOutcome> {
    if (sessionRefreshRequest?.sessionID === sessionID) {
      if (silent) return sessionRefreshRequest.promise
      isRefreshing.value = true
      return sessionRefreshRequest.promise.finally(() => { isRefreshing.value = false })
    }
    if (!silent) isRefreshing.value = true
    const promise = (async () => {
      try {
        const result = phase.value === 'error' || phase.value === 'starting'
					? await api.resume(sessionID)
					: await api.check(sessionID)
        return applyResponse(result)
      } catch (error) {
        if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
        phase.value = 'error'
        throw error
      } finally {
        if (!silent) isRefreshing.value = false
        if (sessionRefreshRequest?.sessionID === sessionID) sessionRefreshRequest = undefined
      }
    })()
    sessionRefreshRequest = { sessionID, promise }
    return promise
  }

  async function changeNumber(): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) throw new Error('当前没有可更换的手机号。')
    isChangingNumber.value = true
    stop()
    try {
		return applyResponse(await api.change(sessionID))
    } catch (error) {
      if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
      phase.value = 'error'
      throw error
    } finally {
      isChangingNumber.value = false
      if (phase.value === 'waiting') startPolling()
    }
  }

  async function cancel(): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) {
      resetDisplay('idle')
      return 'unavailable'
    }
    isCancelling.value = true
    stop()
    try {
		const result = await api.cancel(sessionID)
		const outcome = applyResponse(result)
      if (outcome !== 'received') {
        clearActiveSession()
        resetDisplay('idle')
      }
      return outcome
    } finally {
      isCancelling.value = false
      if (phase.value === 'waiting') startPolling()
    }
  }

  return {
    phase,
    phoneDisplay,
    phoneForCopy,
    countryCallingCode,
    localPhoneNumber,
    region,
		countryFlag,
    code,
		queuedKeyCount,
		activeSessionCount,
		available,
		feeAmount,
		balance,
		chargeState,
		actionAvailableAt,
		sessionExpiresAt,
		sessionExpiryRemainingSeconds,
		sessionExpiryText,
		memberMutationRemainingSeconds,
    hasActiveSession,
    statusText,
    statusClass,
    canRefresh,
    canChangeNumber,
    canCancel,
    isRefreshing,
    isChangingNumber,
    isCancelling,
    refreshQueueStatus,
    appendCardKeys,
    clearQueuedCardKeys,
    migrateLegacyCardKeys,
    start,
    refresh,
    changeNumber,
    cancel,
    stop
  }
}
