<template>
  <div class="space-y-4" data-testid="openai-reauthorization-view">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 items-start gap-3">
        <button type="button" class="btn btn-secondary flex h-9 w-9 shrink-0 items-center justify-center p-0" title="返回账号管理" aria-label="返回账号管理" @click="backToAccounts">
          <Icon name="arrowLeft" size="sm" :stroke-width="2" />
        </button>
        <div class="min-w-0">
          <h1 class="text-xl font-semibold text-gray-900 dark:text-gray-100">OpenAI OAuth 401 重新授权</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">每个账号使用独立隐私上下文；成功或失败后都会关闭当前授权页面。</p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="btn btn-secondary flex items-center gap-2" :disabled="loading || refreshing" @click="refreshAll">
          <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" :stroke-width="2" />
          <span>刷新状态</span>
        </button>
        <button type="button" class="btn btn-primary flex items-center gap-2" data-testid="reauthorize-all" :disabled="!startableAccounts.length || startingAll" @click="startAll">
          <Icon name="play" size="sm" :stroke-width="2" />
          <span>{{ startingAll ? '正在启动' : `一键授权${startableAccounts.length ? ` (${startableAccounts.length})` : ''}` }}</span>
        </button>
      </div>
    </header>

    <section class="overflow-hidden border-y border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
      <div class="grid gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700 sm:grid-cols-5">
        <div v-for="item in summary" :key="item.label" class="min-w-0">
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ item.label }}</p>
          <p class="mt-0.5 text-lg font-semibold" :class="item.className">{{ item.value }}</p>
        </div>
      </div>

      <div v-if="loadError" role="alert" class="border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/25 dark:text-red-300">
        {{ loadError }}
      </div>

      <div v-if="loading" class="flex min-h-40 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="refresh" size="sm" class="animate-spin" />
        正在读取待授权账号
      </div>

      <div v-else-if="!orderedAccounts.length" class="flex min-h-40 flex-col items-center justify-center px-4 text-center">
        <Icon name="checkCircle" size="lg" class="text-green-500" :stroke-width="2" />
        <p class="mt-2 text-sm font-medium text-gray-800 dark:text-gray-200">当前没有待处理的 OpenAI OAuth 401 账号</p>
      </div>

      <div v-else class="divide-y divide-gray-200 dark:divide-dark-700">
        <article v-for="account in orderedAccounts" :key="account.id" class="grid min-w-0 gap-3 px-4 py-4 xl:grid-cols-[minmax(230px,0.8fr)_minmax(360px,1.6fr)_auto] xl:items-center" :data-testid="`reauthorization-account-${account.id}`">
          <div class="min-w-0">
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <p class="min-w-0 truncate text-sm font-semibold text-gray-900 dark:text-gray-100">#{{ account.id }} {{ account.name }}</p>
              <span v-if="account.execution_node_id" class="rounded border border-gray-200 px-1.5 py-0.5 text-[11px] text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ account.execution_node_id }}</span>
            </div>
            <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">{{ accountEmail(account) }}</p>
          </div>

          <div class="min-w-0">
            <div class="flex min-w-0 items-center justify-between gap-3">
              <p class="truncate text-sm font-medium" :class="statusClass(account)">{{ statusLabel(account) }}</p>
              <span class="shrink-0 text-xs text-gray-500 dark:text-gray-400">{{ elapsedText(account) }}</span>
            </div>
            <div class="mt-2 h-1.5 overflow-hidden rounded bg-gray-200 dark:bg-dark-700">
              <div class="h-full transition-[width] duration-300" :class="progressClass(account)" :style="{ width: `${progressPercent(account)}%` }" />
            </div>
            <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ stepLabel(account) }}</p>
            <p v-if="failureText(account)" class="mt-1 break-words text-sm text-red-600 dark:text-red-400" role="alert">{{ failureText(account) }}</p>
          </div>

          <div class="flex flex-wrap items-center justify-end gap-2">
            <button v-if="isActive(account)" type="button" class="btn btn-secondary btn-sm flex items-center gap-1.5" :disabled="busyAccountIDs.has(account.id)" @click="stopAccount(account)">
              <Icon name="x" size="sm" :stroke-width="2" />
              <span>停止</span>
            </button>
            <button v-else-if="canStart(account)" type="button" class="btn btn-primary btn-sm flex items-center gap-1.5" :disabled="busyAccountIDs.has(account.id) || activeCount >= maxConcurrency" :data-testid="`start-reauthorization-${account.id}`" @click="startAccount(account)">
              <Icon :name="taskFor(account)?.status === 'failed' || taskFor(account)?.status === 'blocked' || taskFor(account)?.status === 'canceled' ? 'refresh' : 'play'" size="sm" :class="busyAccountIDs.has(account.id) ? 'animate-spin' : ''" :stroke-width="2" />
              <span>{{ retryLabel(account) }}</span>
            </button>
            <span v-else-if="taskFor(account)?.reason === 'account_blocked'" class="text-xs font-medium text-red-600 dark:text-red-400">账号受限，不再重试</span>
            <span v-else-if="taskFor(account)?.status === 'completed'" class="flex items-center gap-1.5 text-sm font-medium text-green-600 dark:text-green-400">
              <Icon name="check" size="sm" :stroke-width="2.5" />授权成功
            </span>
          </div>
        </article>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Icon } from '@/components/icons'
