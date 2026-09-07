<template>
  <div
    data-testid="team-oauth-workspace"
    class="team-oauth-workspace mx-auto w-full min-w-0 max-w-[1120px] space-y-4"
    aria-live="polite"
  >
    <div class="team-oauth-layout">
      <div class="team-oauth-main-column min-w-0">
    <section data-testid="team-oauth-progress-card" class="team-oauth-deck relative z-20 overflow-visible">
      <div class="team-oauth-step-stack" data-testid="team-oauth-step-stack">
        <TransitionGroup name="team-step-card" tag="div" class="team-oauth-step-stack-inner">
          <article
            v-for="(node, stackIndex) in visibleStackNodes"
            :key="node.key"
            :data-testid="stackIndex === 0 ? 'team-oauth-current-node' : stackIndex === 1 ? 'team-oauth-next-node' : `team-oauth-step-${node.number}`"
            class="team-oauth-step-card min-w-0 overflow-hidden rounded-lg border shadow-[0_12px_28px_rgba(15,23,42,0.12)] transition-transform duration-200 ease-out dark:shadow-none"
            :class="[cardClass(node, stackIndex), stackIndex === 0 ? 'team-oauth-step-card-active' : '']"
            :style="{ '--stack-index': stackIndex }"
          >
            <div v-if="stackIndex === 0" class="flex h-full min-w-0 flex-col items-center justify-center gap-2 px-4 py-5 pb-7 text-center sm:px-5">
              <div class="flex min-w-0 max-w-[38rem] flex-col items-center">
                <div class="flex min-w-0 flex-wrap items-center justify-center gap-3 text-xs font-medium text-cyan-100">
                  <span>{{ cardStatusLabel(node, stackIndex) }}</span>
                  <span v-if="mailboxEmail" class="max-w-[16rem] truncate">{{ mailboxEmail }}</span>
                </div>
                <h3 class="mt-1 flex max-w-full items-center justify-center gap-2 truncate text-lg font-semibold text-white">
                  <Icon v-if="workflow.status === 'completed'" name="check" size="sm" class="shrink-0 text-emerald-300" :stroke-width="2.75" />
                  <span class="truncate">{{ displayNodeLabel(node) }}</span>
                </h3>
                <p class="mt-1 line-clamp-2 text-sm leading-6 text-cyan-50/90">{{ node.message || cardMessage(node, stackIndex) }}</p>
              </div>
            </div>
            <div class="absolute inset-x-0 bottom-0 h-2 bg-gray-100 dark:bg-dark-700" aria-hidden="true">
              <div class="h-full rounded-r-full transition-[width] duration-500" :class="node.status === 'failed' ? 'bg-red-500' : 'bg-emerald-500'" :style="{ width: `${cardProgress(node, stackIndex)}%` }"></div>
            </div>
          </article>
        </TransitionGroup>
      </div>

      <div class="team-oauth-progress-summary mt-3 flex min-h-12 items-center justify-center gap-3 px-3 text-center text-xs font-medium leading-5 text-gray-500 dark:text-gray-400">
        <div class="flex shrink-0 items-center gap-1" aria-label="工作流完成进度">
          <span
            v-for="index in workflowNodes.length"
            :key="`progress-dot-${index}`"
            class="h-2.5 w-2.5 rounded-[3px] border transition-colors duration-300"
            :class="index <= completedCount ? 'border-emerald-400 bg-emerald-500' : 'border-gray-300 bg-gray-200 dark:border-dark-500 dark:bg-dark-700'"
            aria-hidden="true"
          ></span>
        </div>
        <div class="flex items-center gap-2 whitespace-nowrap">
          <span>已完成{{ completedCount }}项</span>
          <span class="text-gray-400 dark:text-gray-500">{{ completedCount }}/{{ workflowNodes.length }}（总步数）</span>
        </div>
      </div>
    </section>

    <section data-testid="team-oauth-operation-card" class="team-oauth-operation-card overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900">
      <header class="flex min-w-0 flex-col gap-3 border-b border-gray-200 px-4 py-3.5 dark:border-dark-700 lg:flex-row lg:items-center lg:justify-between">
        <div class="min-w-0">
          <h3 class="whitespace-nowrap text-sm font-semibold text-gray-900 dark:text-gray-100">实际操作面板</h3>
          <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400">服务器自动执行当前步骤；页面结构变化时可手动接入处理后继续。</p>
        </div>
        <div class="team-oauth-operation-actions">
          <button v-if="showOneClick && workflow.status !== 'paused' && (preflight || automationEnabled || !['running', 'manual_required'].includes(workflow.status))" type="button" data-testid="team-one-click-authorize" class="btn btn-primary flex items-center justify-center gap-2 whitespace-nowrap" :disabled="oneClickDisabled" @click="emit('one-click')">
            <Icon name="play" size="sm" :stroke-width="2" />
            <span>{{ oneClickLabel }}</span>
          </button>
          <button type="button" data-testid="team-manual-browser" class="btn btn-secondary flex items-center justify-center gap-2 whitespace-nowrap" @click="emit('open-browser')">
            <Icon name="server" size="sm" :stroke-width="2" />
            <span>手动接入</span>
          </button>
          <button
            v-if="!preflight && automationEnabled && ['running', 'manual_required'].includes(workflow.status)"
            type="button"
            data-testid="team-pause-workflow"
            class="btn btn-secondary flex items-center justify-center gap-2 whitespace-nowrap"
            :disabled="workflow.pause_requested"
            @click="emit('pause-workflow')"
          >
            <Icon name="pause" size="sm" :stroke-width="2.25" />
            <span>{{ workflow.pause_requested ? '正在暂停' : '暂停自动化' }}</span>
          </button>
          <button
            v-if="!preflight && (workflow.status === 'paused' || (workflow.status === 'manual_required' && workflow.mode === 'reauthorization') || (!automationEnabled && ['running', 'manual_required'].includes(workflow.status)))"
            type="button"
            data-testid="team-resume-workflow"
            class="btn btn-primary flex items-center justify-center gap-2 whitespace-nowrap"
            @click="emit('continue-workflow')"
          >
            <Icon name="play" size="sm" :stroke-width="2.25" />
            <span>继续自动化</span>
          </button>
          <button
            v-if="!preflight && !['completed', 'cancelled'].includes(workflow.status)"
            type="button"
            data-testid="team-reset-workflow"
            class="btn flex items-center justify-center gap-2 whitespace-nowrap border-red-200 bg-white text-red-700 hover:bg-red-50 dark:border-red-900/70 dark:bg-dark-900 dark:text-red-300 dark:hover:bg-red-950/25"
            @click="emit('reset-workflow')"
          >
            <Icon name="x" size="sm" :stroke-width="2.5" />
            <span>取消并重头开始</span>
          </button>
        </div>
      </header>
      <TeamChildBrowserWorkspace
        v-if="browserVisible"
        class="team-oauth-inline-browser"
        :configured="browserConfigured"
        :embed-url="browserEmbedUrl"
        :loading="browserLoading"
        :error="browserError"
        :mailbox-email="mailboxEmail"
        :members-ready="true"
        :control-conflict="browserControlConflict"
        @reload="emit('reload-browser')"
        @refresh-members="emit('refresh-members')"
        @copy-mailbox="emit('copy-mailbox', mailboxEmail)"
        @open-modular="emit('open-modular')"
        @force-take-over="emit('force-take-over')"
      />
      <template v-else>
      <div class="team-oauth-operation-body min-w-0 p-4">
      <div class="team-oauth-operation-content min-w-0">
        <slot v-if="preflight" name="operation"></slot>
        <template v-else>
        <div class="mt-4 flex items-start gap-2 rounded-md border border-gray-200 bg-gray-50 px-3 py-2.5 text-xs leading-5 text-gray-600 dark:border-dark-600 dark:bg-dark-900/40 dark:text-gray-300">
          <Icon name="shield" size="sm" class="mt-0.5 shrink-0 text-gray-500 dark:text-gray-400" :stroke-width="2" />
          <p>系统会先在独立隐私页注册新邮箱，注册成功后才移除成员、发送邀请并确认 Pending invites。随后在同一隐私页打开 OAuth；如果仍出现登录页，会再次输入该邮箱并读取新一轮验证码。验证码和浏览器登录态不会写入工作流记录。</p>
        </div>

        <div v-if="authUrl" class="mt-3 rounded-md border border-primary-200 bg-primary-50/40 p-3 dark:border-primary-900/60 dark:bg-primary-950/15">
          <div class="flex items-center justify-between gap-3">
            <span class="text-xs font-medium text-primary-800 dark:text-primary-200">XIASS 官方 OAuth PKCE 链接</span>
            <button type="button" class="btn btn-secondary flex h-8 w-8 items-center justify-center p-0" title="复制官方授权链接" aria-label="复制官方授权链接" @click="emit('copy-auth')">
              <Icon name="copy" size="sm" :stroke-width="2" />
            </button>
          </div>
          <code class="mt-2 block max-h-16 overflow-auto break-all rounded-md border border-primary-100 bg-white px-2.5 py-2 text-[11px] leading-4 text-gray-700 dark:border-primary-900/50 dark:bg-dark-900 dark:text-gray-300">{{ authUrl }}</code>
          <p class="mt-2 text-xs leading-5 text-primary-700 dark:text-primary-300">XIASS 会在注册新邮箱的同一隐私页打开官方 PKCE 链接，同时保留普通成员管理标签页；上方只显示当前节点，完成后自动进入下一项。</p>
        </div>

        <div v-if="workflow.error" class="mt-3 flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2.5 text-xs leading-5 text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">
          <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" :stroke-width="2" />
          <span>{{ workflow.error }}</span>
        </div>

        <div v-if="showReauthorize || workflow.status === 'failed'" class="mt-3 flex flex-wrap items-center gap-2">
          <button
            v-if="showReauthorize"
            type="button"
            class="btn flex items-center gap-2 whitespace-nowrap border-red-200 bg-red-50 text-red-700 hover:bg-red-100 dark:border-red-900/70 dark:bg-red-950/25 dark:text-red-300 dark:hover:bg-red-950/40"
            title="检测到 401，复用当前临时邮箱并重新准备官方授权"
            data-testid="team-reauthorize"
            @click="emit('reauthorize')"
          >
            <Icon name="key" size="sm" :stroke-width="2" />
            <span>重新授权</span>
          </button>
          <button v-if="workflow.status === 'failed' && !replacementRequired" type="button" data-testid="team-continue-after-failure" class="btn btn-primary flex items-center gap-2 whitespace-nowrap !px-2.5 !py-1.5 text-xs" @click="emit('continue-workflow')">
            <Icon name="refresh" size="xs" :stroke-width="2" />
            <span>继续自动化</span>
          </button>
        </div>

        <div v-if="receiverActive" class="mt-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2.5 text-xs leading-5 text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-200">
          已进入手机号验证阶段。号码被拒或等待两分钟仍未收到验证码时，系统会释放旧号码并返回手机号步骤；如果 OAuth 再次出现登录页，会重新输入本次邮箱并轮询新的邮箱验证码后继续。
        </div>
        </template>

        <slot name="operation-after"></slot>
      </div>

      </div>

      <footer v-if="callbackURL" class="flex flex-col gap-2 border-t border-green-200 bg-green-50/70 px-4 py-3 dark:border-green-900/60 dark:bg-green-950/20 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex min-w-0 items-center gap-2 text-xs text-green-800 dark:text-green-200">
          <Icon name="check" size="sm" class="shrink-0" :stroke-width="2.5" />
          <span class="truncate">已校验授权回调</span>
        </div>
        <button type="button" class="btn btn-secondary flex items-center justify-center gap-2 whitespace-nowrap" @click="emit('copy-callback')">
          <Icon name="clipboard" size="sm" :stroke-width="2" />
          <span>复制回调地址</span>
        </button>
      </footer>
      </template>
    </section>
      </div>

      <aside data-testid="team-oauth-side-actions" class="team-oauth-side-column" aria-label="授权与接码操作">
        <section class="team-oauth-side-module" aria-labelledby="team-mailbox-tools-title">
          <div class="team-oauth-side-module-heading">
            <span class="team-oauth-side-module-icon" aria-hidden="true">
              <Icon name="mail" size="sm" :stroke-width="2" />
            </span>
            <h3 id="team-mailbox-tools-title">邮箱与验证码</h3>
            <button
              type="button"
              data-testid="team-open-mailbox-share"
              class="btn btn-secondary ml-auto flex shrink-0 items-center gap-1 whitespace-nowrap !px-2 !py-1 text-xs"
              :disabled="!mailboxActionEmail || mailboxSelecting"
              title="生成或管理当前邮箱的接码链接"
              @click="mailboxActionEmail && emit('open-mailbox-share', mailboxActionEmail)"
            >
              <Icon name="link" size="xs" :stroke-width="2" />
              <span>接码链接</span>
            </button>
          </div>

          <label class="team-oauth-side-label" for="team-history-mailbox">历史邮箱</label>
          <Select
            id="team-history-mailbox"
            data-testid="team-history-mailbox-select"
            class="team-oauth-mailbox-select"
            :model-value="selectedMailboxValue"
            :options="historyMailboxOptions"
            :disabled="mailboxSelecting || historyMailboxes.length === 0"
            placeholder="请选择历史邮箱"
            placement="bottom"
            searchable
            search-placeholder="搜索邮箱..."
            empty-text="暂无历史邮箱"
            aria-label="历史邮箱"
            @update:model-value="emit('select-mailbox', String($event ?? ''))"
          />

          <button
            type="button"
            class="team-oauth-mailbox-value w-full text-left"
            :disabled="!mailboxActionEmail"
            :title="mailboxActionEmail ? '点击邮箱框复制' : '尚未创建邮箱'"
            @click="mailboxActionEmail && emit('copy-mailbox', mailboxActionEmail)"
          >
            <span class="min-w-0 flex-1">
              <span class="team-oauth-side-label">当前邮箱</span>
              <code class="mt-1 block truncate">{{ mailboxActionEmail || '尚未创建' }}</code>
            </span>
            <Icon name="copy" size="xs" class="shrink-0 opacity-60" :stroke-width="2" aria-hidden="true" />
          </button>

          <div
            data-testid="team-sidebar-mailbox-code"
            class="team-oauth-mailbox-value"
            :class="mailboxCode ? 'cursor-copy' : ''"
            :title="mailboxCode ? '点击验证码框复制' : undefined"
            role="button"
            :tabindex="mailboxCode ? 0 : -1"
            @click="mailboxCode && emit('copy-mailbox-code', mailboxCode)"
            @keydown.enter.prevent="mailboxCode && emit('copy-mailbox-code', mailboxCode)"
            @keydown.space.prevent="mailboxCode && emit('copy-mailbox-code', mailboxCode)"
          >
            <div class="min-w-0 flex-1">
              <span class="team-oauth-side-label">邮箱验证码</span>
              <code data-testid="team-sidebar-mailbox-code-value" class="mt-1 block truncate">{{ mailboxCode || (mailboxCodePolling ? '轮询中' : '等待邮件') }}</code>
            </div>
            <button
              type="button"
              class="team-oauth-icon-button"
              title="立即检查邮箱"
              aria-label="立即检查邮箱"
              :disabled="!mailboxEmail || mailboxCodeLoading"
              @click.stop="emit('poll-mailbox')"
            >
              <Icon name="refresh" size="xs" :class="mailboxCodePolling || mailboxCodeLoading ? 'animate-spin' : ''" :stroke-width="2" />
            </button>
          </div>

          <div class="team-oauth-mailbox-actions">
            <button
              type="button"
              data-testid="team-create-mailbox"
              class="btn btn-secondary flex min-w-0 items-center justify-center gap-1.5 whitespace-nowrap !px-2 !py-1.5 text-xs"
              :disabled="createMailboxDisabled || !mailboxConfigured"
              title="创建新的 Team 临时邮箱并开始轮询"
              @click="emit('create-mailbox')"
            >
              <Icon name="mail" size="xs" :stroke-width="2" />
              <span>新建邮箱</span>
            </button>
            <button
              type="button"
              data-testid="team-open-mailbox"
              class="btn btn-secondary flex min-w-0 items-center justify-center gap-1.5 whitespace-nowrap !px-2 !py-1.5 text-xs"
              :disabled="mailboxActionsDisabled || mailboxSelecting || !mailboxActionEmail"
              title="打开选中的历史邮箱并重新轮询验证码"
              @click="mailboxActionEmail && emit('open-mailbox', mailboxActionEmail)"
            >
              <Icon name="refresh" size="xs" :class="mailboxSelecting ? 'animate-spin' : ''" :stroke-width="2" />
              <span>{{ mailboxSelecting ? '打开中' : '接收验证码' }}</span>
            </button>
          </div>

          <button type="button" data-testid="team-open-history" class="team-oauth-history-link" @click="emit('open-history')">
            <span>历史账号、状态与额度</span>
            <Icon name="arrowRight" size="xs" :stroke-width="2" />
          </button>
        </section>

        <template v-if="workflow.password_available">
          <section data-testid="team-child-password-panel" class="team-oauth-side-module" aria-labelledby="team-password-tools-title">
            <div class="team-oauth-side-module-heading">
              <span class="team-oauth-side-module-icon" aria-hidden="true">
                <Icon name="key" size="sm" :stroke-width="2" />
              </span>
              <h3 id="team-password-tools-title">本次登录密码</h3>
            </div>
            <div class="team-oauth-password-row">
              <code data-testid="team-child-password-value" class="min-w-0 flex-1 truncate font-mono text-xs text-gray-900 dark:text-gray-100">{{ revealedPassword || '•••••••••••••' }}</code>
              <button v-if="revealedPassword" type="button" class="team-oauth-icon-button" title="复制登录密码" aria-label="复制登录密码" @click="emit('copy-password')">
                <Icon name="copy" size="xs" :stroke-width="2" />
              </button>
              <button type="button" class="team-oauth-icon-button" :title="revealedPassword ? '隐藏登录密码' : '查看登录密码'" :aria-label="revealedPassword ? '隐藏登录密码' : '查看登录密码'" :disabled="passwordLoading" @click="revealedPassword ? emit('clear-password') : emit('reveal-password')">
                <Icon :name="passwordLoading ? 'refresh' : revealedPassword ? 'eyeOff' : 'eye'" size="xs" :class="passwordLoading ? 'animate-spin' : ''" :stroke-width="2" />
              </button>
            </div>
          </section>
        </template>

        <div class="team-oauth-sms-module">
          <PixlabSMSReceiver
            :active="receiverActive"
            :replacement-required="replacementRequired"
            :submission-key="workflow.current_node || ''"
            :automation-cancel-signal="smsCancelSignal"
            :auto-submit="true"
            :automation-mode="true"
            :automation-replace-after-ms="120000"
            @phone-ready="emit('phone-ready', $event)"
            @code-received="emit('sms-code-received', $event)"
            @session-cancelled="emit('session-cancelled')"
          />
        </div>
      </aside>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Icon } from '@/components/icons'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import TeamChildBrowserWorkspace from '@/components/admin/account/TeamChildBrowserWorkspace.vue'
