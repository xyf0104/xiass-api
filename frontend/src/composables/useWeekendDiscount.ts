import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getWeekendDiscount, type WeekendDiscountStatus } from '@/api/weekendDiscount'

export function weekendMultiplierAt(status: WeekendDiscountStatus | null, now: number): number {
  if (!status?.enabled || status.starts_at && now < Date.parse(status.starts_at) || status.ends_at && now >= Date.parse(status.ends_at)) return 1
  if (status.mode === 'custom') return status.discount_multiplier
  const weekday = new Date(now + 8 * 3_600_000).getUTCDay()
  return weekday === 0 || weekday === 6 ? status.discount_multiplier : 1
}

export function weekendNextChangeAt(status: WeekendDiscountStatus | null, now: number): number | null {
  if (!status?.enabled) return null
  const start = status.starts_at ? Date.parse(status.starts_at) : null
  const end = status.ends_at ? Date.parse(status.ends_at) : null
  if (end !== null && now >= end) return null
  if (status.mode === 'custom') return start !== null && now < start ? start : end

  // Recompute boundaries locally so an open page switches at midnight without waiting for a poll.
  const candidate = Math.max(now, start ?? now)
  const beijing = new Date(candidate + 8 * 3_600_000)
  const day = beijing.getUTCDay()
  const midnight = Date.UTC(beijing.getUTCFullYear(), beijing.getUTCMonth(), beijing.getUTCDate()) - 8 * 3_600_000
  if (weekendMultiplierAt(status, now) < 1) {
    const monday = midnight + ((8 - day) % 7) * 86_400_000
    return end === null ? monday : Math.min(monday, end)
  }
  const nextStart = day === 0 || day === 6 ? candidate : midnight + (6 - day) * 86_400_000
  return end !== null && nextStart >= end ? null : nextStart
}

export function formatPromotionCountdown(milliseconds: number): string {
  const seconds = Math.max(0, Math.ceil(milliseconds / 1000))
  const days = Math.floor(seconds / 86_400)
  const time = [Math.floor(seconds / 3600) % 24, Math.floor(seconds / 60) % 60, seconds % 60]
    .map(value => String(value).padStart(2, '0')).join(':')
  return days > 0 ? `${days}天 ${time}` : time
}

const status = ref<WeekendDiscountStatus | null>(null)
const clock = ref(Date.now())
let offset = 0
let consumers = 0
let generation = 0
let timer: ReturnType<typeof setInterval> | undefined
let lastFetch = 0
let pendingGeneration: number | null = null
let revision = 0

export function updateWeekendDiscountStatus(data: WeekendDiscountStatus) {
  revision++
  status.value = data
  offset = Date.parse(data.server_time) - Date.now()
  clock.value = Date.now() + offset
  lastFetch = Date.now()
}

async function refresh() {
  if (pendingGeneration === generation || document.hidden) return
  lastFetch = Date.now()
  const current = generation
  pendingGeneration = current
  const currentRevision = revision
  try {
    const data = await getWeekendDiscount()
    if (current !== generation || currentRevision !== revision || consumers === 0) return
    updateWeekendDiscountStatus(data)
  } catch {
    // Do not advertise stale promotional prices after a failed refresh.
    if (current === generation && currentRevision === revision) status.value = null
  } finally { if (pendingGeneration === current) pendingGeneration = null }
}

function visibilityChanged() { if (!document.hidden) void refresh() }

export function useWeekendDiscount() {
  onMounted(() => {
    if (++consumers !== 1) return
    generation++
    void refresh()
    document.addEventListener('visibilitychange', visibilityChanged)
    timer = setInterval(() => {
      clock.value = Date.now() + offset
      if (Date.now() - lastFetch >= 30_000) void refresh()
    }, 1000)
  })
  onUnmounted(() => {
    if (--consumers !== 0) return
    generation++
    clearInterval(timer)
    document.removeEventListener('visibilitychange', visibilityChanged)
    status.value = null
  })
  const multiplier = computed(() => weekendMultiplierAt(status.value, clock.value))
  const scheduled = computed(() => Boolean(status.value?.enabled && (!status.value.ends_at || clock.value < Date.parse(status.value.ends_at))))
  const nextChangeAt = computed(() => weekendNextChangeAt(status.value, clock.value))
  const countdown = computed(() => nextChangeAt.value === null ? null : formatPromotionCountdown(nextChangeAt.value - clock.value))
  return { status, multiplier, scheduled, nextChangeAt, countdown, active: computed(() => multiplier.value < 1) }
}

export function formatPromotionRate(value: number): string {
  return Number(value.toFixed(8)).toString()
}
