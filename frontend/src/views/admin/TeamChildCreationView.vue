<template>
  <div class="team-child-page mx-auto w-full max-w-[1200px] min-w-0 space-y-5 p-3 sm:space-y-6 sm:p-4 md:p-6">
    <header class="mx-auto flex w-full max-w-[1120px] min-w-0 flex-col gap-3 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <div class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary-600 dark:text-primary-400">
          <Icon name="users" size="sm" :stroke-width="2" />
          <span>管理员工作流</span>
        </div>
        <h1 class="whitespace-nowrap text-2xl font-semibold text-gray-900 dark:text-gray-100">Team 子号创建</h1>
        <p class="mt-1 max-w-2xl text-sm text-gray-500 dark:text-gray-400">
          悬浮进度卡持续显示当前节点，实际操作集中在下方工作区。
        </p>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <div class="flex min-w-[6.5rem] items-center gap-2 whitespace-nowrap text-sm" :class="statusTone.text">
          <Icon :name="statusTone.icon" size="sm" :class="status === 'creating' || status === 'polling' ? 'animate-spin' : ''" :stroke-width="2" />
          <span>{{ statusLabel }}</span>
        </div>
        <input ref="mailboxConfigFileInput" type="file" class="hidden" accept=".json,.env,.txt,application/json,text/plain" @change="handleMailboxConfigFileChange" />
        <button type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" :disabled="configImporting" @click="mailboxConfigFileInput?.click()">
          <Icon v-if="configImporting" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
          <Icon v-else name="upload" size="sm" :stroke-width="2" />
          <span>{{ configImporting ? '正在导入...' : '导入邮箱配置' }}</span>
        </button>
      </div>
    </header>

    <div v-if="errorMessage" class="mx-auto flex w-full max-w-[1120px] items-start gap-3 rounded-[18px] border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">
      <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" :stroke-width="2" />
      <span>{{ errorMessage }}</span>
    </div>

    <div class="team-child-layout grid min-w-0 gap-5 xl:grid-cols-[minmax(340px,420px)_minmax(0,1fr)]">
      <TeamChildOAuthWorkspace
        v-if="displayWorkflow"
        class="xl:col-span-2"
        :workflow="displayWorkflow"
        :preflight="!teamWorkflow"
        :mailbox-email="mailbox?.email || ''"
        :auth-url="authUrl || ''"
        :callback-url="callbackURL"
        :show-reauthorize="workflowNeedsReauth"
        :revealed-password="revealedWorkflowPassword"
        :password-loading="passwordLoading"
        :show-one-click="true"
        :one-click-disabled="workspaceOneClickDisabled"
        :one-click-label="workspaceOneClickLabel"
        :history-mailboxes="knownMailboxes"
        :selected-mailbox-email="selectedMailboxEmail"
        :mailbox-code="mailboxCode"
        :mailbox-code-loading="mailboxCodeLoading"
        :mailbox-code-polling="mailboxCodePolling"
        :mailbox-selecting="mailboxSelecting"
        :mailbox-configured="mailboxConfigured"
        :mailbox-actions-disabled="workflowBusy"
        :create-mailbox-disabled="busy || Boolean(teamWorkflow)"
        :browser-visible="browserVisible"
        :browser-configured="browserConfigured"
        :browser-embed-url="browserEmbedURL"
        :browser-loading="browserLoading"
        :browser-error="browserError"
        :browser-control-conflict="browserControlConflict"
        :sms-cancel-signal="smsCancelSignal"
        :automation-enabled="workflowExecutionArmed"
        @session-cancelled="pauseWorkflowAfterSMSCancel"
        @reauthorize="restartWorkflowOAuth"
        @one-click="handleWorkspaceOneClick"
        @open-browser="openBrowserWorkspace"
        @continue-workflow="continueWorkflow"
        @pause-workflow="pauseWorkflow"
        @reset-workflow="resetWorkflowFromStart"
        @reveal-password="revealWorkflowPassword"
        @clear-password="clearRevealedPassword"
        @copy-password="copyText(revealedWorkflowPassword)"
        @copy-auth="copyText(authUrl || '')"
        @copy-callback="copyText(callbackURL)"
        @copy-mailbox-code="copyText"
        @phone-ready="submitWorkflowPhone"
        @sms-code-received="submitWorkflowSMSCode"
        @select-mailbox="selectedMailboxEmail = $event"
        @create-mailbox="startFlow"
        @open-mailbox="openHistoryMailbox"
        @open-mailbox-share="openCurrentMailboxShare"
        @poll-mailbox="requestMailboxPoll"
        @copy-mailbox="copyText"
        @open-history="scrollToTeamChildHistory"
        @reload-browser="forceReloadBrowserWorkspace"
        @refresh-members="refreshMembers"
        @open-modular="closeBrowserWorkspace"
        @force-take-over="browserTakeOverConfirmOpen = true"
      >
        <template #operation>
          <TeamChildMembersWorkspace
            embedded
            :members="members"
            :pending-invites="pendingInvites"
            :loading="membersLoading"
            :error="membersError"
            :ready="membersReady"
            :seat-email="seatEmail"
            :workspace-name="workspaceName"
            :invitation-email="mailbox?.email || ''"
            :selected-email="selectedMemberEmail"
            :seat-already-removed="manualSeatReady"
            :workflow-ready="workflowReady"
            :workflow-busy="workflowBusy"
            :workflow="teamWorkflow"
            :workflow-continuing="workflowContinuing"
            :browser-configured="browserConfigured"
            :browser-loading="browserLoading"
            @refresh="refreshMembers"
            @inspect="inspectSeat"
            @select="selectedMemberEmail = $event"
            @invite="inviteMember"
            @edit="editMember"
            @remove="removeMember"
            @open-browser="openBrowserWorkspace"
            @start-workflow="openWorkflowConfirmation"
            @continue-workflow="continueWorkflow"
          />
        </template>

        <template #operation-after>
          <section class="team-child-import-workspace mt-4 min-w-0 border-t border-gray-200 pt-4 dark:border-dark-700">
            <div class="flex min-w-0 flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
              <div class="min-w-0">
                <h3 class="text-sm font-semibold text-gray-900 dark:text-gray-100">账号导入配置</h3>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">工作流完成后将按当前分组、并发数和优先级保存到服务器。</p>
              </div>
              <span class="shrink-0 text-xs text-gray-500 dark:text-gray-400">{{ createdAccount ? '已锁定' : '导入前可调整' }}</span>
            </div>

            <fieldset :disabled="Boolean(createdAccount)" class="mt-4 min-w-0">
              <GroupSelector v-model="selectedGroupIDs" :groups="groups" platform="openai" />
            </fieldset>
            <div class="mt-4 grid min-w-0 gap-3 sm:grid-cols-2">
              <div class="min-w-0">
                <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">并发数</label>
                <input v-model.number="accountConcurrency" type="number" min="1" max="1000" class="input w-full" :disabled="Boolean(createdAccount)" />
              </div>
              <div class="min-w-0">
                <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">优先级</label>
                <input v-model.number="accountPriority" type="number" min="1" max="999" class="input w-full" :disabled="Boolean(createdAccount)" />
              </div>
            </div>

            <template v-if="teamWorkflow || callbackURL || createdAccount">
              <label class="mt-4 mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">完整回调 URL</label>
              <textarea v-model="callbackURL" rows="3" class="input w-full resize-none font-mono text-xs" placeholder="http://localhost:1455/auth/callback?code=...&state=..."></textarea>
              <p v-if="parsedCallbackError" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ parsedCallbackError }}</p>
              <div class="mt-3 flex min-w-0 flex-wrap gap-2">
                <button
                  v-if="teamWorkflow && teamWorkflow.status !== 'callback_ready'"
                  type="button"
                  class="btn btn-secondary flex items-center gap-2 whitespace-nowrap"
                  :disabled="!canConfirmCallback || callbackConfirming"
                  @click="confirmWorkflowCallback"
                >
                  <Icon v-if="callbackConfirming" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
                  <Icon v-else name="check" size="sm" :stroke-width="2" />
                  <span>{{ callbackConfirming ? '正在校验回调...' : '确认回调状态' }}</span>
                </button>
                <button v-if="!createdAccount" type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="!canImport || importing" @click="importAccount">
                  <Icon v-if="importing" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
                  <Icon v-else name="upload" size="sm" :stroke-width="2" />
                  <span>{{ importing ? '正在导入...' : '校验并导入' }}</span>
                </button>
              </div>
            </template>

            <div v-if="createdAccount" class="mt-4 min-w-0 rounded-[14px] border border-green-200 bg-green-50 p-4 dark:border-green-900/60 dark:bg-green-950/20">
              <div class="flex min-w-0 items-start gap-3">
                <Icon name="check" size="md" class="mt-0.5 shrink-0 text-green-600 dark:text-green-400" :stroke-width="2.5" />
                <div class="min-w-0 flex-1">
                  <div class="font-semibold text-green-800 dark:text-green-200">已导入 XIASS</div>
                  <div class="mt-1 break-all text-sm text-green-700 dark:text-green-300">{{ createdAccount.name || mailbox?.email }}</div>
                  <div class="mt-2 text-xs text-green-700/80 dark:text-green-300/80">{{ displayedGroupsLabel }} · 并发 {{ displayedImportConfig.concurrency }} · 优先级 {{ displayedImportConfig.priority }}</div>
                </div>
                <button type="button" class="btn btn-secondary shrink-0 whitespace-nowrap" @click="successAccountDialogOpen = true">
                  <Icon name="edit" size="sm" :stroke-width="2" />
                  <span>编辑账号配置</span>
                </button>
              </div>
            </div>
          </section>
        </template>
      </TeamChildOAuthWorkspace>


      <TeamChildHistoryPanel
        ref="teamChildHistoryPanelRef"
        class="xl:col-span-2"
        :entries="teamChildHistory"
        :active-mailbox-email="mailbox?.email || ''"
        :opening-email="historyOpeningEmail"
        :loading="teamChildHistoryLoading"
        :password-loading-account-id="historyPasswordLoadingAccountID"
        :reauthorizing-account-id="historyReauthorizingAccountID"
        :deleting-account-id="historyDeletingAccountID"
        :revealed-passwords="revealedHistoryPasswords"
        @refresh="loadTeamChildHistory"
        @open-mailbox="openHistoryMailbox"
        @toggle-password="toggleHistoryPassword"
        @copy-password="copyText"
        @share-mailbox="openTeamChildMailboxShare"
        @reauthorize="requestHistoryReauthorization"
        @delete-account="deleteHistoryTeamAccount"
      />

      <OpenAIOAuthReauthorizationPanel
        id="openai-oauth-reauthorization"
        class="xl:col-span-2"
        :accounts="ordinaryOpenAIOAuthAccounts"
        :loading="teamChildHistoryLoading"
        :saving="openAIReauthorizationSaving"
        :reauthorizing-account-id="otherOpenAIReauthorizingAccountID"
        @refresh="loadTeamChildHistory"
        @save-credentials="saveOpenAIAccountReauthorizationCredentials"
        @reauthorize="startOrdinaryOpenAIReauthorization"
      />
    </div>
    <ConfirmDialog
      :show="workflowConfirmOpen"
      :title="workflowConfirmationTitle"
      :message="workflowConfirmationMessage"
      :confirm-text="manualSeatReady ? '邀请并授权' : '移除并邀请'"
      cancel-text="取消"
      danger
      @confirm="startConfirmedWorkflow"
      @cancel="workflowConfirmOpen = false"
    />
    <ConfirmDialog
      :show="browserTakeOverConfirmOpen"
      title="接管服务器浏览器"
      message="另一台设备可能正在查看同一个服务器浏览器。接管会结束对方的图形浏览器控制，但不会影响已登录状态或正在运行的自动化工作流。"
      confirm-text="确认接管"
      cancel-text="取消"
      @confirm="confirmBrowserTakeOver"
      @cancel="browserTakeOverConfirmOpen = false"
    />
    <ConfirmDialog
      :show="Boolean(pendingHistoryReauthorization)"
      title="确认重新授权"
      :message="pendingHistoryReauthorizationMessage"
      confirm-text="确认重新授权"
      cancel-text="暂不处理"
      @confirm="confirmHistoryReauthorization"
      @cancel="pendingHistoryReauthorization = null"
    />
    <TotpStepUpDialog :controller="passwordStepUp" />
    <TeamChildMailboxShareDialog
      :show="Boolean(mailboxShareHistoryEntry || mailboxShareCurrentEmail)"
      :account-id="mailboxShareHistoryEntry?.account?.id || null"
      :email="mailboxShareHistoryEntry?.email || mailboxShareCurrentEmail"
      :scope="mailboxShareHistoryEntry ? 'account' : 'mailbox'"
      @close="closeTeamMailboxShareDialog"
    />
    <TeamChildAccountSuccessDialog
      :show="successAccountDialogOpen"
      :account="createdAccount"
      :groups="groups"
      :group-ids="successAccountGroupIDs"
      :concurrency="successAccountConcurrency"
      :priority="successAccountPriority"
      :saving="successAccountSaving"
      @close="successAccountDialogOpen = false"
      @update:group-ids="successAccountGroupIDs = $event"
      @update:concurrency="successAccountConcurrency = $event"
      @update:priority="successAccountPriority = $event"
      @save="saveCreatedAccountConfiguration"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Icon } from '@/components/icons'
