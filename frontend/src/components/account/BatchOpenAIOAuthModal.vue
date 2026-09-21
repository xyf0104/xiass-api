<template>
  <component
    :is="embedded ? 'section' : BaseDialog"
    v-if="embedded || show"
    :show="embedded ? undefined : show"
    :title="embedded ? undefined : '批量添加账号'"
    :width="embedded ? undefined : 'full'"
    :close-on-escape="embedded ? undefined : !hasWork"
    :class="embedded ? 'min-w-0' : undefined"
    @close="requestClose"
  >
    <div class="space-y-4" data-testid="batch-oauth">
      <p v-if="error || localError" role="alert" class="text-sm text-red-600 dark:text-red-300">{{ error || localError }}</p>
      <div v-if="adsPowerHelperMissing" class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/25 dark:text-red-200" role="alert" data-testid="adspower-helper-missing">
        <p>本次 Ads 授权已停止。请安装并启动 XIASS AdsPower 助手和 AdsPower，再到工作台顶部的“Ads 设置”完成 API Key 与代理出口配置。</p>
        <div class="mt-3 flex flex-wrap gap-2">
          <a class="btn btn-secondary btn-sm" :href="adsPowerHelperMacDownloadURL"><Icon name="download" size="sm" />macOS 安装包</a>
          <a class="btn btn-secondary btn-sm" :href="adsPowerHelperWindowsDownloadURL"><Icon name="download" size="sm" />Windows 安装包</a>
        </div>
      </div>
      <fieldset v-if="!started" :disabled="loading || hasWork" class="space-y-4">
        <div class="inline-flex max-w-full overflow-x-auto rounded-md border border-gray-200 p-1 dark:border-dark-600" role="tablist" aria-label="批量登录方式">
          <button type="button" role="tab" class="batch-mode-tab" :class="loginMode === 'password' ? 'batch-mode-tab-active' : 'batch-mode-tab-idle'" :aria-selected="loginMode === 'password'" data-testid="batch-mode-password" @click="loginMode = 'password'">
            <Icon name="key" size="sm" />密码 + 2FA
          </button>
          <button type="button" role="tab" class="batch-mode-tab" :class="loginMode === 'email_code' ? 'batch-mode-tab-active' : 'batch-mode-tab-idle'" :aria-selected="loginMode === 'email_code'" data-testid="batch-mode-email-code" @click="loginMode = 'email_code'">
            <Icon name="mail" size="sm" />邮箱验证码
          </button>
        </div>

        <label v-if="loginMode === 'password'" class="block text-sm text-gray-700 dark:text-gray-200">
          邮箱、密码、2FA
          <textarea v-model="passwordInput" class="input mt-2 min-h-28 resize-y font-mono" rows="4" autocomplete="off" autocapitalize="off" :spellcheck="false" placeholder="邮箱----密码----2FA，每行一个账号" data-testid="batch-credentials-password" />
        </label>
        <label v-else class="block text-sm text-gray-700 dark:text-gray-200">
          邮箱验证码账号
          <textarea v-model="emailCodeInput" class="input mt-2 min-h-36 resize-y font-mono" rows="5" autocomplete="off" autocapitalize="off" :spellcheck="false" placeholder="粘贴包含邮箱和 64 位 Token 的账号资料，每行一个账号" data-testid="batch-credentials-email-code" />
          <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">验证码来源固定为 {{ emailCodeProvider }}，其余套餐、地区、日期和浏览器字段会被忽略。</span>
        </label>
        <div class="flex flex-wrap gap-3 text-sm text-gray-600 dark:text-gray-300">
          <span>已识别 {{ credentials.length }} 个账号</span>
          <span v-if="duplicates">已忽略 {{ duplicates }} 个重复邮箱</span>
          <span v-if="invalidLines.length" role="alert" class="text-red-600 dark:text-red-300">格式错误：第 {{ invalidLines.join('、') }} 行</span>
        </div>
        <div v-if="showBrowserModeSelector">
          <label class="input-label">授权浏览器</label>
          <div class="inline-flex max-w-full overflow-x-auto rounded-md border border-gray-200 p-1 dark:border-dark-600" role="group" aria-label="授权浏览器模式">
            <button type="button" class="batch-mode-tab" :class="settings.browser_mode === 'server' ? 'batch-mode-tab-active' : 'batch-mode-tab-idle'" :aria-pressed="settings.browser_mode === 'server'" data-testid="batch-browser-server" @click="settings.browser_mode = 'server'">
              <Icon name="server" size="sm" />内置浏览器授权
            </button>
            <button type="button" class="batch-mode-tab" :class="settings.browser_mode === 'adspower' ? 'batch-mode-tab-active' : 'batch-mode-tab-idle'" :aria-pressed="settings.browser_mode === 'adspower'" data-testid="batch-browser-adspower" @click="settings.browser_mode = 'adspower'">
              <Icon name="globe" size="sm" />Ads 指纹浏览器授权
            </button>
          </div>
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            {{ settings.browser_mode === 'adspower' ? '一个账号固定一个 AdsPower 环境，出口跟随账号所属 XIASS 节点。' : '沿用 XIASS 服务器端自动授权流程。' }}
          </p>
        </div>
        <div v-if="credentials.length" class="max-h-28 overflow-y-auto border-y border-gray-200 py-2 dark:border-dark-700">
          <div v-for="row in credentials" :key="row.account" class="flex gap-3 py-1 text-sm">
            <span class="min-w-0 flex-1 break-all">{{ row.account }}</span>
            <span class="shrink-0 text-emerald-700 dark:text-emerald-300">{{ loginMode === 'email_code' ? '邮箱 Token 已识别' : '密码与 2FA 已识别' }}</span>
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
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">共 {{ rows.length }} 个 · 进行中 {{ activeCount }}/3 · 待开始 {{ pendingCount }} · 成功 {{ completedCount }} · 已跳过 {{ skippedCount }} · 失败 {{ failedCount }}</p>
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
            本批次已结束：成功 {{ completedCount }} 个，已存在跳过 {{ skippedCount }} 个，失败 {{ failedCount }} 个。
          </p>
          <p v-if="failedEmails.length" class="mt-1 break-words text-xs text-red-600 dark:text-red-300">失败账号：{{ failedEmails.join('、') }}</p>
          <div v-if="failedEmailCodeLines.length" class="mt-3 border-y border-amber-200 py-3 dark:border-amber-900/60">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <span class="text-xs font-medium text-amber-800 dark:text-amber-200">邮箱验证码失败账号（邮箱 + Token）</span>
              <button type="button" class="btn btn-secondary btn-sm" data-testid="copy-failed-email-code-credentials" @click="copyFailedEmailCodeCredentials">
                <Icon :name="failedCredentialsCopied ? 'check' : 'copy'" size="sm" />{{ failedCredentialsCopied ? '已复制' : '复制全部' }}
              </button>
            </div>
            <textarea class="input mt-2 min-h-24 resize-y font-mono text-xs" readonly :value="failedEmailCodeLines.join('\n')" aria-label="失败邮箱验证码账号" />
          </div>
        </div>
        <div class="max-h-[48vh] overflow-y-auto">
          <article v-for="row in displayRows" :key="row.key" class="grid grid-cols-[1.25rem_minmax(0,1fr)] gap-2 border-t border-gray-100 py-3 dark:border-dark-700 sm:grid-cols-[1.25rem_minmax(0,0.85fr)_minmax(0,1.4fr)_auto]" :data-testid="`oauth-row-${row.email}`">
            <div class="pt-0.5">
              <input v-if="canRetry(row)" type="checkbox" class="h-4 w-4 accent-primary-600" :aria-label="`选择重新授权 ${row.email}`" :checked="selectedRetryKeys.has(row.key)" @change="toggleRetry(row)">
            </div>
            <div class="min-w-0">
              <div class="break-all text-sm font-medium">{{ row.email }}</div>
              <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ row.task?.login_method === 'email_code' ? '邮箱验证码登录' : '密码 + 2FA 登录' }}</div>
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
                <span>第 {{ progressStep(row) }}/{{ flowStepCount(row) }} 步 · {{ stageText(row) }}</span>
                <span v-if="row.task?.restart_count">已重新授权 {{ row.task.restart_count }} 次</span>
              </div>
              <p v-if="row.error || (row.task?.reason && !isSkipped(row))" class="mt-1 break-words text-xs text-red-600 dark:text-red-300" role="alert">{{ row.error || reasonText(row.task) }}</p>
            </div>
            <div class="col-span-2 flex flex-wrap items-start justify-end gap-2 sm:col-span-1">
              <button v-if="row.task?.browser_mode === 'adspower' && row.task.stage === 'external_browser' && row.task.status === 'running'" class="btn btn-primary btn-sm" :disabled="busyKeys.has(row.key)" :data-testid="`launch-adspower-${row.email}`" @click="launchAdsPower(row)"><Icon name="globe" size="sm" />打开固定环境</button>
              <button v-if="row.task?.browser_mode !== 'adspower' && row.task?.stage === 'phone_required' && row.task.status === 'running'" class="btn btn-primary btn-sm" :disabled="busyKeys.has(row.key)" @click="ask(row.number ? 'change' : 'acquire', row)">{{ row.number ? '更换号码' : '领取号码' }}</button>
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
    <div class="mt-4 flex flex-wrap justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-700">
      <button class="btn btn-secondary" :disabled="loading" @click="refresh"><Icon name="refresh" size="sm" />刷新状态</button>
      <button v-if="hasWork" class="btn btn-secondary text-red-600 dark:text-red-300" :disabled="busyKeys.size > 0" @click="ask('stopAll')">停止全部</button>
      <button v-if="!started" class="btn btn-primary" :disabled="!canStart" data-testid="batch-start" @click="begin"><Icon name="play" size="sm" />开始授权</button>
      <button v-else-if="embedded && batchFinished" class="btn btn-primary" @click="prepareNextBatch"><Icon name="userPlus" size="sm" />添加下一批</button>
      <button v-else-if="!embedded" class="btn btn-secondary" @click="requestClose">关闭</button>
    </div>
  </component>
  <ConfirmDialog :show="!!confirmation" :title="confirmationTitle" :message="confirmationMessage" :danger="confirmation?.action === 'stop' || confirmation?.action === 'stopAll' || confirmation?.action === 'close'" @cancel="dismiss" @confirm="confirmAction" />
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
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
import { OPENAI_EMAIL_CODE_PROVIDER, parseOpenAIEmailCodeCredentials } from '@/features/token-converter/openAIEmailCodeCredentials'
import { normalizeBase32Secret } from '@/features/token-converter/totp'
import { useClipboard } from '@/composables/useClipboard'
import { useBatchOpenAIOAuth, batchTaskActive, batchTaskSkipped, batchTaskWillAutoRestart, type BatchOAuthQueueCredential, type OAuthQueueRow } from '@/composables/useBatchOpenAIOAuth'
import { adsPowerHelperMacDownloadURL, adsPowerHelperWindowsDownloadURL } from '@/utils/adspowerHelper'

