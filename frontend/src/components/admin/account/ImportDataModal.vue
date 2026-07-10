<template>
  <BaseDialog
    :show="show"
    title="导入数据"
    width="wide"
    close-on-click-outside
    @close="handleClose"
  >
    <!-- Tabs -->
    <div class="mb-4 flex border-b border-gray-200 dark:border-dark-700">
      <button
        type="button"
        @click="activeTab = 'json'"
        :class="[
          'px-4 py-2 text-sm font-medium border-b-2 transition-colors',
          activeTab === 'json' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
        ]"
      >
        JSON 文件导入
      </button>
      <button
        type="button"
        @click="activeTab = 'online'"
        :class="[
          'px-4 py-2 text-sm font-medium border-b-2 transition-colors',
          activeTab === 'online' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
        ]"
      >
        在线批量导入
      </button>
    </div>

    <!-- JSON Import Tab -->
    <form v-if="activeTab === 'json'" id="import-data-form" class="space-y-4" @submit.prevent="handleJsonImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.dataImportHint') }}
      </div>
      <div class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-600 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400">
        {{ t('admin.accounts.dataImportWarning') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.dataImportFile') }}</label>
        <div
          class="flex items-center justify-between gap-3 rounded-lg border border-dashed px-4 py-3 transition-colors"
          :class="dragActive
            ? 'border-primary-400 bg-primary-50/70 dark:border-primary-500 dark:bg-primary-900/20'
            : 'border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-800'"
          @dragenter.prevent="handleDragEnter"
          @dragover.prevent
          @dragleave.prevent="handleDragLeave"
          @drop.prevent="handleDrop"
        >
          <div class="min-w-0">
            <div class="truncate text-sm text-gray-700 dark:text-dark-200" :title="fileListTitle">
              {{ selectedFilesLabel || t('admin.accounts.dataImportSelectFile') }}
            </div>
            <div class="text-xs text-gray-500 dark:text-dark-400">
              JSON (.json)
              <span v-if="files.length > 1"> · {{ fileListTitle }}</span>
            </div>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" @click="openFilePicker">
            {{ t('common.chooseFile') }}
          </button>
        </div>
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          multiple
          @change="handleFileChange"
        />
      </div>

      <div v-if="result" class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.dataImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.dataImportResultSummary', result) }}
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800">
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} — {{ item.message }}
            </div>
          </div>
        </div>
      </div>
    </form>

    <!-- Online Batch Import Tab -->
    <form v-if="activeTab === 'online'" id="import-online-form" class="space-y-4" @submit.prevent="handleOnlineImport">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label class="input-label">平台类型</label>
          <select v-model="onlineForm.platform" class="input">
            <option value="antigravity">Antigravity</option>
            <option value="openai">OpenAI</option>
            <option value="anthropic">Anthropic</option>
            <option value="gemini">Gemini</option>
          </select>
        </div>
        <div>
          <label class="input-label">绑定分组 (可选)</label>
          <select v-model="onlineForm.groupId" class="input">
            <option :value="null">不绑定分组</option>
            <option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option>
          </select>
        </div>
      </div>

      <div>
        <label class="input-label">Base URL (端点地址)</label>
        <input 
          v-model="onlineForm.baseUrl" 
          type="text" 
          class="input" 
          placeholder="例如: https://api.openai.com/v1"
          list="base-url-history"
        />
        <datalist id="base-url-history">
          <option v-for="url in baseUrlHistory" :key="url" :value="url" />
        </datalist>
      </div>

      <div>
        <label class="input-label">批量粘贴 名称 和 API Key (以空格或Tab分隔，较长者识别为Key)</label>
        <textarea 
          v-model="onlineForm.rawText" 
          rows="5" 
          class="input font-mono text-sm"
          placeholder="jojo-codex 0.28x-WFDdCl    sk-t62AsevLAoVjKRcSXzqqGJSXVfUziFAgCADpk1BESp0vrbvW&#10;jojo-codex 0.28x-A7DMDp    sk-j0e9MwIoPdOUMYzF8jpebBdQMDVmWwa4DQa9GTz4fPuKYWbk"
        ></textarea>
      </div>

      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary text-sm" @click="verifyOnlineData">
          验证解析
        </button>
      </div>

      <div v-if="verifiedItems.length > 0" class="mt-4 border rounded-lg overflow-hidden dark:border-dark-700">
        <div class="bg-gray-50 dark:bg-dark-800 px-4 py-2 border-b dark:border-dark-700 flex justify-between items-center">
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">验证结果 (共 {{ verifiedItems.length }} 条)</span>
        </div>
        <div class="max-h-60 overflow-y-auto p-2 space-y-2">
          <div v-for="(item, idx) in verifiedItems" :key="idx" class="flex items-center gap-2">
            <span class="text-xs text-gray-500 w-6 text-center">{{ idx + 1 }}</span>
            <input v-model="item.name" type="text" class="input flex-1 !py-1 !text-sm" placeholder="名称" />
            <input v-model="item.key" type="text" class="input flex-[2] !py-1 !text-sm font-mono" placeholder="API Key" />
            <button type="button" @click="verifiedItems.splice(idx, 1)" class="p-1 text-red-500 hover:bg-red-50 rounded">
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path></svg>
            </button>
          </div>
        </div>
      </div>

      <div v-if="result" class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700 mt-4">
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.dataImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.dataImportResultSummary', result) }}
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800">
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} — {{ item.message }}
            </div>
          </div>
        </div>
      </div>

    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          v-if="activeTab === 'json'"
          class="btn btn-primary"
          type="submit"
          form="import-data-form"
          :disabled="importing || files.length === 0"
        >
          {{ importing ? t('admin.accounts.dataImporting') : t('admin.accounts.dataImportButton') }}
        </button>
        <button
          v-if="activeTab === 'online'"
          class="btn btn-primary"
          type="submit"
          form="import-online-form"
          :disabled="importing || verifiedItems.length === 0"
        >
          {{ importing ? '正在添加...' : '确认添加' }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminDataImportResult, AdminDataPayload, AdminGroup } from '@/types'

