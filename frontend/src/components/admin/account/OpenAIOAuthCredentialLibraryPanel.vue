<template>
  <div class="min-w-0" data-testid="openai-credential-library">
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700">
      <div class="min-w-0">
        <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">OpenAI 账号登录资料</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">密码 + 2FA 与邮箱验证码 Token 分开保存；同一账号只使用当前选择的登录方式。</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm flex items-center gap-1.5" :disabled="loading || saving" data-testid="refresh-credential-library" @click="loadAccounts">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
        <span>重新读取</span>
      </button>
    </div>

    <div class="grid border-b border-gray-200 dark:border-dark-700 sm:grid-cols-3">
      <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700 sm:border-b-0 sm:border-r">
        <p class="text-xs text-gray-500 dark:text-gray-400">当前服务器可管理</p>
        <p class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-gray-100">{{ manageableAccounts.length }}</p>
      </div>
      <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700 sm:border-b-0 sm:border-r">
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ currentModeLabel }}已保存</p>
        <p class="mt-0.5 text-lg font-semibold text-emerald-700 dark:text-emerald-300">{{ currentModeCompleteCount }}</p>
      </div>
      <div class="px-4 py-3">
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ currentModeLabel }}待补充</p>
        <p class="mt-0.5 text-lg font-semibold text-amber-700 dark:text-amber-300">{{ Math.max(0, manageableAccounts.length - currentModeCompleteCount) }}</p>
      </div>
    </div>

    <div v-if="loading" class="flex min-h-56 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
      <Icon name="refresh" size="sm" class="animate-spin" />
      正在读取 OpenAI OAuth 账号
    </div>

    <template v-else>
      <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
        <div class="inline-flex max-w-full overflow-x-auto rounded-md border border-gray-200 p-1 dark:border-dark-600" role="tablist" aria-label="账号登录资料类型">
          <button type="button" role="tab" class="credential-mode-tab" :class="loginMode === 'password' ? 'credential-mode-tab-active' : 'credential-mode-tab-idle'" :aria-selected="loginMode === 'password'" data-testid="credential-mode-password" @click="selectMode('password')">
            <Icon name="key" size="sm" />密码 + 2FA
          </button>
          <button type="button" role="tab" class="credential-mode-tab" :class="loginMode === 'email_code' ? 'credential-mode-tab-active' : 'credential-mode-tab-idle'" :aria-selected="loginMode === 'email_code'" data-testid="credential-mode-email-code" @click="selectMode('email_code')">
            <Icon name="mail" size="sm" />邮箱验证码
          </button>
        </div>
      </div>

      <div class="grid min-w-0 xl:grid-cols-[minmax(0,1.05fr)_minmax(380px,0.95fr)]">
        <div class="min-w-0 px-4 py-5 xl:border-r xl:border-gray-200 xl:dark:border-dark-700">
          <label v-if="loginMode === 'password'" class="block text-sm font-medium text-gray-800 dark:text-gray-200" for="credential-library-password-input">
            邮箱或账号名称、密码、2FA
          </label>
          <label v-else class="block text-sm font-medium text-gray-800 dark:text-gray-200" for="credential-library-email-code-input">
            邮箱、邮箱验证码 Token
          </label>
          <textarea
            v-if="loginMode === 'password'"
            id="credential-library-password-input"
            v-model="passwordInput"
            class="input mt-2 min-h-48 resize-y font-mono text-sm leading-6"
            rows="8"
            autocomplete="off"
            autocapitalize="off"
            :spellcheck="false"
            placeholder="邮箱----密码----2FA，每行一个账号"
            data-testid="credential-library-password-input"
          />
          <textarea
            v-else
            id="credential-library-email-code-input"
            v-model="emailCodeInput"
            class="input mt-2 min-h-48 resize-y font-mono text-sm leading-6"
            rows="8"
            autocomplete="off"
            autocapitalize="off"
            :spellcheck="false"
            placeholder="粘贴包含邮箱和 64 位 Token 的账号资料，每行一个账号"
            data-testid="credential-library-email-code-input"
          />
          <p v-if="loginMode === 'email_code'" class="mt-2 text-xs text-gray-500 dark:text-gray-400">验证码地址固定为 {{ emailCodeProvider }}，套餐、地区、日期和浏览器等字段不会保存。</p>

          <div class="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-sm text-gray-600 dark:text-gray-300">
            <span>已识别 {{ uniqueRows.length }} 条</span>
            <span v-if="totalDuplicateCount">已忽略 {{ totalDuplicateCount }} 条重复内容</span>
            <span v-if="targetCount" class="text-primary-700 dark:text-primary-300">可补充或切换 {{ targetCount }} 个账号</span>
            <span v-if="invalidLines.length" class="text-red-600 dark:text-red-300" role="alert">格式错误：第 {{ invalidLines.join('、') }} 行</span>
          </div>

          <p v-if="loadError" class="mt-3 text-sm text-red-600 dark:text-red-300" role="alert">{{ loadError }}</p>
          <div v-if="lastResult" class="mt-4 border-y border-gray-200 py-3 text-sm dark:border-dark-700" role="status">
            <p :class="lastResult.failed ? 'text-amber-700 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">
              {{ lastResult.modeLabel }}：已保存 {{ lastResult.saved }} 个，失败 {{ lastResult.failed }} 个，未匹配 {{ lastResult.unmatched }} 条，当前方式已保存跳过 {{ lastResult.alreadySaved }} 个。
            </p>
            <p v-if="lastResult.errors.length" class="mt-1 break-words text-xs text-red-600 dark:text-red-300">{{ lastResult.errors.join('；') }}</p>
          </div>
        </div>

        <div class="min-w-0 border-t border-gray-200 px-4 py-5 dark:border-dark-700 xl:border-t-0">
          <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-gray-100">匹配结果</h3>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ previewEntries.length }} 条</span>
          </div>
          <div v-if="previewEntries.length" class="mt-3 max-h-[26rem] overflow-y-auto border-y border-gray-200 dark:border-dark-700">
            <div v-for="entry in previewEntries" :key="entry.key" class="border-b border-gray-100 py-3 last:border-b-0 dark:border-dark-700">
              <div class="flex min-w-0 items-start justify-between gap-3">
                <span class="min-w-0 break-all text-sm font-medium text-gray-900 dark:text-gray-100">{{ entry.label }}</span>
                <span class="shrink-0 text-xs font-medium" :class="previewStatusClass(entry)">{{ previewStatusText(entry) }}</span>
              </div>
              <p class="mt-1 min-w-0 truncate text-xs text-gray-500 dark:text-gray-400">{{ matchedAccountText(entry) }}</p>
              <p v-if="entry.targets.length && entry.savedMethods.length" class="mt-1 text-xs text-amber-700 dark:text-amber-300">当前保存：{{ entry.savedMethods.join('、') }}；保存后切换为 {{ currentModeLabel }}。</p>
            </div>
          </div>
          <div v-else class="mt-3 flex min-h-48 items-center justify-center border-y border-gray-200 px-4 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
            在左侧粘贴 {{ currentModeLabel }}资料后，这里会显示账号匹配与保存状态。
          </div>
        </div>
      </div>

      <div class="flex justify-end border-t border-gray-200 px-4 py-3 dark:border-dark-700">
        <button type="button" class="btn btn-primary flex items-center gap-2" data-testid="save-credential-library" :disabled="!canSave" @click="saveAll">
          <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" />
          <span>{{ saving ? '正在保存' : `保存${currentModeLabel}${targetCount ? ` (${targetCount})` : ''}` }}</span>
        </button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { accountsAPI, executionNodesAPI } from '@/api/admin'