import PixlabSMSReceiver from '@/components/account/PixlabSMSReceiver.vue'
import type { TeamChildWorkflow, TeamChildWorkflowNode } from '@/api/admin/teamChild'

const props = withDefaults(defineProps<{
  workflow: TeamChildWorkflow
  preflight?: boolean
  mailboxEmail?: string
  callbackURL?: string
  authUrl?: string
  showReauthorize?: boolean
  revealedPassword?: string
  passwordLoading?: boolean
  showOneClick?: boolean
  oneClickDisabled?: boolean
  oneClickLabel?: string
  historyMailboxes?: string[]
  selectedMailboxEmail?: string
  mailboxCode?: string
  mailboxCodeLoading?: boolean
  mailboxCodePolling?: boolean
  mailboxSelecting?: boolean
  mailboxConfigured?: boolean
  mailboxActionsDisabled?: boolean
  createMailboxDisabled?: boolean
  browserVisible?: boolean
  browserConfigured?: boolean
  browserEmbedUrl?: string
  browserLoading?: boolean
  browserError?: string
  browserControlConflict?: boolean
  smsCancelSignal?: number
  automationEnabled?: boolean
}>(), {
  mailboxEmail: '',
  preflight: false,
  callbackURL: '',
  authUrl: '',
  showReauthorize: false,
  revealedPassword: '',
  passwordLoading: false,
  showOneClick: false,
  oneClickDisabled: false,
  oneClickLabel: '一键授权',
  historyMailboxes: () => [],
  selectedMailboxEmail: '',
  mailboxCode: '',
  mailboxCodeLoading: false,
  mailboxCodePolling: false,
  mailboxSelecting: false,
  mailboxConfigured: false,
  mailboxActionsDisabled: false,
  createMailboxDisabled: false,
  browserVisible: false,
  browserConfigured: false,
  browserEmbedUrl: '',
  browserLoading: false,
  browserError: '',
  browserControlConflict: false,
  smsCancelSignal: 0,
  automationEnabled: false
})