import TeamChildOAuthWorkspace from '@/components/admin/account/TeamChildOAuthWorkspace.vue'
import TeamChildMembersWorkspace from '@/components/admin/account/TeamChildMembersWorkspace.vue'
import TeamChildHistoryPanel from '@/components/admin/account/TeamChildHistoryPanel.vue'
import TeamChildMailboxShareDialog from '@/components/admin/account/TeamChildMailboxShareDialog.vue'
import TeamChildAccountSuccessDialog from '@/components/admin/account/TeamChildAccountSuccessDialog.vue'
import OpenAIOAuthReauthorizationPanel from '@/components/admin/account/OpenAIOAuthReauthorizationPanel.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useAppStore } from '@/stores/app'
import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import {
  teamChildAPI,
  type TeamChildMailbox,
  type TeamChildMember,
  type TeamChildWorkflow,
  type TeamChildWorkflowNodeKey
} from '@/api/admin/teamChild'
import { accountsAPI } from '@/api/admin/accounts'
import { groupsAPI } from '@/api/admin/groups'
import type { Account, AccountUsageInfo, AdminGroup } from '@/types'

type FlowStatus = 'idle' | 'creating' | 'ready' | 'workflow' | 'waiting' | 'polling' | 'received' | 'callback' | 'importing' | 'completed' | 'error'
type TeamChildHistoryEntry = { email: string; account: Account | null; usage: AccountUsageInfo | null; passwordAvailable: boolean }

const appStore = useAppStore()
const openaiOAuth = useOpenAIOAuth()
const passwordStepUp = useStepUp()
const status = ref<FlowStatus>('idle')
const mailboxConfigured = ref(false)
const browserConfigured = ref(false)
const browserEmbedURL = ref('')
const browserLoading = ref(false)
const browserError = ref('')
const browserVisible = ref(false)
const browserControllerID = ref('')
const browserControlConflict = ref(false)
const browserTakeOverConfirmOpen = ref(false)
const browserHeartbeatFailures = ref(0)
const membersReady = ref(false)
const membersLoading = ref(false)
const membersError = ref('')
const members = ref<TeamChildMember[]>([])
const pendingInvites = ref(0)
const seatEmail = ref('')
const selectedMemberEmail = ref('')
const workspaceName = ref('')
const configImporting = ref(false)
const mailboxConfigFileInput = ref<HTMLInputElement | null>(null)
const teamChildHistoryPanelRef = ref<InstanceType<typeof TeamChildHistoryPanel> | null>(null)
const mailbox = ref<TeamChildMailbox | null>(null)
const knownMailboxes = ref<string[]>([])
const teamChildHistory = ref<TeamChildHistoryEntry[]>([])
const openAIOAuthAccounts = ref<Account[]>([])
const teamChildHistoryLoading = ref(false)
const historyOpeningEmail = ref('')
const historyPasswordLoadingAccountID = ref<number | null>(null)
const historyReauthorizingAccountID = ref<number | null>(null)
const historyDeletingAccountID = ref<number | null>(null)
const revealedHistoryPasswords = ref<Record<number, string>>({})
const pendingHistoryReauthorization = ref<TeamChildHistoryEntry | null>(null)
const mailboxShareHistoryEntry = ref<TeamChildHistoryEntry | null>(null)
const mailboxShareCurrentEmail = ref('')
const openAIReauthorizationSaving = ref(false)
const otherOpenAIReauthorizingAccountID = ref<number | null>(null)
const selectedMailboxEmail = ref('')
const mailboxSelecting = ref(false)
const mailboxCode = ref('')
const mailboxCodeLoading = ref(false)
const mailboxCodeError = ref('')
const mailboxWaitingPolls = ref(0)
const authUrl = openaiOAuth.authUrl
const oauthState = openaiOAuth.oauthState
const oauthSessionID = openaiOAuth.sessionId
const oauthGenerating = openaiOAuth.loading
const oauthError = openaiOAuth.error
const callbackURL = ref('')
const groups = ref<AdminGroup[]>([])
const selectedGroupIDs = ref<number[]>([])
const accountConcurrency = ref(10)
const accountPriority = ref(1)
const errorMessage = ref('')
const createdAccount = ref<Account | null>(null)
const successAccountDialogOpen = ref(false)
const successAccountSaving = ref(false)
const successAccountGroupIDs = ref<number[]>([])
const successAccountConcurrency = ref(10)
const successAccountPriority = ref(1)
const appliedImportConfig = ref<{ groupIDs: number[]; groupNames: string[]; concurrency: number; priority: number } | null>(null)
const teamWorkflow = ref<TeamChildWorkflow | null>(null)
const revealedWorkflowPassword = ref('')
const passwordLoading = ref(false)
const workflowConfirmOpen = ref(false)
const workflowStarting = ref(false)
const workflowContinuing = ref(false)
const callbackConfirming = ref(false)
const emailCodeSubmitting = ref(false)
const phoneSubmitting = ref(false)
const smsCodeSubmitting = ref(false)
const smsCancelSignal = ref(0)
// Restoring persisted state must never grant permission to resume browser,
// mailbox, or SMS actions. Only an explicit start/continue click arms them.
const workflowExecutionArmed = ref(false)
const mailboxPollingRequested = ref(false)
const lastSubmittedEmailCode = ref('')
const lastSubmittedPhone = ref('')
const lastSubmittedSMSCode = ref('')
const submittedEmailCodes = new Set<string>()
let lastEmailCodeGeneration = 0
let pollTimer: ReturnType<typeof setTimeout> | null = null
let pollTimerDueAt = 0
let workflowPollTimer: ReturnType<typeof setTimeout> | null = null
let browserHeartbeatTimer: ReturnType<typeof setTimeout> | null = null
let historyUsageRefreshTimer: ReturnType<typeof setTimeout> | null = null
let mailboxSessionRefreshInFlight = false
let historyUsageRefreshInFlight = false
let teamChildViewUnmounted = false

function resetEmailCodeTracking() {
  lastEmailCodeGeneration = 0
  submittedEmailCodes.clear()
  mailboxCode.value = ''
  mailboxCodeError.value = ''
  lastSubmittedEmailCode.value = ''
  resetMailboxPollRecovery()
}

function syncWorkflowEmailChallenge(workflow: TeamChildWorkflow) {
  const generation = Math.max(0, Math.trunc(Number(workflow.email_code_generation || 0)))
  if (generation <= lastEmailCodeGeneration) return
  lastEmailCodeGeneration = generation
  mailboxCode.value = ''
  mailboxCodeError.value = ''
  lastSubmittedEmailCode.value = ''
  resetMailboxPollRecovery()
  mailboxPollingRequested.value = true
  if (workflowExecutionArmed.value) schedulePoll(50)
}

function assignTeamWorkflow(workflow: TeamChildWorkflow | null) {
  if ((teamWorkflow.value?.id || '') !== (workflow?.id || '')) resetEmailCodeTracking()
  teamWorkflow.value = workflow
  if (workflow) syncWorkflowEmailChallenge(workflow)
  return workflow
}

const workflowNodeDefinitions: Array<[TeamChildWorkflowNodeKey, string]> = [
  ['signup', '隐私页注册新邮箱'], ['email', '填入注册邮箱'], ['mail', '发送注册邮箱验证码'],
  ['mailbox', '读取注册邮箱验证码'], ['email_code', '提交注册邮箱验证码'], ['members', '读取成员席位'],
  ['remove', '移除已选成员'], ['invite', '提交成员邀请'], ['invite_confirm', '确认 Pending invites'],
  ['oauth', '同一隐私页打开 OAuth'], ['password', '完成 OAuth 邮箱登录验证'], ['phone', '进入手机号页面'],
  ['sms_confirm', '确认领取手机号'], ['phone_submit', '填入号码并选择 Text message'], ['sms_poll', '轮询短信验证码'],
  ['sms_code', '自动填入短信验证码'], ['profile_wait', '等待资料页 5 秒'], ['profile', '填写 black / 26'],
  ['workspace_wait', '等待工作空间 10 秒'], ['workspace', '默认工作空间继续'], ['callback', '捕获 OAuth 回调'],
  ['import', '按勾选配置导入 XIASS']
]

const busy = computed(() => ['creating', 'polling', 'workflow', 'importing'].includes(status.value) || workflowStarting.value || workflowContinuing.value)
// Mailbox polling is a background task and must not make workflow controls
// appear, disappear, or resize while the refresh icon is spinning.
const importing = computed(() => status.value === 'importing')
const mailboxCodePolling = computed(() => Boolean(mailbox.value)
  && !mailboxCode.value
  && !['completed', 'callback'].includes(status.value)
  && (workflowExecutionArmed.value || mailboxPollingRequested.value))
