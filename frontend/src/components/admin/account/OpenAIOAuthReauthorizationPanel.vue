<template>
  <section data-testid="openai-oauth-reauthorization-panel" class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
    <header class="flex min-w-0 items-start justify-between gap-3 border-b border-gray-200 px-4 py-3.5 dark:border-dark-700">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-primary-50 text-primary-700 dark:bg-primary-950/50 dark:text-primary-300">
            <Icon name="key" size="sm" :stroke-width="2" />
          </span>
          <div class="min-w-0">
            <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">OpenAI OAuth 401 重新授权管理</h2>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">成功批量授权的账号会自动保留登录信息；既有账号仍可由管理员手动配置</p>
          </div>
        </div>
      </div>
      <button type="button" class="btn btn-secondary flex h-8 w-8 shrink-0 items-center justify-center p-0" title="刷新 OpenAI OAuth 账号" aria-label="刷新 OpenAI OAuth 账号" :disabled="loading" @click="emit('refresh')">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
      </button>
    </header>

    <div class="grid min-w-0 gap-4 p-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div class="min-w-0">
        <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400" for="openai-reauth-account">OpenAI OAuth 账号</label>
        <Select
          id="openai-reauth-account"
          v-model="selectedAccountID"
          :options="accountOptions"
          placeholder="请选择已有 OAuth 账号"
          class="w-full"
          aria-label="选择 OpenAI OAuth 账号"
          :disabled="loading || accounts.length === 0"
        />
        <p v-if="accounts.length === 0" class="mt-2 text-xs text-gray-500 dark:text-gray-400">暂无可管理的普通 OpenAI OAuth 账号。</p>
        <p v-else-if="selectedAccount" class="mt-2 text-xs" :class="credentialsConfigured ? 'text-green-600 dark:text-green-400' : 'text-gray-500 dark:text-gray-400'">
          {{ credentialsConfigured ? '已保存目标邮箱，可直接重新授权。' : '尚未保存目标邮箱，请先填写邮箱。' }}
        </p>
      </div>

      <div class="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2">
        <div class="min-w-0">
          <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400" for="openai-reauth-email">登录邮箱</label>
          <input id="openai-reauth-email" v-model.trim="loginEmail" type="email" autocomplete="username" class="input w-full" placeholder="name@example.com" :disabled="!selectedAccount || saving" />
        </div>
        <div class="min-w-0">
          <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400" for="openai-reauth-password">登录密码（可选）</label>
          <input id="openai-reauth-password" v-model="loginPassword" type="password" autocomplete="current-password" class="input w-full" placeholder="仅在官方页面要求时使用" :disabled="!selectedAccount || saving" @input="passwordDirty = true" @keydown.enter.prevent="saveCredentials" />
          <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ passwordConfigured ? '密码已加密保存；留空且未编辑时保留原密码，清空后保存会删除。' : '尚未保存密码；填写后会加密保存。' }}</p>
        </div>
        <div class="min-w-0">
          <label class="mb-2 flex items-center gap-2 text-sm">
            <input v-model="updateTOTP" type="checkbox" :disabled="!selectedAccount || saving" />修改 2FA 密钥
          </label>
          <input v-if="updateTOTP" v-model="loginTOTP" type="password" autocomplete="off" aria-label="2FA 密钥" class="input w-full" placeholder="留空保存将清除 2FA 密钥" :disabled="saving" />
          <span v-else class="text-xs text-gray-500 dark:text-gray-400">{{ selectedAccount?.credentials_status?.has_xiass_openai_oauth_reauth_totp_secret_encrypted ? '2FA 已保存' : '未保存 2FA' }}</span>
          <p v-if="totpInvalid" role="alert" class="mt-1 text-xs text-red-600">2FA 密钥格式无效</p>
        </div>
      </div>
    </div>

    <footer class="flex min-w-0 flex-wrap items-center justify-end gap-2 border-t border-gray-200 px-4 py-3 dark:border-dark-700">
      <button type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" :disabled="!canSaveCredentials" @click="saveCredentials">
        <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" :stroke-width="2" />
        <span>{{ saving ? '正在保存' : '保存登录信息' }}</span>
      </button>
      <button type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="!canReauthorize" @click="selectedAccount && emit('reauthorize', selectedAccount)">
        <Icon :name="reauthorizing ? 'refresh' : 'key'" size="sm" :class="reauthorizing ? 'animate-spin' : ''" :stroke-width="2" />
        <span>{{ reauthorizing ? '正在重新授权' : '一键重新授权' }}</span>
      </button>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Icon } from '@/components/icons'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import type { Account } from '@/types'
