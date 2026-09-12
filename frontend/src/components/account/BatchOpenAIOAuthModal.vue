<template>
  <BaseDialog :show="show" title="批量添加账号" width="full" :close-on-escape="!hasWork" @close="requestClose">
    <div class="space-y-4" data-testid="batch-oauth">
      <p v-if="error || localError" role="alert" class="text-sm text-red-600 dark:text-red-300">{{ error || localError }}</p>
      <fieldset v-if="!started" :disabled="loading || hasWork" class="space-y-4">
        <label class="block text-sm text-gray-700 dark:text-gray-200">
          邮箱、密码、2FA
          <textarea v-model="input" class="input mt-2 min-h-28 resize-y font-mono" rows="4" autocomplete="off" autocapitalize="off" :spellcheck="false" placeholder="邮箱----密码----2FA，每行一个账号" data-testid="batch-credentials" />
        </label>
        <div class="flex flex-wrap gap-3 text-sm text-gray-600 dark:text-gray-300">
          <span>已识别 {{ credentials.length }} 个账号</span>
          <span v-if="duplicates">已忽略 {{ duplicates }} 个重复邮箱</span>
          <span v-if="invalidLines.length" role="alert" class="text-red-600 dark:text-red-300">格式错误：第 {{ invalidLines.join('、') }} 行</span>
        </div>
        <div v-if="credentials.length" class="max-h-28 overflow-y-auto border-y border-gray-200 py-2 dark:border-dark-700">
          <div v-for="row in credentials" :key="row.account" class="flex gap-3 py-1 text-sm">
            <span class="min-w-0 flex-1 break-all">{{ row.account }}</span>
            <span class="shrink-0 text-emerald-700 dark:text-emerald-300">密码与 2FA 已识别</span>
          </div>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          <div>
            <label class="input-label">号池</label>
            <Select v-model="settings.pool_id" :options="poolOptions" :disabled="loading || hasWork" />
          </div>
          <div>
            <label class="input-label">出口代理</label>
            <ProxySelector :model-value="effectiveProxy" :proxies="proxies" :disabled="settings.pool_id !== null || loading || hasWork" @update:model-value="settings.proxy_id = $event" />
          </div>
          <div>
            <label class="input-label">指纹收敛</label>
            <Select v-model="settings.codex_fingerprint_mode" :options="fingerprintOptions" :disabled="loading || hasWork" />
          </div>
          <label class="block"><span class="input-label">并发数</span><input v-model.number="settings.concurrency" class="input" type="number" min="1" max="1000" step="1" /></label>
          <label class="block"><span class="input-label">优先级</span><input v-model.number="settings.priority" class="input" type="number" min="0" max="1000" step="1" /></label>
        </div>
        <GroupSelector v-model="settings.group_ids" :groups="groups.filter(group => group.platform === 'openai')" platform="openai" :searchable="true" />
      </fieldset>

      <section v-if="rows.length" class="border-t border-gray-200 dark:border-dark-700">
        <header class="flex flex-wrap items-center justify-between gap-3 py-3">
          <div>
            <h3 class="text-sm font-semibold">授权任务</h3>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">共 {{ rows.length }} 个 · 进行中 {{ activeCount }}/3 · 待开始 {{ pendingCount }} · 成功 {{ completedCount }} · 失败 {{ failedCount }}</p>
          </div>
          <div v-if="retryableRows.length" class="flex flex-wrap items-center gap-3">
            <label class="inline-flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
              <input type="checkbox" class="h-4 w-4 accent-primary-600" :checked="allRetryableSelected" @change="toggleAllRetryable">
              全选失败
            </label>
            <button class="btn btn-primary btn-sm" :disabled="selectedRetryCount === 0 || busyKeys.size > 0" @click="ask('retrySelected')">
              <Icon name="refresh" size="sm" />重新授权所选<span v-if="selectedRetryCount">（{{ selectedRetryCount }}）</span>
            </button>
          </div>
          <button v-if="failedRows.length" class="btn btn-secondary btn-sm text-red-600 dark:text-red-300" :disabled="busyKeys.size > 0" @click="ask('clearFailed')">
            <Icon name="trash" size="sm" />清除失败记录
          </button>
        </header>
        <div v-if="batchFinished" class="border-t border-gray-100 py-3 text-sm dark:border-dark-700" role="status">
          <p class="font-medium" :class="failedCount ? 'text-amber-700 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">
            本批次已结束：成功 {{ completedCount }} 个，失败 {{ failedCount }} 个。
          </p>
          <p v-if="failedEmails.length" class="mt-1 break-words text-xs text-red-600 dark:text-red-300">失败账号：{{ failedEmails.join('、') }}</p>
        </div>
        <div class="max-h-[48vh] overflow-y-auto">
          <article v-for="row in rows" :key="row.key" class="grid grid-cols-[1.25rem_minmax(0,1fr)] gap-2 border-t border-gray-100 py-3 dark:border-dark-700 sm:grid-cols-[1.25rem_minmax(0,0.85fr)_minmax(0,1.4fr)_auto]" :data-testid="`oauth-row-${row.email}`">
            <div class="pt-0.5">
              <input v-if="canRetry(row)" type="checkbox" class="h-4 w-4 accent-primary-600" :aria-label="`选择重新授权 ${row.email}`" :checked="selectedRetryKeys.has(row.key)" @change="toggleRetry(row)">
            </div>
            <div class="min-w-0">
              <div class="break-all text-sm font-medium">{{ row.email }}</div>
              <div v-if="row.task?.account_id" class="mt-1 text-xs text-gray-500">账号 #{{ row.task.account_id }}</div>
              <div v-if="row.number" class="mt-1 text-xs tabular-nums text-gray-600 dark:text-gray-300">{{ row.number }}</div>
            </div>
            <div class="col-span-2 min-w-0 text-sm sm:col-span-1" :class="row.task?.status === 'completed' ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-300'">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <span aria-live="polite">{{ statusText(row) }}</span>
                <span class="shrink-0 text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ elapsedText(row) }}</span>
              </div>
              <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600" aria-hidden="true">
                <div class="h-full rounded-full transition-[width] duration-300" :class="progressClass(row)" :style="{ width: `${progressPercent(row)}%` }" />
              </div>
              <div class="mt-1 flex flex-wrap justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
                <span>第 {{ progressStep(row) }}/{{ flowStepCount }} 步 · {{ stageText(row) }}</span>
                <span v-if="row.task?.restart_count">已重新授权 {{ row.task.restart_count }} 次</span>
              </div>
              <p v-if="row.error || row.task?.reason" class="mt-1 break-words text-xs text-red-600 dark:text-red-300" role="alert">{{ row.error || reasonText(row.task) }}</p>
            </div>
            <div class="col-span-2 flex flex-wrap items-start justify-end gap-2 sm:col-span-1">
              <button v-if="row.task?.stage === 'phone_required' && row.task.status === 'running'" class="btn btn-primary btn-sm" :disabled="busyKeys.has(row.key)" @click="ask(row.number ? 'change' : 'acquire', row)">{{ row.number ? '更换号码' : '领取号码' }}</button>
              <button v-if="row.task?.status === 'ready' && row.error" class="btn btn-primary btn-sm" :disabled="busyKeys.has(row.key)" @click="complete(row)">核验并添加</button>
              <button v-if="canRetry(row)" class="btn btn-primary btn-sm" :disabled="busyKeys.has(row.key) || (activeCount >= 3 && row.localStatus !== 'uncertain')" @click="ask('retry', row)"><Icon name="refresh" size="sm" />重新授权</button>
              <button v-if="batchTaskActive(row.task) || row.localStatus === 'pending' || row.localStatus === 'uncertain' || row.retryPending" class="btn btn-secondary btn-sm" :disabled="busyKeys.has(row.key)" @click="ask('stop', row)"><Icon name="x" size="sm" />停止</button>
              <button v-else-if="row.task?.requires_sms_confirmation" class="btn btn-secondary btn-sm" :disabled="busyKeys.has(row.key)" @click="ask('cancel', row)">取消接码</button>
              <button v-if="canDelete(row)" class="btn btn-secondary btn-sm" :disabled="busyKeys.has(row.key)" :title="row.task?.status === 'completed' ? '仅删除任务记录，不删除已添加账号' : '删除任务记录'" @click="ask('delete', row)"><Icon name="trash" size="sm" />删除记录</button>
            </div>
          </article>
        </div>
      </section>
    </div>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button class="btn btn-secondary" :disabled="loading" @click="refresh"><Icon name="refresh" size="sm" />刷新状态</button>
        <button v-if="hasWork" class="btn btn-secondary text-red-600 dark:text-red-300" :disabled="busyKeys.size > 0" @click="ask('stopAll')">停止全部</button>
        <button v-if="!started" class="btn btn-primary" :disabled="!canStart" data-testid="batch-start" @click="begin"><Icon name="play" size="sm" />开始授权</button>
        <button v-else class="btn btn-secondary" @click="requestClose">关闭</button>
      </div>
    </template>
  </BaseDialog>
  <ConfirmDialog :show="!!confirmation" :title="confirmationTitle" :message="confirmationMessage" :danger="confirmation?.action === 'stop' || confirmation?.action === 'stopAll' || confirmation?.action === 'close'" @cancel="dismiss" @confirm="confirmAction" />
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import Icon from '@/components/icons/Icon.vue'
import { apiClient } from '@/api/client'
import type { AdminGroup, Proxy } from '@/types'
import type { BatchOAuthConfig, BatchOAuthTask } from '@/api/admin/openaiBatchOAuth'
import { parseAccountCredentials } from '@/features/token-converter/accountCredentials'
import { normalizeBase32Secret } from '@/features/token-converter/totp'
import { useBatchOpenAIOAuth, batchTaskActive, batchTaskWillAutoRestart, type OAuthQueueRow } from '@/composables/useBatchOpenAIOAuth'