import { accountsAPI } from '@/api/admin'
import { openAIReauthorizationAPI, type OpenAIReauthorizationTask } from '@/api/admin/openaiReauthorization'
import type { Account } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const route = useRoute()
const router = useRouter()
const accounts = ref<Account[]>([])
const tasks = ref<OpenAIReauthorizationTask[]>([])
const loading = ref(true)
const refreshing = ref(false)
const startingAll = ref(false)
const loadError = ref('')
const maxConcurrency = ref(3)
const maxRestarts = ref(2)
const now = ref(Date.now())
const queuedAccountIDs = ref<number[]>([])
const busyAccountIDs = ref(new Set<number>())
const localErrors = ref(new Map<number, string>())
const completingTaskIDs = new Set<string>()
const smsTaskIDs = new Set<string>()
let pollTimer: ReturnType<typeof setTimeout> | null = null
let disposed = false

const activeStatuses = new Set(['queued', 'running', 'ready'])
const terminalFailureStatuses = new Set(['failed', 'blocked', 'canceled'])
const restrictedReasons = new Set(['account_blocked'])

const taskByAccountID = computed(() => {
  const result = new Map<number, OpenAIReauthorizationTask>()
  for (const task of tasks.value) {
    if (!task.target_account_id) continue
    const current = result.get(task.target_account_id)
    if (!current || Date.parse(task.created_at) >= Date.parse(current.created_at)) result.set(task.target_account_id, task)
  }
  return result
})

const activeCount = computed(() => tasks.value.filter(task => activeStatuses.has(task.status)).length + busyAccountIDs.value.size)
const completedCount = computed(() => accounts.value.filter(account => taskFor(account)?.status === 'completed').length)
const failedCount = computed(() => accounts.value.filter(account => terminalFailureStatuses.has(taskFor(account)?.status || '') || localErrors.value.has(account.id)).length)
const pendingCount = computed(() => Math.max(0, accounts.value.length - completedCount.value - failedCount.value - tasks.value.filter(task => activeStatuses.has(task.status)).length))
const startableAccounts = computed(() => accounts.value.filter(canStart))
const summary = computed(() => [
  { label: '待授权账号', value: accounts.value.length, className: 'text-gray-900 dark:text-gray-100' },
  { label: '进行中', value: tasks.value.filter(task => activeStatuses.has(task.status)).length, className: 'text-primary-600 dark:text-primary-400' },
  { label: '等待开始', value: pendingCount.value, className: 'text-gray-700 dark:text-gray-300' },
  { label: '成功', value: completedCount.value, className: 'text-green-600 dark:text-green-400' },
  { label: '失败', value: failedCount.value, className: 'text-red-600 dark:text-red-400' }
])

const orderedAccounts = computed(() => [...accounts.value].sort((a, b) => {
  const rank = (account: Account) => {
    const task = taskFor(account)
    if (!task || activeStatuses.has(task.status)) return 0
    if (task.status === 'completed') return 1
    return 2
  }
  const difference = rank(a) - rank(b)
  return difference || a.id - b.id
}))

function queryAccountIDs(): number[] {
  const raw = Array.isArray(route.query.account_ids) ? route.query.account_ids[0] : route.query.account_ids
  if (typeof raw !== 'string') return []
  return [...new Set(raw.split(',').map(value => Number(value.trim())).filter(Number.isSafeInteger).filter(value => value > 0))]
}

