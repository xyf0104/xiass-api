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
          <h3 class="text-sm font-semibold">授权任务</h3>
          <span class="text-xs text-gray-600 dark:text-gray-300">进行中 {{ activeCount }}/3 · 待开始 {{ pendingCount }} · 已完成 {{ completedCount }}</span>
        </header>
        <div class="max-h-[48vh] overflow-y-auto">
          <article v-for="row in rows" :key="row.key" class="grid gap-2 border-t border-gray-100 py-3 dark:border-dark-700 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]" :data-testid="`oauth-row-${row.email}`">
            <div class="min-w-0">
              <div class="break-all text-sm font-medium">{{ row.email }}</div>
              <div v-if="row.task?.account_id" class="mt-1 text-xs text-gray-500">账号 #{{ row.task.account_id }}</div>
              <div v-if="row.number" class="mt-1 text-xs tabular-nums text-gray-600 dark:text-gray-300">{{ row.number }}</div>
            </div>
            <div class="min-w-0 text-sm" :class="row.task?.status === 'completed' ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-300'">
              <div aria-live="polite">{{ statusText(row) }}</div>
              <p v-if="row.error || row.task?.reason" class="mt-1 break-words text-xs text-red-600 dark:text-red-300" role="alert">{{ row.error || reasonText(row.task?.reason) }}</p>
            </div>
            <div class="flex flex-wrap items-start justify-end gap-2">
              <button v-if="row.task?.stage === 'phone_required' && row.task.status === 'running'" class="btn btn-primary btn-sm" :disabled="busyKeys.has(row.key)" @click="ask(row.number ? 'change' : 'acquire', row)">{{ row.number ? '更换号码' : '领取号码' }}</button>
              <button v-if="row.task?.status === 'ready' && row.error" class="btn btn-primary btn-sm" :disabled="busyKeys.has(row.key)" @click="complete(row)">核验并添加</button>
              <button v-if="canRetry(row)" class="btn btn-secondary btn-sm" :disabled="busyKeys.has(row.key) || (activeCount >= 3 && row.localStatus !== 'uncertain')" @click="ask('retry', row)"><Icon name="refresh" size="sm" />重试</button>
              <button v-if="batchTaskActive(row.task) || row.localStatus === 'pending' || row.localStatus === 'uncertain'" class="btn btn-secondary btn-sm" :disabled="busyKeys.has(row.key)" @click="ask('stop', row)"><Icon name="x" size="sm" />停止</button>
              <button v-else-if="row.task?.requires_sms_confirmation" class="btn btn-secondary btn-sm" :disabled="busyKeys.has(row.key)" @click="ask('cancel', row)">取消接码</button>
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
import type { BatchOAuthConfig } from '@/api/admin/openaiBatchOAuth'
import { parseAccountCredentials } from '@/features/token-converter/accountCredentials'
import { normalizeBase32Secret } from '@/features/token-converter/totp'
import { useBatchOpenAIOAuth, batchTaskActive, type OAuthQueueRow } from '@/composables/useBatchOpenAIOAuth'

defineProps<{ show: boolean; groups: AdminGroup[]; proxies: Proxy[] }>()
const emit = defineEmits<{ close: []; created: [] }>()
const { rows, error, loading, started, busyKeys, activeCount, pendingCount, hasWork, start, sms, cancel, cancelAll, retry, complete, refresh } = useBatchOpenAIOAuth(() => emit('created'))
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
type Action = 'acquire' | 'change' | 'cancel' | 'stop' | 'stopAll' | 'close' | 'retry'
const confirmation = ref<{ action: Action; row?: OAuthQueueRow }>()
const confirmationTitle = computed(() => ({ acquire: '确认领取号码', change: '确认更换号码', cancel: '确认取消接码', stop: '确认停止授权', stopAll: '确认停止全部', close: '停止任务并关闭', retry: '重新授权' })[confirmation.value?.action || 'stop'])
const confirmationMessage = computed(() => {
  const email = confirmation.value?.row?.email || ''
  switch (confirmation.value?.action) {
    case 'acquire': return `为 ${email} 领取独立接码号码？`
    case 'change': return `取消 ${email} 当前号码并领取新号码？`
    case 'retry': return `重新处理 ${email}。如有未结束的接码，将先取消；授权会在新的独立窗口中开始。`
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
  } else if (current.row) {
    if (current.action === 'stop') await cancel(current.row)
    else if (current.action === 'retry') await retry(current.row)
    else await sms(current.row, current.action)
  }
}
function canRetry(row: OAuthQueueRow) { return row.localStatus === 'uncertain' || (!!row.task && ['failed', 'canceled'].includes(row.task.status) && !row.task.account_id && row.task.restart_count < 2 && row.task.reason !== 'account_creation_requires_review') }
function statusText(row: OAuthQueueRow) {
  if (row.localStatus) return { pending: '待开始', starting: '正在启动', uncertain: '启动结果待核验', canceled: '已停止' }[row.localStatus]
  const task = row.task
  if (!task) return ''
  if (task.status !== 'running') return { queued: '正在启动', ready: '正在核验并添加', completed: '已添加', failed: '未完成', blocked: '需要人工处理', canceled: '已停止' }[task.status] || task.status
  return ({ login: '正在登录', email: '输入邮箱', password: '输入密码', totp: '验证 2FA', phone_required: '等待领取手机号', sms_waiting: '等待短信验证码', workspace: '确认工作空间', callback: '正在完成授权' } as Record<string, string>)[task.stage] || '正在授权'
}
function reasonText(reason?: string) {
  return ({ sms_timeout: '短信等待超过 3 分钟，请确认取消后重试。', sms_confirmation_timeout: '接码确认已超时。', oauth_identity_mismatch: '返回的账号与输入邮箱不一致，未添加。', pool_assignment_failed: '账号已添加，但加入号池失败，请在号池管理中重新分配。', account_creation_requires_review: '创建结果需要人工核对，已禁止重复创建。', account_readback_failed: '账号已创建，读取核验未成功，请刷新后核对。', account_configuration_mismatch: '已保存的配置与本次选择不一致，请核对原账号，勿重复添加。', account_login_credentials_mismatch: '登录信息保存核验未通过，请核对原账号。', automation_start_failed: '授权浏览器未确认启动。', invalid_configuration: '账号配置不可用，请核对分组与代理。', oauth_exchange_failed: '授权交换失败。', manual_challenge: '上游要求人工验证，自动化已停止。', email_code_required: '上游要求邮件验证码，需人工处理。', captcha_required: '上游要求人机验证，自动化已停止。', account_blocked: '上游已停用或限制该账号。', authenticator_required: '上游要求 2FA，但未提供有效密钥。', invalid_credentials: '邮箱、密码或账号身份未通过验证。', phone_rejected: '上游拒绝了当前号码，请确认更换号码。', task_expired: '本次授权已超时。' } as Record<string, string>)[reason || ''] || (reason ? `授权未完成（${reason}）` : '')
}
function beforeUnload(event: BeforeUnloadEvent) { if (hasWork.value) { event.preventDefault(); event.returnValue = '' } }
onMounted(async () => {
  window.addEventListener('beforeunload', beforeUnload)
  try { pools.value = (await apiClient.get<{ items: typeof pools.value }>('/admin/account-pools')).data.items; poolsReady.value = true } catch { localError.value = '号池列表加载失败，请关闭后重新打开。' }
})
onUnmounted(() => { input.value = ''; window.removeEventListener('beforeunload', beforeUnload) })
</script>
