<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.reAuthorizeAccount')"
    width="normal"
    @close="handleClose"
  >
    <div v-if="account" class="space-y-4">
      <!-- Account Info -->
      <div
        class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-700"
      >
        <div class="flex items-center gap-3">
          <div
            :class="[
              'flex h-10 w-10 items-center justify-center rounded-lg bg-gradient-to-br',
              isOpenAILike
                ? 'from-green-500 to-green-600'
                : isGemini
                  ? 'from-blue-500 to-blue-600'
                  : isAntigravity
                    ? 'from-purple-500 to-purple-600'
                    : isGrok
                      ? 'from-zinc-700 to-zinc-900'
                      : 'from-orange-500 to-orange-600'
            ]"
          >
            <Icon name="sparkles" size="md" class="text-white" />
          </div>
          <div>
            <span class="block font-semibold text-gray-900 dark:text-white">{{
              account.name
            }}</span>
            <span class="text-sm text-gray-500 dark:text-gray-400">
              {{
                isOpenAI
                  ? t('admin.accounts.openaiAccount')
                  : isGemini
                    ? t('admin.accounts.geminiAccount')
                    : isAntigravity
                      ? t('admin.accounts.antigravityAccount')
                      : isGrok
                        ? t('admin.accounts.grokAccount')
                        : t('admin.accounts.claudeCodeAccount')
              }}
            </span>
          </div>
        </div>
      </div>

      <div v-if="adsPowerBinding" class="rounded-lg border border-cyan-200 bg-cyan-50/70 p-3 dark:border-cyan-900/60 dark:bg-cyan-950/20">
        <div class="flex min-w-0 items-start gap-3">
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-cyan-100 text-cyan-700 dark:bg-cyan-950/70 dark:text-cyan-300">
            <Icon name="globe" size="sm" :stroke-width="2" />
          </span>
          <div class="min-w-0 flex-1">
            <p class="text-sm font-semibold text-cyan-900 dark:text-cyan-100">已绑定固定指纹环境</p>
            <p class="mt-1 truncate text-xs text-cyan-800/80 dark:text-cyan-200/80">
              {{ adsPowerBinding.profile_name || adsPowerBinding.profile_id }} · {{ adsPowerBinding.environment_key }}<template v-if="adsPowerBinding.proxy_exit_ip"> · {{ adsPowerBinding.proxy_exit_ip }}</template>
            </p>
            <p class="mt-1 text-xs text-cyan-700 dark:text-cyan-300">随机指纹 · WebRTC 已关闭 · 后续授权继续复用</p>
          </div>
          <button type="button" class="btn btn-secondary flex h-9 w-9 shrink-0 items-center justify-center p-0" title="解除固定环境绑定" aria-label="解除固定环境绑定" @click="showAdsPowerUnbindConfirm = true">
            <Icon name="trash" size="sm" class="text-red-500" :stroke-width="2" />
          </button>
        </div>
      </div>

      <div v-if="isTeamChildAccount" class="rounded-lg border border-primary-200 bg-primary-50/50 p-4 dark:border-primary-900/60 dark:bg-primary-950/15">
        <div class="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div class="min-w-0">
            <div class="text-sm font-medium text-gray-900 dark:text-gray-100">Team 子号登录信息</div>
            <div class="mt-1 truncate text-xs text-gray-600 dark:text-gray-300" :title="teamChildEmail">{{ teamChildEmail }}</div>
            <code class="mt-1 block truncate font-mono text-sm text-gray-900 dark:text-gray-100">{{ revealedTeamPassword || '•••••••••••••' }}</code>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <button v-if="revealedTeamPassword" type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" title="复制登录密码" aria-label="复制登录密码" @click="copyTeamPassword">
              <Icon name="copy" size="sm" />
            </button>
            <button type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" :disabled="teamPasswordLoading" @click="revealedTeamPassword ? clearTeamPassword() : revealTeamPassword()">
              <Icon :name="teamPasswordLoading ? 'refresh' : revealedTeamPassword ? 'eyeOff' : 'eye'" size="sm" :class="teamPasswordLoading ? 'animate-spin' : ''" />
              <span>{{ teamPasswordLoading ? '验证中' : revealedTeamPassword ? '隐藏密码' : '查看保存密码' }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- Add Method Selection (Claude only) -->
      <fieldset v-if="isAnthropic" class="border-0 p-0">
        <legend class="input-label">{{ t('admin.accounts.oauth.authMethod') }}</legend>
        <div class="mt-2 flex gap-4">
          <label class="flex cursor-pointer items-center">
            <input
              v-model="addMethod"
              type="radio"
              value="oauth"
              class="mr-2 text-primary-600 focus:ring-primary-500"
            />
            <span class="text-sm text-gray-700 dark:text-gray-300">{{
              t('admin.accounts.types.oauth')
            }}</span>
          </label>
          <label class="flex cursor-pointer items-center">
            <input
              v-model="addMethod"
              type="radio"
              value="setup-token"
              class="mr-2 text-primary-600 focus:ring-primary-500"
            />
            <span class="text-sm text-gray-700 dark:text-gray-300">{{
              t('admin.accounts.setupTokenLongLived')
            }}</span>
          </label>
        </div>
      </fieldset>

      <!-- Gemini OAuth Type Display (read-only) -->
      <div v-if="isGemini" class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-700">
        <div class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('admin.accounts.oauth.gemini.oauthTypeLabel') }}
        </div>
        <div class="flex items-center gap-3">
          <div
            :class="[
              'flex h-8 w-8 shrink-0 items-center justify-center rounded-lg',
              geminiOAuthType === 'google_one'
                ? 'bg-purple-500 text-white'
                : geminiOAuthType === 'code_assist'
                  ? 'bg-blue-500 text-white'
                  : 'bg-amber-500 text-white'
            ]"
          >
            <Icon v-if="geminiOAuthType === 'google_one'" name="user" size="sm" />
            <Icon v-else-if="geminiOAuthType === 'code_assist'" name="cloud" size="sm" />
            <Icon v-else name="sparkles" size="sm" />
          </div>
          <div>
            <span class="block text-sm font-medium text-gray-900 dark:text-white">
              {{
                geminiOAuthType === 'google_one'
                  ? 'Google One'
                  : geminiOAuthType === 'code_assist'
                    ? t('admin.accounts.gemini.oauthType.builtInTitle')
                    : t('admin.accounts.gemini.oauthType.customTitle')
              }}
            </span>
            <span class="text-xs text-gray-500 dark:text-gray-400">
              {{
                geminiOAuthType === 'google_one'
                  ? t('admin.accounts.gemini.oauthType.googleOneDesc')
                  : geminiOAuthType === 'code_assist'
                    ? t('admin.accounts.gemini.oauthType.builtInDesc')
                    : t('admin.accounts.gemini.oauthType.customDesc')
              }}
            </span>
          </div>
        </div>
      </div>

      <OAuthAuthorizationFlow
        ref="oauthFlowRef"
        :add-method="addMethod"
        :auth-url="currentAuthUrl"
        :session-id="currentSessionId"
        :loading="currentLoading"
        :error="currentError"
        :show-help="isAnthropic"
        :show-proxy-warning="isAnthropic"
        :show-cookie-option="isAnthropic"
        :show-refresh-token-option="isOpenAILike || isAntigravity || isGrok"
        :allow-multiple="false"
        :method-label="t('admin.accounts.inputMethod')"
        :platform="isOpenAI ? 'openai' : isGemini ? 'gemini' : isAntigravity ? 'antigravity' : isGrok ? 'grok' : 'anthropic'"
        :show-project-id="isGemini && geminiOAuthType === 'code_assist'"
        :show-ads-power-option="adsPowerEligible"
        :ads-power-launching="adsPowerLaunching"
        @generate-url="handleGenerateUrl"
        @launch-adspower="handleLaunchAdsPower"
        @cookie-auth="handleCookieAuth"
        @validate-refresh-token="handleValidateRefreshToken"
      />

    </div>

    <template #footer>
      <div v-if="account" class="flex justify-between gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          v-if="isManualInputMethod"
          type="button"
          :disabled="!canExchangeCode"
          class="btn btn-primary"
          @click="handleExchangeCode"
        >
          <svg
            v-if="currentLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
            fill="none"
            viewBox="0 0 24 24"
          >
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="4"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
          </svg>
          {{
            currentLoading
              ? t('admin.accounts.oauth.verifying')
              : t('admin.accounts.oauth.completeAuth')
          }}
        </button>
      </div>
    </template>
  </BaseDialog>
  <ConfirmDialog
    :show="showAdsPowerUnbindConfirm"
    title="解除固定环境绑定"
    message="解除后，下次固定环境授权会创建新的账号专属浏览器。AdsPower 中原有环境不会自动删除。"
    confirm-text="解除绑定"
    cancel-text="取消"
    danger
    @confirm="handleUnbindAdsPower"
    @cancel="showAdsPowerUnbindConfirm = false"
  />
  <TotpStepUpDialog :controller="teamPasswordStepUp" />
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { teamChildAPI } from '@/api/admin/teamChild'
import {
  useAccountOAuth,
  type AddMethod,
  type AuthInputMethod
} from '@/composables/useAccountOAuth'
import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import { useGeminiOAuth } from '@/composables/useGeminiOAuth'
import { useAntigravityOAuth } from '@/composables/useAntigravityOAuth'
import { useGrokOAuth } from '@/composables/useGrokOAuth'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import type { Account } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import OAuthAuthorizationFlow from '@/components/account/OAuthAuthorizationFlow.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

