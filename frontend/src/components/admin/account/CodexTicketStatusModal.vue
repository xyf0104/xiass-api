<template>
  <BaseDialog :show="show" :title="t('admin.accounts.codexTicket.title')" width="wide" :close-on-escape="!busy" :show-close-button="!busy" @close="requestClose">
    <div v-if="account" class="space-y-4">
      <div class="rounded-lg border border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-600 dark:bg-dark-700/50">
        <div class="font-medium text-gray-900 dark:text-white">{{ account.name }}</div>
        <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">#{{ account.id }} · {{ t('admin.accounts.codexTicket.redactedNotice') }}</div>
      </div>

      <div class="flex items-center justify-between gap-4 border-y border-gray-200 py-3 dark:border-dark-600">
        <div class="min-w-0">
          <div class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.codexTicket.accountEnabled') }}</div>
          <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ enabled ? t('admin.accounts.codexTicket.enabledHint') : t('admin.accounts.codexTicket.disabledHint') }}</p>
        </div>
        <Toggle :model-value="enabled" :disabled="busy" :aria-label="t('admin.accounts.codexTicket.accountEnabled')" @update:model-value="setEnabled" />
      </div>

      <section class="space-y-3 border-b border-gray-200 pb-4 dark:border-dark-600">
        <div>
          <div class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.codexTicket.captureProxyTitle') }}</div>
          <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.captureProxyHint') }}</p>
        </div>
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-3" role="radiogroup" :aria-label="t('admin.accounts.codexTicket.captureProxyTitle')">
          <button v-for="mode in captureModes" :key="mode.value" type="button" role="radio" :aria-checked="captureMode === mode.value" :disabled="busy" class="rounded-md border px-3 py-2 text-left text-sm transition-colors" :class="captureMode === mode.value ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-500 dark:bg-primary-900/20 dark:text-primary-300' : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-dark-600 dark:text-gray-300 dark:hover:bg-dark-700'" @click="captureMode = mode.value">
            <span class="block font-medium">{{ mode.label }}</span>
            <span class="mt-0.5 block text-xs opacity-75">{{ mode.hint }}</span>
          </button>
        </div>

        <div v-if="captureMode === 'account'" class="rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:bg-dark-700/50 dark:text-gray-300">
          {{ accountDefaultSummary }}
          <div v-if="savedUnknownCount" class="mt-1 text-amber-700 dark:text-amber-300">
            {{ t('admin.accounts.codexTicket.savedUnknownWarning', { count: savedUnknownCount }) }}
          </div>
        </div>
        <div v-else class="space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <div class="relative min-w-48 flex-1">
              <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input v-model="proxySearch" class="input h-9 w-full pl-9" :placeholder="t('admin.accounts.codexTicket.searchProxies')" :disabled="busy" />
            </div>
            <select v-model="proxySourceFilter" class="input h-9 min-w-40 flex-1 sm:max-w-56" :disabled="busy">
              <option value="">{{ t('admin.accounts.codexTicket.allSources') }}</option>
              <option v-for="source in captureSources" :key="source.id" :value="source.id">{{ source.name }}</option>
              <option value="__manual__">{{ t('admin.accounts.codexTicket.manualSource') }}</option>
            </select>
            <template v-if="captureMode === 'custom'">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || filteredCaptureProxies.length === 0" @click="selectVisibleProxies">{{ t('admin.accounts.codexTicket.selectAll') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || filteredCaptureProxies.length === 0" @click="invertVisibleProxies">{{ t('admin.accounts.codexTicket.invert') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || selectedProxyIDs.size === 0" @click="clearProxySelection">{{ t('admin.accounts.codexTicket.clear') }}</button>
            </template>
          </div>

          <div v-if="loadingProxies" class="rounded-md border border-gray-200 px-3 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ t('common.loading') }}</div>
          <div v-else class="max-h-64 overflow-y-auto rounded-md border border-gray-200 dark:border-dark-600">
            <label v-for="option in filteredCaptureProxies" :key="option.proxy.id" class="flex items-start gap-3 border-b border-gray-100 px-3 py-2.5 last:border-b-0 dark:border-dark-700" :class="captureMode === 'custom' && 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-800/70'">
              <input type="checkbox" class="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600" :checked="isCaptureProxySelected(option.proxy.id)" :disabled="busy || captureMode === 'all'" @change="toggleCaptureProxy(option.proxy.id)" />
              <span class="min-w-0 flex-1">
                <span class="flex min-w-0 flex-wrap items-center gap-2">
                  <span class="truncate text-sm font-medium text-gray-800 dark:text-gray-100">{{ option.proxy.name }}</span>
                  <span class="font-mono text-[11px] uppercase text-gray-400">{{ option.proxy.protocol }}</span>
                </span>
                <span class="block break-all font-mono text-xs text-gray-500 dark:text-gray-400">{{ proxyAddress(option.proxy) }}</span>
                <span class="mt-1 block text-xs text-gray-400">{{ option.sourceNames.length ? option.sourceNames.join(' · ') : t('admin.accounts.codexTicket.manualSource') }}</span>
              </span>
            </label>
            <label v-for="id in (captureMode === 'custom' ? missingSavedProxyIDs : [])" :key="'missing-' + id" class="flex items-start gap-3 border-b border-gray-100 px-3 py-2.5 last:border-b-0 dark:border-dark-700" :class="captureMode === 'custom' && 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-800/70'">
              <input type="checkbox" class="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600" :checked="isCaptureProxySelected(id)" :disabled="busy || captureMode === 'all'" @change="toggleCaptureProxy(id)" />
              <span class="min-w-0 flex-1 text-sm text-amber-700 dark:text-amber-300">
                {{ t('admin.accounts.codexTicket.missingProxy', { id }) }}
              </span>
            </label>
            <div v-if="filteredCaptureProxies.length === 0" class="px-3 py-8 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('common.noOptionsFound') }}</div>
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.proxySelectionSummary', { count: effectiveProxyIDs?.length || 0 }) }}</p>
          <div class="flex flex-wrap justify-end gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || savingCaptureDefaults" @click="resetCaptureDefaults">{{ t('admin.accounts.codexTicket.followBusiness') }}</button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="busy || savingCaptureDefaults || effectiveProxyIDs === undefined || effectiveProxyIDs.length === 0" @click="saveCaptureDefaults">{{ savingCaptureDefaults ? t('admin.accounts.codexTicket.savingCaptureDefaults') : t('admin.accounts.codexTicket.saveCaptureDefaults') }}</button>
          </div>
        </div>
      </section>

      <div v-if="statuses.length" class="space-y-3">
        <div v-for="status in statuses" :key="status.model" class="rounded-lg border border-gray-200 px-4 py-3 dark:border-dark-600">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ status.model }}</span>
            <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', status.ready ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300']">{{ status.ready ? t('admin.accounts.codexTicket.ready') : t('admin.accounts.codexTicket.unavailable') }}</span>
          </div>
          <div class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.length') }}</span><span class="text-right font-mono text-gray-800 dark:text-gray-200">{{ formatLength(status) }}</span>
            <template v-if="status.shape_valid !== undefined"><span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.shapeValid') }}</span><span class="text-right text-gray-800 dark:text-gray-200">{{ status.shape_valid ? t('common.yes') : t('common.no') }}</span></template>
            <template v-if="status.account_mode"><span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.accountMode') }}</span><span class="text-right font-mono text-gray-800 dark:text-gray-200">{{ status.account_mode }}</span></template>
            <template v-if="status.issued_at"><span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.issuedAt') }}</span><span class="text-right text-gray-800 dark:text-gray-200">{{ formatDateTime(status.issued_at) }}</span></template>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.expiresAt') }}</span><span class="text-right text-gray-800 dark:text-gray-200">{{ status.expires_at ? formatDateTime(status.expires_at) : '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.remaining') }}</span><span class="text-right text-gray-800 dark:text-gray-200">{{ status.ready ? formatRemaining(status.remaining_seconds) : '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.observedModel') }}</span><span class="text-right font-mono text-gray-800 dark:text-gray-200">{{ status.observed_model || '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.egress') }}</span><span class="text-right text-gray-800 dark:text-gray-200">{{ status.proxy_name || status.proxy_id || '-' }}</span>
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.latency') }}</span><span class="text-right text-gray-800 dark:text-gray-200">{{ status.latency_ms ? `${status.latency_ms} ms` : '-' }}</span>
          </div>
          <p v-if="status.fallback" class="mt-3 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.accounts.codexTicket.fallbackHint', { model: status.observed_model || '-' }) }}</p>
          <p class="mt-3 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.egressHint') }}</p>
          <p v-if="status.blocked" class="mt-3 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.accounts.codexTicket.blockedHint') }}</p>
          <div class="mt-3 flex justify-end">
            <button type="button" class="btn btn-secondary" :disabled="refreshDisabled" @click="refresh(status.model)">{{ refreshingModel === status.model ? t('admin.accounts.codexTicket.refreshing') : t('admin.accounts.codexTicket.refresh') }}</button>
          </div>
          <details v-if="status.probes?.length" class="mt-3 text-xs text-gray-500 dark:text-gray-400">
            <summary class="cursor-pointer select-none">{{ t('admin.accounts.codexTicket.probeDetails', { count: status.probes.length }) }}</summary>
            <div class="mt-2 space-y-1 border-l-2 border-gray-200 pl-3 dark:border-dark-600">
              <div v-for="(probe, index) in status.probes" :key="`${status.model}-${probe.proxy_id || probe.proxy_name || index}`" class="flex items-center justify-between gap-3">
                <span>{{ probe.proxy_name || probe.proxy_id || t('admin.accounts.codexTicket.unknownEgress') }}</span>
                <span class="font-mono" :class="probe.valid ? 'text-emerald-600 dark:text-emerald-300' : 'text-gray-500 dark:text-gray-400'">{{ probe.observed_model || '-' }} · {{ probe.latency_ms ? `${probe.latency_ms} ms` : '-' }}</span>
              </div>
            </div>
          </details>
        </div>
      </div>
      <div v-else class="rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ t('admin.accounts.codexTicket.noStatus') }}</div>

      <p v-if="errorMessage" class="text-xs leading-5 text-red-600 dark:text-red-300">{{ errorMessage }}</p>
      <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codexTicket.storageHint') }}</p>
    </div>
    <template #footer><div class="flex justify-end"><button type="button" class="btn btn-secondary" :disabled="busy" @click="requestClose">{{ t('common.close') }}</button></div></template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { accountsAPI } from '@/api/admin/accounts'