type AuthorizationBrowserMode = 'server' | 'adspower'

const props = withDefaults(defineProps<{ show?: boolean; embedded?: boolean; groups: AdminGroup[]; proxies: Proxy[]; browserMode?: AuthorizationBrowserMode; showBrowserModeSelector?: boolean }>(), {
  show: true,
  embedded: false,
  browserMode: 'server',
  showBrowserModeSelector: true,
})
const emit = defineEmits<{ close: []; created: [] }>()
const { rows, error, adsPowerHelperMissing, loading, started, busyKeys, activeCount, pendingCount, hasWork, start, sms, cancel, cancelAll, retry, complete, remove, prepareNextBatch, launchAdsPower, hasSecret, emailCodeToken, refresh } = useBatchOpenAIOAuth(() => emit('created'))
const { copied: failedCredentialsCopied, copyToClipboard } = useClipboard()
const loginMode = ref<'password' | 'email_code'>('password')
const passwordInput = ref('')
const emailCodeInput = ref('')
const emailCodeProvider = OPENAI_EMAIL_CODE_PROVIDER
const localError = ref('')
const pools = ref<{ id: number; name: string; proxy_id: number | null }[]>([])
const poolsReady = ref(false)
const settings = reactive<BatchOAuthConfig>({ group_ids: [], proxy_id: null, pool_id: null, concurrency: 1, priority: 2, codex_fingerprint_mode: 'off', browser_mode: props.browserMode })
watch(() => props.browserMode, (browserMode) => {
  if (!started.value && !hasWork.value) settings.browser_mode = browserMode
}, { immediate: true })
const passwordParsed = computed(() => parseAccountCredentials(passwordInput.value))
const emailCodeParsed = computed(() => parseOpenAIEmailCodeCredentials(emailCodeInput.value))
const passwordRows = computed(() => {
  const seen = new Set<string>()
  return passwordParsed.value.rows.filter(row => {
    const email = row.account.toLowerCase()
    if (seen.has(email)) return false
    seen.add(email)
    return true
  })
})
const emailCodeRows = computed(() => {
  const seen = new Set<string>()
  return emailCodeParsed.value.rows.filter(row => {
    const email = row.account.toLowerCase()
    if (seen.has(email)) return false
    seen.add(email)
    return true
  })
})
const credentials = computed<BatchOAuthQueueCredential[]>(() => loginMode.value === 'email_code'
  ? emailCodeRows.value.map(row => ({
      account: row.account,
      login: { login_method: 'email_code', email_code_token: row.emailCodeToken },
    }))
  : passwordRows.value.map(row => ({
      account: row.account,
      login: { login_method: 'password', password: row.password, totp_secret: normalizeBase32Secret(row.twoFactor) },
    })))