const statusLabel = computed(() => ({ idle: '未开始', creating: '创建中', ready: '等待授权', workflow: '自动化执行中', waiting: '等待当前节点', polling: '检查邮箱', received: '已收到验证码', callback: '已获取回调', importing: '正在导入', completed: '已完成', error: '需要处理' })[status.value])
const statusTone = computed(() => {
  if (status.value === 'error') return { text: 'text-red-600 dark:text-red-400', icon: 'exclamationTriangle' as const }
  if (status.value === 'completed' || status.value === 'received' || status.value === 'callback') return { text: 'text-green-600 dark:text-green-400', icon: 'check' as const }
  if (busy.value) return { text: 'text-primary-600 dark:text-primary-400', icon: 'refresh' as const }
  return { text: 'text-gray-500 dark:text-gray-400', icon: 'clock' as const }
})
const parsedCallback = computed(() => {
  if (!callbackURL.value.trim()) return null
  try {
    const parsed = new URL(callbackURL.value.trim())
    const code = parsed.searchParams.get('code')?.trim() || ''
    const state = parsed.searchParams.get('state')?.trim() || ''
    if (!code || !state) return null
    return { code, state }
  } catch {
    return null
  }
})
const parsedCallbackError = computed(() => {
  if (!callbackURL.value.trim()) return ''
  if (!parsedCallback.value) return '请输入包含 code 和 state 的完整回调 URL。'
  if (oauthState.value && parsedCallback.value.state !== oauthState.value) return '回调 state 与本次授权不匹配，请重新粘贴当前会话的回调地址。'
  return ''
})
function isReplaceableMember(member: TeamChildMember | undefined) {
  return Boolean(member) && /^(member|成员)$/i.test(member!.role.trim()) && !member!.protected
}
function isProtectedMember(member: TeamChildMember) {
  return Boolean(member.protected) || /^(owner|所有者|admin|administrator|管理员)$/i.test(member.role.trim())
}
const selectedReplaceableMember = computed(() => members.value.find((member) => normalizeTeamChildEmail(member.email) === normalizeTeamChildEmail(selectedMemberEmail.value)))
const workflowBusy = computed(() => workflowStarting.value || workflowContinuing.value || (workflowExecutionArmed.value && teamWorkflow.value?.status === 'running'))
const workspaceOneClickLabel = computed(() => {
  if (workflowContinuing.value || (workflowExecutionArmed.value && teamWorkflow.value?.status === 'running')) return '自动执行中'
  if (!workflowExecutionArmed.value && ['running', 'manual_required'].includes(teamWorkflow.value?.status || '')) return '继续自动化'
  if (['failed', 'paused'].includes(teamWorkflow.value?.status || '')) return '继续自动化'
  if (teamWorkflow.value?.status === 'callback_ready') return '准备导入'
  if (teamWorkflow.value?.status === 'completed') return '已完成'
  return '一键授权'
})
const workflowNeedsReauth = computed(() => {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return false
  const messages = [workflow.error || '', ...workflow.nodes.map((node) => node.message || '')].join(' ')
  return /\b401\b|unauthori[sz]ed|invalid_auth_step|authorization step|授权步骤.*失效|token\s*(?:已|已被)?(?:失效|过期)|重新授权/i.test(messages)
})
const manualSeatReady = computed(() => membersReady.value && members.value.length > 0 && !members.value.some((member) => isReplaceableMember(member)) && members.value.every(isProtectedMember))
const preflightWorkflow = computed<TeamChildWorkflow>(() => {
  const currentKey: TeamChildWorkflowNodeKey = 'signup'
  const currentIndex = workflowNodeDefinitions.findIndex(([key]) => key === currentKey)
  const failed = status.value === 'error'
  return {
    schema_version: 4,
    id: 'team-child-preflight',
    status: failed ? 'failed' : 'manual_required',
    expires_at: mailbox.value?.expires_at || '',
    manual_required: true,
    current_node: currentKey,
    error: failed ? errorMessage.value : undefined,
    password_available: false,
    nodes: workflowNodeDefinitions.map(([key, label], index) => ({
      key,
      label,
      number: index + 1,
      status: index < currentIndex ? 'completed' : index === currentIndex ? (failed ? 'failed' : 'waiting') : 'pending'
    }))
  }
})
const displayWorkflow = computed<TeamChildWorkflow>(() => teamWorkflow.value || preflightWorkflow.value)
const workspaceOneClickDisabled = computed(() => {
  if (teamWorkflow.value) {
    if (!workflowExecutionArmed.value && ['running', 'manual_required'].includes(teamWorkflow.value.status)) return workflowContinuing.value
    return !['failed', 'paused'].includes(teamWorkflow.value.status) || workflowContinuing.value
  }
  return busy.value || !mailboxConfigured.value || !browserConfigured.value
})
const workflowReady = computed(() => Boolean(mailbox.value?.email) && Boolean(authUrl.value) && (isReplaceableMember(selectedReplaceableMember.value) || manualSeatReady.value) && membersReady.value && !workflowBusy.value && !['manual_required', 'callback_ready', 'failed', 'paused'].includes(teamWorkflow.value?.status || ''))
const workflowConfirmationTitle = computed(() => manualSeatReady.value ? '确认邀请并授权' : '确认替换成员并授权')
const workflowConfirmationMessage = computed(() => {
  if (!mailbox.value?.email) return '当前缺少临时邮箱。'
  if (manualSeatReady.value) return `将先在独立隐私页使用 ${mailbox.value.email} 完成 ChatGPT 注册。注册成功后不移除任何成员，直接发送邀请并确认 Pending invites；随后在同一隐私页打开 OAuth。若 OAuth 仍显示登录页，系统会再次输入该邮箱并读取新验证码。`
  if (!selectedMemberEmail.value) return '当前缺少待替换成员。'
  return `将先在独立隐私页使用 ${mailbox.value.email} 完成 ChatGPT 注册。确认注册成功后，才从工作区移除 ${selectedMemberEmail.value}、发送邀请并核对 Pending invites；随后在同一隐私页打开 OAuth。若 OAuth 仍显示登录页，系统会再次输入该邮箱并读取新验证码。`
})
const canImport = computed(() => Boolean(parsedCallback.value)
  && !parsedCallbackError.value
  && (Boolean(mailbox.value) || Boolean(currentReauthorizationAccount()))
  && Boolean(oauthSessionID.value)
  && Boolean(teamWorkflow.value?.id)
  && !importing.value)
const canConfirmCallback = computed(() => Boolean(teamWorkflow.value) && Boolean(parsedCallback.value) && !parsedCallbackError.value && !callbackConfirming.value)
const normalizedConcurrency = computed(() => Math.min(1000, Math.max(1, Math.trunc(Number(accountConcurrency.value) || 10))))
const normalizedPriority = computed(() => Math.min(999, Math.max(1, Math.trunc(Number(accountPriority.value) || 1))))
const selectedGroupNames = computed(() => selectedGroupIDs.value
  .map((id) => groups.value.find((group) => group.id === id)?.name || `#${id}`))
const displayedImportConfig = computed(() => appliedImportConfig.value || {
  groupIDs: selectedGroupIDs.value,
  groupNames: selectedGroupNames.value,
  concurrency: normalizedConcurrency.value,
  priority: normalizedPriority.value
})
const displayedGroupsLabel = computed(() => displayedImportConfig.value.groupNames.length > 0 ? displayedImportConfig.value.groupNames.join('、') : '未选择分组')
const ordinaryOpenAIOAuthAccounts = computed(() => openAIOAuthAccounts.value.filter((account) => {
  const extra = account.extra as Record<string, unknown> | undefined
  return account.platform === 'openai'
    && account.type === 'oauth'
    && account.parent_account_id == null
    && extra?.xiass_team_child !== true
}))
const pendingHistoryReauthorizationMessage = computed(() => {
  const entry = pendingHistoryReauthorization.value
  if (!entry?.account) return '将使用该账号已保存的登录信息重新运行 OpenAI OAuth 登录流程。'
  return historyEntryNeedsReauth(entry)
    ? `检测到 ${entry.email} 的 OpenAI OAuth 凭据返回 401。确认后会重新运行登录授权流程，不会重复移除成员或发送邀请。`
    : `将为 ${entry.email} 重新运行 OpenAI OAuth 登录流程，不会重复移除成员或发送邀请。`
})

function normalizedGroupIDs(groupIDs: readonly number[]): number[] {
  return [...new Set(groupIDs.filter((id) => Number.isSafeInteger(id) && id > 0))]
}

// Older create-account responses did not carry freshly persisted group_ids.
// Only fall back to the pre-import selection when the field is absent. An
// explicit empty list is a real persisted state and must never be papered over
// by a stale UI selection.
function persistedOrImportedGroupIDs(account: Account | null | undefined, fallback: readonly number[] = []): number[] {
  if (Array.isArray(account?.group_ids)) return normalizedGroupIDs(account.group_ids)
  if (Array.isArray(account?.groups)) return normalizedGroupIDs(account.groups.map((group) => group.id))
  return normalizedGroupIDs(fallback)
}

function assertOfficialOAuthSession(auth: { auth_url: string; session_id: string }) {
  const parsed = new URL(auth.auth_url)
  const query = parsed.searchParams
  const valid = parsed.protocol === 'https:'
    && parsed.hostname.toLowerCase() === 'auth.openai.com'
    && parsed.pathname === '/oauth/authorize'
    && query.get('response_type') === 'code'
    && query.get('client_id') === 'app_EMoamEEZ73f0CkXaXp7hrann'
    && query.get('redirect_uri') === 'http://localhost:1455/auth/callback'
    && query.get('scope') === 'openid profile email offline_access'
    && Boolean(query.get('state'))
    && Boolean(query.get('code_challenge'))
    && query.get('code_challenge_method') === 'S256'
    && query.get('codex_cli_simplified_flow') === 'true'
    && query.get('id_token_add_organizations') === 'true'
  if (!valid || !auth.session_id) throw new Error('内置 OpenAI OAuth 会话无效，请重新生成授权链接')
}

function normalizeTeamChildEmail(value: string) {
  const normalized = String(value || '')
    .normalize('NFKC')
    .replace(/[\u200B-\u200D\uFEFF]/g, '')
    .trim()
    .toLowerCase()
  return normalized.match(/[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}/i)?.[0] || normalized
}

async function generateFreshOAuthSession(proxyID?: number | null) {
  if (oauthGenerating.value) return null
  callbackURL.value = ''
  try {
    // Delegate to the same composable used by the standard Add Account form.
    // The XIASS backend owns PKCE, state and session persistence; this view
    // only consumes the returned session.
    const generated = await openaiOAuth.generateAuthUrl(proxyID)
    if (!generated || !authUrl.value || !oauthSessionID.value) {
      throw new Error(oauthError.value || '生成 XIASS 官方 OpenAI 授权链接失败')
    }
    const auth = { auth_url: authUrl.value, session_id: oauthSessionID.value }
    assertOfficialOAuthSession(auth)
    return auth
  } catch (error) {
    const message = oauthError.value || extractApiErrorMessage(error, '生成 XIASS 官方 OpenAI 授权链接失败')
    openaiOAuth.resetState()
    oauthError.value = message
    throw error
  }
}

async function copyText(value: string) {
  try { await navigator.clipboard.writeText(value); appStore.showSuccess('已复制') } catch { appStore.showError('复制失败，请手动复制') }
}
async function loadKnownMailboxes() {
  if (!mailboxConfigured.value) return
  try {
    knownMailboxes.value = await teamChildAPI.listMailboxes()
  } catch {
    knownMailboxes.value = []
  }
}

function teamChildAccountEmail(account: Account): string {
  const extra = account.extra as Record<string, unknown> | undefined
  const stored = typeof extra?.xiass_team_child_email === 'string' ? extra.xiass_team_child_email : ''
  const candidate = stored || account.name
  return normalizeTeamChildEmail(candidate)
}

function isTeamChildAccount(account: Account): boolean {
  const extra = account.extra as Record<string, unknown> | undefined
  return extra?.xiass_team_child === true
}