import { proxiesAPI } from '@/api/admin/proxies'
import type { Account, CodexTurnTicketStatus, Proxy, ProxySubscriptionOverview } from '@/types'

type CaptureMode = 'account' | 'all' | 'custom'
type CaptureProxyOption = { proxy: Proxy; sourceIDs: string[]; sourceNames: string[] }

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ close: []; updated: [payload: { enabled: boolean; statuses: CodexTurnTicketStatus[]; codex_ticket_capture_proxy_ids?: number[] }] }>()
const { t } = useI18n()
const statuses = ref<CodexTurnTicketStatus[]>([])
const enabled = ref(false)
const toggling = ref(false)
const savingCaptureDefaults = ref(false)
const refreshingModel = ref('')
const loadingProxies = ref(false)
const errorMessage = ref('')
const captureMode = ref<CaptureMode>('account')
const captureProxies = ref<CaptureProxyOption[]>([])
const captureSources = ref<Array<{ id: string; name: string }>>([])
const selectedProxyIDs = ref<Set<number>>(new Set())
const savedCaptureProxyIDs = ref<number[]>([])
const proxySearch = ref('')
const proxySourceFilter = ref('')
let viewVersion = 0

const busy = computed(() => toggling.value || savingCaptureDefaults.value || Boolean(refreshingModel.value) || loadingProxies.value)
const boundProxyIDs = computed(() => {
  const ids = new Set<number>()
  if (props.account?.proxy_id) ids.add(props.account.proxy_id)
  props.account?.proxy_bindings?.forEach(binding => { if (binding.proxy_id > 0) ids.add(binding.proxy_id) })
  return [...ids]
})
const allProxyIDs = computed(() => captureProxies.value.map(option => option.proxy.id))
const missingSavedProxyIDs = computed(() => savedCaptureProxyIDs.value.filter(id => !captureProxies.value.some(option => option.proxy.id === id)))
const effectiveProxyIDs = computed<number[] | undefined>(() => {
  if (captureMode.value === 'account') return undefined
  if (captureMode.value === 'all') return allProxyIDs.value
  return [...selectedProxyIDs.value]
})
const savedUnknownCount = computed(() => missingSavedProxyIDs.value.length)
const savedConfigured = computed(() => savedCaptureProxyIDs.value.length > 0)
const accountDefaultSummary = computed(() => savedConfigured.value
  ? t('admin.accounts.codexTicket.savedDefaultSummary', { count: savedCaptureProxyIDs.value.length })
  : t('admin.accounts.codexTicket.accountDefaultSummary', { count: boundProxyIDs.value.length }))