import { saveOpenAIAccountReauthorizationCredentials } from '@/api/admin/teamChild'
import type { OpenAIAccountReauthorizationCredentialsRequest } from '@/api/admin/teamChild'
import type { ExecutionNodeAdminStatus } from '@/api/admin/executionNodes'
import { parseAccountCredentials } from '@/features/token-converter/accountCredentials'
import { OPENAI_EMAIL_CODE_PROVIDER, parseOpenAIEmailCodeCredentials } from '@/features/token-converter/openAIEmailCodeCredentials'
import { normalizeBase32Secret } from '@/features/token-converter/totp'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const emit = defineEmits<{ updated: [] }>()

type LoginMode = 'password' | 'email_code'
type LibraryCredential =
  | { method: 'password'; account: string; lineNumber: number; password: string; totpSecret: string }
  | { method: 'email_code'; account: string; lineNumber: number; emailCodeToken: string }
type PreviewStatus = 'ready' | 'complete' | 'duplicate' | 'unmatched' | 'unsupported'

interface CredentialTarget {
  account: Account
  email: string
  credential: LibraryCredential
}

interface PreviewEntry {
  key: string
  label: string
  matched: Account[]
  targets: CredentialTarget[]
  completeCount: number
  duplicateCount: number
  savedMethods: string[]
  status: PreviewStatus
}