defineProps<{ show: boolean; groups: AdminGroup[]; proxies: Proxy[] }>()
const emit = defineEmits<{ close: []; created: [] }>()
const { rows, error, loading, started, busyKeys, activeCount, pendingCount, hasWork, start, sms, cancel, cancelAll, retry, complete, remove, hasSecret, refresh } = useBatchOpenAIOAuth(() => emit('created'))
const input = ref('')
const localError = ref('')
const pools = ref<{ id: number; name: string; proxy_id: number | null }[]>([])
const poolsReady = ref(false)
const settings = reactive<BatchOAuthConfig>({ group_ids: [], proxy_id: null, pool_id: null, concurrency: 3, priority: 1, codex_fingerprint_mode: 'off' })
const parsed = computed(() => parseAccountCredentials(input.value))
const credentials = computed(() => {
  const seen = new Set<string>()
  return parsed.value.rows.filter(row => {
    const email = row.account.toLowerCase()
    if (seen.has(email)) return false
    seen.add(email)
    return true
  })
})
const duplicates = computed(() => parsed.value.rows.length - credentials.value.length)
const invalidLines = computed(() => {
  const invalid = parsed.value.invalidRows.map(row => row.lineNumber)
  for (const row of credentials.value) {
    try {
      const secret = normalizeBase32Secret(row.twoFactor)
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(row.account) || !/^[A-Z2-7]{16,256}$/.test(secret)) invalid.push(row.lineNumber)
    } catch { invalid.push(row.lineNumber) }
  }
  return [...new Set(invalid)].sort((a, b) => a - b)
})
const canStart = computed(() => !loading.value && poolsReady.value && !error.value && !hasWork.value && credentials.value.length > 0 && !invalidLines.value.length
  && Number.isInteger(settings.concurrency) && settings.concurrency >= 1 && settings.concurrency <= 1000
  && Number.isInteger(settings.priority) && settings.priority >= 0 && settings.priority <= 1000)