// Type for exposed OAuthAuthorizationFlow component
// Note: defineExpose automatically unwraps refs, so we use the unwrapped types
interface OAuthFlowExposed {
  authCode: string
  oauthState: string
  projectId: string
  sessionKey: string
  inputMethod: AuthInputMethod
  reset: () => void
}

interface Props {
  show: boolean
  account: Account | null
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
  reauthorized: [account: Account]
}>()

const appStore = useAppStore()
const { t } = useI18n()

// OAuth composables
const claudeOAuth = useAccountOAuth()
const openaiOAuth = useOpenAIOAuth()
const geminiOAuth = useGeminiOAuth()
const antigravityOAuth = useAntigravityOAuth()
const grokOAuth = useGrokOAuth()
const teamPasswordStepUp = useStepUp()

// Refs
const oauthFlowRef = ref<OAuthFlowExposed | null>(null)

// State
const addMethod = ref<AddMethod>('oauth')
const geminiOAuthType = ref<'code_assist' | 'google_one' | 'ai_studio'>('code_assist')
const revealedTeamPassword = ref('')
const teamPasswordLoading = ref(false)
const adsPowerLaunching = ref(false)
const adsPowerSessionId = ref('')
const adsPowerBindingCleared = ref(false)
const adsPowerUnbinding = ref(false)
const showAdsPowerUnbindConfirm = ref(false)

