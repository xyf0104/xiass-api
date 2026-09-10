<template>
  <BaseDialog :show="show" :title="t(`${prefix}.title`)" width="extra-wide" @close="close">
    <div class="pelican-benchmark min-w-0 space-y-4">
      <div class="flex border-b border-gray-200 dark:border-dark-600" role="tablist" :aria-label="t(`${prefix}.title`)">
        <button
          v-for="item in tabs" :id="`pelican-tab-${item}`" :key="item" type="button" role="tab"
          :aria-selected="tab === item" :aria-controls="`pelican-panel-${item}`" :tabindex="tab === item ? 0 : -1"
          class="min-h-11 min-w-0 flex-1 border-b-2 px-2 py-2 text-sm font-medium sm:flex-none sm:px-5"
          :class="tab === item ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 dark:text-gray-400'"
          :data-testid="`tab-${item}`" @click="tab = item" @keydown="navigateTabs($event, item)"
        >{{ t(`${prefix}.tabs.${item}`) }}</button>
      </div>

      <div v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</div>
      <div v-if="notice" role="status" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ notice }}</div>
      <p v-if="busy" role="status" class="text-xs text-amber-700 dark:text-amber-300">{{ t(`${prefix}.${waitingForExit ? 'waitingForExit' : 'busy'}`) }}</p>

      <section :id="`pelican-panel-${tab}`" role="tabpanel" :aria-labelledby="`pelican-tab-${tab}`" :aria-busy="loading">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <span class="text-sm text-gray-500 dark:text-gray-400">{{ t(`${prefix}.count`, { count: tab === 'new' ? accounts.length : tab === 'history' ? historyTotal : current.length }) }}</span>
          <div class="flex flex-wrap items-center gap-2">
            <template v-if="tab === 'new'">
              <label class="flex items-center gap-2 text-sm"><input type="checkbox" data-testid="select-all" :checked="allSelected" :indeterminate="selectedStartable.length > 0 && !allSelected" :disabled="busy || loading || !ready || !startable.length" @change="selectAll(($event.target as HTMLInputElement).checked)">{{ t(`${prefix}.selectAll`) }}</label>
              <button type="button" class="btn btn-primary" data-testid="start-selected" :disabled="!ready || busy || loading || !selectedStartable.length" @click="start(selectedStartable)"><Icon name="play" size="sm" />{{ t(`${prefix}.startSelected`, { count: selectedStartable.length }) }}</button>
            </template>
            <Select v-else v-model="filters[tab]" class="w-32" :options="filterOptions" :aria-label="t(`${prefix}.filter`)" data-testid="result-filter" />
            <button v-if="tab === 'new'" type="button" class="btn btn-primary" data-testid="start-all" :disabled="!ready || busy || loading || !startable.length" @click="start(startable)">
              <Icon name="play" size="sm" />{{ t(`${prefix}.startAll`) }}
            </button>
            <button v-if="tab === 'current'" type="button" class="btn btn-secondary" data-testid="stop-all" :disabled="busy || !stoppable.length" @click="stop(stoppable, true)">
              <Icon name="xCircle" size="sm" />{{ t(`${prefix}.stopAll`) }}
            </button>
            <button type="button" class="btn btn-secondary h-11 w-11 !p-0" :title="t(`${prefix}.refresh`)" :aria-label="t(`${prefix}.refresh`)" :disabled="loading || busy" data-testid="refresh" @click="refresh">
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>

        <div class="benchmark-row hidden gap-2 border-b border-gray-200 py-2 text-xs font-medium text-gray-500 dark:border-dark-600 dark:text-gray-400 lg:grid" aria-hidden="true">
          <span>{{ t(`${prefix}.name`) }}</span><span>{{ t(`${prefix}.plan`) }}</span><span>{{ t(`${prefix}.statusLabel`) }}</span><span>{{ t(`${prefix}.model`) }}</span><span>{{ t(`${prefix}.date`) }}</span><span>{{ t(`${prefix}.duration`) }}</span><span>{{ t(`${prefix}.result`) }}</span>
        </div>
        <p v-if="loading && !(tab === 'new' ? accounts.length : rows.length)" role="status" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
        <p v-else-if="!(tab === 'new' ? accounts.length : rows.length)" class="py-10 text-center text-sm text-gray-500 dark:text-gray-400">{{ t(`${prefix}.empty.${tab}`) }}</p>

        <template v-if="tab === 'new'">
          <div v-for="account in sortedAccounts" :key="account.id" class="benchmark-row grid items-center gap-2 border-b border-gray-100 py-2 text-sm dark:border-dark-700" :data-testid="`account-${account.id}`">
            <div class="account-name min-w-0 break-words font-medium text-gray-900 dark:text-gray-100">
              <label class="flex items-center gap-2"><input v-model="selectedIds" type="checkbox" :value="account.id" :disabled="busy || loading || !ready || !canStart(account)" :data-testid="`select-${account.id}`"><span>{{ account.name }}</span></label>
              <span :class="nodeClass(account.execution_node_id)" data-testid="node-badge">{{ nodeLabel(account.execution_node_id) }}</span>
            </div>
            <span class="plan-label text-xs font-semibold text-primary-700 dark:text-primary-300">{{ planLabel(account.plan_type) }}</span>
            <span class="run-status text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.accounts.status.${account.status}`) }}</span>
            <Select v-model="models[account.id]" class="model-select min-w-0" :options="modelOptions(account)" :aria-label="`${account.name}: ${t(`${prefix}.model`)}`" :disabled="busy || isAccountRunning(account.id) || !account.can_test" :data-testid="`model-${account.id}`" @pointerdown="loadModels(account)" @focusin="loadModels(account)" />
            <span class="hidden text-gray-400 lg:block">--</span><span class="hidden text-gray-400 lg:block">--</span>
            <button type="button" class="row-action btn btn-secondary h-11 w-11 !p-0" :title="t(`${prefix}.${canStart(account) ? 'start' : 'unavailable'}`)" :aria-label="`${account.name}: ${t(`${prefix}.start`)}`" :disabled="!ready || busy || loading || !canStart(account)" :data-testid="`start-${account.id}`" @click="start([account])"><Icon name="play" size="sm" /></button>
          </div>
        </template>
        <template v-else>
          <div v-for="run in rows" :key="run.id" class="border-b border-gray-100 dark:border-dark-700" :data-testid="`run-${run.id}`">
            <div class="benchmark-row grid items-center gap-2 py-2 text-sm">
              <div class="account-name min-w-0 break-words font-medium text-gray-900 dark:text-gray-100">{{ run.account_name }}<br><span :class="nodeClass(runOwner(run))" data-testid="node-badge">{{ nodeLabel(runOwner(run)) }}</span></div>
              <span class="plan-label text-xs font-semibold text-primary-700 dark:text-primary-300">{{ planLabel(accounts.find(account => account.id === run.account_id)?.plan_type ?? null) }}</span>
              <span class="run-status min-w-0 text-xs" :class="statusClass(run.status)" :title="run.error_code ? errorLabel(run.error_code) : undefined">{{ t(`${prefix}.status.${run.status}`) }}</span>
              <span class="model-select min-w-0 break-all text-xs text-gray-700 dark:text-gray-300">{{ run.model }}</span>
              <time class="run-date min-w-0 break-words text-xs text-gray-500 dark:text-gray-400" :datetime="run.created_at">{{ formatDate(run.created_at) }}</time>
              <span class="run-duration text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ formatDuration(run) }}</span>
              <div class="row-action flex flex-col items-end gap-1">
                <button v-if="active(run)" type="button" class="btn btn-secondary h-11 w-11 !p-0" :disabled="busy" :title="t(`${prefix}.stop`)" :aria-label="`${run.account_name}: ${t(`${prefix}.stop`)}`" :data-testid="`stop-${run.id}`" @click="stop([run])"><Icon name="xCircle" size="sm" /></button>
                <PelicanResultPreview v-else-if="run.status === 'succeeded' && run.html_bytes > 0" :id="run.id" :title="`${run.account_name}: ${t(`${prefix}.preview`)}`" />
                <Icon v-else :name="run.status === 'failed' ? 'exclamationCircle' : 'clock'" size="sm" class="m-3 text-gray-400" :title="(run.error_code ? errorLabel(run.error_code) : '') || t(`${prefix}.status.${run.status}`)" />
                <div v-if="tab === 'current'" class="flex items-center gap-1">
                  <button v-for="action in ['continue', 'retry'] as const" :key="action" type="button" class="btn btn-secondary !px-2 !py-1 text-xs" :disabled="busy || loading" :data-testid="`${action}-${run.id}`" @click="restart(run, action)"><Icon :name="action === 'continue' ? 'play' : 'refresh'" size="sm" />{{ t(`${prefix}.${action}`) }}</button>
                </div>
              </div>
            </div>
            <p v-if="run.error_code" class="pb-2 text-xs text-red-600 [overflow-wrap:anywhere] dark:text-red-400">{{ errorLabel(run.error_code) }}</p>
          </div>
        </template>

        <div v-if="tab === 'history' && historyPages > 1" class="mt-4 flex items-center justify-center gap-3">
          <button type="button" class="btn btn-secondary h-11 w-11 !p-0" :disabled="loading || historyPage <= 1" :title="t(`${prefix}.previous`)" :aria-label="t(`${prefix}.previous`)" data-testid="history-previous" @click="loadHistory(historyPage - 1)"><Icon name="chevronLeft" size="sm" /></button>
          <span class="text-sm tabular-nums text-gray-500">{{ historyPage }} / {{ historyPages }}</span>
          <button type="button" class="btn btn-secondary h-11 w-11 !p-0" :disabled="loading || historyPage >= historyPages" :title="t(`${prefix}.next`)" :aria-label="t(`${prefix}.next`)" data-testid="history-next" @click="loadHistory(historyPage + 1)"><Icon name="chevronRight" size="sm" /></button>
        </div>
      </section>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import PelicanResultPreview from './PelicanResultPreview.vue'
