<template>
  <Teleport to="body">
    <div v-if="show && (position || anchorRect)">
      <!-- Backdrop: click anywhere outside to close -->
      <div class="fixed inset-0 z-[9998]" @click="emit('close')"></div>
      <div
        ref="menuRef"
        class="action-menu-content fixed z-[9999] w-52 overflow-y-auto overscroll-contain rounded-xl bg-white shadow-lg ring-1 ring-black/5 dark:bg-dark-800"
        :style="menuStyle"
        @click.stop
      >
        <div class="py-1">
          <template v-if="account">
            <button :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('test', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="play" size="sm" class="text-green-500" :stroke-width="2" />
              {{ t('admin.accounts.testConnection') }}
            </button>
            <button @click="$emit('stats', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700">
              <Icon name="chart" size="sm" class="text-indigo-500" />
              {{ t('admin.accounts.viewStats') }}
            </button>
            <button v-if="isOpenAICodexAccount" @click="$emit('codex-ticket', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700">
              <Icon name="shield" size="sm" class="text-emerald-500" />
              {{ t('admin.accounts.codexTicket.viewStatus') }}
            </button>
            <button :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('schedule', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="clock" size="sm" class="text-orange-500" />
              {{ t('admin.scheduledTests.schedule') }}
            </button>
            <button v-if="hasGroups" :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('manage-user-allowlist', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="users" size="sm" class="text-primary-500" />
              {{ t('admin.groups.userAccountAllowlist.title') }}
            </button>
            <button v-if="canDuplicate" :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('duplicate', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="copy" size="sm" class="text-sky-500" />
              {{ t('admin.accounts.duplicateAccount') }}
            </button>
            <!-- 影子账号不持凭据:重授权/刷新 token 对其无效(后端拒绝),故隐藏(外审 G4)。 -->
            <template v-if="(account.type === 'oauth' || account.type === 'setup-token') && !isShadow && !isOpenAIOAuthCredentialCopy">
              <button :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('reauth', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-blue-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
                <Icon name="link" size="sm" />
                {{ t('admin.accounts.reAuthorize') }}
              </button>
              <button :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('refresh-token', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-purple-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
                <Icon name="refresh" size="sm" />
                {{ t('admin.accounts.refreshToken') }}
              </button>
            </template>
            <button v-if="isOpenAIOAuthParent" :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('create-spark-shadow', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-amber-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="sparkles" size="sm" />
              {{ t('admin.accounts.createSparkShadow') }}
            </button>
            <button v-if="supportsPrivacy" :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('set-privacy', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-emerald-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="shield" size="sm" />
              {{ t('admin.accounts.setPrivacy') }}
            </button>
            <div v-if="hasRecoverableState" class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
            <button v-if="hasRecoverableState" :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('recover-state', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-emerald-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="sync" size="sm" />
              {{ t('admin.accounts.recoverState') }}
            </button>
            <button v-if="hasQuotaLimit" :disabled="!canManage" :title="!canManage ? managementBlockReason : undefined" @click="$emit('reset-quota', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-teal-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700">
              <Icon name="refresh" size="sm" />
              {{ t('admin.accounts.resetQuota') }}
            </button>
          </template>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import { useResizeObserver, useWindowSize } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { Account } from '@/types'

const props = withDefaults(defineProps<{
  show: boolean
  account: Account | null
  position?: { top: number; left: number } | null
  anchorRect?: DOMRect | null
  canManage?: boolean
  managementBlockReason?: string
}>(), {
  canManage: true,
  managementBlockReason: '',
  position: null,
  anchorRect: null
})
const emit = defineEmits(['close', 'test', 'stats', 'codex-ticket', 'schedule', 'manage-user-allowlist', 'duplicate', 'reauth', 'refresh-token', 'recover-state', 'reset-quota', 'set-privacy', 'create-spark-shadow'])
const { t } = useI18n()
const menuRef = ref<HTMLElement | null>(null)
const { width: viewportWidth, height: viewportHeight } = useWindowSize()
const viewportPadding = 8
const measuredPosition = ref({ top: viewportPadding, left: viewportPadding })
const menuStyle = computed(() => {
  const current = props.position ?? measuredPosition.value
  return {
    top: `${current.top}px`,
    left: `${current.left}px`,
    maxWidth: `${Math.max(0, viewportWidth.value - viewportPadding * 2)}px`,
    maxHeight: `${Math.max(0, viewportHeight.value - viewportPadding * 2)}px`
  }
})

const updatePosition = () => {
  if (props.position || !menuRef.value || !props.anchorRect) return
  const { width, height } = menuRef.value.getBoundingClientRect()
  const anchor = props.anchorRect
  const gap = 4
  const maxTop = viewportHeight.value - height - viewportPadding
  const top = anchor.bottom + gap <= maxTop
    ? anchor.bottom + gap
    : anchor.top - height - gap
  const left = viewportWidth.value < 768
    ? anchor.left + anchor.width / 2 - width / 2
    : anchor.right - width

  measuredPosition.value.top = Math.max(viewportPadding, Math.min(top, maxTop))
  measuredPosition.value.left = Math.max(
    viewportPadding,
    Math.min(left, viewportWidth.value - width - viewportPadding)
  )
}

watch(
  [menuRef, () => props.anchorRect, () => props.show, viewportWidth, viewportHeight],
  updatePosition,
  { flush: 'post' }
)
useResizeObserver(menuRef, updatePosition)
const canManage = computed(() => props.canManage)
const managementBlockReason = computed(() => props.managementBlockReason || t('admin.accounts.executionNodeRemoteUnavailable'))
const canDuplicate = computed(() => {
  if (!props.account || props.account.parent_account_id != null) return false
  return ['apikey', 'upstream', 'bedrock', 'service_account'].includes(props.account.type)
    || (props.account.platform === 'openai' && props.account.type === 'oauth' && !isOpenAIAgentIdentity.value)
})
const hasGroups = computed(() => {
  if (!props.account) return false
  return (props.account.group_ids?.length ?? props.account.groups?.length ?? 0) > 0
})
const isRateLimited = computed(() => {
  if (props.account?.rate_limit_reset_at && new Date(props.account.rate_limit_reset_at) > new Date()) {
    return true
  }
  const modelLimits = (props.account?.extra as Record<string, unknown> | undefined)?.model_rate_limits as
    | Record<string, { rate_limit_reset_at: string }>
    | undefined
  if (modelLimits) {
    const now = new Date()
    return Object.values(modelLimits).some(info => new Date(info.rate_limit_reset_at) > now)
  }
  return false
})
const isOverloaded = computed(() => props.account?.overload_until && new Date(props.account.overload_until) > new Date())
const isTempUnschedulable = computed(() => props.account?.temp_unschedulable_until && new Date(props.account.temp_unschedulable_until) > new Date())
const hasRecoverableState = computed(() => {
  return props.account?.status === 'error' || Boolean(isRateLimited.value) || Boolean(isOverloaded.value) || Boolean(isTempUnschedulable.value)
})
const isAntigravityOAuth = computed(() => props.account?.platform === 'antigravity' && props.account?.type === 'oauth')
const isOpenAIOAuth = computed(() => props.account?.platform === 'openai' && props.account?.type === 'oauth')
const isOpenAICodexAccount = computed(() => props.account?.platform === 'openai' && ['oauth', 'setup-token'].includes(props.account.type))
const isOpenAIAgentIdentity = computed(() => {
  const authMode = (props.account?.credentials as Record<string, unknown> | undefined)?.auth_mode
  return isOpenAIOAuth.value && typeof authMode === 'string' && authMode.trim().toLowerCase() === 'agentidentity'
})
// 影子账号(链接型,持 parent_account_id)不持凭据、type 不可变,凭据/隐私类操作对其无效。
const isShadow = computed(() => props.account?.parent_account_id != null)
const isOpenAIOAuthCredentialCopy = computed(() => {
  const sourceID = Number((props.account?.extra as Record<string, unknown> | undefined)?.xiass_openai_oauth_credential_source_id)
  return isOpenAIOAuth.value && Number.isInteger(sourceID) && sourceID > 0 && sourceID !== props.account?.id
})
// A "parent" OpenAI OAuth account is one that is NOT itself a shadow (parent_account_id == null)
const isOpenAIOAuthParent = computed(() => isOpenAIOAuth.value && !isShadow.value && !isOpenAIOAuthCredentialCopy.value)
const supportsPrivacy = computed(() => (isAntigravityOAuth.value || isOpenAIOAuth.value) && !isShadow.value && !isOpenAIOAuthCredentialCopy.value)
const hasQuotaLimit = computed(() => {
  return (props.account?.type === 'apikey' || props.account?.type === 'bedrock') && (
    (props.account?.quota_limit ?? 0) > 0 ||
    (props.account?.quota_daily_limit ?? 0) > 0 ||
    (props.account?.quota_weekly_limit ?? 0) > 0
  )
})

const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') emit('close')
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      window.addEventListener('keydown', handleKeydown)
    } else {
      window.removeEventListener('keydown', handleKeydown)
    }
  },
  { immediate: true }
)

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
})
</script>