// Computed - check platform
const isOpenAI = computed(() => props.account?.platform === 'openai')
const isOpenAILike = computed(() => isOpenAI.value)
const isGemini = computed(() => props.account?.platform === 'gemini')
const isAnthropic = computed(() => props.account?.platform === 'anthropic')
const isAntigravity = computed(() => props.account?.platform === 'antigravity')
const isGrok = computed(() => props.account?.platform === 'grok')
const adsPowerEligible = computed(
  () => isOpenAI.value && props.account?.type === 'oauth' && !props.account?.parent_account_id
)
const adsPowerBinding = computed(() => {
  if (adsPowerBindingCleared.value) return null
  const value = (props.account?.extra as Record<string, unknown> | undefined)?.xiass_openai_adspower_binding
  if (!value || typeof value !== 'object') return null
  return value as {
    profile_id: string
    profile_name?: string
    environment_key: string
    proxy_exit_ip?: string
  }
})
const teamChildEmail = computed(() => {
  const value = (props.account?.extra as Record<string, unknown> | undefined)?.xiass_team_child_email
  return typeof value === 'string' ? value.trim().toLowerCase() : ''
})
const isTeamChildAccount = computed(() => isOpenAI.value
  && (props.account?.extra as Record<string, unknown> | undefined)?.xiass_team_child === true
  && Boolean(teamChildEmail.value)
  && props.account?.credentials_status?.has_xiass_team_child_password_encrypted === true)

// Computed - current OAuth state based on platform
const currentAuthUrl = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.authUrl.value
  if (isGemini.value) return geminiOAuth.authUrl.value
  if (isAntigravity.value) return antigravityOAuth.authUrl.value
  if (isGrok.value) return grokOAuth.authUrl.value
  return claudeOAuth.authUrl.value
})
const currentSessionId = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.sessionId.value
  if (isGemini.value) return geminiOAuth.sessionId.value
  if (isAntigravity.value) return antigravityOAuth.sessionId.value
  if (isGrok.value) return grokOAuth.sessionId.value
  return claudeOAuth.sessionId.value
})
const currentLoading = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.loading.value
  if (isGemini.value) return geminiOAuth.loading.value
  if (isAntigravity.value) return antigravityOAuth.loading.value
  if (isGrok.value) return grokOAuth.loading.value
  return claudeOAuth.loading.value
})
const currentError = computed(() => {
  if (isOpenAILike.value) return openaiOAuth.error.value
  if (isGemini.value) return geminiOAuth.error.value
  if (isAntigravity.value) return antigravityOAuth.error.value
  if (isGrok.value) return grokOAuth.error.value
  return claudeOAuth.error.value
})