import { getStatus as getExecutionNodeStatus, type ExecutionNodeAdminStatus } from '@/api/admin/executionNodes'
import {
  DEFAULT_PELICAN_MODEL, getPelicanAccounts, getPelicanCurrent, getPelicanHistory,
  getPelicanModels, getPelicanResult, restartPelicanTest, startPelicanTests, stopPelicanTests, stopAllPelicanTests, PelicanPartialStartError,
  type PelicanBenchmarkAccount, type PelicanBenchmarkRun, type PelicanBenchmarkStatus, type PelicanBenchmarkSkipped
} from '@/api/admin/pelicanBenchmark'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (event: 'close'): void }>()
const { t, locale } = useI18n()
const prefix = 'admin.accounts.pelicanBenchmark'
const tabs = ['new', 'current', 'history'] as const
type Tab = typeof tabs[number]
const tab = ref<Tab>('new')
const accounts = ref<PelicanBenchmarkAccount[]>([])
const models = ref<Record<number, string>>({})
const selectedIds = ref<number[]>([])
const nodeStatus = ref<ExecutionNodeAdminStatus | null>(null)
const filters = ref<{ current: '' | 'succeeded'; history: '' | 'succeeded' }>({ current: '', history: '' })
const filterOptions = computed(() => [{ value: '', label: t(`${prefix}.allResults`) }, { value: 'succeeded', label: t(`${prefix}.passed`) }])
const waitingForExit = ref(false)
const current = ref<PelicanBenchmarkRun[]>([])
const history = ref<PelicanBenchmarkRun[]>([])
const historyPage = ref(1)
const historyPages = ref(0)
const historyTotal = ref(0)
const ready = ref(false)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const now = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | undefined
let session = 0
let controller = new AbortController()
let currentRequest: AbortController | undefined
let readVersion = 0
let currentVersion = 0
let pollTimer: ReturnType<typeof setTimeout> | undefined
const loadedModels = new Set<number>()
const loadingModels = new Set<number>()