function openAIOAuthAccountEmail(account: Account): string {
  const credentialEmail = typeof account.credentials?.email === 'string' ? account.credentials.email : ''
  return normalizeTeamChildEmail(credentialEmail || account.name)
}

function currentReauthorizationAccount(): Account | null {
  const accountID = teamWorkflow.value?.mode === 'reauthorization'
    ? teamWorkflow.value.target_account_id
    : undefined
  if (!accountID) return null
  return openAIOAuthAccounts.value.find((account) => account.id === accountID)
    || teamChildHistory.value.find((entry) => entry.account?.id === accountID)?.account
    || null
}

async function loadTeamChildHistory() {
  if (teamChildHistoryLoading.value) return
  teamChildHistoryLoading.value = true
  try {
    const [mailboxResult, accountResult] = await Promise.allSettled([
      teamChildAPI.listMailboxes(),
      accountsAPI.list(1, 200, { platform: 'openai', type: 'oauth' })
    ])
    const mailboxEmails = mailboxResult.status === 'fulfilled' ? mailboxResult.value : []
    const accounts = accountResult.status === 'fulfilled' && Array.isArray(accountResult.value?.items)
      ? accountResult.value.items
      : []
    openAIOAuthAccounts.value = accounts
    const teamAccounts = accounts.filter((account) => {
      return isTeamChildAccount(account) && Boolean(teamChildAccountEmail(account))
    })
    // Team history needs the current OpenAI quota snapshot, including the 5h
    // window. The active forced path updates the provider-backed cache instead
    // of repeatedly rendering a stale passive result.
    const usageResults = await Promise.allSettled(teamAccounts.map((account) => accountsAPI.getUsage(account.id, 'active', true)))
    const usageByAccountID = new Map<number, AccountUsageInfo>()
    usageResults.forEach((result, index) => {
      if (result.status === 'fulfilled') usageByAccountID.set(teamAccounts[index].id, result.value)
    })
    const byEmail = new Map<string, { email: string; account: Account | null; usage: AccountUsageInfo | null; passwordAvailable: boolean }>()
    mailboxEmails.forEach((email) => {
      const normalized = normalizeTeamChildEmail(email)
      if (normalized) byEmail.set(normalized, { email: normalized, account: null, usage: null, passwordAvailable: false })
    })
    teamAccounts.forEach((account) => {
      const email = teamChildAccountEmail(account)
      if (!email) return
      const existing = byEmail.get(email)
      byEmail.set(email, {
        email,
        account,
        usage: usageByAccountID.get(account.id) || null,
        passwordAvailable: account.credentials_status?.has_xiass_team_child_password_encrypted === true || existing?.passwordAvailable === true
      })
    })
    if (mailbox.value?.email) {
      const email = normalizeTeamChildEmail(mailbox.value.email)
      if (email && !byEmail.has(email)) byEmail.set(email, { email, account: null, usage: null, passwordAvailable: false })
    }
    teamChildHistory.value = [...byEmail.values()].sort((left, right) => left.email.localeCompare(right.email))
  } catch {
    // History is supplementary. A stale usage endpoint or an expired mailbox
    // session must never delay or interrupt member/OAuth workflow startup.
    teamChildHistory.value = mailbox.value?.email
      ? [{ email: normalizeTeamChildEmail(mailbox.value.email), account: null, usage: null, passwordAvailable: false }]
      : []
    openAIOAuthAccounts.value = []
  } finally {
    teamChildHistoryLoading.value = false
    scheduleTeamChildHistoryUsageRefresh()
  }
}

function replaceOpenAIOAuthAccount(updated: Account) {
  openAIOAuthAccounts.value = openAIOAuthAccounts.value.map((account) => account.id === updated.id ? updated : account)
  teamChildHistory.value = teamChildHistory.value.map((entry) => entry.account?.id === updated.id
    ? {
        ...entry,
        account: updated,
        passwordAvailable: updated.credentials_status?.has_xiass_team_child_password_encrypted === true
      }
    : entry)
}

async function saveOpenAIAccountReauthorizationCredentials(payload: { account: Account; email: string; password: string; totp_secret?: string }) {
  if (openAIReauthorizationSaving.value) return
  openAIReauthorizationSaving.value = true
  try {
    const updated = await teamChildAPI.saveOpenAIAccountReauthorizationCredentials(payload.account.id, {
      email: payload.email,
      password: payload.password,
      ...(payload.totp_secret !== undefined ? { totp_secret: payload.totp_secret } : {})
    })
    replaceOpenAIOAuthAccount(updated)
    appStore.showSuccess('OpenAI OAuth 登录信息已加密保存')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '保存 OpenAI OAuth 登录信息失败'))
  } finally {
    openAIReauthorizationSaving.value = false
  }
}

async function startOrdinaryOpenAIReauthorization(account: Account) {
  if (otherOpenAIReauthorizingAccountID.value !== null) return
  if (account.credentials_status?.has_xiass_openai_oauth_reauth_email !== true) {
    appStore.showError('请先保存该账号的登录邮箱')
    return
  }
  if (teamWorkflow.value && ['running', 'manual_required', 'callback_ready', 'paused'].includes(teamWorkflow.value.status)) {
    if (teamWorkflow.value.mode === 'reauthorization' && teamWorkflow.value.target_account_id === account.id) {
      appStore.showInfo('该账号的重新授权工作流已经在运行')
    } else {
      appStore.showError('已有 Team 子号工作流正在运行，请先完成当前流程')
    }
    return
  }

  otherOpenAIReauthorizingAccountID.value = account.id
  errorMessage.value = ''
  createdAccount.value = null
  appliedImportConfig.value = null
  callbackURL.value = ''
  mailboxCode.value = ''
  mailboxCodeError.value = ''
  mailboxPollingRequested.value = false
  workflowExecutionArmed.value = true
  try {
    if (teamWorkflow.value?.status === 'failed') {
      await teamChildAPI.cancelTeamChildWorkflow(teamWorkflow.value.id)
      smsCancelSignal.value += 1
      assignTeamWorkflow(null)
      lastSubmittedEmailCode.value = ''
      lastSubmittedPhone.value = ''
      lastSubmittedSMSCode.value = ''
    }

    const accountEmail = openAIOAuthAccountEmail(account)
    const matchingMailbox = knownMailboxes.value.find((email) => normalizeTeamChildEmail(email) === accountEmail)
    if (matchingMailbox) {
      const selected = await teamChildAPI.selectMailbox(matchingMailbox)
      mailbox.value = selected
      selectedMailboxEmail.value = selected.email
      mailboxPollingRequested.value = true
      resetMailboxPollRecovery()
      schedulePoll(250)
    } else {
      // Never reuse a previously selected Team mailbox for another account.
      // A non-Team account can finish the official email verification manually
      // in the embedded browser and still import its callback safely.
      mailbox.value = null
      selectedMailboxEmail.value = ''
    }

    const auth = await generateFreshOAuthSession(account.proxy_id)
    if (!auth) throw new Error('未生成 XIASS 官方 OpenAI 授权链接')
    assignTeamWorkflow(await teamChildAPI.reauthorizeOpenAIAccount(account.id, auth.auth_url, auth.session_id))
    status.value = 'workflow'
    appStore.showInfo(matchingMailbox
      ? `正在为 ${account.name} 重新授权，并自动轮询匹配邮箱`
      : `正在为 ${account.name} 重新授权；如需邮箱验证码，请在内嵌浏览器处理后粘贴回调 URL`)
    scheduleWorkflowPoll(500)
  } catch (error) {
    workflowExecutionArmed.value = false
    mailboxPollingRequested.value = false
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, 'OpenAI OAuth 重新授权启动失败')
  } finally {
    otherOpenAIReauthorizingAccountID.value = null
  }
}

function clearTeamChildHistoryUsageRefresh() {
  if (historyUsageRefreshTimer) clearTimeout(historyUsageRefreshTimer)
  historyUsageRefreshTimer = null
}

function scheduleTeamChildHistoryUsageRefresh(delay = 60_000) {
  clearTeamChildHistoryUsageRefresh()
  if (teamChildViewUnmounted || teamChildHistory.value.every((entry) => !entry.account)) return
  historyUsageRefreshTimer = setTimeout(() => void refreshTeamChildHistoryUsage(), delay)
}

async function refreshTeamChildHistoryUsage() {
  if (historyUsageRefreshInFlight || teamChildViewUnmounted) return
  const accounts = teamChildHistory.value
    .map((entry) => entry.account)
    .filter((account): account is Account => Boolean(account))
  if (accounts.length === 0) return

  historyUsageRefreshInFlight = true
  try {
    // The first page load and the explicit history refresh intentionally force
    // a fresh provider snapshot. The background pass stays cache-aware so a
    // history list with many Team accounts does not fan out an upstream quota
    // request every minute while the view remains open.
    const results = await Promise.allSettled(accounts.map((account) => accountsAPI.getUsage(account.id, 'active')))
    if (teamChildViewUnmounted) return
    const usageByAccountID = new Map<number, AccountUsageInfo>()
    results.forEach((result, index) => {
      if (result.status === 'fulfilled') usageByAccountID.set(accounts[index].id, result.value)
    })
    if (usageByAccountID.size > 0) {
      teamChildHistory.value = teamChildHistory.value.map((entry) => entry.account && usageByAccountID.has(entry.account.id)
        ? { ...entry, usage: usageByAccountID.get(entry.account.id) || entry.usage }
        : entry)
    }
  } finally {
    historyUsageRefreshInFlight = false
    scheduleTeamChildHistoryUsageRefresh()
  }
}

async function openHistoryMailbox(email: string) {
  if (workflowBusy.value) {
    appStore.showInfo('当前自动化正在运行，完成或停止后再切换邮箱')
    return
  }
  historyOpeningEmail.value = email
  selectedMailboxEmail.value = email
  try {
    await selectExistingMailbox()
  } finally {
    historyOpeningEmail.value = ''
  }
}

async function deleteHistoryTeamAccount(entry: TeamChildHistoryEntry) {
  const account = entry.account
  if (!account || historyDeletingAccountID.value !== null) return
  historyDeletingAccountID.value = account.id
  try {
    await accountsAPI.delete(account.id)
    const nextPasswords = { ...revealedHistoryPasswords.value }
    delete nextPasswords[account.id]
    revealedHistoryPasswords.value = nextPasswords
    await loadTeamChildHistory()
    appStore.showSuccess(`已删除 Team 账号：${entry.email}`)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '删除 Team 账号失败'))
  } finally {
    historyDeletingAccountID.value = null
  }
}

function historyEntryNeedsReauth(entry: TeamChildHistoryEntry): boolean {
  const account = entry.account
  if (!account) return false
  const extra = account.extra as Record<string, unknown> | undefined
  const errorText = [
    entry.usage?.error || '',
    entry.usage?.error_code || '',
    account.error_message || '',
    account.temp_unschedulable_reason || '',
    typeof extra?.error === 'string' ? extra.error : '',
    typeof extra?.error_code === 'string' ? extra.error_code : ''
  ].join(' ')
  return entry.usage?.needs_reauth === true
    || extra?.needs_reauth === true
    || entry.usage?.error_code === 'unauthenticated'
    || extra?.error_code === 'unauthenticated'
    || /\b401\b|unauthori[sz]ed|invalid[_ ](?:grant|token)|token\s*(?:expired|invalid|失效|过期)/i.test(errorText)
}