interface Props {
  show: boolean
  groups?: AdminGroup[]
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const activeTab = ref<'json' | 'online'>('json')
const importing = ref(false)
const files = ref<File[]>([])
const dragDepth = ref(0)
const dragActive = computed(() => dragDepth.value > 0)
const hasCreatedData = ref(false)
const result = ref<AdminDataImportResult | null>(null)
const errorItems = computed(() => result.value?.errors || [])

// --- JSON Import Logic ---
const fileInput = ref<HTMLInputElement | null>(null)
const selectedFilesLabel = computed(() => {
  if (files.value.length === 0) return ''
  if (files.value.length === 1) return files.value[0]?.name || ''
  return t('admin.accounts.selectedCount', { count: files.value.length })
})
const fileListTitle = computed(() => files.value.map((item) => item.name).join(', '))

// --- Online Import Logic ---
interface OnlineImportForm {
  platform: AdminDataPayload['accounts'][number]['platform']
  groupId: number | null
  baseUrl: string
  rawText: string
}

const onlineForm = ref<OnlineImportForm>({
  platform: 'antigravity',
  groupId: null,
  baseUrl: '',
  rawText: ''
})
const verifiedItems = ref<{name: string, key: string}[]>([])
const baseUrlHistory = ref<string[]>([])

onMounted(() => {
  const history = localStorage.getItem('import-baseurl-history')
  if (history) {
    try {
      baseUrlHistory.value = JSON.parse(history)
    } catch {
      baseUrlHistory.value = []
    }
  }
})

const saveBaseUrlHistory = (url: string) => {
  if (!url) return
  const set = new Set(baseUrlHistory.value)
  set.add(url)
  baseUrlHistory.value = Array.from(set).slice(-10) // keep last 10
  localStorage.setItem('import-baseurl-history', JSON.stringify(baseUrlHistory.value))
}

const verifyOnlineData = () => {
  if (!onlineForm.value.rawText.trim()) return
  const lines = onlineForm.value.rawText.split('\n')
  const items: {name: string, key: string}[] = []
  
  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed) continue
    