const active = (run: PelicanBenchmarkRun) => ['queued', 'running', 'canceling'].includes(run.status)
const isAccountRunning = (id: number) => current.value.some(run => run.account_id === id && active(run))
const canStart = (account: PelicanBenchmarkAccount) => account.can_test && !isAccountRunning(account.id) && account.model_ids.includes(models.value[account.id])
const startable = computed(() => accounts.value.filter(canStart))
const selectedStartable = computed(() => startable.value.filter(account => selectedIds.value.includes(account.id)))
const allSelected = computed(() => startable.value.length > 0 && selectedStartable.value.length === startable.value.length)
function selectAll(checked: boolean) { selectedIds.value = checked ? startable.value.map(account => account.id) : [] }
function runOwner(run: PelicanBenchmarkRun) {
  // Migrated history has an empty snapshot. Fall back only to a known account,
  // never to a guessed default or remote node.
  return run.execution_node_id?.trim() || accounts.value.find(account => account.id === run.account_id)?.execution_node_id
}
function isLocal(owner?: string) {
  const id = owner?.trim()
  return !!id && (id === nodeStatus.value?.runtime.node_id?.trim() || nodeStatus.value?.nodes.some(node => node.node_id === id && node.is_local) === true)
}
function nodeLabel(owner?: string) { return isLocal(owner) ? t('admin.accounts.executionNodeLocal') : owner?.trim() || '--' }
function nodeClass(owner?: string) {
  const base = 'mt-1 inline-flex max-w-full break-all rounded px-1.5 py-0.5 text-[11px] font-medium leading-4 '
  return base + (!owner?.trim() ? 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400' : isLocal(owner)
    ? 'bg-sky-50 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300'
    : 'bg-amber-50 text-amber-700 ring-1 ring-inset ring-amber-200 dark:bg-amber-900/25 dark:text-amber-300 dark:ring-amber-800/70')
}
const stoppable = computed(() => current.value.filter(active))
function planRank(plan: string | null | undefined) {
  const normalized = plan?.toLowerCase().replace(/[\s_-]/g, '')
  return ({ pro: 0, prolite: 1, plus: 2, team: 3 } as Record<string, number>)[normalized ?? ''] ?? 4
}
const sortedAccounts = computed(() => [...accounts.value].sort((a, b) => planRank(a.plan_type) - planRank(b.plan_type) || a.id - b.id))
const rows = computed(() => [...(tab.value === 'history' ? history.value : current.value.filter(run => !filters.value.current || run.status === filters.value.current))].sort((a, b) =>
  planRank(accounts.value.find(item => item.id === a.account_id)?.plan_type) - planRank(accounts.value.find(item => item.id === b.account_id)?.plan_type)))