async function startHistoryReauthorization(entry: TeamChildHistoryEntry) {
  const account = entry.account
  if (!account || historyReauthorizingAccountID.value !== null) return
  const detected401 = historyEntryNeedsReauth(entry)
  if (teamWorkflow.value && ['running', 'manual_required', 'callback_ready', 'paused'].includes(teamWorkflow.value.status)) {
    if (teamWorkflow.value.mode === 'reauthorization' && teamWorkflow.value.target_account_id === account.id) {
      appStore.showInfo('该账号的重新授权工作流已经在运行')
    } else {
      appStore.showError('已有 Team 子号工作流正在运行，请先完成当前流程')
    }
    return
  }

  historyReauthorizingAccountID.value = account.id
  errorMessage.value = ''
  createdAccount.value = null
  appliedImportConfig.value = null
  callbackURL.value = ''
  workflowExecutionArmed.value = true
  mailboxPollingRequested.value = true
  try {
    if (teamWorkflow.value?.status === 'failed') {
      await teamChildAPI.cancelTeamChildWorkflow(teamWorkflow.value.id)
      smsCancelSignal.value += 1
      assignTeamWorkflow(null)
      lastSubmittedEmailCode.value = ''
      lastSubmittedPhone.value = ''
      lastSubmittedSMSCode.value = ''
    }
    const selected = await teamChildAPI.selectMailbox(entry.email)
    mailbox.value = selected
    selectedMailboxEmail.value = selected.email
    mailboxCode.value = ''
    mailboxCodeError.value = ''
    lastSubmittedEmailCode.value = ''
    schedulePoll(250)

    const auth = await generateFreshOAuthSession(account.proxy_id)
    if (!auth) throw new Error('未生成 XIASS 官方 OpenAI 授权链接')
    assignTeamWorkflow(await teamChildAPI.reauthorizeTeamChildAccount(account.id, auth.auth_url, auth.session_id))
    status.value = 'workflow'
    appStore.showInfo(detected401
      ? `检测到 401，正在为 ${entry.email} 重新授权`
      : `正在为 ${entry.email} 手动重新授权`)
    scheduleWorkflowPoll(500)
  } catch (error) {
    workflowExecutionArmed.value = false
    mailboxPollingRequested.value = false
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, 'Team 账号重新授权启动失败')
  } finally {
    historyReauthorizingAccountID.value = null
  }
}

function requestHistoryReauthorization(entry: TeamChildHistoryEntry) {
  if (!entry.account) return
  pendingHistoryReauthorization.value = entry
}

function openTeamChildMailboxShare(entry: TeamChildHistoryEntry) {
  if (!entry.account) return
  mailboxShareCurrentEmail.value = ''
  mailboxShareHistoryEntry.value = entry
}

function openCurrentMailboxShare(email: string) {
  const normalized = normalizeTeamChildEmail(email)
  if (!normalized) return
  // The same header button is available before and after import. Once this
  // mailbox belongs to an imported Team account, use the account-bound route
  // so deleting that account also invalidates its shared inbox link.
  const historyEntry = teamChildHistory.value.find((entry) => {
    return normalizeTeamChildEmail(entry.email) === normalized && Boolean(entry.account)
  })
  mailboxShareHistoryEntry.value = historyEntry || null
  mailboxShareCurrentEmail.value = historyEntry ? '' : normalized
}

function closeTeamMailboxShareDialog() {
  mailboxShareHistoryEntry.value = null
  mailboxShareCurrentEmail.value = ''
}

async function confirmHistoryReauthorization() {
  const entry = pendingHistoryReauthorization.value
  pendingHistoryReauthorization.value = null
  if (entry) await startHistoryReauthorization(entry)
}

function requestHistoryReauthorizationFromURL() {
  const currentURL = new URL(window.location.href)
  const raw = currentURL.searchParams.get('reauthorize')
  const accountID = Number(raw)
  if (!Number.isSafeInteger(accountID) || accountID <= 0) return
  currentURL.searchParams.delete('reauthorize')
  window.history.replaceState(window.history.state, '', `${currentURL.pathname}${currentURL.search}${currentURL.hash}`)
  if (teamWorkflow.value) return
  const entry = teamChildHistory.value.find((candidate) => candidate.account?.id === accountID)
  if (entry && historyEntryNeedsReauth(entry)) requestHistoryReauthorization(entry)
}

async function openReauthorizationManagementFromURL() {
  const currentURL = new URL(window.location.href)
  if (currentURL.searchParams.get('openai_reauthorization') !== '1') return
  currentURL.searchParams.delete('openai_reauthorization')
  window.history.replaceState(window.history.state, '', `${currentURL.pathname}${currentURL.search}${currentURL.hash}`)
  await nextTick()
  document.getElementById('openai-oauth-reauthorization')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function scrollToTeamChildHistory() {
  const element = teamChildHistoryPanelRef.value?.$el as HTMLElement | undefined
  element?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function toggleHistoryPassword(entry: { email: string; account: Account | null }) {
  const account = entry.account
  if (!account) return
  if (revealedHistoryPasswords.value[account.id]) {
    const next = { ...revealedHistoryPasswords.value }
    delete next[account.id]
    revealedHistoryPasswords.value = next
    return
  }
  if (historyPasswordLoadingAccountID.value === account.id) return
  historyPasswordLoadingAccountID.value = account.id
  try {
    const secret = await passwordStepUp.run(() => teamChildAPI.revealTeamChildAccountPassword(account.id))
    if (normalizeTeamChildEmail(secret.email) !== normalizeTeamChildEmail(entry.email)) throw new Error('历史账号密码与邮箱记录不一致')
    revealedHistoryPasswords.value = { ...revealedHistoryPasswords.value, [account.id]: secret.password }
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? '请使用管理员网页登录会话查看密码' : '请先为当前管理员启用 TOTP 二次验证')
      return
    }
    appStore.showError(extractApiErrorMessage(error, '无法查看历史 Team 登录密码'))
  } finally {
    historyPasswordLoadingAccountID.value = null
  }
}

async function selectExistingMailbox() {
  const email = normalizeTeamChildEmail(selectedMailboxEmail.value)
  if (!email || mailboxSelecting.value || workflowBusy.value) return
  mailboxSelecting.value = true
  errorMessage.value = ''
  const previous = mailbox.value
  try {
    const selected = await teamChildAPI.selectMailbox(email)
    mailbox.value = selected
    selectedMailboxEmail.value = selected.email
    mailboxCode.value = ''
    mailboxCodeError.value = ''
    resetMailboxPollRecovery()
    lastSubmittedEmailCode.value = ''
    schedulePoll(250)
    await loadKnownMailboxes()
    if (!authUrl.value || !oauthSessionID.value) await generateFreshOAuthSession()
    status.value = 'ready'
    if (previous && previous.session_id !== selected.session_id) {
      await teamChildAPI.deleteMailboxSession(previous.session_id).catch(() => undefined)
    }
    appStore.showSuccess(`已打开 Team 邮箱：${selected.email}`)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法打开已有 Team 邮箱')
  } finally {
    mailboxSelecting.value = false
  }
}

function clearRevealedPassword() {
  revealedWorkflowPassword.value = ''
}

async function revealWorkflowPassword() {
  const workflow = teamWorkflow.value
  if (!workflow?.password_available || passwordLoading.value) return
  passwordLoading.value = true
  try {
    const secret = await passwordStepUp.run(() => teamChildAPI.revealTeamChildWorkflowPassword(workflow.id))
    if (!mailbox.value || normalizeTeamChildEmail(secret.email) !== normalizeTeamChildEmail(mailbox.value.email)) {
      throw new Error('工作流密码邮箱与当前邮箱不一致')
    }
    revealedWorkflowPassword.value = secret.password
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? '请使用管理员网页登录会话查看密码' : '请先为当前管理员启用 TOTP 二次验证')
      return
    }
    appStore.showError(extractApiErrorMessage(error, '无法查看当前 Team 登录密码'))
  } finally {
    passwordLoading.value = false
  }
}
async function handleMailboxConfigFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  configImporting.value = true
  try {
    const result = await teamChildAPI.importMailboxConfig(file)
    mailboxConfigured.value = result.configured
    appStore.showSuccess(`邮箱配置已导入（${result.domain || '默认域名'}），无需重启即可使用`)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '邮箱配置导入失败'))
  } finally {
    configImporting.value = false
  }
}
function clearBrowserHeartbeat() {
  if (browserHeartbeatTimer) clearTimeout(browserHeartbeatTimer)
  browserHeartbeatTimer = null
}
function scheduleBrowserHeartbeat() {
  clearBrowserHeartbeat()
  if (!browserVisible.value || !browserControllerID.value) return
  const retryDelay = browserHeartbeatFailures.value > 0
    ? Math.min(15_000, 2_500 * (2 ** Math.min(browserHeartbeatFailures.value - 1, 2)))
    : 45_000
  browserHeartbeatTimer = setTimeout(() => void heartbeatBrowserControl(), retryDelay)
}
async function heartbeatBrowserControl() {
  if (!browserVisible.value || !browserControllerID.value) return
  try {
    await teamChildAPI.heartbeatTeamChildBrowserControl(browserControllerID.value)
    browserHeartbeatFailures.value = 0
    scheduleBrowserHeartbeat()
  } catch (error) {
    const errorCode = extractApiErrorCode(error)
    if (errorCode === 'TEAM_CHILD_BROWSER_CONTROL_LOST' || errorCode === 'TEAM_CHILD_BROWSER_CONTROLLED') {
      clearBrowserHeartbeat()
      browserEmbedURL.value = ''
      browserControlConflict.value = true
      browserError.value = '浏览器控制权已由其他设备接管。请返回自动化工作区，或在确认后重新接管。'
      return
    }

    // A single timeout/502 must not tear down the visible iframe. Retry the
    // lease quickly while keeping the current Chromium page available for
    // copying and manual recovery.
    browserHeartbeatFailures.value += 1
    if (browserHeartbeatFailures.value < 4) {
      scheduleBrowserHeartbeat()
      return
    }
    // Keep the iframe visible even after repeated transient failures. The
    // explicit reload action remains available, while silently retrying the
    // lease avoids replacing a usable page with a full-screen reconnect mask.
    browserError.value = ''
    scheduleBrowserHeartbeat()
  }
}
async function releaseBrowserControl() {
  clearBrowserHeartbeat()
  browserHeartbeatFailures.value = 0
  const controllerID = browserControllerID.value
  browserControllerID.value = ''
  if (controllerID) await teamChildAPI.releaseTeamChildBrowserControl(controllerID).catch(() => undefined)
}
async function reloadBrowserWorkspace({ takeOver = false, forceReload = false }: { takeOver?: boolean; forceReload?: boolean } = {}): Promise<boolean> {
  if (!browserConfigured.value) return false
  // Opening an already-visible workspace keeps its persistent Chromium tab.
  // The explicit refresh control must mint a fresh iframe ticket so the
  // browser viewer actually reloads instead of being silently short-circuited.
  if (!takeOver && !forceReload && browserEmbedURL.value && browserControllerID.value) return true
  if (browserLoading.value) return false
  browserLoading.value = true
  browserError.value = ''
  browserControlConflict.value = false
  browserHeartbeatFailures.value = 0
  try {
    const session = await teamChildAPI.createBrowserSession({
      ...(browserControllerID.value ? { controller_id: browserControllerID.value } : {}),
      ...(takeOver ? { take_over: true } : {})
    })
    browserEmbedURL.value = session.embed_url
    browserControllerID.value = session.controller_id
    scheduleBrowserHeartbeat()
    return true
  } catch (error) {
    browserControlConflict.value = extractApiErrorCode(error) === 'TEAM_CHILD_BROWSER_CONTROLLED'
    browserError.value = browserControlConflict.value
      ? '已有设备正在手动查看服务器浏览器。需要继续时，请明确确认接管。'
      : extractApiErrorMessage(error, '无法连接服务器浏览器，请检查部署状态。')
    return false
  } finally {
    browserLoading.value = false
  }
}
async function openBrowserWorkspace() {
  browserVisible.value = true
  await reloadBrowserWorkspace()
}