const refreshDisabled = computed(() => !enabled.value || busy.value || (effectiveProxyIDs.value !== undefined && effectiveProxyIDs.value.length === 0))
const captureModes = computed(() => [
  { value: 'account' as const, label: t('admin.accounts.codexTicket.accountDefault'), hint: t('admin.accounts.codexTicket.accountDefaultHint') },
  { value: 'all' as const, label: t('admin.accounts.codexTicket.allActive'), hint: t('admin.accounts.codexTicket.allActiveHint') },
  { value: 'custom' as const, label: t('admin.accounts.codexTicket.customProxies'), hint: t('admin.accounts.codexTicket.customProxiesHint') }
])
const filteredCaptureProxies = computed(() => {
  const query = proxySearch.value.trim().toLowerCase()
  return captureProxies.value.filter(option => {
    if (proxySourceFilter.value === '__manual__' && option.sourceIDs.length > 0) return false
    if (proxySourceFilter.value && proxySourceFilter.value !== '__manual__' && !option.sourceIDs.includes(proxySourceFilter.value)) return false
    if (!query) return true
    return `${option.proxy.name} ${option.proxy.host} ${option.proxy.port} ${option.sourceNames.join(' ')}`.toLowerCase().includes(query)
  })
})

function requestClose() { if (!busy.value) emit('close') }
function proxyAddress(proxy: Proxy): string { const host = proxy.host.includes(':') && !proxy.host.startsWith('[') ? `[${proxy.host}]` : proxy.host; return `${host}:${proxy.port}` }
function formatLength(status: CodexTurnTicketStatus): string { if (status.length == null) return '-'; return status.expected_length ? `${status.length} / ${status.expected_length}` : String(status.length) }
function isCaptureProxySelected(id: number): boolean { return captureMode.value === 'all' || selectedProxyIDs.value.has(id) }
function toggleCaptureProxy(id: number) { if (busy.value || captureMode.value !== 'custom') return; const next = new Set(selectedProxyIDs.value); if (next.has(id)) next.delete(id); else next.add(id); selectedProxyIDs.value = next }
function selectVisibleProxies() { const next = new Set(selectedProxyIDs.value); filteredCaptureProxies.value.forEach(option => next.add(option.proxy.id)); selectedProxyIDs.value = next }
function invertVisibleProxies() { const next = new Set(selectedProxyIDs.value); filteredCaptureProxies.value.forEach(option => { if (next.has(option.proxy.id)) next.delete(option.proxy.id); else next.add(option.proxy.id) }); selectedProxyIDs.value = next }
function clearProxySelection() { selectedProxyIDs.value = new Set() }

