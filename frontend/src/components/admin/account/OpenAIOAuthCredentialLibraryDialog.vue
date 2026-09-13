<template>
  <BaseDialog :show="show" title="401 账号库" width="wide" :close-on-escape="!saving" @close="requestClose">
    <div class="space-y-4" data-testid="openai-credential-library">
      <div class="flex flex-wrap gap-x-5 gap-y-2 border-y border-gray-200 py-3 text-sm dark:border-dark-700">
        <span>可管理 OpenAI OAuth {{ manageableAccounts.length }} 个</span>
        <span class="text-emerald-700 dark:text-emerald-300">已完整保存 {{ completeAccountCount }} 个</span>
        <span class="text-amber-700 dark:text-amber-300">待补充 {{ Math.max(0, manageableAccounts.length - completeAccountCount) }} 个</span>
      </div>

      <div v-if="loading" class="flex min-h-32 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="refresh" size="sm" class="animate-spin" />
        正在读取账号
      </div>

      <template v-else>
        <label class="block text-sm text-gray-700 dark:text-gray-200">
          邮箱或账号名称、密码、2FA
          <textarea
            v-model="input"
            class="input mt-2 min-h-32 resize-y font-mono"
            rows="5"
            autocomplete="off"
            autocapitalize="off"
            :spellcheck="false"
            placeholder="邮箱----密码----2FA，每行一个账号"
            data-testid="credential-library-input"
          />
        </label>

        <div class="flex flex-wrap gap-x-4 gap-y-2 text-sm text-gray-600 dark:text-gray-300">
          <span>已识别 {{ uniqueRows.length }} 条</span>
          <span v-if="totalDuplicateCount">已忽略 {{ totalDuplicateCount }} 条重复内容</span>
          <span v-if="targetCount" class="text-primary-700 dark:text-primary-300">可补充 {{ targetCount }} 个账号</span>
          <span v-if="invalidLines.length" class="text-red-600 dark:text-red-300" role="alert">格式错误：第 {{ invalidLines.join('、') }} 行</span>
        </div>

        <div v-if="previewEntries.length" class="max-h-72 overflow-y-auto border-y border-gray-200 dark:border-dark-700">
          <div
            v-for="entry in previewEntries"
            :key="entry.key"
            class="grid gap-1 border-b border-gray-100 px-1 py-3 last:border-b-0 dark:border-dark-700 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto] sm:items-center sm:gap-3"
          >
            <span class="min-w-0 break-all text-sm font-medium text-gray-900 dark:text-gray-100">{{ entry.label }}</span>
            <span class="min-w-0 truncate text-xs text-gray-500 dark:text-gray-400">{{ matchedAccountText(entry) }}</span>
            <span class="text-xs font-medium" :class="previewStatusClass(entry)">{{ previewStatusText(entry) }}</span>
          </div>
        </div>

        <p v-if="loadError" class="text-sm text-red-600 dark:text-red-300" role="alert">{{ loadError }}</p>
        <div v-if="lastResult" class="border-y border-gray-200 py-3 text-sm dark:border-dark-700" role="status">
          <p :class="lastResult.failed ? 'text-amber-700 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">
            已保存 {{ lastResult.saved }} 个，失败 {{ lastResult.failed }} 个，未匹配 {{ lastResult.unmatched }} 条，已完整保存跳过 {{ lastResult.alreadySaved }} 个。
          </p>
          <p v-if="lastResult.errors.length" class="mt-1 break-words text-xs text-red-600 dark:text-red-300">{{ lastResult.errors.join('；') }}</p>
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="requestClose">关闭</button>
        <button type="button" class="btn btn-primary flex items-center gap-2" data-testid="save-credential-library" :disabled="!canSave" @click="saveAll">
          <Icon name="check" size="sm" :class="saving ? 'animate-spin' : ''" />
          <span>{{ saving ? '正在保存' : '保存登录信息' }}</span>
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { accountsAPI } from '@/api/admin'
import { saveOpenAIAccountReauthorizationCredentials } from '@/api/admin/teamChild'
import { parseAccountCredentials, type AccountCredentialRow } from '@/features/token-converter/accountCredentials'
import { normalizeBase32Secret } from '@/features/token-converter/totp'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'

const props = withDefaults(defineProps<{
  show: boolean
  executionNodeEnabled?: boolean
  localExecutionNodeID?: string
  legacyUnassignedNodeID?: string
  pairedFullAccess?: boolean
}>(), {
  executionNodeEnabled: false,
  localExecutionNodeID: 'api',
  legacyUnassignedNodeID: 'api',
  pairedFullAccess: false,
})
const emit = defineEmits<{ close: []; updated: [] }>()

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
  const owner = account.execution_node_id?.trim() || props.legacyUnassignedNodeID || 'api'
  const nodeWritable = !props.executionNodeEnabled || props.pairedFullAccess || owner === props.localExecutionNodeID
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
  loading.value = true
  loadError.value = ''
  try {
    const found: Account[] = []
    let page = 1
    while (true) {
      const result = await accountsAPI.list(page, 200, {
        platform: 'openai',
        type: 'oauth',
        sort_by: 'id',
        sort_order: 'asc'
      })
      found.push(...result.items)
      if (page >= result.pages || result.items.length === 0) break
      page++
    }
    accounts.value = found
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

function requestClose() {
  if (saving.value) return
  input.value = ''
  lastResult.value = null
  emit('close')
}

watch(() => props.show, show => {
  if (show) void loadAccounts()
  else input.value = ''
}, { immediate: true })

onUnmounted(() => {
  input.value = ''
})
</script>
