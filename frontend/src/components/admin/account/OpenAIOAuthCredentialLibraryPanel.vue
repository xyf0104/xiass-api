<template>
  <div class="min-w-0" data-testid="openai-credential-library">
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700">
      <div class="min-w-0">
        <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">保存账号登录信息</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">批量补全邮箱、密码和 2FA；重复账号与已完整保存的账号会自动跳过。</p>
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
        <p class="text-xs text-gray-500 dark:text-gray-400">已完整保存</p>
        <p class="mt-0.5 text-lg font-semibold text-emerald-700 dark:text-emerald-300">{{ completeAccountCount }}</p>
      </div>
      <div class="px-4 py-3">
        <p class="text-xs text-gray-500 dark:text-gray-400">待补充</p>
        <p class="mt-0.5 text-lg font-semibold text-amber-700 dark:text-amber-300">{{ Math.max(0, manageableAccounts.length - completeAccountCount) }}</p>
      </div>
    </div>

    <div v-if="loading" class="flex min-h-56 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
      <Icon name="refresh" size="sm" class="animate-spin" />
      正在读取 OpenAI OAuth 账号
    </div>

    <template v-else>
      <div class="grid min-w-0 xl:grid-cols-[minmax(0,1.05fr)_minmax(380px,0.95fr)]">
        <div class="min-w-0 px-4 py-5 xl:border-r xl:border-gray-200 xl:dark:border-dark-700">
          <label class="block text-sm font-medium text-gray-800 dark:text-gray-200" for="credential-library-input">
            邮箱或账号名称、密码、2FA
          </label>
          <textarea
            id="credential-library-input"
            v-model="input"
            class="input mt-2 min-h-48 resize-y font-mono text-sm leading-6"
            rows="8"
            autocomplete="off"
            autocapitalize="off"
            :spellcheck="false"
            placeholder="邮箱----密码----2FA，每行一个账号"
            data-testid="credential-library-input"
          />

          <div class="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-sm text-gray-600 dark:text-gray-300">
            <span>已识别 {{ uniqueRows.length }} 条</span>
            <span v-if="totalDuplicateCount">已忽略 {{ totalDuplicateCount }} 条重复内容</span>
            <span v-if="targetCount" class="text-primary-700 dark:text-primary-300">可补充 {{ targetCount }} 个账号</span>
            <span v-if="invalidLines.length" class="text-red-600 dark:text-red-300" role="alert">格式错误：第 {{ invalidLines.join('、') }} 行</span>
          </div>

          <p v-if="loadError" class="mt-3 text-sm text-red-600 dark:text-red-300" role="alert">{{ loadError }}</p>
          <div v-if="lastResult" class="mt-4 border-y border-gray-200 py-3 text-sm dark:border-dark-700" role="status">
            <p :class="lastResult.failed ? 'text-amber-700 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">
              已保存 {{ lastResult.saved }} 个，失败 {{ lastResult.failed }} 个，未匹配 {{ lastResult.unmatched }} 条，已完整保存跳过 {{ lastResult.alreadySaved }} 个。
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
            <div
              v-for="entry in previewEntries"
              :key="entry.key"
              class="border-b border-gray-100 py-3 last:border-b-0 dark:border-dark-700"
            >
              <div class="flex min-w-0 items-start justify-between gap-3">
                <span class="min-w-0 break-all text-sm font-medium text-gray-900 dark:text-gray-100">{{ entry.label }}</span>
                <span class="shrink-0 text-xs font-medium" :class="previewStatusClass(entry)">{{ previewStatusText(entry) }}</span>
              </div>
              <p class="mt-1 min-w-0 truncate text-xs text-gray-500 dark:text-gray-400">{{ matchedAccountText(entry) }}</p>
            </div>
          </div>
          <div v-else class="mt-3 flex min-h-48 items-center justify-center border-y border-gray-200 px-4 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
            粘贴登录信息后，这里会显示账号匹配与保存状态。
          </div>
        </div>
      </div>

      <div class="flex justify-end border-t border-gray-200 px-4 py-3 dark:border-dark-700">
        <button type="button" class="btn btn-primary flex items-center gap-2" data-testid="save-credential-library" :disabled="!canSave" @click="saveAll">
          <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" />
          <span>{{ saving ? '正在保存' : `保存登录信息${targetCount ? ` (${targetCount})` : ''}` }}</span>
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
import type { ExecutionNodeAdminStatus } from '@/api/admin/executionNodes'
import { parseAccountCredentials, type AccountCredentialRow } from '@/features/token-converter/accountCredentials'
import { normalizeBase32Secret } from '@/features/token-converter/totp'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const emit = defineEmits<{ updated: [] }>()

type PreviewStatus = 'ready' | 'complete' | 'duplicate' | 'unmatched' | 'unsupported'

interface CredentialTarget {
  account: Account
  email: string
  row: AccountCredentialRow
  totpSecret: string
}