function accountNeedsReauthorization(account: Account, requested: Set<number>): boolean {
  if (account.platform !== 'openai' || account.type !== 'oauth' || account.parent_account_id != null) return false
  if (requested.has(account.id)) return true
  const extra = account.extra as Record<string, unknown> | undefined
  const text = [account.error_message || '', extra?.error, extra?.error_code].filter(value => typeof value === 'string').join(' ')
  return account.status === 'error' && (extra?.needs_reauth === true || extra?.error_code === 'unauthenticated' || /\b401\b|unauthori[sz]ed|token\s*(?:expired|invalid|失效|过期)/i.test(text))
}

async function loadAccounts() {
  const requestedIDs = queryAccountIDs()
  const requested = new Set(requestedIDs)
  const found = new Map<number, Account>()
  const requestedResults = await Promise.allSettled(requestedIDs.map(id => accountsAPI.getById(id)))
  for (const result of requestedResults) {
    if (result.status === 'fulfilled') found.set(result.value.id, result.value)
  }
  let page = 1
  while (true) {
    const result = await accountsAPI.list(page, 200, { platform: 'openai', type: 'oauth', status: 'error', sort_by: 'id', sort_order: 'asc' })
    for (const account of result.items) found.set(account.id, account)
    if (page >= result.pages || result.items.length === 0) break
    page++
  }
  accounts.value = [...found.values()].filter(account => accountNeedsReauthorization(account, requested)).sort((a, b) => a.id - b.id)
}

function taskFor(account: Account): OpenAIReauthorizationTask | undefined {
  return taskByAccountID.value.get(account.id)
}

function accountEmail(account: Account): string {
  const email = typeof account.credentials?.email === 'string' ? account.credentials.email.trim() : ''
  return email || account.name
}

function canStart(account: Account): boolean {
  if (busyAccountIDs.value.has(account.id)) return false
  const task = taskFor(account)
  if (!task) return true
  if (task.account_id) return task.reason === 'account_state_recovery_failed'
  if (task.status === 'completed' || activeStatuses.has(task.status) || restrictedReasons.has(task.reason || '')) return false
  return task.restart_count < maxRestarts.value
}

function isActive(account: Account): boolean {
  const task = taskFor(account)
  return Boolean(task && activeStatuses.has(task.status))
}

function retryLabel(account: Account): string {
  const task = taskFor(account)
  if (busyAccountIDs.value.has(account.id)) return '正在启动'
  if (task?.reason === 'account_state_recovery_failed') return '重试恢复状态'
  return task && terminalFailureStatuses.has(task.status) ? '重新授权' : '开始授权'
}

const stageDetails: Record<string, { step: number; label: string }> = {
  queued: { step: 1, label: '等待独立隐私上下文' },
  opening: { step: 1, label: '打开 OpenAI OAuth 授权页' },
  login: { step: 1, label: '进入 OpenAI 登录' },
  email: { step: 2, label: '填写登录邮箱' },
  password: { step: 3, label: '填写保存的登录密码' },
  totp: { step: 4, label: '填写实时 2FA 验证码' },
  phone_required: { step: 4, label: 'OpenAI 要求额外手机号验证' },
  phone_submitting: { step: 4, label: '提交手机号' },
  sms_waiting: { step: 4, label: '等待短信验证码' },
  sms_submitting: { step: 4, label: '提交短信验证码' },
  workspace: { step: 5, label: '确认个人工作空间并点击继续' },
  callback_waiting: { step: 6, label: '等待 localhost OAuth 回调' },
  callback_received: { step: 6, label: '已取得 OAuth 回调' },
  completed: { step: 7, label: '新 OAuth 凭据已保存到原账号' },
  failed: { step: 7, label: '授权任务失败' },
  blocked: { step: 7, label: 'OpenAI 阻止了当前授权' },
  canceled: { step: 7, label: '任务已停止' }
}