const emit = defineEmits<{
  'session-cancelled': []
  reauthorize: []
  'one-click': []
  'copy-auth': []
  'copy-callback': []
  'open-browser': []
  'continue-workflow': []
  'pause-workflow': []
  'reset-workflow': []
  'reveal-password': []
  'clear-password': []
  'copy-password': []
  'phone-ready': [phone: string]
  'sms-code-received': [code: string]
  'select-mailbox': [email: string]
  'create-mailbox': []
  'open-mailbox': [email: string]
  'poll-mailbox': []
  'open-mailbox-share': [email: string]
  'copy-mailbox': [email: string]
  'copy-mailbox-code': [code: string]
  'open-history': [],
  'reload-browser': [],
  'refresh-members': [],
  'open-modular': [],
  'force-take-over': []
}>()

const receiverNodes = new Set(['sms_confirm', 'phone_submit', 'sms_poll', 'sms_code'])
const receiverActive = computed(() => props.automationEnabled
  && !props.workflow.pause_requested
  && !['callback_ready', 'completed', 'paused', 'cancelled'].includes(props.workflow.status)
  && receiverNodes.has(props.workflow.current_node || ''))
const replacementRequired = computed(() => props.workflow.status === 'failed'
  && props.workflow.current_node === 'phone_submit'
  && /未进入短信验证码页面|手机号.*(?:不可用|次数过多)|phone.*(?:invalid|unavailable|too many)|verification.*page/i.test(props.workflow.error || ''))
