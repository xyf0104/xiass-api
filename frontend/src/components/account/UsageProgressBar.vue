<template>
  <div
    :class="[
      hasIntegratedAside
        ? 'grid w-full max-w-full grid-cols-1 items-center gap-y-1 sm:inline-grid sm:w-[18.5rem] sm:grid-cols-[auto_5rem] sm:gap-x-3'
        : ''
    ]"
    :data-test="hasIntegratedAside ? 'usage-window-integrated-layout' : undefined"
  >
    <!-- Window stats row (above progress bar) -->
    <div
      v-if="windowStats && (windowStats.requests > 0 || windowStats.tokens > 0)"
      :class="[
        hasIntegratedAside ? 'mb-0 sm:col-span-2' : wide ? 'mb-1' : 'mb-0.5',
        'flex w-full items-center'
      ]"
    >
      <div
        :class="[
          'grid w-full items-center text-[9px] text-gray-500 dark:text-gray-400',
          wide
            ? 'grid-cols-2 gap-1 sm:grid-cols-[4rem_2rem_6rem_5.75rem]'
            : 'grid-cols-2 gap-1 sm:flex sm:w-auto sm:gap-1.5'
        ]"
        data-test="window-stats-grid"
      >
        <span class="whitespace-nowrap rounded bg-gray-100 px-1.5 py-0.5 text-center dark:bg-gray-800">
          {{ formatExactRequests }} {{ t('usage.requestCountUnit') }}
        </span>
        <span class="whitespace-nowrap rounded bg-gray-100 px-1.5 py-0.5 text-center dark:bg-gray-800">
          {{ formatTokens }}
        </span>
        <span
          :class="[
            'whitespace-nowrap rounded bg-gray-100 px-1.5 py-0.5 text-center dark:bg-gray-800',
            highlightBilling
              ? 'font-medium text-red-600 dark:text-red-400'
              : 'text-gray-500 dark:text-gray-400'
          ]"
          :title="t('usage.accountBilled')"
          data-test="account-billing"
        >
          {{ t('usage.accountBilled') }} ${{ formatAccountCost }}
        </span>
        <button
          v-if="windowStats?.user_cost != null"
          type="button"
          :class="[
            'whitespace-nowrap rounded bg-gray-100 px-1.5 py-0.5 text-center transition-colors hover:bg-gray-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-500/50 dark:bg-gray-800 dark:hover:bg-gray-700',
            highlightBilling
              ? 'font-medium text-amber-600 dark:text-amber-400'
              : 'text-gray-500 dark:text-gray-400'
          ]"
          :title="t('usage.openBillingBreakdown')"
          data-test="user-billing"
          @click.stop="emit('open-billing-details')"
        >
          {{ t('usage.userBilled') }} ¥{{ formatUserCost }}
        </button>
      </div>
    </div>

    <!-- Progress bar row -->
    <div
      :class="[
        'flex items-center justify-center gap-1 sm:justify-start',
        hasIntegratedAside
          ? hasWindowStats
            ? 'sm:col-start-1 sm:row-start-2'
            : 'sm:col-start-1 sm:row-start-1'
          : ''
      ]"
      data-test="usage-progress-row"
    >
      <!-- Label badge (fixed width for alignment) -->
      <span
        :class="['w-[32px] shrink-0 rounded px-1 text-center text-[10px] font-medium', labelClass]"
      >
        {{ label }}
      </span>

      <!-- Progress bar container -->
      <div
        :class="[
          'h-1.5 shrink-0 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700',
          wide ? 'w-16' : 'w-8'
        ]"
      >
        <div
          :class="['h-full transition-all duration-300', barClass]"
          :style="{ width: barWidth }"
        ></div>
      </div>

      <!-- Percentage -->
      <span :class="['w-[32px] shrink-0 text-right text-[10px] font-medium', textClass]">
        {{ displayPercent }}
      </span>

      <!-- Reset time -->
      <span v-if="shouldShowResetTime" class="shrink-0 text-[10px] text-gray-400">
        {{ formatResetTime }}
      </span>
    </div>

    <div
      v-if="$slots.footer"
      :class="[
        'min-w-0 sm:col-start-1',
        hasWindowStats ? 'sm:row-start-3' : 'sm:row-start-2'
      ]"
      data-test="usage-window-footer"
    >
      <slot name="footer" />
    </div>

    <div
      v-if="$slots.aside"
      :class="[
        'flex min-h-0 min-w-0 items-center justify-center overflow-hidden border-t border-gray-200 pt-1.5 dark:border-dark-700 sm:col-start-2 sm:h-full sm:border-t-0 sm:pt-0',
        hasWindowStats ? 'sm:row-start-2 sm:row-span-2' : 'sm:row-start-1 sm:row-span-2'
      ]"
      data-test="usage-window-aside"
    >
      <slot name="aside" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, useSlots, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import type { WindowStats } from '@/types'