const planLabel = (plan: string | null) => plan?.trim().toUpperCase() || '--'

function errorLabel(code: string) {
  const known = ['account_unavailable', 'ineligible', 'free_plan', 'unknown_plan', 'unsupported_auth_mode', 'model_not_supported', 'fixed_egress_unavailable', 'upstream_failed', 'html_too_large', 'invalid_html', 'timeout', 'runner_interrupted', 'runner_panic', 'account_access_denied', 'account_not_found']
  const upstream = ['upstream_http_400', 'upstream_http_401', 'upstream_http_403', 'upstream_http_404', 'upstream_http_408', 'upstream_http_429', 'upstream_http_500', 'upstream_http_502', 'upstream_http_503', 'upstream_http_504', 'upstream_network_error', 'upstream_stream_error', 'upstream_incomplete']
  const continuation = ['upstream_client_upgrade_required', 'continuation_source_unavailable', 'invalid_continuation_source']
  return t(`${prefix}.errors.${known.includes(code) || upstream.includes(code) || continuation.includes(code) ? code : 'unknown'}`)
}

async function loadModels(account: PelicanBenchmarkAccount) {
  if (busy.value || !account.can_test || loadedModels.has(account.id) || loadingModels.has(account.id)) return
  const signal = controller.signal
  loadingModels.add(account.id)
  try {
    const ids = await getPelicanModels(account.id, signal)
    if (signal.aborted) return
    const currentAccount = accounts.value.find(item => item.id === account.id)
    if (currentAccount) {
      currentAccount.model_ids = [...new Set([DEFAULT_PELICAN_MODEL, ...ids])]
      loadedModels.add(account.id)
    }
  } catch (cause) {
    if (!signal.aborted) error.value = message(cause, 'modelsFailed')
  } finally {
    if (!signal.aborted) loadingModels.delete(account.id)
  }
}