const duplicates = computed(() => loginMode.value === 'email_code'
  ? emailCodeParsed.value.rows.length - emailCodeRows.value.length
  : passwordParsed.value.rows.length - passwordRows.value.length)
const invalidLines = computed(() => {
  if (loginMode.value === 'email_code') {
    return [...new Set(emailCodeParsed.value.invalidRows.map(row => row.lineNumber))].sort((a, b) => a - b)
  }
  const invalid = passwordParsed.value.invalidRows.map(row => row.lineNumber)
  for (const row of passwordRows.value) {
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
const fingerprintOptions = [{ value: 'off', label: '关闭' }, { value: 'device', label: '设备' }, { value: 'session', label: '会话' }, { value: 'full', label: '指纹 + 会话' }]
const isSkipped = (row: OAuthQueueRow) => batchTaskSkipped(row.task)
const completedCount = computed(() => rows.value.filter(row => row.task?.status === 'completed' && !isSkipped(row)).length)
const skippedCount = computed(() => rows.value.filter(isSkipped).length)
const willAutoRestart = (row: OAuthQueueRow) => hasSecret(row) && batchTaskWillAutoRestart(row.task)
const failedRows = computed(() => rows.value.filter(row => row.task && ['failed', 'blocked'].includes(row.task.status) && !willAutoRestart(row) && !row.retryPending))
const failedCount = computed(() => failedRows.value.length)
const failedEmails = computed(() => failedRows.value.map(row => row.email))
const failedEmailCodeLines = computed(() => failedRows.value.flatMap(row => {
  const token = emailCodeToken(row)
  return token ? [`${row.email}\t${token}`] : []
}))
const batchFinished = computed(() => rows.value.length > 0 && !hasWork.value)
const displayRows = computed(() => {
  if (!batchFinished.value) return rows.value
  const rank = (row: OAuthQueueRow) => {
    if (row.task?.status === 'completed' && !isSkipped(row)) return 0
    if (isSkipped(row)) return 1
    if (row.task && ['failed', 'blocked', 'canceled'].includes(row.task.status)) return 2
    return 3
  }
  return [...rows.value].sort((left, right) => rank(left) - rank(right))
})
const selectedRetryKeys = ref(new Set<string>())
const retryableRows = computed(() => rows.value.filter(canRetry))
const selectedRetryRows = computed(() => retryableRows.value.filter(row => selectedRetryKeys.value.has(row.key)))
const selectedRetryCount = computed(() => selectedRetryRows.value.length)
const allRetryableSelected = computed(() => retryableRows.value.length > 0 && selectedRetryCount.value === retryableRows.value.length)
const passwordFlowStages = ['opening', 'email', 'password', 'totp', 'phone', 'sms', 'profile', 'workspace', 'callback', 'verify'] as const
const emailCodeFlowStages = ['opening', 'email', 'email_code', 'phone', 'sms', 'profile', 'workspace', 'callback', 'verify'] as const
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
function requestClose() { if (hasWork.value) ask('close'); else { passwordInput.value = ''; emailCodeInput.value = ''; emit('close') } }
function begin() {
  if (!canStart.value) return
  start(credentials.value, { ...settings, proxy_id: effectiveProxy.value })
  if (started.value) {
    if (loginMode.value === 'email_code') emailCodeInput.value = ''
    else passwordInput.value = ''
  }
}
async function copyFailedEmailCodeCredentials() {
  await copyToClipboard(failedEmailCodeLines.value.join('\n'), '失败账号邮箱和 Token 已复制')
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
  if (isSkipped(row)) return '已存在，已跳过'
  if (task.status !== 'running') return { queued: '正在启动', ready: '正在核验并添加', completed: '已添加成功', failed: '授权失败', blocked: '授权失败', canceled: '已停止' }[task.status] || task.status
  return ({ external_browser: '等待打开固定指纹环境', opening: '正在打开隐私授权窗口', login: '正在进入登录页面', email: '正在填写邮箱', password: '正在填写密码', totp: '正在验证 2FA', email_code_waiting: '正在查询邮箱验证码', email_code_submitting: '正在填写邮箱验证码', phone_required: '正在准备手机号', phone_submitting: '正在提交手机号', sms_waiting: '正在等待短信验证码', sms_submitting: '正在提交短信验证码', workspace: '正在确认工作空间', callback_waiting: '正在等待 OAuth 回调', callback_received: '已收到 OAuth 回调' } as Record<string, string>)[task.stage] || '正在授权'
}
function reasonText(task?: BatchOAuthTask) {
  const reason = task?.reason
  if (reason === 'adspower_profile_busy') return '可复用的 AdsPower 环境正在使用，请等当前授权完成或关闭手动打开的环境后重试。'
  if (reason === 'adspower_profile_limit') return 'AdsPower 浏览器环境名额已满，尚未进入登录。请清理不再使用的环境或扩容后重试；不要删除其他账号的固定环境。'
  if (reason === 'sms_channel_selection_failed') return '无法确认已选择短信验证，未继续发送验证码，请重试。'
  return ({ sms_timeout: '短信等待超过 3 分钟，已取消旧号码并准备从 OAuth 起点重新授权。', sms_confirmation_timeout: '领号阶段超时，已清理当前隐私会话。', oauth_identity_mismatch: 'OAuth 返回账号与输入邮箱不一致，未添加。', pool_assignment_failed: '账号已添加，但加入号池失败，请在号池管理中重新分配。', account_creation_requires_review: '账号创建结果不确定，已禁止重复创建，请核对账号列表。', account_readback_failed: '账号已创建，但读取核验失败。', account_configuration_mismatch: '账号已创建，但分组、代理、并发、优先级或指纹配置不一致。', account_login_credentials_mismatch: '账号已创建，但登录信息的加密保存核验失败。', automation_start_failed: '授权浏览器没有确认启动。', invalid_configuration: '分组、号池或代理配置不可用。', oauth_exchange_failed: 'OAuth 回调已收到，但换取 Token 失败。', manual_challenge: 'OpenAI 页面结构无法识别，当前隐私会话已关闭。', email_code_required: '当前是密码 + 2FA 模式，不能处理 OpenAI 邮箱验证码页面。', email_code_timeout: '等待邮箱验证码超过 2 分钟，已停止该账号。', email_code_access_denied: '邮箱与 64 位 Token 不匹配或 Token 已失效，已停止该账号。', email_code_unavailable: '邮箱验证码服务暂时不可用，已停止该账号。', invalid_email_code: 'OpenAI 拒绝了邮箱验证码，已停止该账号。', captcha_required: 'OpenAI 出现人机验证，当前隐私会话已关闭。', account_blocked: '未知错误，可以恢复状态后重试。', unknown_error: '未知错误，可以恢复状态后重试。', account_banned: 'OpenAI 页面明确显示账号已封禁，不能继续授权。', account_deleted_or_disabled: 'OpenAI 页面明确显示账号已删除或停用，不能继续授权。', authenticator_required: 'OpenAI 要求验证器验证码，但没有可用的 2FA 密钥。', invalid_credentials: '邮箱、密码或登录后的账号身份未通过验证。', invalid_totp: 'OpenAI 拒绝了当前 2FA 验证码，请检查密钥与服务器时间。', invalid_sms_code: 'OpenAI 拒绝了短信验证码。', proxy_unavailable: '所选出口代理无法从授权浏览器连接。', navigation_timeout: '打开 OpenAI OAuth 页面超时。', browser_context_lost: '独立隐私浏览器上下文意外关闭。', page_interaction_failed: 'OpenAI 页面控件操作失败或页面结构发生变化。', openai_route_error: 'OpenAI 登录页临时返回 Route Error，当前隐私会话已关闭，请重新授权。', oauth_session_expired: 'OpenAI 登录会话已失效（invalid_state），当前隐私会话已关闭，请重新授权。', phone_rejected: 'OpenAI 拒绝了当前号码，系统将自动更换号码。', task_expired: '本次授权总时长已超时。' } as Record<string, string>)[reason || ''] || (reason ? `授权未完成（${reason}）` : '')
}
function normalizedStage(row: OAuthQueueRow) {
  if (row.localStatus) return 'opening'
  if (row.task?.status === 'ready') return 'verify'
  if (row.task?.status === 'completed') return 'verify'
  const stage = row.task?.stage || 'opening'
  if (stage === 'external_browser') return 'browser'
  if (['login', 'email'].includes(stage)) return 'email'
  if (['email_code_waiting', 'email_code_submitting'].includes(stage)) return 'email_code'
  if (['phone_required', 'phone_submitting'].includes(stage)) return 'phone'
  if (['sms_waiting', 'sms_submitting'].includes(stage)) return 'sms'
  if (stage === 'profile') return 'profile'
  if (['callback_waiting', 'callback_received'].includes(stage)) return 'callback'
  const flow = flowStages(row)
  return flow.some(item => item === stage) ? stage : 'opening'
}
function flowStages(row: OAuthQueueRow): readonly string[] { return row.task?.login_method === 'email_code' ? emailCodeFlowStages : passwordFlowStages }
function flowStepCount(row: OAuthQueueRow) { return flowStages(row).length }
function progressStep(row: OAuthQueueRow) { return Math.max(1, flowStages(row).indexOf(normalizedStage(row)) + 1) }
function progressPercent(row: OAuthQueueRow) { return row.task?.status === 'completed' ? 100 : Math.round((progressStep(row) / flowStepCount(row)) * 100) }
function progressClass(row: OAuthQueueRow) {
  if (row.task?.status === 'completed') return 'bg-emerald-500'
  if (row.retryPending) return 'bg-primary-500'
  if (row.task && ['failed', 'blocked'].includes(row.task.status) && !willAutoRestart(row)) return 'bg-red-500'
  return 'bg-primary-500'
}
function stageText(row: OAuthQueueRow) {
  return ({ browser: '打开固定指纹环境', opening: '打开授权窗口', email: '登录邮箱', password: '登录密码', totp: '验证 2FA', email_code: '查询并提交邮箱验证码', phone: '提交手机号', sms: '等待并提交短信', profile: '填写姓名和年龄', workspace: '确认工作空间', callback: '等待回调链接', verify: '核验并保存账号' } as Record<string, string>)[normalizedStage(row)]
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
onUnmounted(() => { passwordInput.value = ''; emailCodeInput.value = ''; if (elapsedTimer) clearInterval(elapsedTimer); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<style scoped>
.batch-mode-tab {
  display: inline-flex;
  height: 2rem;
  flex-shrink: 0;
  align-items: center;
  gap: 0.375rem;
  border-radius: 0.25rem;
  padding-inline: 0.75rem;
  font-size: 0.875rem;
  font-weight: 500;
  transition: color 150ms ease, background-color 150ms ease;
}

.batch-mode-tab-active {
  @apply bg-primary-50 text-primary-700 dark:bg-primary-950/50 dark:text-primary-300;
}

.batch-mode-tab-idle {
  @apply text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200;
}
</style>