// Computed
const isManualInputMethod = computed(() => {
  // OpenAI/Gemini/Antigravity always use manual input (no cookie auth option)
  return isOpenAILike.value || isGemini.value || isAntigravity.value || isGrok.value || oauthFlowRef.value?.inputMethod === 'manual'
})

const canExchangeCode = computed(() => {
  const authCode = oauthFlowRef.value?.authCode || ''
  const sessionId = currentSessionId.value
  const loading = currentLoading.value
  return authCode.trim() && sessionId && !loading
})

// Watchers
watch(
  () => props.show,
  (newVal) => {
    if (newVal && props.account) {
      adsPowerBindingCleared.value = false
      // Initialize addMethod based on current account type (Claude only)
      if (
        isAnthropic.value &&
        (props.account.type === 'oauth' || props.account.type === 'setup-token')
      ) {
        addMethod.value = props.account.type as AddMethod
      }
      if (isGemini.value) {
        const creds = (props.account.credentials || {}) as Record<string, unknown>
        geminiOAuthType.value =
          creds.oauth_type === 'google_one'
            ? 'google_one'
            : creds.oauth_type === 'ai_studio'
              ? 'ai_studio'
              : 'code_assist'
      }
    } else {
      resetState()
    }
  }
)

// Methods
const resetState = () => {
  addMethod.value = 'oauth'
  geminiOAuthType.value = 'code_assist'
  claudeOAuth.resetState()
  openaiOAuth.resetState()
  adsPowerSessionId.value = ''
  adsPowerBindingCleared.value = false
  adsPowerUnbinding.value = false
  showAdsPowerUnbindConfirm.value = false
  geminiOAuth.resetState()
  antigravityOAuth.resetState()
  grokOAuth.resetState()
  oauthFlowRef.value?.reset()
  clearTeamPassword()
}

const handleClose = () => {
  clearTeamPassword()
  emit('close')
}

function clearTeamPassword() {
  revealedTeamPassword.value = ''
}

async function copyTeamPassword() {
  if (!revealedTeamPassword.value) return
  try {
    await navigator.clipboard.writeText(revealedTeamPassword.value)
    appStore.showSuccess('登录密码已复制')
  } catch {
    appStore.showError('复制失败，请手动复制')
  }
}

async function revealTeamPassword() {
  if (!props.account || !isTeamChildAccount.value || teamPasswordLoading.value) return
  teamPasswordLoading.value = true
  try {
    const secret = await teamPasswordStepUp.run(() => teamChildAPI.revealTeamChildAccountPassword(props.account!.id))
    if (secret.email.trim().toLowerCase() !== teamChildEmail.value) throw new Error('保存的 Team 邮箱不一致')
    revealedTeamPassword.value = secret.password
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? '请使用管理员网页登录会话查看密码' : '请先为当前管理员启用 TOTP 二次验证')
      return
    }
    appStore.showError('无法查看保存的 Team 登录密码')
  } finally {
    teamPasswordLoading.value = false
  }
}

const handleGenerateUrl = async () => {
  if (!props.account) return

  if (isOpenAILike.value) {
    adsPowerSessionId.value = ''
    await openaiOAuth.generateAuthUrl(props.account.proxy_id)
  } else if (isGemini.value) {
    const creds = (props.account.credentials || {}) as Record<string, unknown>
    const tierId = typeof creds.tier_id === 'string' ? creds.tier_id : undefined
    const projectId = geminiOAuthType.value === 'code_assist' ? oauthFlowRef.value?.projectId : undefined
    await geminiOAuth.generateAuthUrl(props.account.proxy_id, projectId, geminiOAuthType.value, tierId)
  } else if (isAntigravity.value) {
    await antigravityOAuth.generateAuthUrl(props.account.proxy_id)
  } else if (isGrok.value) {
    await grokOAuth.generateAuthUrl(props.account.proxy_id)
  } else {
    await claudeOAuth.generateAuthUrl(addMethod.value, props.account.proxy_id)
  }
}