async function forceReloadBrowserWorkspace() {
  await reloadBrowserWorkspace({ forceReload: true })
}

async function confirmBrowserTakeOver() {
  browserTakeOverConfirmOpen.value = false
  await reloadBrowserWorkspace({ takeOver: true })
}
async function closeBrowserWorkspace() {
  browserVisible.value = false
  browserEmbedURL.value = ''
  browserError.value = ''
  browserControlConflict.value = false
  await releaseBrowserControl()
}
function applyMembersResult(result: { members?: TeamChildMember[]; pending_invites?: number; seat_email?: string; workspace_name?: string; ready?: boolean }) {
  const nextMembers = result.members || []
  members.value = nextMembers
  pendingInvites.value = result.pending_invites || 0
  seatEmail.value = result.seat_email || ''
  workspaceName.value = result.workspace_name || ''
  membersReady.value = Boolean(result.ready)

  const replaceable = nextMembers.find((member) => isReplaceableMember(member))
  const selectedStillAvailable = nextMembers.some((member) => member.email === selectedMemberEmail.value && isReplaceableMember(member))
  if (!selectedStillAvailable) selectedMemberEmail.value = seatEmail.value || replaceable?.email || ''
}
async function loadMembers(notify = false) {
  if (membersLoading.value) return
  membersLoading.value = true
  membersError.value = ''
  try {
    const result = await teamChildAPI.listTeamChildMembers()
    applyMembersResult(result)
    if (notify && membersReady.value) appStore.showSuccess(`成员信息已读取（${members.value.length} 位成员）`)
  } catch (error) {
    membersReady.value = false
    membersError.value = extractApiErrorMessage(error, '无法读取成员信息，请先在服务器浏览器中登录。')
  } finally {
    membersLoading.value = false
  }
}
async function refreshMembers(notify = true) {
  if (membersLoading.value) return
  membersLoading.value = true
  membersError.value = ''
  try {
    const result = await teamChildAPI.refreshTeamChildMembers()
    applyMembersResult(result)
    if (notify && membersReady.value) appStore.showSuccess(`成员信息已刷新（${members.value.length} 位成员）`)
  } catch (error) {
    membersReady.value = false
    membersError.value = extractApiErrorMessage(error, '无法读取成员信息，请先在服务器浏览器中登录。')
  } finally {
    membersLoading.value = false
  }
}
async function inspectSeat() {
  if (membersLoading.value) return
  membersLoading.value = true
  membersError.value = ''
  try {
    const result = await teamChildAPI.inspectTeamChildSeat()
    applyMembersResult(result)
    appStore.showSuccess(seatEmail.value ? `已识别席位邮箱：${seatEmail.value}` : '已读取成员页，但页面未提供明确的席位邮箱')
  } catch (error) {
    membersReady.value = false
    membersError.value = extractApiErrorMessage(error, '无法识别席位邮箱，请先在服务器浏览器中登录。')
  } finally {
    membersLoading.value = false
  }
}
async function inviteMember(email: string) {
  membersLoading.value = true
  const inviteEmail = normalizeTeamChildEmail(email)
  try {
    const result = await teamChildAPI.inviteTeamChildMember(inviteEmail)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false)
      appStore.showError('邀请请求已提交，但服务器未确认成员状态，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    pendingInvites.value = result.pending_invites || 0
    appStore.showSuccess(`邀请已发送：${inviteEmail}`)
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '邀请成员失败')) } finally { membersLoading.value = false }
}
async function editMember(email: string, role: string) {
  membersLoading.value = true
  try {
    const result = await teamChildAPI.updateTeamChildMember(email, role)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false)
      appStore.showError('角色更新未得到服务器确认，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    appStore.showSuccess('成员角色已更新')
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '编辑成员失败')) } finally { membersLoading.value = false }
}
async function removeMember(email: string) {
  membersLoading.value = true
  try {
    const result = await teamChildAPI.removeTeamChildMember(email)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false)
      appStore.showError('移除请求已提交，但服务器未确认成员状态，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    pendingInvites.value = result.pending_invites || 0
    if (selectedMemberEmail.value === email) selectedMemberEmail.value = ''
    appStore.showSuccess('成员已从工作空间移除')
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '移除成员失败')) } finally { membersLoading.value = false }
}
function openWorkflowConfirmation() {
  if (!workflowReady.value) {
    appStore.showError('请先获取临时邮箱，并选择可替换成员或刷新确认已人工腾出的席位')
    return
  }
  workflowConfirmOpen.value = true
}
function clearWorkflowPoll() {
  if (workflowPollTimer) clearTimeout(workflowPollTimer)
  workflowPollTimer = null
}
function scheduleWorkflowPoll(delay = 1200) {
  clearWorkflowPoll()
  if (!workflowExecutionArmed.value || !teamWorkflow.value || !['running', 'manual_required'].includes(teamWorkflow.value.status)) return
  workflowPollTimer = setTimeout(() => void pollWorkflow(), delay)
}
async function startConfirmedWorkflow() {
  const seatAlreadyRemoved = manualSeatReady.value
  if (!mailbox.value?.email || workflowStarting.value || (!seatAlreadyRemoved && !isReplaceableMember(selectedReplaceableMember.value))) return
  if (!authUrl.value || !oauthSessionID.value) {
    appStore.showError('请先使用 XIASS 内置 OpenAI 手动授权流程生成授权链接')
    return
  }
  workflowConfirmOpen.value = false
  workflowStarting.value = true
  errorMessage.value = ''
  try {
    // Keep the exact URL/session generated by XIASS's native OpenAI manual
    // authorization flow. Do not mint a second session here: the browser page
    // opened by the automation and the later callback exchange must refer to
    // the same PKCE state visible in this workspace.
    callbackURL.value = ''
    assignTeamWorkflow(await teamChildAPI.startTeamChildWorkflow({
      ...(seatAlreadyRemoved ? {} : { seat_email: normalizeTeamChildEmail(selectedMemberEmail.value) }),
      invite_email: normalizeTeamChildEmail(mailbox.value.email),
      auth_url: authUrl.value,
      oauth_session_id: oauthSessionID.value,
      seat_already_removed: seatAlreadyRemoved,
      confirmed: true
    }))
    workflowExecutionArmed.value = true
    mailboxPollingRequested.value = true
    status.value = 'workflow'
    schedulePoll(250)
    scheduleWorkflowPoll()
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法启动成员替换和授权工作流')
  } finally {
    workflowStarting.value = false
  }
}
async function continueWorkflow() {
  if (!teamWorkflow.value || workflowContinuing.value) return
  if (!workflowExecutionArmed.value && ['running', 'manual_required'].includes(teamWorkflow.value.status)) {
    workflowExecutionArmed.value = true
    mailboxPollingRequested.value = true
    status.value = teamWorkflow.value.status === 'running' ? 'workflow' : 'waiting'
    scheduleWorkflowPoll(250)
    if (teamWorkflow.value.status === 'manual_required' && !mailboxCode.value) schedulePoll(250)
    if (teamWorkflow.value.status !== 'manual_required' || teamWorkflow.value.mode !== 'reauthorization') {
      appStore.showInfo('已继续当前自动化')
      return
    }
  }
  const canResumeBrowserState = teamWorkflow.value.status === 'manual_required'
    && teamWorkflow.value.mode === 'reauthorization'
  if (!canResumeBrowserState && !['failed', 'paused'].includes(teamWorkflow.value.status)) return
  workflowContinuing.value = true
  errorMessage.value = ''
  try {
    assignTeamWorkflow(await teamChildAPI.continueTeamChildWorkflow(teamWorkflow.value.id))
    workflowExecutionArmed.value = true
    mailboxPollingRequested.value = true
    status.value = 'workflow'
    scheduleWorkflowPoll(500)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法继续自动化工作流')
  } finally {
    workflowContinuing.value = false
  }
}

async function pauseWorkflow() {
  const workflow = teamWorkflow.value
  if (!workflow || !['running', 'manual_required'].includes(workflow.status) || workflow.pause_requested) return
  workflowExecutionArmed.value = false
  mailboxPollingRequested.value = false
  clearWorkflowPoll()
  clearMailboxPoll()
  try {
    assignTeamWorkflow(await teamChildAPI.pauseTeamChildWorkflow(workflow.id))
    status.value = 'waiting'
    appStore.showInfo('自动化已暂停，刷新或重新进入页面都不会自动继续')
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法暂停当前自动化')
  }
}

async function pauseWorkflowAfterSMSCancel() {
  lastSubmittedPhone.value = ''
  lastSubmittedSMSCode.value = ''
  await pauseWorkflow()
}

async function resetWorkflowFromStart() {
  const workflow = teamWorkflow.value
  if (!workflow) return
  clearWorkflowPoll()
  clearMailboxPoll()
  workflowExecutionArmed.value = false
  mailboxPollingRequested.value = false
  errorMessage.value = ''
  try {
    await teamChildAPI.cancelTeamChildWorkflow(workflow.id)
    smsCancelSignal.value += 1
    if (mailbox.value?.session_id) {
      await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
    }
    assignTeamWorkflow(null)
    mailbox.value = null
    selectedMailboxEmail.value = ''
    mailboxCode.value = ''
    mailboxCodeError.value = ''
    callbackURL.value = ''
    createdAccount.value = null
    successAccountDialogOpen.value = false
    appliedImportConfig.value = null
    lastSubmittedEmailCode.value = ''
    lastSubmittedPhone.value = ''
    lastSubmittedSMSCode.value = ''
    openaiOAuth.resetState()
    status.value = 'idle'
    await refreshMembers(true).catch(() => undefined)
    await loadKnownMailboxes()
    await loadTeamChildHistory()
    appStore.showSuccess('当前自动化已取消，可以从第一步重新开始')
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法取消当前自动化工作流')
  }
}

async function handleWorkspaceOneClick() {
  if (!workflowExecutionArmed.value && ['running', 'manual_required'].includes(teamWorkflow.value?.status || '')) {
    await continueWorkflow()
    return
  }
  if (['failed', 'paused'].includes(teamWorkflow.value?.status || '')) {
    await continueWorkflow()
    return
  }
  if (teamWorkflow.value || busy.value) return

  // A new one-click run always starts from the real ChatGPT Members home.
  // The automation service brings that dedicated tab to the foreground while
  // preserving any separate OAuth tab from an earlier run.
  await refreshMembers(false)
  if (!membersReady.value) {
    appStore.showError('成员页刷新失败，请先在内嵌浏览器完成 ChatGPT 管理员登录')
    return
  }
  if (!mailbox.value) await startFlow()
  if (!mailbox.value || status.value === 'error') return
  if (!authUrl.value || !oauthSessionID.value) await generateFreshOAuthSession().catch(() => null)

  if (workflowReady.value) {
    openWorkflowConfirmation()
    return
  }
  appStore.showError('尚未识别到可替换的普通成员席位，请刷新成员页或使用手动接入处理')
}