const selectedMailboxValue = computed(() => props.selectedMailboxEmail || props.mailboxEmail || props.historyMailboxes[0] || '')
const mailboxActionEmail = computed(() => selectedMailboxValue.value || props.mailboxEmail)
const historyMailboxOptions = computed<SelectOption[]>(() => props.historyMailboxes.map((email) => ({ value: email, label: email })))

const workflowNodes = computed<TeamChildWorkflowNode[]>(() => props.workflow.nodes)
const currentNode = computed(() => {
  const reported = props.workflow.current_node
    ? workflowNodes.value.find((node) => node.key === props.workflow.current_node)
    : undefined
  if (reported) return reported
  const active = workflowNodes.value.find((node) => ['running', 'waiting', 'failed'].includes(node.status))
  if (active) return active
  const pending = workflowNodes.value.find((node) => node.status === 'pending')
  if (pending) return pending
  return workflowNodes.value[workflowNodes.value.length - 1]
})
const currentNodeIndex = computed(() => Math.max(0, workflowNodes.value.findIndex((node) => node.key === currentNode.value?.key)))
const completedCount = computed(() => workflowNodes.value.filter((node) => node.status === 'completed').length)
const terminalNode = computed<TeamChildWorkflowNode | null>(() => {
  if (props.workflow.status !== 'completed') return null
  const last = workflowNodes.value[workflowNodes.value.length - 1]
  if (!last) return null
  return {
    ...last,
    label: props.workflow.mode === 'reauthorization' ? '已完成 Team 账号重新授权' : '已完成创建 Team 子账号',
    message: props.workflow.mode === 'reauthorization' ? '新的 OAuth 凭据已覆盖保存到原账号。' : '账号已按勾选配置保存到 XIASS。',
    status: 'completed'
  }
})
const visibleStackNodes = computed(() => terminalNode.value
  ? [terminalNode.value]
  : workflowNodes.value.slice(currentNodeIndex.value, currentNodeIndex.value + 3))