const reasonLabels: Record<string, string> = {
  invalid_credentials: '邮箱、密码或登录后的账号身份未通过验证。',
  invalid_totp: '2FA 验证码未通过验证。',
  authenticator_required: 'OpenAI 要求 2FA，但该账号没有可用的已保存密钥。',
  account_blocked: 'OpenAI 限制了当前账号，系统不会自动重试。',
  captcha_required: 'OpenAI 要求完成人机验证。',
  email_code_required: 'OpenAI 要求邮箱验证码，当前自动流程未继续。',
  proxy_unavailable: '账号代理无法用于浏览器授权。',
  navigation_timeout: '打开 OpenAI 授权页超时。',
  browser_context_lost: '独立隐私浏览器上下文意外关闭。',
  page_interaction_failed: 'OpenAI 页面控件发生变化或操作失败。',
  oauth_exchange_failed: 'OAuth 回调换取凭据失败。',
  oauth_identity_mismatch: '授权完成后的 OpenAI 身份与目标账号不一致。',
  account_update_failed: 'OAuth 已完成，但凭据未能保存到原账号。',
  account_configuration_changed: '授权期间账号的所属服务器或代理发生变化，请按当前配置重新授权。',
  account_state_recovery_failed: '新 OAuth 凭据已保存，但账号错误状态尚未清理，请重试恢复状态。',
  sms_timeout: '等待短信验证码超过 3 分钟。',
  sms_confirmation_timeout: '等待手机号处理超时。',
  phone_rejected: 'OpenAI 拒绝了当前手机号。',
  task_expired: '授权任务已过期。',
  manual_challenge: 'OpenAI 页面出现了当前自动化未识别的验证步骤。'
}

function statusLabel(account: Account): string {
  if (localErrors.value.has(account.id)) return '启动失败'
  const task = taskFor(account)
  if (!task) return '等待授权'
  if (task.status === 'completed') return '授权成功'
  if (task.status === 'failed' || task.status === 'blocked') return '授权失败'
  if (task.status === 'canceled') return '已停止'
  return stageDetails[task.stage]?.label || '正在授权'
}

function stepLabel(account: Account): string {
  const task = taskFor(account)
  if (!task) return '尚未启动'
  const detail = stageDetails[task.stage] || stageDetails[task.status]
  return detail ? `第 ${Math.min(detail.step, 7)}/7 步 · ${detail.label}` : '正在读取实际授权状态'
}

function failureText(account: Account): string {
  const local = localErrors.value.get(account.id)
  if (local) return local
  const task = taskFor(account)
  if (!task || !terminalFailureStatuses.has(task.status)) return ''
  return reasonLabels[task.reason || ''] || 'OpenAI OAuth 重新授权未完成。'
}

function progressPercent(account: Account): number {
  const task = taskFor(account)
  if (!task) return 0
  if (task.status === 'completed') return 100
  const detail = stageDetails[task.stage] || stageDetails[task.status]
  return detail ? Math.max(8, Math.min(100, Math.round(detail.step / 7 * 100))) : 8
}

function progressClass(account: Account): string {
  const task = taskFor(account)
  if (localErrors.value.has(account.id) || (task && terminalFailureStatuses.has(task.status))) return 'bg-red-500'
  if (task?.status === 'completed') return 'bg-green-500'
  return 'bg-primary-500'
}

function statusClass(account: Account): string {
  const task = taskFor(account)
  if (localErrors.value.has(account.id) || (task && terminalFailureStatuses.has(task.status))) return 'text-red-600 dark:text-red-400'
  if (task?.status === 'completed') return 'text-green-600 dark:text-green-400'
  if (task && activeStatuses.has(task.status)) return 'text-primary-600 dark:text-primary-400'
  return 'text-gray-800 dark:text-gray-200'
}

function elapsedText(account: Account): string {
  const task = taskFor(account)
  if (!task?.created_at) return '0 秒'
  const end = task.finished_at ? Date.parse(task.finished_at) : now.value
  const start = Date.parse(task.created_at)
  return `${Math.max(0, Math.round((end - start) / 1000))} 秒`
}

function setBusy(accountID: number, busy: boolean) {
  const next = new Set(busyAccountIDs.value)
  if (busy) next.add(accountID)
  else next.delete(accountID)
  busyAccountIDs.value = next
}

