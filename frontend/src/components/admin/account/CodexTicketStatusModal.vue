<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.codexTicket.title')"
    width="normal"
    @close="$emit('close')"
  >
    <div v-if="account" class="space-y-4">
      <div class="rounded-lg border border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-600 dark:bg-dark-700/50">
        <div class="font-medium text-gray-900 dark:text-white">{{ account.name }}</div>
        <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">#{{ account.id }} · {{ t('admin.accounts.codexTicket.redactedNotice') }}</div>
      </div>

      <div v-if="statuses.length" class="space-y-3">
        <div
          v-for="status in statuses"
          :key="status.model"
          class="rounded-lg border border-gray-200 px-4 py-3 dark:border-dark-600"
        >
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ status.model }}</span>
            <span
              :class="[
                'rounded-full px-2 py-0.5 text-xs font-medium',
                status.ready
                  ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
              ]"
            >
              {{ status.ready ? t('admin.accounts.codexTicket.ready') : t('admin.accounts.codexTicket.unavailable') }}
            </span>
          </div>
          <div class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.length') }}</span>
            <span class="text-right font-mono text-gray-800 dark:text-gray-200">{{ status.length || '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.expiresAt') }}</span>
            <span class="text-right text-gray-800 dark:text-gray-200">{{ status.expires_at ? formatDateTime(status.expires_at) : '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.remaining') }}</span>
            <span class="text-right text-gray-800 dark:text-gray-200">{{ status.ready ? formatRemaining(status.remaining_seconds) : '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.observedModel') }}</span>
            <span class="text-right font-mono text-gray-800 dark:text-gray-200">{{ status.observed_model || '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.egress') }}</span>
            <span class="text-right text-gray-800 dark:text-gray-200">{{ status.proxy_name || status.proxy_id || '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.latency') }}</span>
            <span class="text-right text-gray-800 dark:text-gray-200">{{ status.latency_ms ? `${status.latency_ms} ms` : '-' }}</span>
          </div>
          <p v-if="status.fallback" class="mt-3 text-xs text-amber-700 dark:text-amber-300">
            {{ t('admin.accounts.codexTicket.fallbackHint', { model: status.observed_model || '-' }) }}
          </p>
          <p v-if="status.blocked" class="mt-3 text-xs text-amber-700 dark:text-amber-300">
            {{ t('admin.accounts.codexTicket.blockedHint') }}
          </p>
          <div class="mt-3 flex justify-end">
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="refreshingModel === status.model"
              @click="refresh(status.model)"
            >
              {{ refreshingModel === status.model ? t('admin.accounts.codexTicket.refreshing') : t('admin.accounts.codexTicket.refresh') }}
            </button>
          </div>
          <details v-if="status.probes?.length" class="mt-3 text-xs text-gray-500 dark:text-gray-400">
            <summary class="cursor-pointer select-none">{{ t('admin.accounts.codexTicket.probeDetails', { count: status.probes.length }) }}</summary>
            <div class="mt-2 space-y-1 border-l-2 border-gray-200 pl-3 dark:border-dark-600">
              <div v-for="(probe, index) in status.probes" :key="`${status.model}-${probe.proxy_id || probe.proxy_name || index}`" class="flex items-center justify-between gap-3">
                <span>{{ probe.proxy_name || probe.proxy_id || t('admin.accounts.codexTicket.unknownEgress') }}</span>
                <span class="font-mono" :class="probe.valid ? 'text-emerald-600 dark:text-emerald-300' : 'text-gray-500 dark:text-gray-400'">
                  {{ probe.observed_model || '-' }} · {{ probe.latency_ms ? `${probe.latency_ms} ms` : '-' }}
                </span>
              </div>
            </div>
          </details>
        </div>
      </div>
      <div v-else class="rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
        {{ t('admin.accounts.codexTicket.noStatus') }}
      </div>

      <p v-if="errorMessage" class="text-xs leading-5 text-red-600 dark:text-red-300">
        {{ errorMessage }}
      </p>
      <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.codexTicket.storageHint') }}
      </p>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="$emit('close')">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { formatDateTime } from '@/utils/format'
import { accountsAPI } from '@/api/admin/accounts'
import type { Account, CodexTurnTicketStatus } from '@/types'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{ close: []; updated: [statuses: CodexTurnTicketStatus[]] }>()

const { t } = useI18n()
const statuses = ref<CodexTurnTicketStatus[]>([])
const refreshingModel = ref('')
const errorMessage = ref('')

watch(
  () => [props.show, props.account?.id, props.account?.codex_turn_tickets] as const,
  () => {
    statuses.value = props.account?.codex_turn_tickets ? [...props.account.codex_turn_tickets] : []
    errorMessage.value = ''
  },
  { immediate: true }
)

async function refresh(model: string) {
  if (!props.account || refreshingModel.value) return
  refreshingModel.value = model
  errorMessage.value = ''
  try {
    const next = await accountsAPI.refreshCodexTicket(props.account.id, model)
    statuses.value = next
    emit('updated', next)
  } catch (error: any) {
    errorMessage.value = error?.message || t('admin.accounts.codexTicket.refreshFailed')
  } finally {
    refreshingModel.value = ''
  }
}

function formatRemaining(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds || 0))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  if (hours > 0) return t('admin.accounts.codexTicket.remainingHours', { hours, minutes })
  return t('admin.accounts.codexTicket.remainingMinutes', { minutes: Math.max(1, minutes) })
}
</script>