function cardStatusLabel(node: TeamChildWorkflowNode, stackIndex: number): string {
  if (node.status === 'failed') return '需要处理'
  if (node.status === 'completed') return '已完成'
  if (stackIndex === 0) return node.status === 'waiting' ? '等待当前节点' : '当前节点'
  return '下一项'
}

function cardMessage(node: TeamChildWorkflowNode, stackIndex: number): string {
  if (stackIndex > 0) return '完成上一步后自动进入。'
  if (node.key === 'sms_confirm') return '正在通过 XIASS SMS 服务自动领取手机号。'
  if (node.key === 'sms_poll') return '正在等待 XIASS SMS 服务返回验证码，最长等待 2 分钟。'
  if (node.key === 'import') return '回调已就绪，等待按已勾选配置导入。'
  return '正在等待服务器自动化状态。'
}

function cardProgress(node: TeamChildWorkflowNode, stackIndex: number): number {
  if (node.status === 'completed') return 100
  if (node.status === 'failed') return 64
  if (stackIndex === 0) return 48
  return 0
}

function cardClass(node: TeamChildWorkflowNode, stackIndex: number): string {
  if (node.status === 'failed') return 'team-oauth-card-error border-red-300 dark:border-red-900/70'
  if (node.status === 'completed') return 'team-oauth-card-complete border-emerald-200 dark:border-emerald-900/70'
  if (stackIndex === 0) return 'team-oauth-card-current border-primary-300 dark:border-primary-900/70'
  return 'team-oauth-card-pending border-gray-200 dark:border-dark-600'
}

