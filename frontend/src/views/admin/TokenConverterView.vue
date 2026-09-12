<template>
  <component :is="pageLayout">
    <div class="mx-auto max-w-[1480px] space-y-5">
      <header class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
            {{ t('tokenConverter.title') }}
          </h1>
          <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-600 dark:text-gray-400">
            {{ t('tokenConverter.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <div class="inline-flex w-fit items-center gap-2 rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs font-medium text-emerald-700 dark:border-emerald-800/70 dark:bg-emerald-950/30 dark:text-emerald-300">
            <Icon name="shield" size="sm" />
            {{ authStore.isAdmin ? t('tokenConverter.localOnlyWithImport') : t('tokenConverter.localOnly') }}
          </div>
          <button
            v-if="authStore.isAdmin"
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="adminAccountsLoading"
            @click="openAdminAccountPicker"
          >
            <span v-if="adminAccountsLoading" class="h-4 w-4 animate-spin rounded-full border-2 border-gray-300 border-t-primary-500" />
            <Icon v-else name="database" size="sm" />
            {{ t('tokenConverter.adminAccounts.action') }}
          </button>
        </div>
      </header>

      <AccountCredentialParser />

      <OpenAIReauthorizationPanel @tokens="loadTokenJson" />

      <section class="card overflow-hidden" aria-labelledby="token-converter-workspace">
        <h2 id="token-converter-workspace" class="sr-only">{{ t('tokenConverter.workspace') }}</h2>

        <div class="border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-5">
          <div class="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
            <div class="min-w-0">
              <div class="mb-2 text-xs font-semibold uppercase text-gray-500 dark:text-gray-400">
                {{ t('tokenConverter.outputFormat') }}
              </div>
              <div class="grid grid-cols-2 gap-2 sm:flex sm:flex-wrap" role="group" :aria-label="t('tokenConverter.outputFormat')">
                <button
                  v-for="format in outputFormats"
                  :key="format.value"
                  type="button"
                  class="min-h-10 rounded-md border px-3 py-2 text-sm font-medium transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500/40 disabled:cursor-not-allowed disabled:opacity-45"
                  :class="outputFormat === format.value
                    ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300'
                    : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300 hover:text-gray-900 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-dark-500 dark:hover:text-white'"
                  :aria-pressed="outputFormat === format.value"
                  :disabled="!format.compatible"
                  :title="format.compatible ? undefined : t('tokenConverter.incompatibleFormat')"
                  @click="selectOutputFormat(format.value, format.compatible)"
                >
                  {{ format.label }}
                </button>
              </div>
              <p class="mt-2 max-w-3xl text-xs leading-5 text-gray-500 dark:text-gray-400">
                {{ t('tokenConverter.formatCompatibility') }}
              </p>
            </div>

            <div v-if="outputFormat === 'xiass'" class="grid grid-cols-2 gap-3 sm:w-auto">
              <label class="block">
                <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('tokenConverter.concurrency') }}
                </span>
                <input v-model.number="concurrency" class="input h-10 w-full sm:w-28" type="number" min="0" max="1000" />
              </label>
              <label class="block">
                <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('tokenConverter.priority') }}
                </span>
                <input v-model.number="priority" class="input h-10 w-full sm:w-28" type="number" min="0" />
              </label>
            </div>
          </div>
        </div>

        <div class="grid lg:grid-cols-2">
          <section
            class="relative border-b border-gray-200 dark:border-dark-700 lg:border-b-0 lg:border-r"
            :class="{ 'bg-primary-50/70 dark:bg-primary-950/20': dragActive }"
            @dragenter.prevent="dragActive = true"
            @dragover.prevent="dragActive = true"
            @dragleave.prevent="handleDragLeave"
            @drop.prevent="handleDrop"
          >
            <div class="flex min-h-[58px] flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-4 py-3 dark:border-dark-700/70 sm:px-5">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('tokenConverter.input') }}</h3>
                  <span v-if="detectedFormatLabel" class="rounded bg-gray-100 px-2 py-1 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                    {{ detectedFormatLabel }}
                  </span>
                </div>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('tokenConverter.inputHint') }}</p>
              </div>
              <div class="flex items-center gap-2">
                <input
                  ref="fileInput"
                  class="hidden"
                  type="file"
                  accept=".json,.jsonl,.txt,application/json,text/plain"
                  multiple
                  @change="handleFileChange"
                />
                <button type="button" class="btn btn-secondary btn-sm" @click="fileInput?.click()">
                  <Icon name="upload" size="sm" />
                  {{ t('tokenConverter.chooseFiles') }}
                </button>
                <button type="button" class="btn btn-ghost btn-sm px-2" :disabled="!inputText" :title="t('common.clear')" @click="clearAll">
                  <Icon name="trash" size="sm" />
                  <span class="sr-only">{{ t('common.clear') }}</span>
                </button>
              </div>
            </div>
            <textarea
              v-model="inputText"
              data-test="token-converter-input"
              class="token-editor block min-h-[420px] w-full resize-y bg-transparent px-4 py-4 font-mono text-[13px] leading-6 text-gray-800 outline-none placeholder:text-gray-400 dark:text-gray-200 dark:placeholder:text-gray-600 sm:px-5"
              spellcheck="false"
              autocomplete="off"
              :placeholder="t('tokenConverter.inputPlaceholder')"
              :aria-label="t('tokenConverter.input')"
            ></textarea>
            <div v-if="dragActive" class="pointer-events-none absolute inset-3 flex items-center justify-center rounded-md border-2 border-dashed border-primary-400 bg-white/90 text-sm font-medium text-primary-700 backdrop-blur-sm dark:bg-dark-900/90 dark:text-primary-300">
              <div class="flex items-center gap-2">
                <Icon name="upload" />
                {{ t('tokenConverter.dropFiles') }}
              </div>
            </div>
          </section>

          <section>
            <div class="flex min-h-[58px] flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-4 py-3 dark:border-dark-700/70 sm:px-5">
              <div>
                <div class="flex items-center gap-2">
                  <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('tokenConverter.output') }}</h3>
                  <span v-if="parseResult.accounts.length" class="rounded bg-primary-50 px-2 py-1 text-xs font-medium text-primary-700 dark:bg-primary-950/40 dark:text-primary-300">
                    {{ t('tokenConverter.accountCount', { count: parseResult.accounts.length }) }}
                  </span>
                </div>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ selectedOutputDescription }}</p>
              </div>
              <div class="flex flex-wrap items-center justify-end gap-2">
                <button type="button" class="btn btn-secondary btn-sm" :disabled="!outputText" @click="copyOutput">
                  <Icon :name="copied ? 'check' : 'copy'" size="sm" />
                  {{ t('common.copy') }}
                </button>
                <button
                  type="button"
                  class="btn btn-sm"
                  :class="canDirectImport ? 'btn-secondary' : 'btn-primary'"
                  :disabled="!outputText"
                  @click="downloadOutput"
                >
                  <Icon name="download" size="sm" />
                  {{ t('tokenConverter.download') }}
                </button>
                <button
                  v-if="authStore.isAdmin && outputFormat === 'xiass'"
                  type="button"
                  data-test="open-direct-import"
                  class="btn btn-primary btn-sm"
                  :disabled="!canDirectImport"
                  @click="openDirectImport"
                >
                  <Icon name="upload" size="sm" />
                  {{ t('tokenConverter.directImport.action') }}
                </button>
              </div>
            </div>
            <textarea
              :value="outputText"
              class="token-editor block min-h-[420px] w-full resize-y bg-transparent px-4 py-4 font-mono text-[13px] leading-6 text-gray-800 outline-none placeholder:text-gray-400 dark:text-gray-200 dark:placeholder:text-gray-600 sm:px-5"
              readonly
              spellcheck="false"
              :placeholder="t('tokenConverter.outputPlaceholder')"
              :aria-label="t('tokenConverter.output')"
            ></textarea>
          </section>
        </div>

        <div v-if="inputText" class="border-t border-gray-200 dark:border-dark-700">
          <div class="grid grid-cols-2 divide-x divide-gray-100 border-b border-gray-100 dark:divide-dark-700 dark:border-dark-700 sm:grid-cols-4">
            <div v-for="stat in stats" :key="stat.label" class="px-4 py-3 text-center">
              <div class="text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ stat.value }}</div>
              <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ stat.label }}</div>
            </div>
          </div>

          <div v-if="parseResult.warnings.length || (inputText && !parseResult.accounts.length)" class="border-b border-amber-200 bg-amber-50/80 px-4 py-3 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/25 dark:text-amber-200 sm:px-5">
            <div class="flex items-start gap-2">
              <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" />
              <div class="min-w-0 space-y-1">
                <p v-if="!parseResult.accounts.length">{{ t('tokenConverter.noAccounts') }}</p>
                <p v-for="warning in visibleWarnings" :key="`${warning.index}-${warning.code}`">
                  {{ warningText(warning) }}
                </p>
                <p v-if="parseResult.warnings.length > visibleWarnings.length">
                  {{ t('tokenConverter.moreWarnings', { count: parseResult.warnings.length - visibleWarnings.length }) }}
                </p>
              </div>
            </div>
          </div>

          <div v-if="parseResult.accounts.length" class="divide-y divide-gray-100 dark:divide-dark-700/70 md:hidden">
            <div
              v-for="account in parseResult.accounts"
              :key="`mobile-${account.index}`"
              class="space-y-3 px-4 py-4 text-sm text-gray-700 dark:text-gray-300"
            >
              <div class="flex min-w-0 items-start justify-between gap-3">
                <div class="min-w-0">
                  <div class="break-words font-medium text-gray-900 dark:text-white">{{ account.name }}</div>
                  <div v-if="account.email && account.email !== account.name" class="mt-0.5 break-all text-xs text-gray-500">{{ account.email }}</div>
                </div>
                <span class="flex-none rounded bg-gray-100 px-2 py-1 text-xs dark:bg-dark-700">{{ sourceFormatLabel(account.sourceFormat) }}</span>
              </div>
              <div class="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3">
                <div class="min-w-0">
                  <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('tokenConverter.identity') }}</div>
                  <div class="mt-1 break-all font-mono text-xs text-gray-600 dark:text-gray-300">{{ account.accountId || account.userId || '-' }}</div>
                </div>
                <div class="text-right">
                  <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('tokenConverter.tokens') }}</div>
                  <div class="mt-1 flex justify-end gap-1.5">
                    <span :class="tokenBadgeClass(!!account.accessToken)">AT</span>
                    <span :class="tokenBadgeClass(!!account.refreshToken)">RT</span>
                    <span :class="tokenBadgeClass(!!account.idToken)">ID</span>
                  </div>
                </div>
              </div>
              <div class="flex items-center justify-between gap-3 text-xs text-gray-500 dark:text-gray-400">
                <span>{{ t('tokenConverter.expiresAt') }}</span>
                <span class="text-right tabular-nums">{{ formatExpiry(account.expiresAt) }}</span>
              </div>
            </div>
          </div>

          <div v-if="parseResult.accounts.length" class="hidden overflow-x-auto md:block">
            <table class="w-full min-w-[760px] text-left">
              <thead class="bg-gray-50/70 text-xs font-medium text-gray-500 dark:bg-dark-800/60 dark:text-gray-400">
                <tr>
                  <th class="px-5 py-3">{{ t('tokenConverter.name') }}</th>
                  <th class="px-4 py-3">{{ t('tokenConverter.sourceFormat') }}</th>
                  <th class="px-4 py-3">{{ t('tokenConverter.identity') }}</th>
                  <th class="px-4 py-3">{{ t('tokenConverter.tokens') }}</th>
                  <th class="px-5 py-3 text-right">{{ t('tokenConverter.expiresAt') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700/70">
                <tr v-for="account in parseResult.accounts" :key="account.index" class="text-sm text-gray-700 dark:text-gray-300">
                  <td class="max-w-[260px] px-5 py-3">
                    <div class="truncate font-medium text-gray-900 dark:text-white" :title="account.name">{{ account.name }}</div>
                    <div v-if="account.email && account.email !== account.name" class="mt-0.5 truncate text-xs text-gray-500" :title="account.email">{{ account.email }}</div>
                  </td>
                  <td class="px-4 py-3">
                    <span class="rounded bg-gray-100 px-2 py-1 text-xs dark:bg-dark-700">{{ sourceFormatLabel(account.sourceFormat) }}</span>
                  </td>
                  <td class="max-w-[250px] px-4 py-3 font-mono text-xs text-gray-500 dark:text-gray-400">
                    <span class="block truncate" :title="account.accountId || account.userId || ''">{{ account.accountId || account.userId || '-' }}</span>
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex flex-wrap gap-1.5">
                      <span :class="tokenBadgeClass(!!account.accessToken)">AT</span>
                      <span :class="tokenBadgeClass(!!account.refreshToken)">RT</span>
                      <span :class="tokenBadgeClass(!!account.idToken)">ID</span>
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-5 py-3 text-right text-xs tabular-nums text-gray-500 dark:text-gray-400">
                    {{ formatExpiry(account.expiresAt) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </section>
    </div>

    <AdminAccountPicker
      :show="adminAccountPickerVisible"
      :accounts="adminAccounts"
      :loading="adminAccountsLoading"
      @cancel="closeAdminAccountPicker"
      @confirm="importSelectedAdminAccounts"
    />
    <DirectAccountImportDialog
      :show="directImportVisible"
      :payload="directImportPayload"
      :account-count="directImportPayload?.accounts.length ?? 0"
      @close="closeDirectImport"
      @imported="handleDirectImportComplete"
    />
    <TotpStepUpDialog :controller="stepUp" />
  </component>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import PublicToolLayout from '@/components/layout/PublicToolLayout.vue'
import AdminAccountPicker from '@/components/token-converter/AdminAccountPicker.vue'
import AccountCredentialParser from '@/components/token-converter/AccountCredentialParser.vue'
import DirectAccountImportDialog from '@/components/token-converter/DirectAccountImportDialog.vue'
import OpenAIReauthorizationPanel from '@/components/token-converter/OpenAIReauthorizationPanel.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import { useClipboard } from '@/composables/useClipboard'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import type { Account, AdminDataImportResult, AdminDataPayload } from '@/types'
import {
  areTokenAccountsCompatibleWithFormat,
  exportTokenAccounts,
  parseTokenInput,
  type TokenOutputFormat,
  type TokenParseResult,
  type TokenSourceFormat,
  type TokenWarning,
} from '@/features/token-converter/tokenConverter'

const { t, locale } = useI18n()
const { copied, copyToClipboard } = useClipboard()
const authStore = useAuthStore()
const appStore = useAppStore()
const stepUp = useStepUp()
const pageLayout = computed(() => authStore.isAuthenticated ? AppLayout : PublicToolLayout)

const emptyResult = (): TokenParseResult => ({
  accounts: [],
  sourceFormats: [],
  warnings: [],
  skipped: 0,
  proxies: [],
})

const inputText = ref('')
const outputFormat = ref<TokenOutputFormat>('xiass')
const concurrency = ref(3)
const priority = ref(50)
const parseResult = shallowRef<TokenParseResult>(emptyResult())
const fileInput = ref<HTMLInputElement | null>(null)
const dragActive = ref(false)
const adminAccountPickerVisible = ref(false)
const adminAccountsLoading = ref(false)
const adminAccounts = shallowRef<Account[]>([])
const directImportVisible = ref(false)
const directImportPayload = shallowRef<AdminDataPayload | null>(null)
let parseTimer: ReturnType<typeof setTimeout> | undefined

const MAX_FILE_COUNT = 20
const MAX_TOTAL_FILE_BYTES = 20 * 1024 * 1024

function onlyOAuthAccounts(accounts: Account[]): Account[] {
  return accounts.filter((account) => account.type === 'oauth')
}

const outputFormats = computed<Array<{ value: TokenOutputFormat; label: string; compatible: boolean }>>(() => {
  const formats: Array<{ value: TokenOutputFormat; label: string }> = [
    { value: 'xiass', label: t('tokenConverter.formats.xiass') },
    { value: 'session', label: t('tokenConverter.formats.session') },
    { value: 'codex-auth', label: t('tokenConverter.formats.codexAuth') },
    { value: 'oauth', label: t('tokenConverter.formats.oauth') },
    { value: 'canonical', label: t('tokenConverter.formats.canonical') },
  ]
  return formats.map((format) => ({
    ...format,
    compatible: parseResult.value.accounts.length === 0
      || areTokenAccountsCompatibleWithFormat(parseResult.value.accounts, format.value),
  }))
})

const exported = computed(() => {
  if (!parseResult.value.accounts.length) return undefined
  if (!areTokenAccountsCompatibleWithFormat(parseResult.value.accounts, outputFormat.value)) return undefined
  return exportTokenAccounts(parseResult.value.accounts, outputFormat.value, {
    concurrency: Math.max(0, Number(concurrency.value) || 0),
    priority: Math.max(0, Number(priority.value) || 0),
    proxies: parseResult.value.proxies,
  })
})

const outputText = computed(() => exported.value?.text ?? '')
const canDirectImport = computed(() => (
  authStore.isAdmin
  && outputFormat.value === 'xiass'
  && parseResult.value.accounts.length > 0
  && !!outputText.value
))
const selectedOutputDescription = computed(() => t(`tokenConverter.formatDescriptions.${outputFormat.value}`))
const visibleWarnings = computed(() => parseResult.value.warnings.slice(0, 6))
const detectedFormatLabel = computed(() => {
  const formats = parseResult.value.sourceFormats
  if (!formats.length) return ''
  if (formats.length > 1) return t('tokenConverter.mixedFormats', { count: formats.length })
  return t('tokenConverter.detectedFormat', { format: sourceFormatLabel(formats[0]) })
})
const stats = computed(() => [
  { label: t('tokenConverter.accounts'), value: parseResult.value.accounts.length },
  { label: t('tokenConverter.refreshTokens'), value: parseResult.value.accounts.filter((item) => item.refreshToken).length },
  { label: t('tokenConverter.sourceTypes'), value: parseResult.value.sourceFormats.length },
  { label: t('tokenConverter.skipped'), value: parseResult.value.skipped },
])

watch(inputText, (value) => {
  if (parseTimer) clearTimeout(parseTimer)
  if (!value.trim()) {
    parseResult.value = emptyResult()
    return
  }
  parseTimer = setTimeout(() => {
    const nextResult = parseTokenInput(value)
    parseResult.value = nextResult
    if (
      nextResult.accounts.length > 0
      && !areTokenAccountsCompatibleWithFormat(nextResult.accounts, outputFormat.value)
    ) {
      outputFormat.value = 'xiass'
    }
  }, 160)
}, { immediate: true })

function selectOutputFormat(format: TokenOutputFormat, compatible: boolean): void {
  if (compatible) outputFormat.value = format
}

function sourceFormatLabel(format: TokenSourceFormat): string {
  return t(`tokenConverter.sources.${format}`)
}

function warningText(warning: TokenWarning): string {
  return t(`tokenConverter.warnings.${warning.code}`, { index: warning.index })
}

function tokenBadgeClass(available: boolean): string {
  return [
    'inline-flex h-6 min-w-8 items-center justify-center rounded px-1.5 text-[11px] font-semibold',
    available
      ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
      : 'bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-gray-500',
  ].join(' ')
}

function formatExpiry(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-US', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

async function readFiles(files: File[]): Promise<void> {
  const supported = files.filter((file) => /\.(json|jsonl|txt)$/i.test(file.name) || ['application/json', 'text/plain'].includes(file.type))
  if (!supported.length) return
  const totalBytes = supported.reduce((total, file) => total + file.size, 0)
  if (supported.length > MAX_FILE_COUNT || totalBytes > MAX_TOTAL_FILE_BYTES) {
    appStore.showError(t('tokenConverter.fileLimit', { count: MAX_FILE_COUNT, size: 20 }))
    return
  }
  const contents = await Promise.all(supported.map((file) => file.text()))
  inputText.value = contents.filter((content) => content.trim()).join('\n')
}

function loadTokenJson(tokenJson: string): void {
  inputText.value = tokenJson
  outputFormat.value = 'oauth'
  appStore.showSuccess(t('tokenConverter.reauthorization.success'))
}

async function openAdminAccountPicker(): Promise<void> {
  if (!authStore.isAdmin || adminAccountsLoading.value) return
  adminAccountPickerVisible.value = true
  adminAccountsLoading.value = true
  adminAccounts.value = []
  try {
    const firstPage = await adminAPI.accounts.list(1, 100, {
      lite: 'true',
      type: 'oauth',
      sort_by: 'recent_activity',
      sort_order: 'desc',
    })
    const accounts = [...firstPage.items]
    for (let page = 2; page <= firstPage.pages; page += 1) {
      const nextPage = await adminAPI.accounts.list(page, 100, {
        lite: 'true',
        type: 'oauth',
        sort_by: 'recent_activity',
        sort_order: 'desc',
      })
      accounts.push(...nextPage.items)
    }
    adminAccounts.value = onlyOAuthAccounts(accounts)
  } catch (error) {
    adminAccountPickerVisible.value = false
    const message = (error as { message?: string })?.message
    appStore.showError(message || t('tokenConverter.adminAccounts.loadFailed'))
  } finally {
    adminAccountsLoading.value = false
  }
}

function closeAdminAccountPicker(): void {
  adminAccountPickerVisible.value = false
  adminAccounts.value = []
}

async function importSelectedAdminAccounts(accountIds: number[]): Promise<void> {
  if (!authStore.isAdmin || accountIds.length === 0 || adminAccountsLoading.value) return
  const allowedIds = new Set(onlyOAuthAccounts(adminAccounts.value).map((account) => account.id))
  const oauthAccountIds = [...new Set(accountIds)].filter((accountId) => allowedIds.has(accountId))
  if (oauthAccountIds.length === 0) return
  adminAccountsLoading.value = true
  try {
    const payload = await stepUp.run(() => adminAPI.accounts.exportData({
      ids: oauthAccountIds,
      includeProxies: false,
    }))
    const oauthPayload = {
      ...payload,
      proxies: [],
      accounts: payload.accounts.filter((account) => account.type === 'oauth'),
    }
    inputText.value = JSON.stringify(oauthPayload, null, 2)
    outputFormat.value = 'xiass'
    closeAdminAccountPicker()
    appStore.showSuccess(t('tokenConverter.adminAccounts.loaded', { count: oauthPayload.accounts.length }))
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(
        stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled'),
      )
      return
    }
    const message = (error as { message?: string })?.message
    appStore.showError(message || t('tokenConverter.adminAccounts.exportFailed'))
  } finally {
    adminAccountsLoading.value = false
  }
}

function handleFileChange(event: Event): void {
  const target = event.target as HTMLInputElement
  void readFiles(Array.from(target.files ?? []))
  target.value = ''
}

function handleDrop(event: DragEvent): void {
  dragActive.value = false
  void readFiles(Array.from(event.dataTransfer?.files ?? []))
}

function handleDragLeave(event: DragEvent): void {
  const current = event.currentTarget as HTMLElement | null
  const related = event.relatedTarget as Node | null
  if (!current || !related || !current.contains(related)) dragActive.value = false
}

function clearAll(): void {
  inputText.value = ''
  parseResult.value = emptyResult()
  if (fileInput.value) fileInput.value.value = ''
}

async function copyOutput(): Promise<void> {
  await copyToClipboard(outputText.value, t('tokenConverter.copySuccess'))
}

function downloadOutput(): void {
  if (!exported.value) return
  const blob = new Blob([outputText.value], { type: 'application/json;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = exported.value.fileName
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

function isAdminDataPayload(value: unknown): value is AdminDataPayload {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const candidate = value as Record<string, unknown>
  return Array.isArray(candidate.accounts)
    && candidate.accounts.length > 0
    && Array.isArray(candidate.proxies)
    && typeof candidate.exported_at === 'string'
}

function openDirectImport(): void {
  if (!canDirectImport.value) return
  try {
    const payload = JSON.parse(outputText.value) as unknown
    if (!isAdminDataPayload(payload)) throw new Error('invalid XIASS import payload')
    directImportPayload.value = payload
    directImportVisible.value = true
  } catch {
    appStore.showError(t('tokenConverter.directImport.invalidOutput'))
  }
}

function closeDirectImport(): void {
  directImportVisible.value = false
  directImportPayload.value = null
}

function handleDirectImportComplete(_result: AdminDataImportResult): void {
  // Keep the converted JSON visible so the administrator can inspect or download it after import.
}

onBeforeUnmount(() => {
  if (parseTimer) clearTimeout(parseTimer)
  inputText.value = ''
  parseResult.value = emptyResult()
  adminAccounts.value = []
  directImportPayload.value = null
})
</script>

<style scoped>
.token-editor {
  scrollbar-gutter: stable;
}

@media (max-width: 1023px) {
  .token-editor {
    min-height: 320px;
  }
}
</style>