function mergeCurrent(items: PelicanBenchmarkRun[]) {
  const merged = new Map(current.value.map(run => [run.id, run]))
  for (const run of items) merged.set(run.id, run)
  current.value = [...merged.values()]
}

function statusClass(status: PelicanBenchmarkStatus) {
  if (status === 'succeeded') return 'text-emerald-700 dark:text-emerald-400'
  if (status === 'failed') return 'text-red-600 dark:text-red-400'
  if (status === 'running') return 'text-primary-600 dark:text-primary-400'
  if (status === 'queued' || status === 'canceling') return 'text-amber-700 dark:text-amber-400'
  return 'text-gray-500 dark:text-gray-400'
}

function modelOptions(account: PelicanBenchmarkAccount) {
  return [...new Set([DEFAULT_PELICAN_MODEL, ...account.model_ids])].map(value => ({
    value, label: value, disabled: !account.model_ids.includes(value)
  }))
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString(locale.value, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '--'
}

function formatDuration(run: PelicanBenchmarkRun) {
  const ms = (run.status === 'running' || run.status === 'canceling') && run.started_at
    ? Math.max(0, now.value - Date.parse(run.started_at)) : run.duration_ms
  return ms !== null && Number.isFinite(ms) && ms >= 0 ? t(`${prefix}.seconds`, { seconds: (ms / 1000).toFixed(1) }) : '--'
}

function message(cause: unknown, key = 'loadFailed') {
  const status = (cause as { status?: number; response?: { status?: number } })?.status ?? (cause as { response?: { status?: number } })?.response?.status
  return t(`${prefix}.${status === 404 || status === 501 ? 'notConnected' : key}`)
}

function cancelPoll() {
  clearTimeout(pollTimer)
  pollTimer = undefined
}

function schedulePoll() {
  cancelPoll()
  if (!props.show || controller.signal.aborted || tab.value !== 'current' || document.hidden || busy.value || !current.value.some(active)) return
  pollTimer = setTimeout(() => void loadCurrent(), 3_000)
}

async function loadCurrent(discover = false) {
  cancelPoll()
  currentRequest?.abort()
  const request = new AbortController()
  currentRequest = request
  const version = ++currentVersion
  const ownSession = session
  try {
    const result = await getPelicanCurrent(request.signal, [...new Set(current.value.map(run => run.batch_id))], discover)
    if (session !== ownSession || request.signal.aborted || version !== currentVersion) return false
    current.value = result.items
    schedulePoll()
    return true
  } catch (cause) {
    if (session === ownSession && !request.signal.aborted && version === currentVersion) error.value = message(cause)
    return false
  }
}

async function refresh() {
  if (busy.value) return
  if (tab.value === 'history') return loadHistory(historyPage.value)
  const version = ++readVersion
  const ownSession = session
  const signal = controller.signal
  loading.value = true
  error.value = ''
  try {
    if (tab.value === 'new') {
      ready.value = false
      const result = await getPelicanAccounts(signal)
      if (ownSession !== session || version !== readVersion || signal.aborted) return
      const previous = new Map(accounts.value.map(account => [account.id, account]))
      accounts.value = result.items.map(account => ({ ...account, model_ids: previous.get(account.id)?.model_ids ?? account.model_ids }))
      for (const account of accounts.value) models.value[account.id] ??= DEFAULT_PELICAN_MODEL
    }
    const success = await loadCurrent(true)
    if (ownSession === session && version === readVersion && !signal.aborted) ready.value = success === true
  } catch (cause) {
    if (ownSession === session && version === readVersion && !signal.aborted) error.value = message(cause)
  } finally {
    if (ownSession === session && version === readVersion) loading.value = false
  }
}

async function loadHistory(page: number) {
  const version = ++readVersion
  const ownSession = session
  const signal = controller.signal
  loading.value = true
  error.value = ''
  try {
    const result = await getPelicanHistory(page, signal, filters.value.history || undefined)
    if (ownSession !== session || version !== readVersion || signal.aborted) return
    history.value = result.items
    historyPage.value = result.page
    historyPages.value = result.pages
    historyTotal.value = result.total
  } catch (cause) {
    if (ownSession === session && version === readVersion && !signal.aborted) error.value = message(cause)
  } finally {
    if (ownSession === session && version === readVersion) loading.value = false
  }
}

async function mutate(operation: (signal: AbortSignal) => Promise<{ items: PelicanBenchmarkRun[]; skipped?: PelicanBenchmarkSkipped[] }>, failureKey: string) {
  if (busy.value || controller.signal.aborted) return
  const ownSession = session
  const signal = controller.signal
  busy.value = true
  error.value = ''
  cancelPoll()
  currentRequest?.abort()
  ++currentVersion
  try {
    const result = await operation(signal)
    if (ownSession !== session || signal.aborted) return
    mergeCurrent(result.items)
    notice.value = result.skipped?.length ? t(`${prefix}.skipped`, { count: result.skipped.length }) : ''
    if (result.items.length) tab.value = 'current'
  } catch (cause) {
    if (ownSession === session && !signal.aborted) {
      if (cause instanceof PelicanPartialStartError) {
        mergeCurrent(cause.items)
        if (cause.items.length) tab.value = 'current'
        error.value = message(cause.cause, cause.items.length ? 'partialStart' : failureKey)
      } else error.value = cause instanceof Error && cause.message === 'pelican_stop_wait_timeout' ? t(`${prefix}.stopWaitTimeout`) : message(cause, failureKey)
      ready.value = false
    }
  } finally {
    if (ownSession === session && !signal.aborted) {
      busy.value = false
      schedulePoll()
    }
  }
}

async function start(selected: PelicanBenchmarkAccount[]) {
  if (!ready.value || busy.value || loading.value) return
  const tests = selected.filter(canStart).map(account => ({ account_id: account.id, model: models.value[account.id] }))
  if (!tests.length) return
  await mutate(signal => startPelicanTests(tests, signal), 'startFailed')
}

async function stop(selected: PelicanBenchmarkRun[], all = false) {
  const ids = selected.filter(active).map(run => run.id)
  if (!ids.length) return
  await mutate(async signal => {
    if (!all) return stopPelicanTests(ids, signal)
    await stopAllPelicanTests(signal)
    return getPelicanCurrent(signal, [...new Set(current.value.map(run => run.batch_id))])
  }, 'stopFailed')
}

function waitForPoll(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(new DOMException('Aborted', 'AbortError')) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, 1000)
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
}