function initializeAccount(account: Account) {
  enabled.value = account.codex_ticket_enabled === true
  statuses.value = account.codex_turn_tickets ? [...account.codex_turn_tickets] : []
  captureMode.value = 'account'
  selectedProxyIDs.value = new Set(boundProxyIDs.value)
  savedCaptureProxyIDs.value = [...(account.codex_ticket_capture_proxy_ids || [])]
  proxySearch.value = ''
  proxySourceFilter.value = ''
  errorMessage.value = ''
  toggling.value = false
  savingCaptureDefaults.value = false
  refreshingModel.value = ''
}

async function loadCaptureProxies(accountID: number, version: number) {
  loadingProxies.value = true
  try {
    const [proxies, subscriptions] = await Promise.all([
      proxiesAPI.getAll(),
      proxiesAPI.getSubscriptions().catch(() => null as ProxySubscriptionOverview | null)
    ])
    if (!props.show || props.account?.id !== accountID || version !== viewVersion) return
    const membership = new Map<number, { ids: Set<string>; names: Set<string> }>()
    subscriptions?.nodes.forEach(node => {
      if (!node.proxy_id) return
      const entry = membership.get(node.proxy_id) || { ids: new Set<string>(), names: new Set<string>() }
      entry.ids.add(node.source_id)
      entry.names.add(node.source_name || node.source_id)
      membership.set(node.proxy_id, entry)
    })
    captureProxies.value = proxies.map(proxy => {
      const entry = membership.get(proxy.id)
      return { proxy, sourceIDs: entry ? [...entry.ids] : [], sourceNames: entry ? [...entry.names] : [] }
    })
    captureSources.value = (subscriptions?.sources || []).map(source => ({ id: source.id, name: source.name || source.id }))
    const saved = savedCaptureProxyIDs.value
    selectedProxyIDs.value = new Set(saved.length ? saved : boundProxyIDs.value)
  } catch (error: any) {
    if (props.show && props.account?.id === accountID && version === viewVersion) errorMessage.value = error?.message || t('admin.accounts.codexTicket.proxyLoadFailed')
  } finally {
    if (version === viewVersion) loadingProxies.value = false
  }
}

