<template>
  <section
    class="rounded-lg border border-blue-300 bg-white/80 p-4 dark:border-blue-600 dark:bg-gray-800/80"
    aria-live="polite"
  >
    <div class="flex items-center justify-between gap-3">
      <div class="flex min-w-0 items-center gap-3">
        <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-blue-50 text-blue-700 dark:bg-blue-950/50 dark:text-blue-300" aria-hidden="true">
          <Icon name="chat" size="sm" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <h5 class="text-base font-medium text-blue-900 dark:text-blue-200">获取手机号</h5>
          <p class="truncate text-xs" :class="statusClass">{{ statusText }}</p>
        </div>
      </div>
      <button
        type="button"
        class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-blue-700 transition-colors hover:bg-blue-100 hover:text-blue-950 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 dark:text-blue-300 dark:hover:bg-blue-900/70 dark:hover:text-white"
        title="管理接码卡密"
        aria-label="管理接码卡密"
        @click="openKeyManager"
      >
        <Icon name="cog" size="sm" />
      </button>
    </div>

    <div v-if="needsManualStart" class="mt-3">
      <button
        type="button"
        class="btn btn-primary w-full !py-2.5 text-sm"
        data-testid="request-sms-phone"
        :disabled="!active || isStartingPhone || confirmationAction !== null"
        :title="active ? '领取用于当前授权的手机号' : '请先生成授权链接'"
        @click="requestPhoneConfirmation"
      >
        <svg v-if="isStartingPhone" class="mr-1.5 h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="chat" size="sm" class="mr-1.5" />
        获取手机号
      </button>
    </div>

    <div v-if="replacementRequired" class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2.5 text-sm text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-100">
      <div class="flex items-start justify-between gap-3">
        <span class="leading-5">{{ automationMode ? '当前号码已被授权页拒绝，系统正在自动更换并覆盖内嵌浏览器中的完整国际号码。' : '当前号码已被授权页拒绝。确认换号后会自动覆盖内嵌浏览器中的完整国际号码并继续当前步骤。' }}</span>
        <button v-if="!automationMode" type="button" class="btn btn-secondary shrink-0 whitespace-nowrap !px-2.5 !py-1.5 text-xs" :disabled="!canChangeNumber || confirmationAction !== null" @click="requestRequiredReplacement">
          <Icon name="swap" size="xs" class="mr-1" />换号
        </button>
        <span v-else class="shrink-0 whitespace-nowrap text-xs font-medium text-amber-700 dark:text-amber-300">自动换号中</span>
      </div>
    </div>

    <div v-if="!needsManualStart" class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1.2fr)_minmax(10rem,0.8fr)]">
      <button
        type="button"
        class="group min-w-0 rounded-md border border-blue-200 bg-white/85 px-3 py-2.5 text-left transition-colors hover:border-blue-400 hover:bg-white focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 disabled:cursor-not-allowed disabled:opacity-70 dark:border-blue-900/80 dark:bg-gray-900/70 dark:hover:border-blue-700 dark:hover:bg-gray-900"
        :disabled="!phoneForCopy"
        :title="phoneForCopy ? '点击手机号框复制完整国际号码' : '等待获取手机号'"
        @click="copyPhone"
      >
        <span class="block text-[11px] font-medium text-blue-700 dark:text-blue-300">手机号</span>
        <div class="mt-1 flex min-w-0 items-center gap-2">
          <span
            v-if="countryCallingCode"
            class="shrink-0 rounded border border-blue-200 bg-blue-50 px-1.5 py-0.5 font-mono text-xs font-semibold text-blue-800 dark:border-blue-800 dark:bg-blue-950/60 dark:text-blue-200"
          >
            +{{ countryCallingCode }}
          </span>
          <span class="flex min-w-0 flex-1 items-center justify-between gap-2">
            <span class="truncate font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{{ localPhoneNumber }}</span>
            <Icon
              :name="phoneCopied ? 'check' : 'copy'"
              size="sm"
              :class="phoneCopied ? 'shrink-0 text-emerald-600 dark:text-emerald-300' : 'shrink-0 text-blue-600 opacity-0 transition-opacity group-hover:opacity-100 dark:text-blue-300'"
            />
          </span>
        </div>
      </button>

      <div class="min-w-0 rounded-md border border-blue-200 bg-white/85 px-3 py-2.5 dark:border-blue-900/80 dark:bg-gray-900/70">
        <span class="block text-[11px] font-medium text-blue-700 dark:text-blue-300">地区</span>
        <span class="mt-1 flex min-w-0 items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-gray-100">
          <span v-if="countryFlag" class="shrink-0 text-base leading-none" aria-hidden="true">{{ countryFlag }}</span>
          <span class="truncate">{{ region }}</span>
        </span>
      </div>

      <button
        type="button"
        class="group min-w-0 rounded-md border border-cyan-200 bg-white/85 px-3 py-2.5 text-left transition-colors hover:border-cyan-400 hover:bg-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500 sm:col-span-2 dark:border-cyan-900/80 dark:bg-gray-900/70 dark:hover:border-cyan-600 dark:hover:bg-gray-900"
        :disabled="!code || code === '--'"
        :title="code && code !== '--' ? '点击复制验证码' : '正在等待验证码'"
        @click="copyCode"
      >
        <span class="flex items-center justify-between gap-3">
          <span class="block text-[11px] font-medium text-cyan-700 dark:text-cyan-300">短信验证码</span>
          <Icon
            :name="codeCopied ? 'check' : 'copy'"
            size="sm"
            :class="codeCopied ? 'text-emerald-600 dark:text-emerald-300' : 'text-cyan-600 opacity-60 transition-opacity group-hover:opacity-100 dark:text-cyan-300'"
          />
        </span>
        <span
          data-testid="sms-code-slots"
          class="mt-2 grid min-w-0 gap-1.5"
          :style="{ gridTemplateColumns: `repeat(${codeSlots.length}, minmax(0, 1fr))` }"
          aria-hidden="true"
        >
          <span
            v-for="(character, index) in codeSlots"
            :key="`sms-code-slot-${index}`"
            class="flex aspect-square min-w-0 items-center justify-center rounded-md border border-cyan-200 bg-cyan-50/70 font-mono text-base font-semibold tabular-nums text-gray-900 dark:border-cyan-800 dark:bg-cyan-950/30 dark:text-gray-100"
          >
            {{ character || '·' }}
          </span>
        </span>
      </button>
    </div>

    <p v-if="!autoSubmit" class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-200">
      当前工作区只显示可复制的手机号和验证码；不会自动提交到外部页面。
    </p>

    <div
      v-if="!needsManualStart && sessionExpiresAt"
      class="mt-2 flex items-center justify-between gap-3 rounded-md border border-blue-200 bg-blue-50/70 px-3 py-2 text-xs dark:border-blue-900/80 dark:bg-blue-950/30"
      data-testid="sms-session-countdown"
    >
      <span class="text-blue-700 dark:text-blue-300">号码有效期</span>
      <time :datetime="sessionExpiresAt" class="font-mono font-semibold tabular-nums text-gray-900 dark:text-gray-100">
        {{ sessionExpiryText }}
      </time>
    </div>

    <div v-if="!needsManualStart" class="mt-3 flex flex-wrap items-center gap-2">
      <button
        type="button"
        class="btn btn-secondary !px-2.5 !py-1.5 text-xs"
        :disabled="!canRefresh"
        @click="refresh"
      >
        <svg v-if="isRefreshing" class="mr-1 h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="refresh" size="xs" class="mr-1" />
        刷新
      </button>
      <button
        type="button"
        class="btn btn-secondary !px-2.5 !py-1.5 text-xs"
        :disabled="!canChangeNumber || confirmationAction !== null"
        @click="requestChangeConfirmation"
      >
        <svg v-if="isChangingNumber" class="mr-1 h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="swap" size="xs" class="mr-1" />
        换号
      </button>
      <button
        type="button"
        class="btn btn-secondary !px-2.5 !py-1.5 text-xs text-red-600 hover:!border-red-300 hover:!bg-red-50 hover:!text-red-700 dark:text-red-300 dark:hover:!border-red-700 dark:hover:!bg-red-950/40"
        :disabled="!canCancel || confirmationAction !== null"
        @click="requestCancelConfirmation"
      >
        <svg v-if="isCancelling" class="mr-1 h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="x" size="xs" class="mr-1" />
        取消
      </button>
      <span class="ml-auto text-xs text-gray-500 dark:text-gray-400">待用卡密 {{ queuedKeyCount }} 个</span>
    </div>

    <p v-if="!needsManualStart && phase === 'unavailable'" class="mt-2 text-xs text-amber-700 dark:text-amber-300">
      请点右上角齿轮添加接码卡密；卡密会加密保存到 XIASS API 服务器，收到验证码后才会自动清除。
    </p>
  </section>

  <BaseDialog
    :show="showKeyManager"
    title="接码卡密"
    width="narrow"
    @close="showKeyManager = false"
  >
    <div class="space-y-4">
      <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">
        可一次粘贴多个卡密（换行、空格或逗号分隔）。卡密会加密保存到服务器；只有收到验证码后才会彻底清除，不会下发到浏览器。
      </p>
      <textarea
        v-model="cardKeysInput"
        rows="6"
        class="input w-full resize-y font-mono text-sm"
        placeholder="每行一个接码卡密"
        spellcheck="false"
      />
      <div class="rounded-md border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300">
        当前待用：<span class="font-semibold text-gray-900 dark:text-white">{{ queuedKeyCount }}</span> 个
      </div>
    </div>
    <template #footer>
      <div class="flex items-center justify-between gap-3">
        <button
          type="button"
          class="btn btn-secondary text-red-600 hover:!border-red-300 hover:!bg-red-50 hover:!text-red-700 dark:text-red-300 dark:hover:!border-red-700 dark:hover:!bg-red-950/40"
          :disabled="queuedKeyCount === 0 || isClearingKeys"
          @click="clearQueuedKeys"
        >
          {{ isClearingKeys ? '清空中…' : '清空待用' }}
        </button>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" @click="showKeyManager = false">关闭</button>
          <button type="button" class="btn btn-primary" :disabled="!cardKeysInput.trim() || isSavingKeys" @click="saveKeys">
            {{ isSavingKeys ? '保存中…' : '添加卡密' }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>

  <SMSReceiverActionDialog
    :show="confirmationAction !== null"
    :title="confirmationAction === 'cancel' ? '取消当前号码' : confirmationAction === 'change' ? '更换当前号码' : '领取授权手机号'"
    :message="confirmationAction === 'cancel'
      ? '确认取消当前手机号，并只重新打开 OAuth 授权步骤吗？'
      : confirmationAction === 'change'
        ? '确认释放当前手机号并领取新的号码，覆盖原号码后继续当前 OAuth 步骤吗？'
        : '确认领取一个用于当前 OAuth 授权的手机号吗？'"
    :detail="confirmationAction === 'cancel'
      ? '只重走授权页和验证步骤；成员移除、邀请和临时邮箱不会重复执行。'
      : confirmationAction === 'change'
        ? '确认成功后，新号码会自动填入当前授权页面并覆盖旧号码；已经收到的验证码按现有规则处理。'
        : '领取成功后会立即开始监听验证码，可随时刷新、换号或在未收到验证码时取消。'"
    :confirm-label="confirmationAction === 'cancel' ? '确认取消号码' : confirmationAction === 'change' ? '确认换号' : '确认领取号码'"
    :pending-label="confirmationAction === 'cancel' ? '正在取消…' : confirmationAction === 'change' ? '正在换号…' : '正在领取…'"
    :pending="confirmationPending"
    :danger="confirmationAction === 'cancel' || confirmationAction === 'change'"
    @cancel="closeConfirmation"
    @confirm="confirmReceiverAction"
  />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { usePixlabSMSReceiver } from '@/composables/usePixlabSMSReceiver'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import SMSReceiverActionDialog from '@/components/sms/SMSReceiverActionDialog.vue'

const props = withDefaults(defineProps<{
  active?: boolean
  replacementRequired?: boolean
  /** Re-evaluate an existing confirmed session when the parent moves nodes. */
  submissionKey?: string
  /** Keep the legacy OAuth receiver behavior enabled by default. */
  autoSubmit?: boolean
  /** Team protocol 2 only: bypass manual confirmations for workflow-owned actions. */
  automationMode?: boolean
  /** Replace a waiting Team number once after this interval. */
  automationReplaceAfterMs?: number
  /** Team workflow reset signal; cancels an owned active session without a dialog. */
  automationCancelSignal?: number
}>(), {
  active: false,
  replacementRequired: false,
  submissionKey: '',
  autoSubmit: true,
  automationMode: false,
  automationReplaceAfterMs: 120_000,
  automationCancelSignal: 0
})

const emit = defineEmits<{
  'phone-ready': [phone: string]
  'code-received': [code: string]
  'session-cancelled': []
}>()

const appStore = useAppStore()
const { copyToClipboard } = useClipboard()
const receiver = usePixlabSMSReceiver()
const {
  phase,
  phoneForCopy,
  countryCallingCode,
  localPhoneNumber,
  region,
  countryFlag,
  code,
  queuedKeyCount,
  statusText,
  statusClass,
  sessionExpiresAt,
  sessionExpiryText,
  hasActiveSession,
  canRefresh,
  canChangeNumber,
  canCancel,
  isRefreshing,
  isChangingNumber,
  isCancelling
} = receiver
const showKeyManager = ref(false)
const cardKeysInput = ref('')
const phoneCopied = ref(false)
const codeCopied = ref(false)
const isSavingKeys = ref(false)
const isClearingKeys = ref(false)
const hasManuallyStarted = ref(false)
const isStartingPhone = ref(false)
const confirmationAction = ref<'claim' | 'cancel' | 'change' | null>(null)
const receiverIdentity = computed(() => JSON.stringify([
  props.submissionKey, props.automationCancelSignal, props.active,
  hasActiveSession.value, phoneForCopy.value, sessionExpiresAt.value
]))
let confirmationIdentity = ''
const lastEmittedCode = ref('')
const lastEmittedPhoneSignature = ref('')
const replacementPromptHandled = ref(false)
let automationReplacementTimer: ReturnType<typeof setTimeout> | null = null
const needsManualStart = computed(() => !hasManuallyStarted.value && !hasActiveSession.value)
const replacementRequired = computed(() => props.replacementRequired && hasActiveSession.value && !replacementPromptHandled.value)
const codeSlots = computed(() => {
  const value = code.value === '--' ? '' : code.value.replace(/\s+/g, '').slice(0, 8)
  const slotCount = Math.max(6, value.length)
  return Array.from({ length: slotCount }, (_, index) => value[index] || '')
})
const confirmationPending = computed(() => {
  if (confirmationAction.value === 'claim') return isStartingPhone.value
  if (confirmationAction.value === 'change') return isChangingNumber.value
  return isCancelling.value
})

function showError(error: unknown): void {
  appStore.showError(error instanceof Error ? error.message : '接码服务暂时不可用，请稍后重试。')
}

async function begin(): Promise<boolean> {
  try {
    await receiver.start()
    return true
  } catch (error) {
    showError(error)
    return false
  }
}

async function requestPhone(): Promise<void> {
  if (!props.active || isStartingPhone.value) return
  isStartingPhone.value = true
  hasManuallyStarted.value = true
  const started = await begin()
  if (started) emitPhoneIfReady(true)
  if (!started || ['expired', 'unavailable'].includes(receiver.phase.value)) {
    hasManuallyStarted.value = false
  }
  isStartingPhone.value = false
}

function requestRequiredReplacement(): void {
  if (!props.active || isChangingNumber.value || isStartingPhone.value) return
  // The external page explicitly rejected this number. The same in-app
  // confirmation used for an ordinary replacement remains mandatory.
  openConfirmation('change')
}

function requestPhoneConfirmation(): void {
  if (!props.active || isStartingPhone.value) return
  openConfirmation('claim')
}

async function refresh(): Promise<void> {
  try {
    await receiver.refresh()
  } catch (error) {
    showError(error)
  }
}

async function changeNumber(): Promise<void> {
  clearAutomationReplacementTimer()
  try {
    // A replacement starts a new SMS session. Clear the local display before
    // the server response so an old code cannot be mistaken for the new one.
    lastEmittedCode.value = ''
    lastEmittedPhoneSignature.value = ''
    const outcome = await receiver.changeNumber()
    replacementPromptHandled.value = outcome === 'waiting' && Boolean(receiver.phoneForCopy.value)
    if (outcome === 'waiting') emitPhoneIfReady(true)
  } catch (error) {
    replacementPromptHandled.value = false
    showError(error)
  }
}

function emitPhoneIfReady(confirmedNow = false): void {
  const phone = receiver.phoneForCopy.value.trim()
  if (!props.autoSubmit || !props.active || (!confirmedNow && !hasActiveSession.value) || !phone) return
  const signature = `${props.submissionKey}:${phone}`
  if (signature === lastEmittedPhoneSignature.value) return
  lastEmittedPhoneSignature.value = signature
  emit('phone-ready', phone)
}

function requestChangeConfirmation(): void {
  if (!canChangeNumber.value || isChangingNumber.value) return
  openConfirmation('change')
}

async function cancel(): Promise<void> {
  try {
    const outcome = await receiver.cancel()
    if (outcome === 'received') {
      hasManuallyStarted.value = true
      appStore.showSuccess('验证码已到达，已为您保留。')
      return
    }
    hasManuallyStarted.value = false
    lastEmittedPhoneSignature.value = ''
    lastEmittedCode.value = ''
    appStore.showInfo('已取消当前手机号。')
    emit('session-cancelled')
  } catch (error) {
    showError(error)
  }
}

function requestCancelConfirmation(): void {
  if (!canCancel.value || isCancelling.value) return
  openConfirmation('cancel')
}

function openConfirmation(action: 'claim' | 'cancel' | 'change'): void {
  confirmationIdentity = receiverIdentity.value
  confirmationAction.value = action
}

function closeConfirmation(): void {
  if (!confirmationPending.value) confirmationAction.value = null
}

async function confirmReceiverAction(): Promise<void> {
  const action = confirmationAction.value
  if (!action || confirmationPending.value) return
  if (confirmationIdentity !== receiverIdentity.value
    || (action === 'change' && !canChangeNumber.value)
    || (action === 'cancel' && !canCancel.value)) {
    confirmationAction.value = null
    return
  }
  try {
    if (action === 'claim') await requestPhone()
    else if (action === 'change') await changeNumber()
    else await cancel()
  } finally {
    if (confirmationAction.value === action) confirmationAction.value = null
  }
}

function clearAutomationReplacementTimer(): void {
  if (automationReplacementTimer) clearTimeout(automationReplacementTimer)
  automationReplacementTimer = null
}

function scheduleAutomationReplacement(): void {
  clearAutomationReplacementTimer()
  if (confirmationAction.value || isStartingPhone.value || isChangingNumber.value || isCancelling.value) return
  if (!props.automationMode || !props.active || receiver.phase.value !== 'waiting' || !hasActiveSession.value || !receiver.phoneForCopy.value.trim()) return
  const delay = Math.max(1_000, Math.trunc(props.automationReplaceAfterMs || 120_000))
  automationReplacementTimer = setTimeout(() => {
    automationReplacementTimer = null
    if (confirmationAction.value || isStartingPhone.value || isChangingNumber.value || isCancelling.value) return
    if (!props.automationMode || !props.active || receiver.phase.value !== 'waiting' || !hasActiveSession.value) return
    if (!canChangeNumber.value) {
      scheduleAutomationReplacement()
      return
    }
    void changeNumber()
  }, delay)
}

async function copyPhone(): Promise<void> {
  if (await copyToClipboard(receiver.phoneForCopy.value, '手机号已复制')) {
    phoneCopied.value = true
    window.setTimeout(() => { phoneCopied.value = false }, 2_000)
  }
}

async function copyCode(): Promise<void> {
  if (await copyToClipboard(receiver.code.value, '验证码已复制')) {
    codeCopied.value = true
    window.setTimeout(() => { codeCopied.value = false }, 2_000)
  }
}

async function openKeyManager(): Promise<void> {
  showKeyManager.value = true
  try {
    await receiver.refreshQueueStatus()
  } catch (error) {
    showError(error)
  }
}

async function saveKeys(): Promise<void> {
  isSavingKeys.value = true
  try {
    const added = await receiver.appendCardKeys(cardKeysInput.value)
    cardKeysInput.value = ''
    if (added === 0) {
      appStore.showInfo('没有发现可添加的新卡密。')
      return
    }
    appStore.showSuccess(`已保存 ${added} 个接码卡密到服务器。`)
    showKeyManager.value = false
    if (props.active && receiver.phase.value === 'unavailable') hasManuallyStarted.value = false
  } catch (error) {
    showError(error)
  } finally {
    isSavingKeys.value = false
  }
}

async function clearQueuedKeys(): Promise<void> {
  isClearingKeys.value = true
  try {
    const deleted = await receiver.clearQueuedCardKeys()
    appStore.showInfo(deleted > 0 ? `已清空 ${deleted} 个待用接码卡密。` : '当前没有待用接码卡密。')
  } catch (error) {
    showError(error)
  } finally {
    isClearingKeys.value = false
  }
}

watch(
  () => props.active,
  (active) => {
    clearAutomationReplacementTimer()
    if (!active) {
      hasManuallyStarted.value = false
      confirmationAction.value = null
      receiver.stop()
      return
    }
    receiver.stop()
    if (props.automationMode) void requestPhone()
  },
  { immediate: true }
)

watch(
  () => props.automationCancelSignal,
  (next, previous) => {
    if (!props.automationMode || next === previous || !hasActiveSession.value || isCancelling.value) return
    confirmationAction.value = null
    void cancel()
  }
)

watch(receiverIdentity, () => {
  if (!confirmationPending.value) confirmationAction.value = null
})

watch([replacementRequired, canChangeNumber, confirmationAction], ([required, canChange, action], [wasRequired, couldChange]) => {
  if (!required || action || isChangingNumber.value) return
  if (props.automationMode) {
    if (!canChange || !props.active || isStartingPhone.value) return
    void changeNumber()
  } else if (required !== wasRequired || canChange !== couldChange) {
    requestRequiredReplacement()
  }
}, { immediate: true })

watch(
  () => props.replacementRequired,
  (required) => {
    if (!required) replacementPromptHandled.value = false
  },
)

watch(
  [() => props.active, () => props.submissionKey, phoneForCopy, hasActiveSession],
  () => { emitPhoneIfReady() },
  { immediate: true }
)

watch(code, (value) => {
  if (!props.autoSubmit) return
  const normalized = value.trim()
  if (!normalized || normalized === '--') {
    lastEmittedCode.value = ''
    return
  }
  if (normalized === lastEmittedCode.value) return
  lastEmittedCode.value = normalized
  emit('code-received', normalized)
})

watch(
  [() => props.automationMode, () => props.active, phase, phoneForCopy, hasActiveSession,
    confirmationAction, isStartingPhone, isChangingNumber, isCancelling],
  () => { scheduleAutomationReplacement() },
  { immediate: true }
)

onBeforeUnmount(() => {
  clearAutomationReplacementTimer()
  confirmationAction.value = null
  receiver.stop()
})
</script>