const poolOptions = computed(() => [{ value: null, label: '不加入号池' }, ...pools.value.map(pool => ({ value: pool.id, label: pool.name }))])
const effectiveProxy = computed(() => settings.pool_id === null ? settings.proxy_id : pools.value.find(p => p.id === settings.pool_id)?.proxy_id ?? null)
const fingerprintOptions = [{ value: 'off', label: '关闭' }, { value: 'device', label: '设备' }, { value: 'session', label: '会话' }, { value: 'full', label: '完整' }]
const completedCount = computed(() => rows.value.filter(row => row.task?.status === 'completed').length)
const willAutoRestart = (row: OAuthQueueRow) => hasSecret(row) && batchTaskWillAutoRestart(row.task)
const failedRows = computed(() => rows.value.filter(row => row.task && ['failed', 'blocked'].includes(row.task.status) && !willAutoRestart(row) && !row.retryPending))
const failedCount = computed(() => failedRows.value.length)
const failedEmails = computed(() => failedRows.value.map(row => row.email))
const batchFinished = computed(() => rows.value.length > 0 && !hasWork.value)
const selectedRetryKeys = ref(new Set<string>())
const retryableRows = computed(() => rows.value.filter(canRetry))
const selectedRetryRows = computed(() => retryableRows.value.filter(row => selectedRetryKeys.value.has(row.key)))
const selectedRetryCount = computed(() => selectedRetryRows.value.length)
const allRetryableSelected = computed(() => retryableRows.value.length > 0 && selectedRetryCount.value === retryableRows.value.length)
const flowStages = ['opening', 'email', 'password', 'totp', 'phone', 'sms', 'workspace', 'callback', 'verify'] as const
const flowStepCount = flowStages.length
const now = ref(Date.now())
let elapsedTimer: ReturnType<typeof setInterval> | undefined
type Action = 'acquire' | 'change' | 'cancel' | 'stop' | 'stopAll' | 'close' | 'retry' | 'retrySelected' | 'delete' | 'clearFailed'
const confirmation = ref<{ action: Action; row?: OAuthQueueRow }>()
const confirmationTitle = computed(() => ({ acquire: '确认领取号码', change: '确认更换号码', cancel: '确认取消接码', stop: '确认停止授权', stopAll: '确认停止全部', close: '停止任务并关闭', retry: '重新授权', retrySelected: '批量重新授权', delete: '删除任务记录', clearFailed: '清除失败记录' })[confirmation.value?.action || 'stop'])
const confirmationMessage = computed(() => {
  const email = confirmation.value?.row?.email || ''
  switch (confirmation.value?.action) {
    case 'acquire': return `为 ${email} 领取独立接码号码？`
    case 'change': return `取消 ${email} 当前号码并领取新号码？`
    case 'retry': return `重新处理 ${email}。如有未结束的接码，将先取消；授权会在新的独立窗口中开始。`
    case 'retrySelected': return `重新授权已选择的 ${selectedRetryCount.value} 个失败账号。每个账号继续使用独立隐私窗口，最多同时运行 3 个。`
    case 'delete': return confirmation.value.row?.task?.status === 'completed' ? `仅删除 ${email} 的授权任务记录，已经添加成功的账号不会被删除。` : `删除 ${email} 的失败任务记录？`
    case 'clearFailed': return `删除当前显示的 ${failedRows.value.length} 条失败任务记录？已经添加成功的账号不受影响。`
    case 'cancel': return `取消 ${email} 当前接码？`
    case 'close': case 'stopAll': return '停止所有未完成的授权并取消对应接码。已添加成功的账号不受影响。'
    default: return `停止 ${email} 的授权并取消对应接码？`
  }
})
function ask(action: Action, row?: OAuthQueueRow) { confirmation.value = { action, row } }
function dismiss() { confirmation.value = undefined }
function requestClose() { if (hasWork.value) ask('close'); else { input.value = ''; emit('close') } }
function begin() {
  if (!canStart.value) return
  start(credentials.value, { ...settings, proxy_id: effectiveProxy.value })
  if (started.value) input.value = ''
}
async function confirmAction() {
  const current = confirmation.value
  if (!current) return
  dismiss()
  if (current.action === 'stopAll' || current.action === 'close') {
    await cancelAll()
    if (current.action === 'close' && !hasWork.value) emit('close')
  } else if (current.action === 'retrySelected') {
    for (const row of selectedRetryRows.value) await retry(row)
    selectedRetryKeys.value = new Set()
  } else if (current.action === 'clearFailed') {
    for (const row of [...failedRows.value]) await remove(row)
  } else if (current.action === 'delete' && current.row) {
    await remove(current.row)
  } else if (current.row) {
    if (current.action === 'stop') await cancel(current.row)
    else if (current.action === 'retry') await retry(current.row)
    else if (current.action === 'acquire' || current.action === 'change' || current.action === 'cancel') {
      await sms(current.row, current.action)
    }
  }
}
function canRetry(row: OAuthQueueRow) { return hasSecret(row) && !row.retryPending && (row.localStatus === 'uncertain' || (!!row.task && ['failed', 'blocked', 'canceled'].includes(row.task.status) && !willAutoRestart(row) && !row.task.account_id && row.task.restart_count < 2 && row.task.reason !== 'account_creation_requires_review')) }
function canDelete(row: OAuthQueueRow) { return !!row.task && ['completed', 'failed', 'blocked', 'canceled'].includes(row.task.status) && !row.retryPending }
function toggleRetry(row: OAuthQueueRow) {
  const next = new Set(selectedRetryKeys.value)
  if (next.has(row.key)) next.delete(row.key)
  else next.add(row.key)
  selectedRetryKeys.value = next
}
function toggleAllRetryable() {
  selectedRetryKeys.value = allRetryableSelected.value ? new Set() : new Set(retryableRows.value.map(row => row.key))
}
function statusText(row: OAuthQueueRow) {
  if (row.retryPending) return '等待重新授权'
  if (row.localStatus) return { pending: '待开始', starting: '正在启动', uncertain: '启动结果待核验', canceled: '已停止' }[row.localStatus]
  const task = row.task
  if (!task) return ''
  if (willAutoRestart(row)) {
    const seconds = Math.max(0, Math.ceil(((row.automaticAfter || now.value) - now.value) / 1000))
    return seconds > 0 ? `将在 ${seconds} 秒后自动重新授权` : '正在重新启动授权'
  }
  if (task.status !== 'running') return { queued: '正在启动', ready: '正在核验并添加', completed: '已添加成功', failed: '授权失败', blocked: '授权失败', canceled: '已停止' }[task.status] || task.status
  return ({ opening: '正在打开隐私授权窗口', login: '正在进入登录页面', email: '正在填写邮箱', password: '正在填写密码', totp: '正在验证 2FA', phone_required: '正在准备手机号', phone_submitting: '正在提交手机号', sms_waiting: '正在等待短信验证码', sms_submitting: '正在提交短信验证码', workspace: '正在确认工作空间', callback_waiting: '正在等待 OAuth 回调', callback_received: '已收到 OAuth 回调' } as Record<string, string>)[task.stage] || '正在授权'
}
function reasonText(task?: BatchOAuthTask) {
  const reason = task?.reason
  return ({ sms_timeout: '短信等待超过 3 分钟，已取消旧号码并准备从 OAuth 起点重新授权。', sms_confirmation_timeout: '领号阶段超时，已清理当前隐私会话。', oauth_identity_mismatch: 'OAuth 返回账号与输入邮箱不一致，未添加。', pool_assignment_failed: '账号已添加，但加入号池失败，请在号池管理中重新分配。', account_creation_requires_review: '账号创建结果不确定，已禁止重复创建，请核对账号列表。', account_readback_failed: '账号已创建，但读取核验失败。', account_configuration_mismatch: '账号已创建，但分组、代理、并发、优先级或指纹配置不一致。', account_login_credentials_mismatch: '账号已创建，但邮箱、密码或 2FA 的加密保存核验失败。', automation_start_failed: '授权浏览器没有确认启动。', invalid_configuration: '分组、号池或代理配置不可用。', oauth_exchange_failed: 'OAuth 回调已收到，但换取 Token 失败。', manual_challenge: 'OpenAI 页面结构无法识别，当前隐私会话已关闭。', email_code_required: 'OpenAI 要求邮件验证码，当前自动流程无法安全读取该验证码。', captcha_required: 'OpenAI 出现人机验证，当前隐私会话已关闭。', account_blocked: 'OpenAI 限制了当前账号。', authenticator_required: 'OpenAI 要求验证器验证码，但没有可用的 2FA 密钥。', invalid_credentials: '邮箱、密码或登录后的账号身份未通过验证。', invalid_totp: 'OpenAI 拒绝了当前 2FA 验证码，请检查密钥与服务器时间。', invalid_sms_code: 'OpenAI 拒绝了短信验证码。', proxy_unavailable: '所选出口代理无法从授权浏览器连接。', navigation_timeout: '打开 OpenAI OAuth 页面超时。', browser_context_lost: '独立隐私浏览器上下文意外关闭。', page_interaction_failed: 'OpenAI 页面控件操作失败或页面结构发生变化。', phone_rejected: 'OpenAI 拒绝了当前号码，系统将自动更换号码。', task_expired: '本次授权总时长已超时。' } as Record<string, string>)[reason || ''] || (reason ? `授权未完成（${reason}）` : '')
}
function normalizedStage(row: OAuthQueueRow) {
  if (row.localStatus) return 'opening'
  if (row.task?.status === 'ready') return 'verify'
  if (row.task?.status === 'completed') return 'verify'
  const stage = row.task?.stage || 'opening'
  if (['login', 'email'].includes(stage)) return 'email'
  if (['phone_required', 'phone_submitting'].includes(stage)) return 'phone'
  if (['sms_waiting', 'sms_submitting'].includes(stage)) return 'sms'
  if (['callback_waiting', 'callback_received'].includes(stage)) return 'callback'
  return flowStages.includes(stage as typeof flowStages[number]) ? stage as typeof flowStages[number] : 'opening'
}
function progressStep(row: OAuthQueueRow) { return Math.max(1, flowStages.indexOf(normalizedStage(row)) + 1) }
function progressPercent(row: OAuthQueueRow) { return row.task?.status === 'completed' ? 100 : Math.round((progressStep(row) / flowStepCount) * 100) }
function progressClass(row: OAuthQueueRow) {
  if (row.task?.status === 'completed') return 'bg-emerald-500'
  if (row.retryPending) return 'bg-primary-500'
  if (row.task && ['failed', 'blocked'].includes(row.task.status) && !willAutoRestart(row)) return 'bg-red-500'
  return 'bg-primary-500'
}
function stageText(row: OAuthQueueRow) {
  return ({ opening: '打开授权窗口', email: '登录邮箱', password: '登录密码', totp: '验证 2FA', phone: '提交手机号', sms: '等待并提交短信', workspace: '确认工作空间', callback: '等待回调链接', verify: '核验并保存账号' } as Record<string, string>)[normalizedStage(row)]
}
function elapsedText(row: OAuthQueueRow) {
  const startedAt = Date.parse(row.task?.created_at || '')
  if (!Number.isFinite(startedAt)) return '0 秒'
  const finishedAt = Date.parse(row.task?.finished_at || '')
  const end = Number.isFinite(finishedAt) ? finishedAt : now.value
  return `${Math.max(0, Math.floor((end - startedAt) / 1000))} 秒`
}
function beforeUnload(event: BeforeUnloadEvent) { if (hasWork.value) { event.preventDefault(); event.returnValue = '' } }
onMounted(async () => {
  window.addEventListener('beforeunload', beforeUnload)
  elapsedTimer = setInterval(() => { now.value = Date.now() }, 1000)
  try { pools.value = (await apiClient.get<{ items: typeof pools.value }>('/admin/account-pools')).data.items; poolsReady.value = true } catch { localError.value = '号池列表加载失败，请关闭后重新打开。' }
})
onUnmounted(() => { input.value = ''; if (elapsedTimer) clearInterval(elapsedTimer); window.removeEventListener('beforeunload', beforeUnload) })
</script>