interface ImportResult {
  modeLabel: string
  saved: number
  failed: number
  unmatched: number
  alreadySaved: number
  errors: string[]
}

const accounts = ref<Account[]>([])
const loginMode = ref<LoginMode>('password')
const passwordInput = ref('')
const emailCodeInput = ref('')
const emailCodeProvider = OPENAI_EMAIL_CODE_PROVIDER
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const lastResult = ref<ImportResult | null>(null)
const executionNodeStatus = ref<ExecutionNodeAdminStatus | null>(null)
const loaded = ref(false)

const currentModeLabel = computed(() => loginMode.value === 'email_code' ? '邮箱验证码' : '密码 + 2FA')
const passwordParsed = computed(() => parseAccountCredentials(passwordInput.value))
const emailCodeParsed = computed(() => parseOpenAIEmailCodeCredentials(emailCodeInput.value))

const parsedRows = computed<LibraryCredential[]>(() => {
  if (loginMode.value === 'email_code') {
    return emailCodeParsed.value.rows.map(row => ({
      method: 'email_code',
      account: row.account,
      lineNumber: row.lineNumber,
      emailCodeToken: row.emailCodeToken,
    }))
  }
  return passwordParsed.value.rows.map(row => {
    let totpSecret = ''
    try { totpSecret = normalizeBase32Secret(row.twoFactor) } catch { /* Reported through invalidLines. */ }
    return {
      method: 'password',
      account: row.account,
      lineNumber: row.lineNumber,
      password: row.password,
      totpSecret,
    }
  })
})

const uniqueRows = computed(() => {
  const seen = new Set<string>()
  return parsedRows.value.filter(row => {
    const key = normalizeIdentity(row.account)
    if (!key || seen.has(key)) return false
    seen.add(key)
    return true
  })
})

const duplicateCount = computed(() => parsedRows.value.length - uniqueRows.value.length)
const invalidLines = computed(() => {
  if (loginMode.value === 'email_code') {
    return [...new Set(emailCodeParsed.value.invalidRows.map(row => row.lineNumber))].sort((left, right) => left - right)
  }
  const invalid = passwordParsed.value.invalidRows.map(row => row.lineNumber)
  for (const row of uniqueRows.value) {
    if (row.method !== 'password') continue
    if (!normalizeIdentity(row.account) || !/^[A-Z2-7]{16,256}$/.test(row.totpSecret)) invalid.push(row.lineNumber)
  }
  return [...new Set(invalid)].sort((left, right) => left - right)
})

const accountsByIdentity = computed(() => {
  const result = new Map<string, Account[]>()
  for (const account of accounts.value) {
    for (const identity of accountIdentities(account)) {
      const current = result.get(identity) || []
      if (!current.some(candidate => candidate.id === account.id)) current.push(account)
      result.set(identity, current)
    }
  }
  return result
})