import { normalizeBase32Secret } from '@/features/token-converter/totp'

const props = withDefaults(defineProps<{
  accounts: Account[]
  loading?: boolean
  saving?: boolean
  reauthorizingAccountID?: number | null
}>(), {
  loading: false,
  saving: false,
  reauthorizingAccountID: null
})

const emit = defineEmits<{
  refresh: []
  'save-credentials': [payload: { account: Account; email: string; password?: string; totp_secret?: string }]
  reauthorize: [account: Account]
}>()

const selectedAccountID = ref(0)
const loginEmail = ref('')
const loginPassword = ref('')
const passwordDirty = ref(false)
const loginTOTP = ref('')
const updateTOTP = ref(false)
const totpInvalid = computed(() => {
  if (!updateTOTP.value || !loginTOTP.value) return false
  try { return !/^[A-Z2-7]{16,256}$/.test(normalizeBase32Secret(loginTOTP.value)) } catch { return true }
})

const selectedAccount = computed(() => props.accounts.find((account) => account.id === selectedAccountID.value) || null)
const accountOptions = computed<SelectOption[]>(() => props.accounts.map((account) => ({
  value: account.id,
  label: accountOptionLabel(account)
})))
const credentialsConfigured = computed(() => Boolean(selectedAccount.value?.credentials_status?.has_xiass_openai_oauth_reauth_email))
const passwordConfigured = computed(() => Boolean(selectedAccount.value?.credentials_status?.has_xiass_openai_oauth_reauth_password_encrypted))
const reauthorizing = computed(() => selectedAccount.value?.id === props.reauthorizingAccountID)
const canSaveCredentials = computed(() => Boolean(selectedAccount.value)
  && !props.saving
  && !totpInvalid.value
  && /^\S+@\S+\.\S+$/.test(loginEmail.value))
const canReauthorize = computed(() => Boolean(selectedAccount.value)
  && credentialsConfigured.value
  && !props.saving
  && !reauthorizing.value)

function accountEmail(account: Account | null): string {
  if (!account) return ''
  const stored = typeof account.credentials?.email === 'string' ? account.credentials.email.trim() : ''
  return stored || account.name.trim()
}

function accountOptionLabel(account: Account): string {
  const email = accountEmail(account)
  return `#${account.id} ${account.name}${email && email !== account.name ? ` (${email})` : ''}`
}

function saveCredentials() {
  if (!selectedAccount.value || !canSaveCredentials.value) return
  emit('save-credentials', {
    account: selectedAccount.value,
    email: loginEmail.value,
    ...(passwordDirty.value ? { password: loginPassword.value } : {}),
    ...(updateTOTP.value ? { totp_secret: loginTOTP.value ? normalizeBase32Secret(loginTOTP.value) : '' } : {})
  })
  // The parent sends the value directly to the dedicated encrypted endpoint.
  // Keep no login password in component state after the explicit action.
  loginPassword.value = ''
  passwordDirty.value = false
  loginTOTP.value = ''
  updateTOTP.value = false
}

watch(selectedAccount, (account) => {
  loginEmail.value = accountEmail(account)
  loginPassword.value = ''
  passwordDirty.value = false
  loginTOTP.value = ''
  updateTOTP.value = false
}, { immediate: true })

watch(() => props.accounts, (accounts) => {
  if (selectedAccountID.value && accounts.some((account) => account.id === selectedAccountID.value)) return
  selectedAccountID.value = accounts[0]?.id || 0
}, { immediate: true })
</script>