function displayNodeLabel(node: { key: string; label: string }) {
  if (props.workflow.mode === 'reauthorization') {
    if (node.key === 'signup') return '进入已有账号登录'
    if (node.key === 'email') return '填入目标登录邮箱'
    if (node.key === 'password') return '判断是否需要登录密码'
    if (node.key === 'mail') return '判断是否需要邮箱验证码'
    if (node.key === 'mailbox') return '轮询登录邮箱验证码'
    if (node.key === 'email_code') return '自动填入邮箱验证码'
    if (node.key === 'workspace') return '选择默认工作空间'
    if (node.key === 'import') return '覆盖导入原 Team 账号'
  }
  if (node.key === 'members') return '读取并确认成员席位'
  if (node.key === 'remove') return '移除普通成员'
  if (node.key === 'invite') return '邀请并确认 Pending invites'
  if (node.key === 'invite_confirm') return '确认 Pending invites'
  if (node.key === 'sms_confirm') return '自动领取手机号'
  return node.label
}

</script>

<style scoped>
.team-oauth-workspace {
  box-sizing: border-box;
  width: 100%;
  max-width: 1120px;
}

.team-oauth-layout {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) clamp(16rem, 23vw, 18rem);
  gap: 1.25rem;
  align-items: stretch;
}

.team-oauth-layout > * {
  min-width: 0;
  max-width: 100%;
}