const handleLaunchAdsPower = async () => {
  if (!props.account || !adsPowerEligible.value || !currentAuthUrl.value || !currentSessionId.value || adsPowerLaunching.value) return
  const helperWindow = window.open('', '_blank')
  adsPowerLaunching.value = true
  try {
    await openaiOAuth.generateAuthUrl(null)
    const sessionId = currentSessionId.value
    const authUrl = currentAuthUrl.value
    if (!sessionId || !authUrl) throw new Error('无法创建固定环境授权会话')
    const result = await adminAPI.adsPower.launch({
      account_id: props.account.id,
      session_id: sessionId,
      auth_url: authUrl,
      profile_label: props.account.name
    })
    adsPowerSessionId.value = sessionId
    if (helperWindow) {
      helperWindow.opener = null
      helperWindow.location.replace(result.helper_url)
    } else {
      window.location.assign(result.helper_url)
    }
    appStore.showSuccess('已交给该账号绑定的 AdsPower 指纹环境')
  } catch (error: any) {
    helperWindow?.close()
    appStore.showError(error?.response?.data?.detail || error?.message || '无法启动 AdsPower 指纹环境')
  } finally {
    adsPowerLaunching.value = false
  }
}

const handleUnbindAdsPower = async () => {
  if (!props.account || adsPowerUnbinding.value) return
  adsPowerUnbinding.value = true
  showAdsPowerUnbindConfirm.value = false
  try {
    await adminAPI.adsPower.unbind(props.account.id)
    adsPowerBindingCleared.value = true
    appStore.showSuccess('固定指纹环境绑定已解除')
  } catch (error: any) {
    appStore.showError(error?.response?.data?.detail || error?.message || '无法解除固定指纹环境绑定')
  } finally {
    adsPowerUnbinding.value = false
  }
}

const handleExchangeCode = async () => {
  if (!props.account) return

  const authCode = oauthFlowRef.value?.authCode || ''
  if (!authCode.trim()) return

  if (isOpenAILike.value) {
    // OpenAI OAuth flow
    const oauthClient = openaiOAuth
    const sessionId = oauthClient.sessionId.value
    if (!sessionId) return
    const stateToUse = (oauthFlowRef.value?.oauthState || oauthClient.oauthState.value || '').trim()
    if (!stateToUse) {
      oauthClient.error.value = t('admin.accounts.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
      return
    }

    const tokenInfo = await oauthClient.exchangeAuthCode(
      authCode.trim(),
      sessionId,
      stateToUse,
      adsPowerSessionId.value === sessionId ? null : props.account.proxy_id
    )
    if (!tokenInfo) return

    // Build credentials and extra info
    const credentials = oauthClient.buildCredentials(tokenInfo)
    const extra = oauthClient.buildExtraInfo(tokenInfo)

    try {
      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: 'oauth',
        credentials,
        extra
      })

      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      oauthClient.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
    }
  } else if (isGemini.value) {
    const sessionId = geminiOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || geminiOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await geminiOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.account.proxy_id,
      oauthType: geminiOAuthType.value,
      tierId: typeof (props.account.credentials as any)?.tier_id === 'string' ? ((props.account.credentials as any).tier_id as string) : undefined
    })
    if (!tokenInfo) return

    const credentials = geminiOAuth.buildCredentials(tokenInfo)

    try {
      await adminAPI.accounts.update(props.account.id, {
        type: 'oauth',
        credentials
      })
      const updatedAccount = await adminAPI.accounts.clearError(props.account.id)
      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      geminiOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(geminiOAuth.error.value)
    }
  } else if (isAntigravity.value) {
    // Antigravity OAuth flow
    const sessionId = antigravityOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || antigravityOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await antigravityOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.account.proxy_id
    })
    if (!tokenInfo) return

    const credentials = antigravityOAuth.buildCredentials(tokenInfo)
    const extra = antigravityOAuth.buildExtraInfo(tokenInfo)

    try {
      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: 'oauth',
        credentials,
        extra
      })
      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      antigravityOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(antigravityOAuth.error.value)
    }
  } else if (isGrok.value) {
    const sessionId = grokOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || grokOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await grokOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.account.proxy_id
    })
    if (!tokenInfo) return

    const credentials = grokOAuth.buildCredentials(tokenInfo)
    const extra = grokOAuth.buildExtraInfo(tokenInfo)

    try {
      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: 'oauth',
        credentials,
        extra
      })

      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      grokOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(grokOAuth.error.value)
    }
  } else {
    // Claude OAuth flow
    const sessionId = claudeOAuth.sessionId.value
    if (!sessionId) return

    claudeOAuth.loading.value = true
    claudeOAuth.error.value = ''

    try {
      const proxyConfig = props.account.proxy_id ? { proxy_id: props.account.proxy_id } : {}
      const endpoint =
        addMethod.value === 'oauth'
          ? '/admin/accounts/exchange-code'
          : '/admin/accounts/exchange-setup-token-code'

      const tokenInfo = await adminAPI.accounts.exchangeCode(endpoint, {
        session_id: sessionId,
        code: authCode.trim(),
        ...proxyConfig
      })

      const extra = claudeOAuth.buildExtraInfo(tokenInfo)

      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: addMethod.value as 'oauth' | 'setup-token',
        credentials: tokenInfo as unknown as Record<string, unknown>,
        extra
      })

      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      claudeOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(claudeOAuth.error.value)
    } finally {
      claudeOAuth.loading.value = false
    }
  }
}

