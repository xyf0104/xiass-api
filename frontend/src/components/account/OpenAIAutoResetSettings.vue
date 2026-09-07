<template>
  <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="auto-reset-settings">
    <label class="flex min-h-11 items-center gap-3">
      <input v-model="enabled" type="checkbox" role="switch" class="h-5 w-5 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="!ready || busy" :aria-checked="enabled" data-testid="auto-reset-enabled" />
      <span class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.accounts.autoResetCredit.title') }}</span>
    </label>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.autoResetCredit.consent') }}</p>
    <div v-if="enabled" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <label class="min-w-0 text-sm text-gray-700 dark:text-gray-200">
        {{ t('admin.accounts.autoResetCredit.threshold5h') }}
        <input v-model.number="fiveHour" type="number" min="0.1" max="100" step="0.1" class="input mt-1 w-full" :disabled="busy" data-testid="auto-reset-5h" />
      </label>
      <label class="min-w-0 text-sm text-gray-700 dark:text-gray-200">
        {{ t('admin.accounts.autoResetCredit.threshold7d') }}
        <input v-model.number="sevenDay" type="number" min="0.1" max="100" step="0.1" class="input mt-1 w-full" :disabled="busy" data-testid="auto-reset-7d" />
      </label>
    </div>
    <p v-if="pending" role="status" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accounts.autoResetCredit.pending') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="saved" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ t('admin.accounts.autoResetCredit.saved') }}</p>
    <button type="button" class="btn btn-secondary min-h-11" :disabled="!ready || busy" @click="save" data-testid="auto-reset-save">{{ t('admin.accounts.autoResetCredit.save') }}</button>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getOpenAIAutoReset, setOpenAIAutoReset } from '@/api/admin/openaiAutoReset'
import type { OpenAIAutoResetConfig } from '@/types'

const props = defineProps<{ accountId: number }>()
const { t } = useI18n()
const enabled = ref(false)
const fiveHour = ref(100)
const sevenDay = ref(100)
const pending = ref(false)
const ready = ref(false)
const busy = ref(false)
const saved = ref(false)
const error = ref('')
let generation = 0
let savedThresholds = { threshold_5h: 1, threshold_7d: 1 }

function apply(config: OpenAIAutoResetConfig) {
  enabled.value = config.enabled === true
  fiveHour.value = config.threshold_5h * 100
  sevenDay.value = config.threshold_7d * 100
  pending.value = config.pending === true
  savedThresholds = { threshold_5h: config.threshold_5h, threshold_7d: config.threshold_7d }
}

watch(() => props.accountId, async id => {
  const request = ++generation
  enabled.value = false
  fiveHour.value = 100
  sevenDay.value = 100
  ready.value = false
  busy.value = false
  pending.value = false
  saved.value = false
  error.value = ''
  try {
    const config = await getOpenAIAutoReset(id)
    if (request !== generation) return
    apply(config)
    ready.value = true
  } catch {
    if (request === generation) error.value = t('admin.accounts.autoResetCredit.loadFailed')
  }
}, { immediate: true })

async function save() {
  if (!ready.value || busy.value) return
  error.value = ''
  saved.value = false
  if (enabled.value && [fiveHour.value, sevenDay.value].some(v => !Number.isFinite(v) || v < 0.1 || v > 100)) {
    error.value = t('admin.accounts.autoResetCredit.invalidThreshold')
    return
  }
  const request = generation
  busy.value = true
  try {
    const thresholds = enabled.value
      ? { threshold_5h: fiveHour.value / 100, threshold_7d: sevenDay.value / 100 }
      : savedThresholds
    const config = await setOpenAIAutoReset(props.accountId, { enabled: enabled.value, ...thresholds })
    if (request !== generation) return
    apply(config)
    saved.value = true
  } catch {
    if (request === generation) error.value = t('admin.accounts.autoResetCredit.saveFailed')
  } finally {
    if (request === generation) busy.value = false
  }
}

onBeforeUnmount(() => { generation++ })
</script>