interface PreviewEntry {
  key: string
  label: string
  matched: Account[]
  targets: CredentialTarget[]
  completeCount: number
  duplicateCount: number
  status: PreviewStatus
}

interface ImportResult {
  saved: number
  failed: number
  unmatched: number
  alreadySaved: number
  errors: string[]
}

const accounts = ref<Account[]>([])
const input = ref('')
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const lastResult = ref<ImportResult | null>(null)
const executionNodeStatus = ref<ExecutionNodeAdminStatus | null>(null)
const loaded = ref(false)

const parsed = computed(() => parseAccountCredentials(input.value))
const uniqueRows = computed(() => {
  const seen = new Set<string>()
  return parsed.value.rows.filter(row => {
    const key = normalizeIdentity(row.account)
    if (!key || seen.has(key)) return false
    seen.add(key)
    return true
  })
})
const duplicateCount = computed(() => parsed.value.rows.length - uniqueRows.value.length)
const invalidLines = computed(() => {
  const invalid = parsed.value.invalidRows.map(row => row.lineNumber)
  for (const row of uniqueRows.value) {
    try {
      const secret = normalizeBase32Secret(row.twoFactor)
      if (!normalizeIdentity(row.account) || !/^[A-Z2-7]{16,256}$/.test(secret)) invalid.push(row.lineNumber)
    } catch {
      invalid.push(row.lineNumber)
    }
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
  return uniqueRows.value.map(row => {
    const key = normalizeIdentity(row.account)
    const matched = accountsByIdentity.value.get(key) || []
    const supported = matched.filter(canStoreCredentials)
    const targets: CredentialTarget[] = []
    let completeCount = 0
    let matchedDuplicateCount = 0
    let totpSecret = ''
    try {
      totpSecret = normalizeBase32Secret(row.twoFactor)
    } catch {
      // Invalid rows are reported separately and cannot be submitted.
    }
    for (const account of supported) {
      if (hasCompleteCredentials(account)) {
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
      const email = loginEmail(account, row.account)
      if (!email) continue
      claimedAccountIDs.add(account.id)
      targets.push({ account, email, row, totpSecret })
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
      label: row.account,
      matched,
      targets,
      completeCount,
      duplicateCount: matchedDuplicateCount,
      status
    }
  })
})

const targetCount = computed(() => previewEntries.value.reduce((sum, entry) => sum + entry.targets.length, 0))
const totalDuplicateCount = computed(() => duplicateCount.value + previewEntries.value.reduce((sum, entry) => sum + entry.duplicateCount, 0))
const manageableAccounts = computed(() => accounts.value.filter(canStoreCredentials))
const completeAccountCount = computed(() => manageableAccounts.value.filter(hasCompleteCredentials).length)
const canSave = computed(() => !loading.value && !saving.value && !loadError.value && invalidLines.value.length === 0 && targetCount.value > 0)

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

function hasCompleteCredentials(account: Account): boolean {
  const status = account.credentials_status
  return status?.has_xiass_openai_oauth_reauth_email === true
    && status?.has_xiass_openai_oauth_reauth_password_encrypted === true
    && status?.has_xiass_openai_oauth_reauth_totp_secret_encrypted === true
}

function matchedAccountText(entry: PreviewEntry): string {
  if (!entry.matched.length) return '未找到现有 OpenAI OAuth 账号'
  return entry.matched.map(account => `#${account.id} ${account.name}`).join('、')
}

function previewStatusText(entry: PreviewEntry): string {
  if (entry.status === 'unmatched') return '不保存'
  if (entry.status === 'unsupported') return '账号类型不支持'
  if (entry.status === 'duplicate') return '同一账号已在上方，跳过'
  if (entry.status === 'complete') return '已完整保存，跳过'
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
            sort_order: 'asc'
          })
          resultAccounts.push(...result.items)
          if (page >= result.pages || result.items.length === 0) break
          page++
        }
        return resultAccounts
      })()
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
  return saveOpenAIAccountReauthorizationCredentials(target.account.id, {
    email: target.email,
    ...(status?.has_xiass_openai_oauth_reauth_password_encrypted === true ? {} : { password: target.row.password }),
    ...(status?.has_xiass_openai_oauth_reauth_totp_secret_encrypted === true ? {} : { totp_secret: target.totpSecret })
  })
}

async function saveAll() {
  if (!canSave.value) return
  saving.value = true
  lastResult.value = null
  const entries = previewEntries.value
  const targets = entries.flatMap(entry => entry.targets)
  const result: ImportResult = {
    saved: 0,
    failed: 0,
    unmatched: entries.filter(entry => entry.status === 'unmatched').length,
    alreadySaved: entries.reduce((sum, entry) => sum + entry.completeCount, 0),
    errors: []
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
  input.value = ''
  lastResult.value = result
  saving.value = false
  if (result.saved > 0) emit('updated')
}

watch(() => props.active, active => {
  if (active && !loaded.value) void loadAccounts()
}, { immediate: true })
</script>