async function restartWorkflowOAuth() {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return
  clearWorkflowPoll()
  try {
    const activeMailbox = await teamChildAPI.getActiveMailbox()
    const reusedMailbox = activeMailbox || mailbox.value
    if (!reusedMailbox) throw new Error('当前临时邮箱已过期，请先重新获取临时邮箱')
    if (activeMailbox) mailbox.value = activeMailbox
    mailboxCode.value = ''
    mailboxCodeError.value = ''
    workflowExecutionArmed.value = true
    mailboxPollingRequested.value = true
    schedulePoll(250)
    const auth = await generateFreshOAuthSession()
    if (!auth) throw new Error('未生成 XIASS 官方 OpenAI 授权链接')
    assignTeamWorkflow(await teamChildAPI.restartTeamChildWorkflowOAuth(workflow.id, auth.auth_url, auth.session_id))
    callbackURL.value = ''
    status.value = 'workflow'
    errorMessage.value = ''
    appStore.showInfo(`已复用临时邮箱 ${reusedMailbox.email}，新的官方 OAuth 链接已准备`)
    scheduleWorkflowPoll(900)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '重置 OAuth 步骤失败，请重新生成官方授权链接')
  }
}

async function syncWorkflowAfterActionError() {
  const workflow = teamWorkflow.value
  if (!workflow) return
  try {
    const latest = await teamChildAPI.getTeamChildWorkflow(workflow.id)
    assignTeamWorkflow(latest)
    if (latest.status === 'failed') {
      status.value = 'error'
      return
    }
    if (latest.status === 'manual_required') {
      status.value = 'waiting'
      scheduleWorkflowPoll(2500)
    }
  } catch {
    // Keep the original action error visible when the status read also fails.
  }
}

async function pollWorkflow() {
  if (!workflowExecutionArmed.value || !teamWorkflow.value || !['running', 'manual_required'].includes(teamWorkflow.value.status)) return
  try {
    const previousEmailCodeGeneration = lastEmailCodeGeneration
    const workflow = await teamChildAPI.getTeamChildWorkflow(teamWorkflow.value.id)
    assignTeamWorkflow(workflow)
    const emailChallengeChanged = lastEmailCodeGeneration > previousEmailCodeGeneration
    if (workflow.status === 'callback_ready' && workflow.callback_url) {
      callbackURL.value = workflow.callback_url
      status.value = 'callback'
      clearWorkflowPoll()
      appStore.showSuccess('已自动捕获并校验授权回调，正在导入 XIASS')
      await importAccount()
      return
    }
    if (workflow.status === 'manual_required') {
      if (status.value !== 'received') status.value = 'waiting'
      if (!emailChallengeChanged && !mailboxCode.value && !mailboxCodeLoading.value) schedulePoll()
      await maybeSubmitMailboxCode()
    }
    if (workflow.status === 'failed') {
      status.value = 'error'
      errorMessage.value = workflow.error || '成员替换或 OAuth 授权交接未完成'
      clearWorkflowPoll()
      return
    }
    if (workflow.status === 'paused') {
      status.value = 'waiting'
      errorMessage.value = ''
      clearWorkflowPoll()
      return
    }
    scheduleWorkflowPoll(workflow.status === 'running' ? 1200 : 2500)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法读取授权工作流状态')
    clearWorkflowPoll()
  }
}
async function startFlow() {
  if (busy.value || !mailboxConfigured.value) return
  clearMailboxPoll()
  clearWorkflowPoll()
  mailboxPollingRequested.value = true
  errorMessage.value = ''; mailboxCodeError.value = ''; createdAccount.value = null; successAccountDialogOpen.value = false; appliedImportConfig.value = null; assignTeamWorkflow(null); openaiOAuth.resetState(); callbackURL.value = ''; mailboxCode.value = ''; lastSubmittedEmailCode.value = ''; lastSubmittedPhone.value = ''; lastSubmittedSMSCode.value = ''; status.value = 'creating'
  try {
    mailbox.value = await teamChildAPI.createMailbox()
    selectedMailboxEmail.value = mailbox.value.email
    resetMailboxPollRecovery()
    await loadKnownMailboxes()
    schedulePoll(250)
    if (!teamWorkflow.value) await generateFreshOAuthSession()
    status.value = 'ready'
  } catch (error) {
    mailboxPollingRequested.value = false
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '流程启动失败')
  }
}

async function restoreActiveMailbox() {
  if (!mailboxConfigured.value || mailbox.value) return
  try {
    const restored = await teamChildAPI.getActiveMailbox()
    if (!restored) return
    mailbox.value = restored
    selectedMailboxEmail.value = restored.email
    mailboxCodeError.value = ''
    resetMailboxPollRecovery()
    if (!teamWorkflow.value) status.value = 'ready'
  } catch {
    // A missing or expired mailbox is normal after its short server-side TTL.
  }
}
function schedulePoll(delay = 5000) {
  if ((!workflowExecutionArmed.value && !mailboxPollingRequested.value) || !mailbox.value || mailboxCode.value || ['completed', 'callback'].includes(status.value)) return
  const normalizedDelay = Math.max(0, delay)
  const dueAt = Date.now() + normalizedDelay
  if (pollTimer && pollTimerDueAt <= dueAt) return
  clearMailboxPoll()
  pollTimerDueAt = dueAt
  pollTimer = setTimeout(() => {
    pollTimer = null
    pollTimerDueAt = 0
    void pollMailbox()
  }, normalizedDelay)
}

function clearMailboxPoll() {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
  pollTimerDueAt = 0
}

function resetMailboxPollRecovery() {
  mailboxWaitingPolls.value = 0
}

async function refreshMailboxPollingSession(expectedSessionID: string): Promise<boolean> {
  const current = mailbox.value
  const email = current ? normalizeTeamChildEmail(current.email) : ''
  if (!current || current.session_id !== expectedSessionID || !email || mailboxSessionRefreshInFlight) return false
  mailboxSessionRefreshInFlight = true
  try {
    const replacement = await teamChildAPI.selectMailbox(email)
    // The operator can select a different mailbox while the provider request is
    // in flight. Only replace the exact session that initiated this recovery.
    if (!mailbox.value || mailbox.value.session_id !== expectedSessionID || normalizeTeamChildEmail(replacement.email) !== email) {
      await Promise.resolve(teamChildAPI.deleteMailboxSession(replacement.session_id)).catch(() => undefined)
      return false
    }
    mailbox.value = replacement
    selectedMailboxEmail.value = replacement.email
    mailboxCode.value = ''
    mailboxCodeError.value = ''
    lastSubmittedEmailCode.value = ''
    resetMailboxPollRecovery()
    await Promise.resolve(teamChildAPI.deleteMailboxSession(expectedSessionID)).catch(() => undefined)
    return true
  } finally {
    mailboxSessionRefreshInFlight = false
  }
}

async function pollMailbox() {
  if ((!workflowExecutionArmed.value && !mailboxPollingRequested.value) || !mailbox.value || mailboxCode.value || ['completed', 'callback'].includes(status.value) || mailboxCodeLoading.value) return
  const pollingMailbox = mailbox.value
  mailboxCodeLoading.value = true
  if (status.value === 'ready' || status.value === 'waiting' || status.value === 'error') status.value = 'polling'
  try {
    const result = await teamChildAPI.pollMailboxCode(pollingMailbox.session_id)
    if (!mailbox.value || mailbox.value.session_id !== pollingMailbox.session_id) return
    mailboxCodeError.value = ''
    const receivedCode = String(result.code || '').replace(/\s+/g, '')
    if (result.status === 'received' && receivedCode && !submittedEmailCodes.has(receivedCode)) {
      mailboxCode.value = receivedCode
      mailboxPollingRequested.value = false
      resetMailboxPollRecovery()
      status.value = 'received'
      clearMailboxPoll()
      void maybeSubmitMailboxCode()
    } else {
      if (receivedCode && submittedEmailCodes.has(receivedCode)) {
        mailboxCodeError.value = '上一轮验证码已使用，正在等待新邮件。'
      }
      if (status.value === 'polling') status.value = 'waiting'
      mailboxWaitingPolls.value += 1
      if (mailboxWaitingPolls.value >= 2) {
        resetMailboxPollRecovery()
        const refreshed = await refreshMailboxPollingSession(pollingMailbox.session_id).catch(() => false)
        if (refreshed) {
          schedulePoll(50)
          return
        }
      }
      schedulePoll()
    }
  } catch (error) {
    const statusCode = Number((error as { status?: unknown; response?: { status?: unknown } })?.status ?? (error as { response?: { status?: unknown } })?.response?.status)
    const retryable = !Number.isFinite(statusCode) || statusCode === 408 || statusCode === 429 || statusCode >= 500
    mailboxCodeError.value = retryable ? '邮箱服务暂时没有响应。' : extractApiErrorMessage(error, '邮箱检查失败')
    if (retryable) {
      if (status.value === 'polling') status.value = 'waiting'
      schedulePoll()
    } else {
      status.value = 'error'
      clearMailboxPoll()
      errorMessage.value = mailboxCodeError.value
    }
  } finally { mailboxCodeLoading.value = false }
}

async function requestMailboxPoll() {
  mailboxPollingRequested.value = true
  const current = mailbox.value
  if (current && !mailboxCodeLoading.value) {
    await refreshMailboxPollingSession(current.session_id).catch(() => undefined)
  }
  void pollMailbox()
}

async function maybeSubmitMailboxCode() {
  const workflow = teamWorkflow.value
  const code = mailboxCode.value.replace(/\s+/g, '')
  const node = workflow?.current_node || ''
  if (!workflowExecutionArmed.value || !workflow || !['mailbox', 'email_code', 'password'].includes(node) || !code || emailCodeSubmitting.value || lastSubmittedEmailCode.value === code) return
  if (submittedEmailCodes.has(code)) {
    mailboxCode.value = ''
    mailboxPollingRequested.value = true
    schedulePoll(500)
    return
  }
  emailCodeSubmitting.value = true
  lastSubmittedEmailCode.value = code
  try {
    const updated = await teamChildAPI.submitTeamChildWorkflowEmailCode(workflow.id, code)
    submittedEmailCodes.add(code)
    mailboxCode.value = ''
    mailboxPollingRequested.value = false
    assignTeamWorkflow(updated)
    status.value = 'workflow'
    scheduleWorkflowPoll(400)
  } catch (error) {
    lastSubmittedEmailCode.value = ''
    mailboxCode.value = ''
    mailboxPollingRequested.value = true
    schedulePoll(5000)
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '邮箱验证码自动填入失败')
    await syncWorkflowAfterActionError()
  } finally {
    emailCodeSubmitting.value = false
  }
}