import { formatCompactNumber } from '@/utils/format'

const props = defineProps<{
  label: string
  utilization: number // Percentage (0-100+)
  resetsAt?: string | null
  color: 'indigo' | 'emerald' | 'purple' | 'amber'
  windowStats?: WindowStats | null
  showNowWhenIdle?: boolean
  remainingCapacity?: boolean
  highlightBilling?: boolean
  wide?: boolean
}>()

const emit = defineEmits<{
  'open-billing-details': []
}>()

const { t } = useI18n()
const slots = useSlots()

const hasIntegratedAside = computed(() => Boolean(slots.footer && slots.aside))
const hasWindowStats = computed(() => Boolean(
  props.windowStats && (props.windowStats.requests > 0 || props.windowStats.tokens > 0)
))

// Reactive clock for countdown — only runs when a reset time is shown,
// to avoid creating many idle timers across large account lists.
const now = ref(new Date())
const { pause: pauseClock, resume: resumeClock } = useIntervalFn(
  () => {
    now.value = new Date()
  },
  60_000,
  { immediate: false },
)
if (props.resetsAt) resumeClock()
watch(
  () => props.resetsAt,
  (val) => {
    if (val) {
      now.value = new Date()
      resumeClock()
    } else {
      pauseClock()
    }
  },
)

// Label background colors
const labelClass = computed(() => {
  const colors = {
    indigo: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300',
    emerald: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300',
    purple: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300',
    amber: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
  }
  return colors[props.color]
})

// Progress bar color based on utilization
const barClass = computed(() => {
  if (props.remainingCapacity) {
    if (props.utilization <= 20) {
      return 'bg-red-500'
    } else if (props.utilization <= 50) {
      return 'bg-amber-500'
    }
    return 'bg-green-500'
  }
  if (props.utilization >= 90) {
    return 'bg-red-500'
  } else if (props.utilization >= 75) {
    return 'bg-amber-500'
  } else {
    return 'bg-green-500'
  }
})

// Text color based on utilization
const textClass = computed(() => {
  if (props.remainingCapacity) {
    if (props.utilization <= 20) {
      return 'text-red-600 dark:text-red-400'
    } else if (props.utilization <= 50) {
      return 'text-amber-600 dark:text-amber-400'
    }
    return 'text-gray-600 dark:text-gray-400'
  }
  if (props.utilization >= 90) {
    return 'text-red-600 dark:text-red-400'
  } else if (props.utilization >= 75) {
    return 'text-amber-600 dark:text-amber-400'
  } else {
    return 'text-gray-600 dark:text-gray-400'
  }
})

// Bar width (capped at 100%)
const barWidth = computed(() => {
  return `${Math.min(Math.max(props.utilization, 0), 100)}%`
})

// Display percentage (cap at 999% for readability)
const displayPercent = computed(() => {
  const percent = Math.round(
    props.remainingCapacity
      ? Math.min(Math.max(props.utilization, 0), 100)
      : props.utilization
  )
  return percent > 999 ? '>999%' : `${percent}%`
})

const shouldShowResetTime = computed(() => {
  if (props.resetsAt) return true
  return Boolean(props.showNowWhenIdle && props.utilization <= 0)
})

// Format reset time
const formatResetTime = computed(() => {
  // For rolling windows, when utilization is 0%, treat as immediately available.
  if (props.showNowWhenIdle && props.utilization <= 0) {
    return t('usage.resetNow')
  }

  if (!props.resetsAt) return '-'

  const date = new Date(props.resetsAt)
  const diffMs = date.getTime() - now.value.getTime()

  // resetsAt 已过期：utilization>0 说明后端窗口数据还没刷新（active poll 没回写），
  // 显示「待刷新」以区别于真正可用的「现在」。
  if (diffMs <= 0) {
    return props.utilization > 0 ? t('usage.resetPending') : t('usage.resetNow')
  }

  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffMins = Math.floor((diffMs % (1000 * 60 * 60)) / (1000 * 60))

  if (diffHours >= 24) {
    const days = Math.floor(diffHours / 24)
    return `${days}d ${diffHours % 24}h`
  } else if (diffHours > 0) {
    return `${diffHours}h ${diffMins}m`
  } else {
    return `${diffMins}m`
  }
})

// Window stats formatters
const formatExactRequests = computed(() => {
  if (!props.windowStats) return '0'
  return String(Math.max(0, Math.trunc(props.windowStats.requests)))
})

const formatTokens = computed(() => {
  if (!props.windowStats) return ''
  return formatCompactNumber(props.windowStats.tokens)
})

const formatAccountCost = computed(() => {
  if (!props.windowStats) return '0.00'
  return props.windowStats.cost.toFixed(2)
})

const formatUserCost = computed(() => {
  if (!props.windowStats || props.windowStats.user_cost == null) return '0.00'
  return props.windowStats.user_cost.toFixed(2)
})

</script>