    // Split by whitespace
    const parts = trimmed.split(/\s+/)
    if (parts.length === 0) continue
    if (parts.length === 1) {
      items.push({ name: 'Untitled Account', key: parts[0] })
      continue
    }
    
    // Find longest part as API Key
    let longestIdx = 0
    for (let i = 1; i < parts.length; i++) {
      if (parts[i].length > parts[longestIdx].length) {
        longestIdx = i
      }
    }
    
    const key = parts[longestIdx]
    parts.splice(longestIdx, 1)
    const name = parts.join(' ')
    
    items.push({ name, key })
  }
  
  verifiedItems.value = items
}

watch(
  () => props.show,
  (open) => {
    if (open) {
      activeTab.value = 'json'
      files.value = []
      dragDepth.value = 0
      hasCreatedData.value = false
      result.value = null
      if (fileInput.value) {
        fileInput.value.value = ''
      }
      onlineForm.value.rawText = ''
      verifiedItems.value = []
    }
  }
)

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  setSelectedFiles(target.files)
  target.value = ''
}

const handleClose = () => {
  if (importing.value) return
  if (hasCreatedData.value) {
    hasCreatedData.value = false
    emit('imported')
  }
  emit('close')
}

const isJsonFile = (sourceFile: File) => {
  const name = sourceFile.name.toLowerCase()
  return name.endsWith('.json') || sourceFile.type === 'application/json'
}

const setSelectedFiles = (sourceFiles: FileList | File[] | null | undefined) => {
  if (importing.value) return
  const incoming = Array.from(sourceFiles || [])
  const picked = incoming.filter(isJsonFile)
  if (!picked.length) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }
  if (picked.length < incoming.length) {
    appStore.showWarning(
      t('admin.accounts.dataImportIgnoredFiles', { count: incoming.length - picked.length })
    )
  }
  files.value = picked
  result.value = null
}

const handleDragEnter = () => {
  if (importing.value) return
  dragDepth.value += 1
}

const handleDragLeave = () => {
  dragDepth.value = Math.max(0, dragDepth.value - 1)
}

const handleDrop = (event: DragEvent) => {
  dragDepth.value = 0
  if (importing.value) return
  setSelectedFiles(event.dataTransfer?.files)
}

const readFileAsText = async (sourceFile: File): Promise<string> => {
  if (typeof sourceFile.text === 'function') {
    return sourceFile.text()
  }
  if (typeof sourceFile.arrayBuffer === 'function') {
    const buffer = await sourceFile.arrayBuffer()
    return new TextDecoder().decode(buffer)
  }
  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(sourceFile)
  })
}

const processImportResult = (res: AdminDataImportResult) => {
  result.value = res
  const msgParams: Record<string, unknown> = {
    account_created: res.account_created,
    account_failed: res.account_failed,
    proxy_created: res.proxy_created,
    proxy_reused: res.proxy_reused,
    proxy_failed: res.proxy_failed,
  }
  if (res.account_failed > 0 || res.proxy_failed > 0) {
    if (res.account_created > 0 || res.proxy_created > 0) {
      hasCreatedData.value = true
    }
    appStore.showError(t('admin.accounts.dataImportCompletedWithErrors', msgParams))
  } else {
    appStore.showSuccess(t('admin.accounts.dataImportSuccess', msgParams))
    emit('imported')
  }
}

const SUPPORTED_DATA_TYPES = ['sub2api-data', 'sub2api-bundle']
const SUPPORTED_DATA_VERSION = 1