async function submitWorkflowPhone(phone: string) {
  const workflow = teamWorkflow.value
  const normalized = phone.replace(/[\s()-]/g, '')
  if (!workflowExecutionArmed.value || !workflow || !['sms_confirm', 'phone_submit', 'sms_poll', 'sms_code'].includes(workflow.current_node || '') || phoneSubmitting.value || lastSubmittedPhone.value === normalized) return
  phoneSubmitting.value = true
  lastSubmittedPhone.value = normalized
  try {
    assignTeamWorkflow(await teamChildAPI.submitTeamChildWorkflowPhone(workflow.id, normalized))
    status.value = 'workflow'
    scheduleWorkflowPoll(400)
  } catch (error) {
    lastSubmittedPhone.value = ''
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '手机号自动填入失败')
    await syncWorkflowAfterActionError()
  } finally {
    phoneSubmitting.value = false
  }
}

async function submitWorkflowSMSCode(code: string) {
  const workflow = teamWorkflow.value
  const normalized = code.replace(/\s+/g, '')
  if (!workflowExecutionArmed.value || !workflow || !['sms_poll', 'sms_code'].includes(workflow.current_node || '') || smsCodeSubmitting.value || lastSubmittedSMSCode.value === normalized) return
  smsCodeSubmitting.value = true
  lastSubmittedSMSCode.value = normalized
  try {
    assignTeamWorkflow(await teamChildAPI.submitTeamChildWorkflowSMSCode(workflow.id, normalized))
    status.value = 'workflow'
    scheduleWorkflowPoll(400)
  } catch (error) {
    lastSubmittedSMSCode.value = ''
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '短信验证码自动填入失败')
    await syncWorkflowAfterActionError()
  } finally {
    smsCodeSubmitting.value = false
  }
}
async function importAccount() {
  const reauthorizationAccount = currentReauthorizationAccount()
  if ((!mailbox.value && !reauthorizationAccount) || !oauthSessionID.value || !parsedCallback.value || parsedCallbackError.value || importing.value) return
  const importConfig = reauthorizationAccount ? {
    groupIDs: persistedOrImportedGroupIDs(reauthorizationAccount),
    groupNames: [...(reauthorizationAccount.groups?.map((group) => group.name) || [])],
    concurrency: reauthorizationAccount.concurrency,
    priority: reauthorizationAccount.priority
  } : {
    // Capture an immutable, normalized snapshot before the asynchronous
    // callback exchange. The selector can otherwise change while the account
    // is being created and leave the persisted groups out of sync with the UI.
    groupIDs: normalizedGroupIDs([...selectedGroupIDs.value]),
    groupNames: normalizedGroupIDs([...selectedGroupIDs.value])
      .map((id) => groups.value.find((group) => group.id === id)?.name || `#${id}`),
    concurrency: normalizedConcurrency.value,
    priority: normalizedPriority.value
  }
  appliedImportConfig.value = importConfig
  errorMessage.value = ''; status.value = 'importing'
  try {
    if (teamWorkflow.value && teamWorkflow.value.status !== 'callback_ready') {
      const confirmed = await confirmWorkflowCallback()
      if (!confirmed) {
        status.value = 'error'
        return
      }
    }
    if (reauthorizationAccount) {
      const tokenInfo = await openaiOAuth.exchangeAuthCode(
        parsedCallback.value.code,
        oauthSessionID.value,
        parsedCallback.value.state,
        reauthorizationAccount.proxy_id
      )
      if (!tokenInfo) throw new Error(oauthError.value || '重新授权回调无法交换 OAuth 凭据')
      createdAccount.value = await accountsAPI.applyOAuthCredentials(reauthorizationAccount.id, {
        type: 'oauth',
        credentials: openaiOAuth.buildCredentials(tokenInfo),
        extra: openaiOAuth.buildExtraInfo(tokenInfo)
      })
    } else {
      const importMailbox = mailbox.value
      if (!importMailbox) throw new Error('当前 Team 子号导入缺少临时邮箱')
      createdAccount.value = await teamChildAPI.createOpenAIAccountFromOAuth({
        session_id: oauthSessionID.value,
        code: parsedCallback.value.code,
        state: parsedCallback.value.state,
        name: importMailbox.email,
        concurrency: importConfig.concurrency,
        priority: importConfig.priority,
        group_ids: importConfig.groupIDs,
        skip_default_group_bind: true,
        schedulable: true,
        team_child: true,
        workflow_id: teamWorkflow.value!.id
      })
    }
    if (teamWorkflow.value) {
      const completedWorkflow = await teamChildAPI.completeTeamChildWorkflow(teamWorkflow.value.id).catch(() => teamWorkflow.value!)
      assignTeamWorkflow(completedWorkflow)
    }
    status.value = 'completed'
    if (reauthorizationAccount) {
      replaceOpenAIOAuthAccount(createdAccount.value)
      successAccountDialogOpen.value = false
      appStore.showSuccess(`已重新授权：${createdAccount.value.name}`)
    } else {
      initializeSuccessAccountConfiguration(createdAccount.value, importConfig)
      successAccountDialogOpen.value = true
    }
    clearMailboxPoll()
    if (mailbox.value?.session_id) await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
    await loadTeamChildHistory()
  } catch (error) { status.value = 'error'; errorMessage.value = extractApiErrorMessage(error, '导入失败') }
}

function initializeSuccessAccountConfiguration(account: Account | null, fallback: { groupIDs: number[]; groupNames: string[]; concurrency: number; priority: number }) {
  const accountGroupIDs = persistedOrImportedGroupIDs(account, fallback.groupIDs)
  successAccountGroupIDs.value = [...accountGroupIDs]
  successAccountConcurrency.value = account?.concurrency || fallback.concurrency
  successAccountPriority.value = account?.priority || fallback.priority
}

async function saveCreatedAccountConfiguration() {
  const account = createdAccount.value
  if (!account || successAccountSaving.value) return
  successAccountSaving.value = true
  try {
    const concurrency = Math.min(1000, Math.max(1, Math.trunc(Number(successAccountConcurrency.value) || 10)))
    const priority = Math.min(999, Math.max(1, Math.trunc(Number(successAccountPriority.value) || 1)))
    const groupIDs = [...new Set(successAccountGroupIDs.value.filter((id) => Number.isSafeInteger(id) && id > 0))]
    const updated = await accountsAPI.update(account.id, {
      group_ids: groupIDs,
      concurrency,
      priority
    })
    createdAccount.value = updated
    selectedGroupIDs.value = persistedOrImportedGroupIDs(updated, groupIDs)
    accountConcurrency.value = updated.concurrency
    accountPriority.value = updated.priority
    appliedImportConfig.value = {
      groupIDs: [...selectedGroupIDs.value],
      groupNames: [...(updated.groups?.map((group) => group.name) || selectedGroupNames.value)],
      concurrency: updated.concurrency,
      priority: updated.priority
    }
    initializeSuccessAccountConfiguration(updated, appliedImportConfig.value)
    successAccountDialogOpen.value = false
    await loadTeamChildHistory()
    appStore.showSuccess('Team 子账号配置已保存')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '保存 Team 子账号配置失败'))
  } finally {
    successAccountSaving.value = false
  }
}

async function confirmWorkflowCallback(): Promise<boolean> {
  const workflow = teamWorkflow.value
  const callback = callbackURL.value.trim()
  if (!workflow || !callback || !parsedCallback.value || parsedCallbackError.value || callbackConfirming.value) return false
  callbackConfirming.value = true
  try {
    const confirmedWorkflow = await teamChildAPI.submitTeamChildWorkflowCallback(workflow.id, callback)
    assignTeamWorkflow(confirmedWorkflow)
    callbackURL.value = confirmedWorkflow.callback_url || callback
    status.value = 'callback'
    clearWorkflowPoll()
    appStore.showSuccess('回调 state 已校验，可以导入 XIASS')
    return true
  } catch (error) {
    errorMessage.value = extractApiErrorMessage(error, '回调校验失败，请确认使用当前 OAuth 会话的完整 URL')
    return false
  } finally {
    callbackConfirming.value = false
  }
}
async function restoreActiveWorkflow() {
  if (teamWorkflow.value) return
  try {
    const restored = await teamChildAPI.getActiveTeamChildWorkflow()
    if (!restored) return
    assignTeamWorkflow(restored)
    if (restored.oauth_session_id) oauthSessionID.value = restored.oauth_session_id
    if (restored.oauth_state) oauthState.value = restored.oauth_state
    if (restored.status === 'callback_ready' && restored.callback_url) {
      callbackURL.value = restored.callback_url
      status.value = 'callback'
      return
    }
    if (restored.status === 'completed') {
      status.value = 'completed'
      return
    }
    if (restored.status === 'failed') {
      status.value = 'error'
      errorMessage.value = restored.error || '自动化步骤未完成，请点击继续自动化'
      return
    }
    if (restored.status === 'paused') {
      status.value = 'waiting'
      errorMessage.value = ''
      return
    }
    status.value = 'waiting'
    errorMessage.value = ''
  } catch (error) {
    const message = extractApiErrorMessage(error, '')
    if (/运行组件版本不匹配/.test(message)) {
      status.value = 'error'
      errorMessage.value = message
    }
  }
}

onMounted(async () => {
  teamChildViewUnmounted = false
  workflowExecutionArmed.value = false
  mailboxPollingRequested.value = false
  const [statusResult, groupsResult] = await Promise.allSettled([
    teamChildAPI.getMailboxStatus(),
    groupsAPI.getAll('openai')
  ])
  mailboxConfigured.value = statusResult.status === 'fulfilled' && Boolean(statusResult.value.configured)
  browserConfigured.value = statusResult.status === 'fulfilled' && Boolean(statusResult.value.browser_configured)
  if (groupsResult.status === 'fulfilled') groups.value = groupsResult.value
  await loadKnownMailboxes()
  await restoreActiveMailbox()
  await restoreActiveWorkflow()
  await loadTeamChildHistory()
  requestHistoryReauthorizationFromURL()
  await openReauthorizationManagementFromURL()
  if (browserConfigured.value) await loadMembers()
})

watch(
  [() => teamWorkflow.value?.current_node, () => teamWorkflow.value?.email_code_generation, mailboxCode],
  () => { void maybeSubmitMailboxCode() }
)
watch(
  () => teamWorkflow.value?.id,
  (nextID, previousID) => {
    if (nextID !== previousID) clearRevealedPassword()
  }
)
onBeforeUnmount(() => {
  teamChildViewUnmounted = true
  workflowExecutionArmed.value = false
  mailboxPollingRequested.value = false
  clearMailboxPoll()
  clearWorkflowPoll()
  clearTeamChildHistoryUsageRefresh()
  clearRevealedPassword()
  void releaseBrowserControl()
})
</script>

<style scoped>
.team-child-page {
  overflow-x: clip;
}

.team-child-layout {
  align-items: stretch;
}

.team-child-layout > * {
  min-width: 0;
}

.team-child-page :deep(.btn),
.team-child-page :deep(button),
.team-child-page :deep(h1),
.team-child-page :deep(h2),
.team-child-page :deep(h3) {
  text-wrap: nowrap;
}

.team-child-page :deep(.input),
.team-child-page :deep(textarea),
.team-child-page :deep(code) {
  min-width: 0;
}

.team-child-page :deep(textarea),
.team-child-page :deep(code) {
  overflow-wrap: anywhere;
}

@media (max-width: 639px) {
  .team-child-page :deep(.btn) {
    max-width: 100%;
  }

}

@media (min-width: 1280px) {
  .team-child-layout :deep(.team-child-members-workspace),
  .team-child-layout :deep(.team-child-browser-workspace) {
    min-height: min(720px, calc(100dvh - 12rem));
  }
}
</style>