const handleCookieAuth = async (sessionKey: string) => {
  if (!props.account || isOpenAILike.value) return

  claudeOAuth.loading.value = true
  claudeOAuth.error.value = ''

  try {
    const proxyConfig = props.account.proxy_id ? { proxy_id: props.account.proxy_id } : {}
    const endpoint =
      addMethod.value === 'oauth'
        ? '/admin/accounts/cookie-auth'
        : '/admin/accounts/setup-token-cookie-auth'

    const tokenInfo = await adminAPI.accounts.exchangeCode(endpoint, {
      session_id: '',
      code: sessionKey.trim(),
      ...proxyConfig
    })

    const extra = claudeOAuth.buildExtraInfo(tokenInfo)

    const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
      type: addMethod.value as 'oauth' | 'setup-token',
      credentials: tokenInfo as unknown as Record<string, unknown>,
      extra
    })

    appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
    emit('reauthorized', updatedAccount)
    handleClose()
  } catch (error: any) {
    claudeOAuth.error.value =
      error.response?.data?.detail || t('admin.accounts.oauth.cookieAuthFailed')
  } finally {
    claudeOAuth.loading.value = false
  }
}

// Re-authorize the selected account with a single refresh token. This must use
// the credential-specific endpoint so account configuration and token caches
// remain consistent with the interactive OAuth path.
const handleValidateRefreshToken = async (refreshTokenInput: string) => {
  if (!props.account) return

  const refreshToken = refreshTokenInput
    .split('\n')
    .map((line) => line.trim())
    .find(Boolean)
  if (!refreshToken) return

  if (isGrok.value) {
    const tokenInfo = await grokOAuth.validateRefreshToken(refreshToken, props.account.proxy_id)
    if (!tokenInfo) return

    grokOAuth.loading.value = true
    try {
      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: 'oauth',
        credentials: grokOAuth.buildCredentials(tokenInfo),
        extra: grokOAuth.buildExtraInfo(tokenInfo)
      })
      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      grokOAuth.error.value =
        error.response?.data?.detail ||
        error.response?.data?.message ||
        error.message ||
        t('admin.accounts.oauth.authFailed')
      appStore.showError(grokOAuth.error.value)
    } finally {
      grokOAuth.loading.value = false
    }
    return
  }

  if (isOpenAILike.value) {
    const tokenInfo = await openaiOAuth.validateRefreshToken(refreshToken, props.account.proxy_id)
    if (!tokenInfo) return

    openaiOAuth.loading.value = true
    try {
      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: 'oauth',
        credentials: openaiOAuth.buildCredentials(tokenInfo),
        extra: openaiOAuth.buildExtraInfo(tokenInfo)
      })
      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      openaiOAuth.error.value =
        error.response?.data?.detail ||
        error.response?.data?.message ||
        error.message ||
        t('admin.accounts.oauth.authFailed')
      appStore.showError(openaiOAuth.error.value)
    } finally {
      openaiOAuth.loading.value = false
    }
    return
  }

  if (!isAntigravity.value) return

  const tokenInfo = await antigravityOAuth.validateRefreshToken(refreshToken, props.account.proxy_id)
  if (!tokenInfo) return

  antigravityOAuth.loading.value = true
  try {
    const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
      type: 'oauth',
      credentials: antigravityOAuth.buildCredentials(tokenInfo, refreshToken),
      extra: antigravityOAuth.buildExtraInfo(tokenInfo)
    })
    appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
    emit('reauthorized', updatedAccount)
    handleClose()
  } catch (error: any) {
    antigravityOAuth.error.value =
      error.response?.data?.detail ||
      error.response?.data?.message ||
      error.message ||
      t('admin.accounts.oauth.authFailed')
    appStore.showError(antigravityOAuth.error.value)
  } finally {
    antigravityOAuth.loading.value = false
  }
}
</script>
