<template>
  <section class="card overflow-hidden" aria-labelledby="account-credential-parser-title">
    <div class="flex flex-col gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between sm:px-5">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <h2 id="account-credential-parser-title" class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('tokenConverter.credentials.title') }}
          </h2>
          <span class="inline-flex items-center gap-1.5 rounded bg-emerald-50 px-2 py-1 text-xs font-medium text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300">
            <Icon name="shield" size="xs" />
            {{ t('tokenConverter.credentials.localOnly') }}
          </span>
        </div>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t('tokenConverter.credentials.description') }}
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <input
          ref="fileInput"
          class="hidden"
          type="file"
          accept=".txt,text/plain"
          @change="handleFileChange"
        />
        <button type="button" class="btn btn-secondary btn-sm" @click="fileInput?.click()">
          <Icon name="upload" size="sm" />
          {{ t('tokenConverter.credentials.chooseFile') }}
        </button>
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="!result.rows.length"
          :aria-pressed="showSecrets"
          @click="showSecrets = !showSecrets"
        >
          <Icon :name="showSecrets ? 'eyeOff' : 'eye'" size="sm" />
          {{ showSecrets ? t('tokenConverter.credentials.hideSecrets') : t('tokenConverter.credentials.showSecrets') }}
        </button>
        <button type="button" class="btn btn-ghost btn-sm px-2" :disabled="!inputText" :title="t('common.clear')" @click="clearInput">
          <Icon name="trash" size="sm" />
          <span class="sr-only">{{ t('common.clear') }}</span>
        </button>
      </div>
    </div>

    <div class="grid lg:grid-cols-[minmax(280px,0.8fr)_minmax(0,1.7fr)]">
      <div
        class="relative border-b border-gray-200 dark:border-dark-700 lg:border-b-0 lg:border-r"
        :class="{ 'bg-primary-50/70 dark:bg-primary-950/20': dragActive }"
        @dragenter.prevent="dragActive = true"
        @dragover.prevent="dragActive = true"
        @dragleave.prevent="handleDragLeave"
        @drop.prevent="handleDrop"
      >
        <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700/70 sm:px-5">
          <div class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('tokenConverter.credentials.input') }}</div>
          <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('tokenConverter.credentials.inputHint') }}</div>
        </div>
        <textarea
          v-model="inputText"
          class="block min-h-[300px] w-full resize-y bg-transparent px-4 py-4 font-mono text-[13px] leading-6 text-gray-800 outline-none placeholder:text-gray-400 dark:text-gray-200 dark:placeholder:text-gray-600 sm:px-5"
          spellcheck="false"
          autocomplete="off"
          :placeholder="t('tokenConverter.credentials.placeholder')"
          :aria-label="t('tokenConverter.credentials.input')"
        ></textarea>
        <div v-if="dragActive" class="pointer-events-none absolute inset-3 flex items-center justify-center rounded-md border-2 border-dashed border-primary-400 bg-white/90 text-sm font-medium text-primary-700 backdrop-blur-sm dark:bg-dark-900/90 dark:text-primary-300">
          <div class="flex items-center gap-2">
            <Icon name="upload" />
            {{ t('tokenConverter.credentials.dropFile') }}
          </div>
        </div>
      </div>

      <div class="min-w-0" data-test="credential-parser-result">
        <div class="flex min-h-[62px] flex-col gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700/70 sm:flex-row sm:items-center sm:justify-between sm:px-5">
          <div class="flex items-center gap-2 text-sm">
            <span class="font-semibold text-gray-900 dark:text-white">{{ t('tokenConverter.credentials.result') }}</span>
            <span v-if="result.rows.length" class="rounded bg-primary-50 px-2 py-1 text-xs font-medium text-primary-700 dark:bg-primary-950/40 dark:text-primary-300">
              {{ t('tokenConverter.credentials.validCount', { count: result.rows.length }) }}
            </span>
            <span v-if="result.invalidRows.length" class="rounded bg-red-50 px-2 py-1 text-xs font-medium text-red-700 dark:bg-red-950/30 dark:text-red-300">
              {{ t('tokenConverter.credentials.invalidCount', { count: result.invalidRows.length }) }}
            </span>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!result.rows.length" @click="copyColumn('account')">
              <Icon name="copy" size="sm" />
              {{ t('tokenConverter.credentials.copyAccounts') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!result.rows.length" @click="copyColumn('password')">
              <Icon name="copy" size="sm" />
              {{ t('tokenConverter.credentials.copyPasswords') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!result.rows.length" @click="copyColumn('twoFactor')">
              <Icon name="copy" size="sm" />
              {{ t('tokenConverter.credentials.copyTwoFactor') }}
            </button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="!result.rows.length" @click="copyAllRows">
              <Icon name="copy" size="sm" />
              {{ t('tokenConverter.credentials.copyAll') }}
            </button>
          </div>
        </div>

        <div v-if="!inputText.trim()" class="flex min-h-[300px] flex-col items-center justify-center px-6 py-12 text-center text-gray-400 dark:text-gray-500">
          <Icon name="key" size="xl" class="mb-3" />
          <p class="text-sm">{{ t('tokenConverter.credentials.empty') }}</p>
        </div>

        <div v-else class="max-h-[520px] overflow-auto">
          <div v-if="result.rows.length" class="hidden min-w-[940px] md:block">
            <div class="credential-grid sticky top-0 z-10 border-b border-gray-100 bg-gray-50/95 px-4 py-2 text-xs font-medium text-gray-500 backdrop-blur-sm dark:border-dark-700 dark:bg-dark-800/95 dark:text-gray-400 sm:px-5">
              <span>#</span>
              <span>{{ t('tokenConverter.credentials.account') }}</span>
              <span>{{ t('tokenConverter.credentials.password') }}</span>
              <span>{{ t('tokenConverter.credentials.twoFactor') }}</span>
              <span>{{ t('tokenConverter.credentials.currentCode') }}</span>
              <span class="sr-only">{{ t('tokenConverter.credentials.copyRow') }}</span>
            </div>
            <div class="divide-y divide-gray-100 dark:divide-dark-700/70">
              <div v-for="row in result.rows" :key="row.lineNumber" class="credential-grid items-center px-4 py-2.5 text-sm sm:px-5">
                <span class="text-xs tabular-nums text-gray-400">{{ row.lineNumber }}</span>
                <CredentialCopyButton :value="row.account" :copied="copiedKey === `${row.index}:account`" @copy="copyField(row, 'account')" />
                <CredentialCopyButton :value="displaySecret(row.password)" :title="showSecrets ? row.password : undefined" :copied="copiedKey === `${row.index}:password`" @copy="copyField(row, 'password')" />
                <CredentialCopyButton :value="displaySecret(row.twoFactor)" :title="twoFactorTitle(row)" :copied="copiedKey === `${row.index}:twoFactor`" @copy="copyField(row, 'twoFactor')" />
                <button
                  type="button"
                  :data-test="`totp-code-${row.index}`"
                  class="group flex h-9 min-w-0 items-center gap-2 rounded-md border border-cyan-200 bg-cyan-50 px-2.5 font-mono text-cyan-800 transition-colors hover:border-cyan-300 hover:bg-cyan-100 focus:outline-none focus:ring-2 focus:ring-cyan-500/40 disabled:cursor-not-allowed disabled:opacity-60 dark:border-cyan-900/70 dark:bg-cyan-950/30 dark:text-cyan-300 dark:hover:bg-cyan-950/50"
                  :disabled="!totpCodes[row.index]?.code"
                  :title="totpCodes[row.index]?.error || t('tokenConverter.credentials.copyCode')"
                  @click="copyTotp(row)"
                >
                  <span class="min-w-0 flex-1 truncate text-center text-sm font-semibold tracking-widest">
                    {{ totpDisplay(row) }}
                  </span>
                  <span v-if="totpCodes[row.index]?.code" class="flex-none font-sans text-[10px] tabular-nums text-cyan-600 dark:text-cyan-400">
                    {{ secondsRemaining }}s
                  </span>
                  <Icon v-if="totpCodes[row.index]?.code" :name="copiedKey === `${row.index}:totp` ? 'check' : 'copy'" size="xs" class="flex-none" />
                </button>
                <button type="button" class="flex h-9 w-9 items-center justify-center rounded-md text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 focus:outline-none focus:ring-2 focus:ring-primary-500/40 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white" :title="t('tokenConverter.credentials.copyRow')" @click="copyRow(row)">
                  <Icon :name="copiedKey === `${row.index}:row` ? 'check' : 'copy'" size="sm" />
                </button>
              </div>
            </div>
          </div>

          <div v-if="result.rows.length" class="divide-y divide-gray-100 dark:divide-dark-700/70 md:hidden">
            <div v-for="row in result.rows" :key="`mobile-${row.lineNumber}`" class="space-y-2 px-4 py-4">
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-gray-500">{{ t('tokenConverter.credentials.line', { line: row.lineNumber }) }}</span>
                <button type="button" class="btn btn-ghost btn-sm" @click="copyRow(row)">
                  <Icon :name="copiedKey === `${row.index}:row` ? 'check' : 'copy'" size="sm" />
                  {{ t('tokenConverter.credentials.copyRow') }}
                </button>
              </div>
              <CredentialCopyButton :label="t('tokenConverter.credentials.account')" :value="row.account" :copied="copiedKey === `${row.index}:account`" @copy="copyField(row, 'account')" />
              <CredentialCopyButton :label="t('tokenConverter.credentials.password')" :value="displaySecret(row.password)" :copied="copiedKey === `${row.index}:password`" @copy="copyField(row, 'password')" />
              <CredentialCopyButton :label="t('tokenConverter.credentials.twoFactor')" :value="displaySecret(row.twoFactor)" :copied="copiedKey === `${row.index}:twoFactor`" @copy="copyField(row, 'twoFactor')" />
              <button
                type="button"
                class="flex min-h-10 w-full min-w-0 items-center gap-2 rounded-md border border-cyan-200 bg-cyan-50 px-2.5 py-2 text-left font-mono text-xs text-cyan-800 focus:outline-none focus:ring-2 focus:ring-cyan-500/40 disabled:opacity-60 dark:border-cyan-900/70 dark:bg-cyan-950/30 dark:text-cyan-300"
                :disabled="!totpCodes[row.index]?.code"
                @click="copyTotp(row)"
              >
                <span class="w-14 flex-none font-sans text-[11px] text-cyan-600 dark:text-cyan-400">{{ t('tokenConverter.credentials.currentCode') }}</span>
                <span class="min-w-0 flex-1 text-center text-sm font-semibold tracking-widest">{{ totpDisplay(row) }}</span>
                <span v-if="totpCodes[row.index]?.code" class="flex-none font-sans text-[10px] tabular-nums">{{ secondsRemaining }}s</span>
                <Icon v-if="totpCodes[row.index]?.code" :name="copiedKey === `${row.index}:totp` ? 'check' : 'copy'" size="xs" class="flex-none" />
              </button>
            </div>
          </div>

          <div v-if="result.invalidRows.length" class="border-t border-red-100 bg-red-50/70 px-4 py-3 dark:border-red-900/50 dark:bg-red-950/20 sm:px-5">
            <div class="mb-2 flex items-center gap-2 text-sm font-medium text-red-800 dark:text-red-300">
              <Icon name="exclamationTriangle" size="sm" />
              {{ t('tokenConverter.credentials.invalidTitle') }}
            </div>
            <div class="space-y-1.5 text-xs text-red-700 dark:text-red-300">
              <div v-for="row in result.invalidRows" :key="`${row.lineNumber}-${row.code}`" class="grid grid-cols-[auto_minmax(0,1fr)] gap-2">
                <span class="tabular-nums">{{ t('tokenConverter.credentials.line', { line: row.lineNumber }) }}</span>
                <span class="min-w-0 truncate" :title="row.source">{{ invalidReason(row.code) }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores'
import {
  formatAccountCredential,
  parseAccountCredentials,
  type AccountCredentialRow,
  type CredentialParseErrorCode,
} from '@/features/token-converter/accountCredentials'
import { generateTotp, totpSecondsRemaining } from '@/features/token-converter/totp'

const CredentialCopyButton = defineComponent({
  props: {
    value: { type: String, required: true },
    label: { type: String, default: '' },
    title: { type: String, default: '' },
    copied: { type: Boolean, default: false },
  },
  emits: ['copy'],
  setup(props, { emit }) {
    return () => h('button', {
      type: 'button',
      class: 'group flex min-h-9 min-w-0 items-center gap-2 rounded-md border border-gray-200 bg-white px-2.5 py-2 text-left font-mono text-xs text-gray-700 transition-colors hover:border-primary-300 hover:bg-primary-50/50 focus:outline-none focus:ring-2 focus:ring-primary-500/40 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:border-primary-700 dark:hover:bg-primary-950/20',
      title: props.title || props.value,
      onClick: () => emit('copy'),
    }, [
      props.label ? h('span', { class: 'w-14 flex-none font-sans text-[11px] text-gray-400' }, props.label) : null,
      h('span', { class: 'min-w-0 flex-1 truncate' }, props.value),
      h(Icon, { name: props.copied ? 'check' : 'copy', size: 'xs', class: props.copied ? 'flex-none text-emerald-500' : 'flex-none text-gray-400 group-hover:text-primary-500' }),
    ])
  },
})

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const appStore = useAppStore()
const inputText = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const dragActive = ref(false)
const showSecrets = ref(false)
const copiedKey = ref('')
const now = ref(Date.now())
const totpCodes = shallowRef<Record<number, { code: string; error: string }>>({})
let copiedTimer: ReturnType<typeof setTimeout> | undefined
let tickTimer: ReturnType<typeof setInterval> | undefined
let totpGeneration = 0

const MAX_FILE_BYTES = 2 * 1024 * 1024
const result = computed(() => parseAccountCredentials(inputText.value))
const secondsRemaining = computed(() => totpSecondsRemaining(now.value))

function displaySecret(value: string): string {
  if (showSecrets.value) return value
  return '•'.repeat(Math.min(Math.max(value.length, 8), 18))
}

function invalidReason(code: CredentialParseErrorCode): string {
  return t(`tokenConverter.credentials.errors.${code}`)
}

function twoFactorTitle(row: AccountCredentialRow): string | undefined {
  if (row.twoFactorCleaned) return t('tokenConverter.credentials.cleanedMarker')
  return showSecrets.value ? row.twoFactor : undefined
}

function totpDisplay(row: AccountCredentialRow): string {
  const state = totpCodes.value[row.index]
  if (!state) return t('tokenConverter.credentials.calculating')
  if (state.error) return t('tokenConverter.credentials.invalidSecret')
  return state.code
}

async function refreshTotpCodes(): Promise<void> {
  const generation = ++totpGeneration
  const entries = await Promise.all(result.value.rows.map(async (row) => {
    try {
      return [row.index, { code: await generateTotp(row.twoFactor, now.value), error: '' }] as const
    } catch (error) {
      return [row.index, { code: '', error: (error as Error)?.message || 'invalid secret' }] as const
    }
  }))
  if (generation !== totpGeneration) return
  totpCodes.value = Object.fromEntries(entries)
}

async function copyValue(value: string, key: string, message: string): Promise<void> {
  if (!await copyToClipboard(value, message)) return
  copiedKey.value = key
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copiedKey.value = ''
  }, 1600)
}

function copyField(row: AccountCredentialRow, field: 'account' | 'password' | 'twoFactor'): void {
  const labels = {
    account: t('tokenConverter.credentials.account'),
    password: t('tokenConverter.credentials.password'),
    twoFactor: t('tokenConverter.credentials.twoFactor'),
  }
  void copyValue(row[field], `${row.index}:${field}`, t('tokenConverter.credentials.copySuccess', { field: labels[field] }))
}

function copyRow(row: AccountCredentialRow): void {
  void copyValue(formatAccountCredential(row), `${row.index}:row`, t('tokenConverter.credentials.rowCopySuccess'))
}

function copyTotp(row: AccountCredentialRow): void {
  const code = totpCodes.value[row.index]?.code
  if (code) void copyValue(code, `${row.index}:totp`, t('tokenConverter.credentials.codeCopySuccess'))
}

function copyColumn(field: 'account' | 'password' | 'twoFactor'): void {
  const value = result.value.rows.map((row) => row[field]).join('\n')
  void copyValue(value, `all:${field}`, t('tokenConverter.credentials.columnCopySuccess'))
}

function copyAllRows(): void {
  const value = result.value.rows.map(formatAccountCredential).join('\n')
  void copyValue(value, 'all:rows', t('tokenConverter.credentials.allCopySuccess'))
}

async function readFile(file?: File): Promise<void> {
  if (!file) return
  if (file.size > MAX_FILE_BYTES) {
    appStore.showError(t('tokenConverter.credentials.fileTooLarge'))
    return
  }
  inputText.value = await file.text()
}

function handleFileChange(event: Event): void {
  const target = event.target as HTMLInputElement
  void readFile(target.files?.[0])
  target.value = ''
}

function handleDrop(event: DragEvent): void {
  dragActive.value = false
  void readFile(event.dataTransfer?.files?.[0])
}

function handleDragLeave(event: DragEvent): void {
  const current = event.currentTarget as HTMLElement | null
  const related = event.relatedTarget as Node | null
  if (!current || !related || !current.contains(related)) dragActive.value = false
}

function clearInput(): void {
  inputText.value = ''
  copiedKey.value = ''
  showSecrets.value = false
  totpCodes.value = {}
  if (fileInput.value) fileInput.value.value = ''
}

watch(
  () => result.value.rows.map((row) => `${row.index}:${row.twoFactor}`).join('\n'),
  () => { void refreshTotpCodes() },
  { immediate: true },
)

onMounted(() => {
  let currentStep = Math.floor(now.value / 30_000)
  tickTimer = setInterval(() => {
    now.value = Date.now()
    const nextStep = Math.floor(now.value / 30_000)
    if (nextStep !== currentStep) {
      currentStep = nextStep
      void refreshTotpCodes()
    }
  }, 1000)
})

onBeforeUnmount(() => {
  if (copiedTimer) clearTimeout(copiedTimer)
  if (tickTimer) clearInterval(tickTimer)
  totpGeneration += 1
  clearInput()
})
</script>

<style scoped>
.credential-grid {
  display: grid;
  grid-template-columns: 28px minmax(150px, 1.15fr) minmax(130px, 1fr) minmax(130px, 1fr) minmax(150px, 0.9fr) 36px;
  gap: 8px;
}
</style>