const previewEntries = computed<PreviewEntry[]>(() => {
  const claimedAccountIDs = new Set<number>()
  const countedCompleteAccountIDs = new Set<number>()
  return uniqueRows.value.map(credential => {
    const key = normalizeIdentity(credential.account)
    const matched = accountsByIdentity.value.get(key) || []
    const supported = matched.filter(canStoreCredentials)
    const targets: CredentialTarget[] = []
    const savedMethods = new Set<string>()
    let completeCount = 0
    let matchedDuplicateCount = 0
    for (const account of supported) {
      const savedMethod = savedLoginMethod(account)
      if (savedMethod) savedMethods.add(savedMethod)
      if (hasCompleteCredentials(account, credential.method)) {
        if (!countedCompleteAccountIDs.has(account.id)) {
          countedCompleteAccountIDs.add(account.id)
          completeCount++
        }
        continue
      }
      if (claimedAccountIDs.has(account.id)) {
        matchedDuplicateCount++
        continue
      }
      const email = loginEmail(account, credential.account)
      if (!email) continue
      claimedAccountIDs.add(account.id)
      targets.push({ account, email, credential })
    }
    const status: PreviewStatus = matched.length === 0
      ? 'unmatched'
      : supported.length === 0
        ? 'unsupported'
        : targets.length > 0
          ? 'ready'
          : matchedDuplicateCount > 0
            ? 'duplicate'
            : 'complete'
    return {
      key,
      label: credential.account,
      matched,
      targets,
      completeCount,
      duplicateCount: matchedDuplicateCount,
      savedMethods: [...savedMethods].filter(method => method !== currentModeLabel.value),
      status,
    }
  })
})

const targetCount = computed(() => previewEntries.value.reduce((sum, entry) => sum + entry.targets.length, 0))
const totalDuplicateCount = computed(() => duplicateCount.value + previewEntries.value.reduce((sum, entry) => sum + entry.duplicateCount, 0))
const manageableAccounts = computed(() => accounts.value.filter(canStoreCredentials))
const currentModeCompleteCount = computed(() => manageableAccounts.value.filter(account => hasCompleteCredentials(account, loginMode.value)).length)
const canSave = computed(() => !loading.value && !saving.value && !loadError.value && invalidLines.value.length === 0 && targetCount.value > 0)

function selectMode(mode: LoginMode) {
  if (saving.value || loginMode.value === mode) return
  loginMode.value = mode
  lastResult.value = null
}

function normalizeIdentity(value: unknown): string {
  return typeof value === 'string' ? value.trim().toLowerCase() : ''
}

function validEmail(value: string): boolean {
  return /^\S+@\S+\.\S+$/.test(value)
}

function accountIdentities(account: Account): string[] {
  const email = normalizeIdentity(account.credentials?.email)
  const name = normalizeIdentity(account.name)
  return [...new Set([email, name].filter(Boolean))]
}

function loginEmail(account: Account, importedIdentity: string): string {
  const stored = typeof account.credentials?.email === 'string' ? account.credentials.email.trim() : ''
  if (validEmail(stored)) return stored.toLowerCase()
  const imported = importedIdentity.trim()
  if (validEmail(imported)) return imported.toLowerCase()
  const name = account.name.trim()
  return validEmail(name) ? name.toLowerCase() : ''
}

function canStoreCredentials(account: Account): boolean {
  const extra = account.extra as Record<string, unknown> | undefined
  const status = executionNodeStatus.value
  const localNodeID = status?.runtime.node_id || 'api'
  const legacyNodeID = status?.runtime.legacy_unassigned_node_id || localNodeID
  const owner = account.execution_node_id?.trim() || legacyNodeID
  const pairedFullAccess = status?.admin_write_mode === 'paired_full_access' && status.admin_write_allowed === true
  const nodeWritable = status?.runtime.enabled !== true || pairedFullAccess || owner === localNodeID
  return account.platform === 'openai'
    && account.type === 'oauth'
    && account.parent_account_id == null
    && extra?.xiass_team_child !== true
    && nodeWritable
}

function hasCompleteCredentials(account: Account, mode: LoginMode): boolean {
  const status = account.credentials_status
  if (status?.has_xiass_openai_oauth_reauth_email !== true) return false
  return mode === 'email_code'
    ? status?.has_xiass_openai_oauth_reauth_email_code_token_encrypted === true
    : status?.has_xiass_openai_oauth_reauth_password_encrypted === true
      && status?.has_xiass_openai_oauth_reauth_totp_secret_encrypted === true
}

function savedLoginMethod(account: Account): string {
  if (hasCompleteCredentials(account, 'email_code')) return '邮箱验证码'
  if (hasCompleteCredentials(account, 'password')) return '密码 + 2FA'
  return ''
}