.team-oauth-main-column {
  display: flex;
  height: 100%;
  min-width: 0;
  flex-direction: column;
  gap: 1rem;
}

.team-oauth-side-column {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.9rem;
  padding: 1.1rem;
  border: 1px solid rgb(229 231 235);
  border-radius: 18px;
  background: rgb(255 255 255);
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.05);
}

:global(.dark .team-oauth-side-column) {
  border-color: rgb(55 65 81);
  background: rgb(17 24 39);
}

.team-oauth-deck,
.team-oauth-operation-card {
  width: 100%;
}

.team-oauth-operation-card {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
}

.team-oauth-inline-browser {
  display: flex;
  min-height: 0;
  flex: 1 1 auto;
  flex-direction: column;
}

.team-oauth-inline-browser :deep(.team-child-browser-workspace) {
  display: flex;
  min-height: 0;
  flex: 1 1 auto;
  flex-direction: column;
  overflow: hidden;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.team-oauth-inline-browser :deep(.team-child-browser-frame) {
  min-height: 0;
  height: auto;
  width: 100%;
  max-height: none;
  flex: 1 1 auto;
  aspect-ratio: auto;
}

.team-oauth-operation-body {
  display: flex;
  min-height: 0;
  flex: 1 1 auto;
  align-items: flex-start;
  justify-content: center;
}

.team-oauth-operation-content {
  width: 100%;
  max-width: 52rem;
  margin-inline: auto;
}

.team-oauth-operation-actions {
  display: flex;
  min-width: 0;
  flex-shrink: 0;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
}

.team-oauth-operation-actions .btn {
  min-width: 0;
  max-width: 100%;
}

.team-oauth-operation-card,
.team-oauth-step-card {
  border-radius: 18px !important;
}

.team-oauth-mailbox-actions :deep(.btn) {
  border-radius: 11px;
}

.team-oauth-side-module {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.65rem;
  border: 1px solid rgb(229 231 235);
  border-radius: 14px;
  background: rgb(249 250 251);
  padding: 0.8rem;
}

:global(.dark .team-oauth-side-module) {
  border-color: rgb(75 85 99);
  background: rgb(20 34 53 / 0.72);
}

.team-oauth-side-module-heading {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.5rem;
  color: rgb(17 24 39);
}

.team-oauth-side-module-heading h3 {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.8rem;
  font-weight: 600;
}

.team-oauth-side-module-icon {
  display: inline-flex;
  height: 1.75rem;
  width: 1.75rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  background: rgb(14 165 233 / 0.12);
  color: rgb(2 132 199);
}

:global(.dark .team-oauth-side-module-heading) {
  color: rgb(243 244 246);
}

:global(.dark .team-oauth-side-module-icon) {
  background: rgb(8 145 178 / 0.2);
  color: rgb(103 232 249);
}

.team-oauth-side-label {
  display: block;
  color: rgb(107 114 128);
  font-size: 0.68rem;
  line-height: 1rem;
}

:global(.dark .team-oauth-side-label) {
  color: rgb(156 163 175);
}

.team-oauth-mailbox-value,
.team-oauth-password-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.5rem;
  border: 1px solid rgb(229 231 235);
  border-radius: 11px;
  background: rgb(249 250 251);
  padding: 0.55rem 0.6rem;
}

.team-oauth-mailbox-value > div {
  min-width: 0;
  flex: 1;
}

.team-oauth-mailbox-value code {
  min-width: 0;
  color: rgb(17 24 39);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.72rem;
  line-height: 1.1rem;
}

.team-oauth-password-row {
  background: rgb(243 244 246);
}

:global(.dark .team-oauth-mailbox-value),
:global(.dark .team-oauth-password-row) {
  border-color: rgb(55 65 81);
  background: rgb(31 41 55 / 0.68);
}

:global(.dark .team-oauth-mailbox-value code) {
  color: rgb(229 231 235);
}

.team-oauth-icon-button {
  display: inline-flex;
  height: 1.8rem;
  width: 1.8rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(209 213 219);
  border-radius: 10px;
  color: rgb(75 85 99);
  transition: border-color 160ms ease, background-color 160ms ease, color 160ms ease;
}

.team-oauth-icon-button:hover:not(:disabled) {
  border-color: rgb(14 165 233);
  background: rgb(224 242 254);
  color: rgb(3 105 161);
}

.team-oauth-icon-button:focus-visible {
  outline: 2px solid rgb(14 165 233);
  outline-offset: 2px;
}

.team-oauth-icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

:global(.dark .team-oauth-icon-button) {
  border-color: rgb(75 85 99);
  color: rgb(209 213 219);
}

:global(.dark .team-oauth-icon-button:hover:not(:disabled)) {
  border-color: rgb(34 211 238);
  background: rgb(8 145 178 / 0.2);
  color: rgb(165 243 252);
}