async function restart(run: PelicanBenchmarkRun, action: 'continue' | 'retry') {
  await mutate(async parentSignal => {
    const waiting = new AbortController()
    const signal = waiting.signal
    const abort = () => waiting.abort()
    parentSignal.addEventListener('abort', abort, { once: true })
    let timer: ReturnType<typeof setTimeout> | undefined
    waitingForExit.value = true
    try {
      const deadline = new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          reject(new Error('pelican_stop_wait_timeout'))
          waiting.abort()
        }, 30_000)
      })
      await Promise.race([deadline, (async () => {
      // Discover other batches too: the selected source may already have an active successor.
      const snapshot = await getPelicanCurrent(signal, [...new Set(current.value.map(item => item.batch_id))], true)
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      mergeCurrent(snapshot.items)
      const pending = new Map(snapshot.items.filter(item => item.account_id === run.account_id && active(item)).map(item => [item.id, item]))
      const source = await getPelicanResult(run.id, signal)
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      if (active(source)) pending.set(source.id, source)
      const stopIds = [...pending.values()].filter(item => item.status !== 'canceling').map(item => item.id)
      if (stopIds.length) {
        const stopped = await stopPelicanTests(stopIds, signal)
        if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
        mergeCurrent(stopped.items)
        for (const item of stopped.items) if (!active(item)) pending.delete(item.id)
      }
      while (pending.size) {
        await waitForPoll(signal)
        const results = await Promise.all([...pending.keys()].map(id => getPelicanResult(id, signal)))
        if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
        mergeCurrent(results)
        for (const item of results) if (!active(item)) pending.delete(item.id)
      }
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      })()])
      clearTimeout(timer)
      if (parentSignal.aborted) throw new DOMException('Aborted', 'AbortError')
      waitingForExit.value = false
      return await restartPelicanTest(run.id, action, parentSignal)
    } finally {
      clearTimeout(timer)
      parentSignal.removeEventListener('abort', abort)
      waiting.abort()
      if (!parentSignal.aborted) waitingForExit.value = false
    }
  }, 'startFailed')
}