// 与后端 validateDataHeader 对齐:合并前逐文件校验,避免坏文件混入合并 payload 后
// 报错无法定位来源,或绕过后端本会对单文件做的 type/version 检查。
const isValidDataPayload = (payload: unknown): payload is AdminDataPayload => {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return false
  const candidate = payload as Record<string, unknown>
  if (
    candidate.type !== undefined &&
    candidate.type !== '' &&
    !SUPPORTED_DATA_TYPES.includes(candidate.type as string)
  ) {
    return false
  }
  if (
    candidate.version !== undefined &&
    candidate.version !== 0 &&
    candidate.version !== SUPPORTED_DATA_VERSION
  ) {
    return false
  }
  return Array.isArray(candidate.proxies) && Array.isArray(candidate.accounts)
}

const mergeDataPayloads = (payloads: AdminDataPayload[]): AdminDataPayload => {
  const [firstPayload] = payloads
  if (payloads.length === 1 && firstPayload) return firstPayload

  return {
    type: payloads.find((item) => typeof item.type === 'string')?.type,
    version: payloads.find((item) => typeof item.version === 'number')?.version,
    exported_at: new Date().toISOString(),
    proxies: payloads.flatMap((item) => item.proxies),
    accounts: payloads.flatMap((item) => item.accounts),
    skipped_shadows: payloads.reduce((sum, item) => {
      const count = Number(item.skipped_shadows || 0)
      return Number.isFinite(count) ? sum + count : sum
    }, 0)
  }
}

const handleJsonImport = async () => {
  if (files.value.length === 0) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }

  importing.value = true
  try {
    const dataPayloads: AdminDataPayload[] = []
    for (const sourceFile of files.value) {
      let parsed: unknown
      try {
        parsed = JSON.parse(await readFileAsText(sourceFile))
      } catch {
        appStore.showError(
          t('admin.accounts.dataImportParseFailedFile', { name: sourceFile.name })
        )
        return
      }
      if (!isValidDataPayload(parsed)) {
        appStore.showError(t('admin.accounts.dataImportInvalidFile', { name: sourceFile.name }))
        return
      }
      dataPayloads.push(parsed)
    }
    const dataPayload = mergeDataPayloads(dataPayloads)

    const res = await adminAPI.accounts.importData({
      data: dataPayload,
      skip_default_group_bind: true
    })
    processImportResult(res)
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.dataImportFailed'))
  } finally {
    importing.value = false
  }
}

const handleOnlineImport = async () => {
  if (verifiedItems.value.length === 0) return
  
  if (onlineForm.value.baseUrl) {
    saveBaseUrlHistory(onlineForm.value.baseUrl)
  }

  importing.value = true
  try {
    const accounts: AdminDataPayload['accounts'] = verifiedItems.value.map(item => ({
      name: item.name,
      platform: onlineForm.value.platform,
      type: 'apikey',
      concurrency: 1,
      priority: 1,
      credentials: {
        api_key: item.key,
        base_url: onlineForm.value.baseUrl.trim() || undefined
      }
    }))

    const payload: AdminDataPayload = {
      type: 'sub2api-data',
      version: 1,
      exported_at: new Date().toISOString(),
      proxies: [],
      accounts
    }

    const reqData: Parameters<typeof adminAPI.accounts.importData>[0] = {
      data: payload,
      skip_default_group_bind: true
    }
    
    if (onlineForm.value.groupId) {
      reqData.group_ids = [onlineForm.value.groupId]
    }

    const res = await adminAPI.accounts.importData(reqData)
    processImportResult(res)
    
    // Clear items on complete success
    if (res.account_failed === 0) {
      verifiedItems.value = []
      onlineForm.value.rawText = ''
    }
  } catch (error: any) {
    appStore.showError(error?.message || '导入失败')
  } finally {
    importing.value = false
  }
}
</script>