.team-oauth-mailbox-actions {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
}

.team-oauth-history-link {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  color: rgb(2 132 199);
  font-size: 0.7rem;
  text-align: left;
  transition: color 160ms ease;
}

.team-oauth-history-link:hover {
  color: rgb(3 105 161);
}

.team-oauth-history-link span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(.dark .team-oauth-history-link) {
  color: rgb(103 232 249);
}

:global(.dark .team-oauth-history-link:hover) {
  color: rgb(165 243 252);
}

.team-oauth-sms-module {
  display: flex;
  flex: 1;
  min-width: 0;
  width: 100%;
  max-width: 100%;
  align-items: center;
  border: 1px solid rgb(229 231 235);
  border-radius: 14px;
  background: rgb(249 250 251);
  padding: 0.8rem;
}

.team-oauth-sms-module :deep(section) {
  width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
}

:global(.dark .team-oauth-sms-module) {
  border-color: rgb(75 85 99);
  background: rgb(20 34 53 / 0.72);
}

.team-oauth-step-stack {
  position: relative;
  width: 100%;
  min-height: 12rem;
  overflow: visible;
  isolation: isolate;
}

.team-oauth-step-stack-inner {
  position: relative;
  min-height: 12rem;
  width: 100%;
  overflow: visible;
}

.team-oauth-step-card {
  position: absolute;
  inset: 0;
  z-index: calc(30 - var(--stack-index));
  left: calc((2 - var(--stack-index)) * 7%);
  right: auto;
  width: 86%;
  transform: translate3d(0, calc(var(--stack-index) * 9px), 0) scale(calc(1 - var(--stack-index) * 0.018));
  transform-origin: top center;
  pointer-events: auto;
  border-color: rgb(103 232 249 / 0.52) !important;
  background-color: #034a68;
  background-image: url('@/assets/team-child-workflow-card.jpg');
  background-position: center;
  background-size: cover;
  color: white;
  box-shadow: 0 16px 36px rgb(2 28 47 / 0.24), inset 0 0 0 1px rgb(165 243 252 / 0.12);
}

.team-oauth-step-card-active {
  cursor: default;
}

.team-oauth-step-card-active:hover {
  transform: translate3d(0, 0, 0) scale(1.012);
}

.team-oauth-step-card:not(.team-oauth-step-card-active):hover {
  transform: translate3d(0, calc(var(--stack-index) * 9px), 0) scale(1.01);
}

.team-oauth-card-pending {
  background-position: 45% center;
  filter: saturate(0.88) brightness(0.8);
}

.team-oauth-card-complete {
  border-color: rgb(110 231 183 / 0.7) !important;
}

.team-oauth-card-error {
  border-color: rgb(252 165 165 / 0.85) !important;
}

.team-oauth-step-card > :last-child {
  background: rgb(4 36 57 / 0.74);
}

.team-step-card-enter-active,
.team-step-card-leave-active,
.team-step-card-move {
  transition: transform 360ms cubic-bezier(0.22, 1, 0.36, 1), opacity 220ms ease;
}

.team-step-card-leave-active {
  z-index: 40;
}

.team-step-card-leave-to {
  opacity: 0;
  transform: translate3d(-112%, 16px, 0) rotate(-3deg) !important;
}

.team-step-card-enter-from {
  opacity: 0;
  transform: translate3d(18%, 12px, 0) scale(0.98);
}

@media (max-width: 1023px) {
  .team-oauth-workspace {
    width: 100%;
    max-width: none;
  }

  .team-oauth-layout {
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
  }

  .team-oauth-side-column {
    min-height: auto;
  }

  .team-oauth-operation-actions {
    width: 100%;
    justify-content: stretch;
  }

  .team-oauth-operation-actions .btn {
    flex: 1 1 0;
  }

  .team-oauth-sms-module {
    width: 100%;
    max-width: 100%;
    margin-inline: 0;
  }
}

@media (max-width: 639px) {
  .team-oauth-step-stack,
  .team-oauth-step-stack-inner {
    min-height: 12.5rem;
  }

  .team-oauth-operation-actions {
    flex-direction: column;
    align-items: stretch;
  }

  .team-oauth-step-card {
    left: 0;
    width: 100%;
  }

  .team-oauth-operation-actions .btn {
    width: 100%;
  }

  .team-oauth-inline-browser :deep(.team-child-browser-frame) {
    min-height: 280px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .team-oauth-step-card,
  .team-step-card-enter-active,
  .team-step-card-leave-active,
  .team-step-card-move {
    transition-duration: 1ms !important;
  }
}
</style>