function dispose() {
  ++session
  ++readVersion
  ++currentVersion
  controller.abort()
  currentRequest?.abort()
  cancelPoll()
  clearInterval(clockTimer)
}

function close() {
  dispose()
  emit('close')
}

function navigateTabs(event: KeyboardEvent, item: Tab) {
  const index = tabs.indexOf(item)
  const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1
  if (next < 0) return
  event.preventDefault()
  tab.value = tabs[next]
  ;(event.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus()
}

watch(tab, () => {
  cancelPoll()
  currentRequest?.abort()
  ++currentVersion
  ++readVersion
  loading.value = false
  if (props.show && !controller.signal.aborted && !busy.value) void refresh()
}, { flush: 'sync' })

watch(() => filters.value.history, () => {
  history.value = []
  historyPage.value = 1
  historyPages.value = 0
  historyTotal.value = 0
  if (props.show && tab.value === 'history') void loadHistory(1)
})

watch(() => props.show, show => {
  dispose()
  if (!show) return
  controller = new AbortController()
  now.value = Date.now()
  clockTimer = setInterval(() => { if (!document.hidden) now.value = Date.now() }, 1000)
  accounts.value = []
  current.value = []
  history.value = []
  models.value = {}
  selectedIds.value = []
  filters.value = { current: '', history: '' }
  waitingForExit.value = false
  nodeStatus.value = null
  const ownSession = session
  void getExecutionNodeStatus().then(status => {
    if (ownSession === session && !controller.signal.aborted) nodeStatus.value = status
  }).catch(() => { /* Unknown ownership remains explicit when status is unavailable. */ })
  loadedModels.clear()
  loadingModels.clear()
  notice.value = ''
  ready.value = false
  busy.value = false
  historyPage.value = 1
  historyPages.value = 0
  historyTotal.value = 0
  error.value = ''
  if (tab.value === 'new') void refresh()
  else tab.value = 'new'
}, { immediate: true })

function onVisibilityChange() {
  cancelPoll()
  if (document.hidden) {
    currentRequest?.abort()
  } else if (props.show && !controller.signal.aborted && tab.value === 'current' && !busy.value && current.value.some(active)) {
    void refresh()
  }
}

onMounted(() => document.addEventListener('visibilitychange', onVisibilityChange))
onUnmounted(() => {
  dispose()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>

<style scoped>
.benchmark-row {
  grid-template-columns: minmax(0, 1fr) 64px 44px;
  grid-template-areas: 'name name plan' 'model model model' 'status duration duration' 'date date date' 'action action action';
}
.account-name { grid-area: name; overflow-wrap: anywhere; }
.plan-label { grid-area: plan; text-align: right; }
.model-select { grid-area: model; }
.run-status { grid-area: status; }
.run-date { grid-area: date; }
.run-duration { grid-area: duration; }
.row-action { grid-area: action; }
.pelican-benchmark :deep(.select-trigger) { min-height: 44px; border-radius: 8px; }
.pelican-benchmark button:focus-visible { outline: 2px solid var(--color-primary-500, #0ea5e9); outline-offset: 2px; }
@media (min-width: 1024px) {
  .benchmark-row { grid-template-columns: minmax(0, 1.5fr) 64px 64px minmax(0, 1fr) minmax(0, 1fr) 64px 220px; grid-template-areas: none; }
  .account-name, .plan-label, .model-select, .row-action, .run-status, .run-date, .run-duration { grid-area: auto; }
  .plan-label { text-align: left; }
}
</style>