watch(() => [props.show, props.account?.id] as const, ([show, id]) => {
  viewVersion++
  const version = viewVersion
  captureProxies.value = []
  captureSources.value = []
  loadingProxies.value = false
  if (!show || !id || !props.account) return
  initializeAccount(props.account)
  void loadCaptureProxies(id, version)
}, { immediate: true })

watch(() => [props.account?.codex_ticket_enabled, props.account?.codex_turn_tickets] as const, () => {
  if (!props.show || !props.account || toggling.value || refreshingModel.value) return
  enabled.value = props.account.codex_ticket_enabled === true
  statuses.value = props.account.codex_turn_tickets ? [...props.account.codex_turn_tickets] : []
}, { deep: true })

async function setEnabled(next: boolean) {
  if (!props.account || busy.value) return
  const accountID = props.account.id
  const version = viewVersion
  toggling.value = true
  errorMessage.value = ''
  try {
    const result = await accountsAPI.setCodexTicketEnabled(accountID, next)
    if (!props.show || props.account?.id !== accountID || version !== viewVersion) return
    enabled.value = result
    emit('updated', { enabled: result, statuses: statuses.value, codex_ticket_capture_proxy_ids: savedCaptureProxyIDs.value })
  } catch (error: any) {
    if (props.show && props.account?.id === accountID && version === viewVersion) errorMessage.value = error?.message || t('admin.accounts.codexTicket.toggleFailed')
  } finally {
    if (version === viewVersion) toggling.value = false
  }
}

async function persistCaptureDefaults(proxyIDs: number[], switchToAccount = true): Promise<boolean> {
  if (!props.account || savingCaptureDefaults.value) return false
  const accountID = props.account.id
  const version = viewVersion
  savingCaptureDefaults.value = true
  errorMessage.value = ''
  try {
    const saved = await accountsAPI.setCodexTicketCaptureProxies(accountID, proxyIDs)
    if (!props.show || props.account?.id !== accountID || version !== viewVersion) return false
    savedCaptureProxyIDs.value = [...saved]
    if (switchToAccount) {
      captureMode.value = 'account'
      selectedProxyIDs.value = new Set(saved.length ? saved : boundProxyIDs.value)
    }
    emit('updated', { enabled: enabled.value, statuses: statuses.value, codex_ticket_capture_proxy_ids: saved })
    return true
  } catch (error: any) {
    if (props.show && props.account?.id === accountID && version === viewVersion) errorMessage.value = error?.message || t('admin.accounts.codexTicket.captureDefaultsSaveFailed')
    return false
  } finally {
    if (version === viewVersion) savingCaptureDefaults.value = false
  }
}

function saveCaptureDefaults() {
  if (busy.value || effectiveProxyIDs.value === undefined) return
  void persistCaptureDefaults([...effectiveProxyIDs.value])
}

function resetCaptureDefaults() {
  if (busy.value) return
  void persistCaptureDefaults([])
}

async function refresh(model: string) {
  if (!props.account || refreshDisabled.value) return
  const accountID = props.account.id
  const version = viewVersion
  const proxyIDs = effectiveProxyIDs.value
  refreshingModel.value = model
  errorMessage.value = ''
  try {
    if (proxyIDs !== undefined) {
      const saved = await persistCaptureDefaults([...proxyIDs], false)
      if (!saved) return
    }
    const next = proxyIDs === undefined ? await accountsAPI.refreshCodexTicket(accountID, model) : await accountsAPI.refreshCodexTicket(accountID, model, proxyIDs)
    if (!props.show || props.account?.id !== accountID || version !== viewVersion) return
    statuses.value = next
    emit('updated', { enabled: enabled.value, statuses: next })
  } catch (error: any) {
    if (props.show && props.account?.id === accountID && version === viewVersion) errorMessage.value = error?.message || t('admin.accounts.codexTicket.refreshFailed')
  } finally {
    if (version === viewVersion) refreshingModel.value = ''
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