async function startAccount(account: Account) {
  if (!canStart(account)) return
  setBusy(account.id, true)
  const errors = new Map(localErrors.value)
  errors.delete(account.id)
  localErrors.value = errors
  try {
    const current = taskFor(account)
    const task = current?.reason === 'account_state_recovery_failed' && current.account_id
      ? await openAIReauthorizationAPI.complete(current.task_id)
      : current && terminalFailureStatuses.has(current.status)
        ? await openAIReauthorizationAPI.restart(current.task_id)
        : await openAIReauthorizationAPI.start(account.id)
    mergeTask(task)
  } catch (error) {
    const next = new Map(localErrors.value)
    next.set(account.id, extractApiErrorMessage(error, '无法启动该账号的 401 重新授权。'))
    localErrors.value = next
  } finally {
    setBusy(account.id, false)
    await syncTasks(false)
    void pumpQueue()
  }
}

async function stopAccount(account: Account) {
  const task = taskFor(account)
  if (!task || !isActive(account)) return
  setBusy(account.id, true)
  try {
    mergeTask(await openAIReauthorizationAPI.cancel(task.task_id))
  } catch (error) {
    const next = new Map(localErrors.value)
    next.set(account.id, extractApiErrorMessage(error, '停止授权失败。'))
    localErrors.value = next
  } finally {
    setBusy(account.id, false)
    await syncTasks(false)
  }
}

function startAll() {
  queuedAccountIDs.value = startableAccounts.value.map(account => account.id)
  startingAll.value = true
  void pumpQueue()
}

async function pumpQueue() {
  while (!disposed && queuedAccountIDs.value.length && activeCount.value < maxConcurrency.value) {
    const accountID = queuedAccountIDs.value.shift()!
    const account = accounts.value.find(candidate => candidate.id === accountID)
    if (!account || !canStart(account)) continue
    void startAccount(account)
  }
  if (!queuedAccountIDs.value.length) startingAll.value = false
}

function mergeTask(task: OpenAIReauthorizationTask) {
  const next = tasks.value.filter(candidate => candidate.task_id !== task.task_id && candidate.target_account_id !== task.target_account_id)
  next.push(task)
  tasks.value = next
}

async function processTask(task: OpenAIReauthorizationTask) {
  if (task.status === 'ready' && !completingTaskIDs.has(task.task_id)) {
    completingTaskIDs.add(task.task_id)
    try { mergeTask(await openAIReauthorizationAPI.complete(task.task_id)) } finally { completingTaskIDs.delete(task.task_id) }
    return
  }
  if (task.status !== 'running' || smsTaskIDs.has(task.task_id)) return
  if (task.stage !== 'phone_required' && task.stage !== 'sms_waiting') return
  smsTaskIDs.add(task.task_id)
  try {
    const action = task.stage === 'sms_waiting' ? 'check' : task.reason === 'phone_rejected' ? 'change' : 'acquire'
    const result = await openAIReauthorizationAPI.sms(task.task_id, action)
    mergeTask(result.task as OpenAIReauthorizationTask)
  } catch {
    // The next poll retries the current task only; other accounts keep running.
  } finally {
    smsTaskIDs.delete(task.task_id)
  }
}

async function syncTasks(showError = true) {
  try {
    const result = await openAIReauthorizationAPI.list()
    maxConcurrency.value = Math.max(1, Math.min(3, result.max_concurrency || 3))
    maxRestarts.value = Math.max(0, result.max_restarts || 0)
    tasks.value = result.items
    await Promise.all(result.items.map(processTask))
    void pumpQueue()
  } catch (error) {
    if (showError) loadError.value = extractApiErrorMessage(error, '暂时无法读取 401 授权任务状态。')
  }
}

async function refreshAll() {
  refreshing.value = true
  loadError.value = ''
  try {
    await Promise.all([loadAccounts(), syncTasks()])
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, '读取待授权账号失败。')
  } finally {
    refreshing.value = false
  }
}

function schedulePoll() {
  if (disposed) return
  pollTimer = setTimeout(async () => {
    now.value = Date.now()
    await syncTasks(false)
    schedulePoll()
  }, 1000)
}

function backToAccounts() {
  void router.push({ name: 'AdminAccounts' })
}

onMounted(async () => {
  window.scrollTo({ top: 0, behavior: 'auto' })
  try {
    await Promise.all([loadAccounts(), syncTasks()])
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, '读取待授权账号失败。')
  } finally {
    loading.value = false
    schedulePoll()
  }
})

onBeforeUnmount(() => {
  disposed = true
  if (pollTimer) clearTimeout(pollTimer)
})
</script>
