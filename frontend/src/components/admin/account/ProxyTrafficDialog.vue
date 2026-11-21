<template>
  <BaseDialog :show="true" :title="t('admin.accounts.traffic.title')" width="wide" @close="$emit('close')">
    <div class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex gap-1" role="group" :aria-label="t('admin.accounts.traffic.period')">
          <button v-for="range in [1, 7, 30]" :key="range" type="button" class="min-h-11 rounded-md px-3 text-sm focus-visible:ring-2 focus-visible:ring-primary-500" :class="days === range ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700'" :aria-pressed="days === range" @click="days = range">
            {{ range === 1 ? t('admin.accounts.traffic.today') : t('admin.accounts.traffic.days', { days: range }) }}
          </button>
        </div>
        <button type="button" class="btn btn-secondary min-h-11" :disabled="loading" :aria-label="t('common.refresh')" :title="t('common.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
      <p v-if="account" class="text-sm text-gray-600 dark:text-gray-300">{{ account.name }} · {{ t('admin.accounts.traffic.accountScope') }} <button type="button" class="ml-2 min-h-11 text-primary-600 dark:text-primary-400" @click="accountOnly = !accountOnly">{{ accountOnly ? t('admin.accounts.traffic.allNodes') : t('admin.accounts.traffic.accountNodes') }}</button></p>
      <label class="block">
        <span class="sr-only">{{ t('admin.accounts.traffic.search') }}</span>
        <input v-model="search" type="search" class="input w-full" :placeholder="t('admin.accounts.traffic.search')" />
      </label>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.accounts.traffic.error') }}</p>
      <div v-else-if="loading" role="status" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
      <template v-else-if="report">
        <p v-if="!report.persistence_ok" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accounts.traffic.persistenceWarning') }}</p>
        <dl class="grid grid-cols-3 gap-3 border-y border-gray-200 py-4 dark:border-dark-600">
          <div v-for="metric in metrics" :key="metric.key" class="min-w-0">
            <dt class="text-xs text-gray-500 dark:text-gray-400">{{ metric.label }}</dt>
            <dd class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-gray-100" :data-testid="`traffic-${metric.key}`">{{ bytes(totals[metric.key]) }}</dd>
          </div>
        </dl>
        <div v-if="nodes.length" class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
              <th class="py-2 pr-3 text-left font-medium">{{ t('admin.accounts.traffic.node') }}</th>
              <th class="px-2 py-2 text-right font-medium">{{ t('admin.accounts.traffic.upload') }}</th>
              <th class="px-2 py-2 text-right font-medium">{{ t('admin.accounts.traffic.download') }}</th>
              <th class="py-2 pl-2 text-right font-medium">{{ t('admin.accounts.traffic.total') }}</th>
            </tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="node in nodes" :key="node.id">
                <td class="max-w-64 py-3 pr-3"><span class="break-words font-medium text-gray-900 dark:text-gray-100">{{ formatProxyDisplayName(node.name) }}</span><div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ node.protocol.toUpperCase() }} · {{ t('admin.accounts.traffic.since', { time: date(node.started_at) }) }}</div></td>
                <td class="whitespace-nowrap px-2 py-3 text-right tabular-nums">{{ bytes(node.upload_bytes) }}</td>
                <td class="whitespace-nowrap px-2 py-3 text-right tabular-nums">{{ bytes(node.download_bytes) }}</td>
                <td class="whitespace-nowrap py-3 pl-2 text-right font-medium tabular-nums">{{ bytes(node.total_bytes) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="py-6 text-center text-sm text-gray-500">{{ t('admin.accounts.traffic.empty') }}</p>
        <div class="flex flex-wrap items-end gap-3 border-t border-gray-200 pt-3 dark:border-dark-600">
          <label class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.traffic.unitPrice') }}<input v-model="unitPrice" type="number" inputmode="decimal" min="0" step="0.01" class="input mt-1 block w-36" /></label>
          <p v-if="estimate !== null" class="pb-2 text-sm">{{ t('admin.accounts.traffic.estimate') }} <strong class="tabular-nums">{{ estimate.toFixed(4) }}</strong></p>
        </div>
        <div class="space-y-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
          <p>{{ t('admin.accounts.traffic.range', { start: date(report.period_start), end: date(report.period_end) }) }}</p>
          <p>{{ t('admin.accounts.traffic.history', { time: date(report.started_at) }) }}</p>
          <p>{{ t('admin.accounts.traffic.scope') }}</p>
        </div>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { getTraffic, type ProxyTrafficReport } from '@/api/admin/proxies'
import { formatProxyDisplayName } from '@/utils/proxyDisplay'
import type { Account } from '@/types'

const props = defineProps<{ account?: Account | null }>()
defineEmits<{ (e: 'close'): void }>()
const { t } = useI18n()
const days = ref(30)
const search = ref('')
const unitPrice = ref<number | string>('')
const accountOnly = ref(Boolean(props.account))
const loading = ref(false)
const error = ref(false)
const report = ref<ProxyTrafficReport | null>(null)
let abort: AbortController | undefined
let generation = 0
const proxyIDs = computed(() => {
  const ids = new Set<number>()
  if (props.account?.proxy_id) ids.add(props.account.proxy_id)
  for (const item of props.account?.proxy_bindings ?? []) ids.add(item.proxy_id)
  return ids
})
const nodes = computed(() => (report.value?.nodes ?? [])
  .filter(node => !accountOnly.value || node.proxy_ids.some(id => proxyIDs.value.has(id)))
  .filter(node => formatProxyDisplayName(node.name).toLowerCase().includes(search.value.trim().toLowerCase()))
  .sort((a, b) => b.total_bytes - a.total_bytes))
const totals = computed(() => nodes.value.reduce((sum, node) => ({
  upload: sum.upload + node.upload_bytes, download: sum.download + node.download_bytes, total: sum.total + node.total_bytes
}), { upload: 0, download: 0, total: 0 }))
const metrics = computed(() => (['upload', 'download', 'total'] as const).map(key => ({ key, label: t(`admin.accounts.traffic.${key}`) })))
const estimate = computed(() => unitPrice.value !== '' && Number.isFinite(Number(unitPrice.value)) && Number(unitPrice.value) >= 0 ? totals.value.total / 1e9 * Number(unitPrice.value) : null)
function bytes(value: number): string {
  if (value < 1000) return `${value} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let number = value / 1000
  let i = 0
  while (number >= 1000 && i < units.length - 1) { number /= 1000; i++ }
  return `${number.toFixed(2)} ${units[i]}`
}
function date(value: string): string {
  return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))
}
async function load() {
  const current = ++generation
  abort?.abort()
  abort = new AbortController()
  loading.value = true
  error.value = false
  try { const data = await getTraffic(days.value, abort.signal); if (current === generation) report.value = data }
  catch { if (current === generation) error.value = true }
  finally { if (current === generation) loading.value = false }
}
watch(days, load, { immediate: true })
onBeforeUnmount(() => { generation++; abort?.abort() })
</script>