function matchedAccountText(entry: PreviewEntry): string {
  if (!entry.matched.length) return '未找到现有 OpenAI OAuth 账号'
  return entry.matched.map(account => `#${account.id} ${account.name}`).join('、')
}

function previewStatusText(entry: PreviewEntry): string {
  if (entry.status === 'unmatched') return '不保存'
  if (entry.status === 'unsupported') return '账号类型不支持'
  if (entry.status === 'duplicate') return '同一账号已在上方，跳过'
  if (entry.status === 'complete') return `${currentModeLabel.value}已保存，跳过`
  return `待保存 ${entry.targets.length} 个${entry.completeCount ? `，跳过 ${entry.completeCount} 个` : ''}`
}

function previewStatusClass(entry: PreviewEntry): string {
  if (entry.status === 'ready') return 'text-primary-700 dark:text-primary-300'
  if (entry.status === 'complete') return 'text-emerald-700 dark:text-emerald-300'
  return 'text-amber-700 dark:text-amber-300'
}

async function loadAccounts() {
  if (loading.value || saving.value) return
  loading.value = true
  loadError.value = ''
  try {
    const [status, found] = await Promise.all([
      executionNodesAPI.getStatus(),
      (async () => {
        const resultAccounts: Account[] = []
        let page = 1
        while (true) {
          const result = await accountsAPI.list(page, 200, {
            platform: 'openai',
            type: 'oauth',
            sort_by: 'id',
            sort_order: 'asc',
          })
          resultAccounts.push(...result.items)
          if (page >= result.pages || result.items.length === 0) break
          page++
        }
        return resultAccounts
      })(),
    ])
    executionNodeStatus.value = status
    accounts.value = found
    loaded.value = true
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, '读取 OpenAI OAuth 账号失败。')
  } finally {
    loading.value = false
  }
}

async function saveTarget(target: CredentialTarget): Promise<Account> {
  const status = target.account.credentials_status
  let payload: OpenAIAccountReauthorizationCredentialsRequest
  if (target.credential.method === 'email_code') {
    payload = {
      email: target.email,
      email_code_token: target.credential.emailCodeToken,
    }
  } else {
    payload = {
      email: target.email,
      ...(status?.has_xiass_openai_oauth_reauth_password_encrypted === true ? {} : { password: target.credential.password }),
      ...(status?.has_xiass_openai_oauth_reauth_totp_secret_encrypted === true ? {} : { totp_secret: target.credential.totpSecret }),
    }
  }
  return saveOpenAIAccountReauthorizationCredentials(target.account.id, payload)
}

async function saveAll() {
  if (!canSave.value) return
  saving.value = true
  lastResult.value = null
  const mode = loginMode.value
  const modeLabel = currentModeLabel.value
  const entries = previewEntries.value
  const targets = entries.flatMap(entry => entry.targets)
  const result: ImportResult = {
    modeLabel,
    saved: 0,
    failed: 0,
    unmatched: entries.filter(entry => entry.status === 'unmatched').length,
    alreadySaved: entries.reduce((sum, entry) => sum + entry.completeCount, 0),
    errors: [],
  }
  let cursor = 0
  const workers = Array.from({ length: Math.min(3, targets.length) }, async () => {
    while (cursor < targets.length) {
      const target = targets[cursor++]
      try {
        const updated = await saveTarget(target)
        accounts.value = accounts.value.map(account => account.id === updated.id ? updated : account)
        result.saved++
      } catch (error) {
        result.failed++
        result.errors.push(`#${target.account.id} ${target.account.name}：${extractApiErrorMessage(error, '保存失败')}`)
      }
    }
  })
  await Promise.all(workers)
  if (mode === 'email_code') emailCodeInput.value = ''
  else passwordInput.value = ''
  lastResult.value = result
  saving.value = false
  if (result.saved > 0) emit('updated')
}

watch(() => props.active, active => {
  if (active && !loaded.value) void loadAccounts()
}, { immediate: true })
</script>

<style scoped>
.credential-mode-tab {
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

.credential-mode-tab-active {
  @apply bg-primary-50 text-primary-700 dark:bg-primary-950/50 dark:text-primary-300;
}

.credential-mode-tab-idle {
  @apply text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200;
}
</style>
