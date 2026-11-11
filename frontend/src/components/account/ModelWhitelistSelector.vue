<template>
  <div>
    <!-- Multi-select Dropdown -->
    <div ref="containerRef" class="relative mb-3">
      <div
        ref="triggerRef"
        @click="toggleDropdown"
        class="cursor-pointer rounded-lg border border-gray-300 bg-white px-3 py-2 dark:border-dark-500 dark:bg-dark-700"
      >
        <div class="grid grid-cols-2 gap-1.5">
          <span
            v-for="model in modelValue"
            :key="model"
            class="inline-flex items-center justify-between gap-1 rounded bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-600 dark:text-gray-300"
          >
            <span class="flex items-center gap-1 truncate">
              <ModelIcon :model="model" size="14px" />
              <span class="truncate">{{ model }}</span>
            </span>
            <button
              type="button"
              @click.stop="removeModel(model)"
              class="shrink-0 rounded-full hover:bg-gray-200 dark:hover:bg-dark-500"
            >
              <Icon name="x" size="xs" class="h-3.5 w-3.5" :stroke-width="2" />
            </button>
          </span>
        </div>
        <div class="mt-2 flex items-center justify-between border-t border-gray-200 pt-2 dark:border-dark-600">
          <span class="text-xs text-gray-400">{{ t('admin.accounts.modelCount', { count: modelValue.length }) }}</span>
          <svg class="h-5 w-5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </div>
      <!-- Dropdown List -->
      <Teleport to="body">
        <div
          v-if="showDropdown"
          ref="dropdownRef"
          data-testid="model-dropdown"
          class="model-whitelist-dropdown flex flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-lg dark:border-dark-600 dark:bg-dark-700"
          :style="dropdownStyle"
        >
          <div class="shrink-0 border-b border-gray-200 bg-white p-2 dark:border-dark-600 dark:bg-dark-700">
            <input
              v-model="searchQuery"
              type="text"
              class="input w-full text-sm"
              :placeholder="t('admin.accounts.searchModels')"
              @click.stop
            />
          </div>
          <div class="min-h-0 max-h-52 overflow-auto">
            <div
              v-for="model in filteredModels"
              :key="model.value"
              data-testid="model-option"
              class="group flex items-center hover:bg-gray-100 dark:hover:bg-dark-600"
            >
              <button
                type="button"
                data-testid="select-model"
                class="flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left text-sm"
                @click="toggleModel(model.value)"
              >
                <span
                  :class="[
                    'flex h-4 w-4 shrink-0 items-center justify-center rounded border',
                    modelValue.includes(model.value)
                      ? 'border-primary-500 bg-primary-500 text-white'
                      : 'border-gray-300 dark:border-dark-500'
                  ]"
                >
                  <svg v-if="modelValue.includes(model.value)" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="3" d="M5 13l4 4L19 7" />
                  </svg>
                </span>
                <ModelIcon :model="model.value" size="18px" />
                <span class="truncate text-gray-900 dark:text-white">{{ model.value }}</span>
              </button>
              <button
                type="button"
                data-testid="copy-model-id"
                class="mr-2 rounded p-1.5 text-gray-400 opacity-70 transition-colors hover:bg-gray-200 hover:text-primary-600 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 group-hover:opacity-100 dark:text-gray-500 dark:hover:bg-dark-500 dark:hover:text-primary-400"
                :title="`${t('common.copy')} ${model.value}`"
                :aria-label="`${t('common.copy')} ${model.value}`"
                @click="copyModelId(model.value)"
              >
                <Icon name="copy" size="sm" />
              </button>
            </div>
            <div v-if="accountModelsLoading" class="px-3 py-4 text-center text-sm text-gray-500" role="status">
              {{ t('common.loading') }}
            </div>
            <div v-else-if="accountModelsError" class="flex items-center gap-2 px-3 py-4 text-sm">
              <span role="alert" class="min-w-0 break-words text-red-500">{{ accountModelsError }}</span>
              <button type="button" data-testid="retry-account-models" class="flex shrink-0 items-center gap-1 text-primary-600" @click="loadAvailableAccountModels(true)">
                <Icon name="refresh" size="sm" />{{ t('common.retry') }}
              </button>
            </div>
            <div v-else-if="filteredModels.length === 0" class="px-3 py-4 text-center text-sm text-gray-500">
              {{ t('admin.accounts.noMatchingModels') }}
            </div>
          </div>
        </div>
      </Teleport>
    </div>

    <!-- Quick Actions -->
    <div class="mb-4 flex flex-wrap gap-2">
      <button
        type="button"
        @click="fillRelated"
        :disabled="accountModelsLoading"
        class="rounded-lg border border-blue-200 px-3 py-1.5 text-sm text-blue-600 hover:bg-blue-50 dark:border-blue-800 dark:text-blue-400 dark:hover:bg-blue-900/30"
      >
        {{ t('admin.accounts.fillRelatedModels') }}
      </button>
      <button
        v-if="canSyncUpstream"
        type="button"
        @click="syncUpstreamModels"
        :disabled="isSyncingUpstream || accountModelsLoading"
        class="rounded-lg border border-emerald-200 px-3 py-1.5 text-sm text-emerald-600 hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-emerald-800 dark:text-emerald-400 dark:hover:bg-emerald-900/30"
      >
        {{ isSyncingUpstream ? t('admin.accounts.syncUpstreamModelsLoading') : t('admin.accounts.syncUpstreamModels') }}
      </button>
      <button
        type="button"
        @click="clearAll"
        class="rounded-lg border border-red-200 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/30"
      >
        {{ t('admin.accounts.clearAllModels') }}
      </button>
    </div>

    <!-- Custom Model Input -->
    <div class="mb-3">
      <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.accounts.customModelName') }}</label>
      <div class="flex gap-2">
        <input
          v-model="customModel"
          type="text"
          class="input flex-1"
          :placeholder="t('admin.accounts.enterCustomModelName')"
          @keydown.enter.prevent="handleEnter"
          @compositionstart="isComposing = true"
          @compositionend="isComposing = false"
        />
        <button
          type="button"
          @click="addCustom"
          class="rounded-lg bg-primary-50 px-4 py-2 text-sm font-medium text-primary-600 hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-400 dark:hover:bg-primary-900/50"
        >
          {{ t('admin.accounts.addModel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { accountsAPI } from '@/api/admin/accounts'
import type { SyncUpstreamModelsResult, SyncUpstreamPreviewParams } from '@/api/admin/accounts'
import { useClipboard } from '@/composables/useClipboard'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  allModels,
  getModelsByPlatform,
  normalizeAntigravityModelsForDisplay
} from '@/composables/useModelWhitelist'

const { t } = useI18n()

const props = defineProps<{
  modelValue: string[]
  platform?: string
  platforms?: string[]
  accountId?: number
  syncCredentials?: {
    platform: string
    type: string
    base_url?: string
    api_key: string
  }
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
}>()

const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const showDropdown = ref(false)
const searchQuery = ref('')
const customModel = ref('')
const isComposing = ref(false)
const isSyncingUpstream = ref(false)
const containerRef = ref<HTMLElement | null>(null)
const triggerRef = ref<HTMLElement | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const dropdownStyle = ref<Record<string, string>>({})

const DROPDOWN_GAP = 4
const VIEWPORT_PADDING = 8
const PREFERRED_DROPDOWN_HEIGHT = 264

const updateDropdownPosition = () => {
  const trigger = triggerRef.value
  if (!trigger) return

  const rect = trigger.getBoundingClientRect()
  const viewport = window.visualViewport
  const viewportTop = viewport?.offsetTop ?? 0
  const viewportLeft = viewport?.offsetLeft ?? 0
  const viewportWidth = viewport?.width ?? window.innerWidth
  const viewportHeight = viewport?.height ?? window.innerHeight
  const viewportRight = viewportLeft + viewportWidth
  const viewportBottom = viewportTop + viewportHeight
  const availableWidth = Math.max(0, viewportWidth - VIEWPORT_PADDING * 2)
  const width = Math.min(Math.max(0, rect.width), availableWidth)
  const left = Math.max(
    viewportLeft + VIEWPORT_PADDING,
    Math.min(rect.left, viewportRight - VIEWPORT_PADDING - width)
  )
  const spaceBelow = Math.max(
    0,
    viewportBottom - VIEWPORT_PADDING - rect.bottom - DROPDOWN_GAP
  )
  const spaceAbove = Math.max(
    0,
    rect.top - viewportTop - VIEWPORT_PADDING - DROPDOWN_GAP
  )
  const openAbove = spaceBelow < PREFERRED_DROPDOWN_HEIGHT && spaceAbove > spaceBelow
  const maxHeight = openAbove ? spaceAbove : spaceBelow

  dropdownStyle.value = {
    position: 'fixed',
    left: `${Math.round(left)}px`,
    top: `${Math.round(openAbove ? rect.top - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP)}px`,
    width: `${Math.round(width)}px`,
    maxHeight: `${Math.floor(maxHeight)}px`,
    transform: openAbove ? 'translateY(-100%)' : 'none',
    zIndex: '100000020'
  }
}

const startPositionTracking = () => {
  window.addEventListener('scroll', updateDropdownPosition, true)
  window.addEventListener('resize', updateDropdownPosition)
  window.visualViewport?.addEventListener('scroll', updateDropdownPosition)
  window.visualViewport?.addEventListener('resize', updateDropdownPosition)
}

const stopPositionTracking = () => {
  window.removeEventListener('scroll', updateDropdownPosition, true)
  window.removeEventListener('resize', updateDropdownPosition)
  window.visualViewport?.removeEventListener('scroll', updateDropdownPosition)
  window.visualViewport?.removeEventListener('resize', updateDropdownPosition)
}

const closeDropdown = () => {
  showDropdown.value = false
  searchQuery.value = ''
  stopPositionTracking()
}

const normalizedPlatforms = computed(() => {
  const rawPlatforms =
    props.platforms && props.platforms.length > 0
      ? props.platforms
      : props.platform
        ? [props.platform]
        : []

  return Array.from(
    new Set(
      rawPlatforms
        .map(platform => platform?.trim())
        .filter((platform): platform is string => Boolean(platform))
    )
  )
})

const upstreamSyncPlatforms = new Set(['anthropic', 'openai', 'gemini', 'antigravity', 'grok'])
const canSyncUpstream = computed(() => {
  if (props.accountId) {
    if (normalizedPlatforms.value.length === 0) return true
    return normalizedPlatforms.value.some(platform => upstreamSyncPlatforms.has(platform.toLowerCase()))
  }
  if (props.syncCredentials) {
    return upstreamSyncPlatforms.has(props.syncCredentials.platform.toLowerCase())
  }
  return false
})

const isAntigravitySync = computed(() =>
  normalizedPlatforms.value.some(platform => platform.toLowerCase() === 'antigravity') ||
  props.syncCredentials?.platform.toLowerCase() === 'antigravity'
)

const scopedAccountId = computed(() => props.accountId && normalizedPlatforms.value.length === 1 &&
  normalizedPlatforms.value[0].toLowerCase() === 'antigravity' ? props.accountId : null)
const localAccountModels = ref<string[] | null>(null)
const localAccountModelsLoading = ref(false)
const localAccountModelsError = ref('')
let accountModelsGeneration = 0
let accountModelsRequest: Promise<string[] | null> | null = null
const availableAccountModels = computed(() => {
  if (!scopedAccountId.value) return null
  return localAccountModels.value
})
const accountModelsLoading = computed(() => !!scopedAccountId.value && localAccountModelsLoading.value)
const accountModelsError = computed(() => scopedAccountId.value ? localAccountModelsError.value : '')

function loadAvailableAccountModels(refresh = false): Promise<string[] | null> {
  const id = scopedAccountId.value
  if (!id) return Promise.resolve(null)
  if (accountModelsRequest) return accountModelsRequest
  if (!refresh && localAccountModels.value !== null) return Promise.resolve(localAccountModels.value)

  const generation = accountModelsGeneration
  const isCurrent = () => generation === accountModelsGeneration && scopedAccountId.value === id
  localAccountModels.value = null
  localAccountModelsLoading.value = true
  localAccountModelsError.value = ''
  const request = (async () => {
    try {
      const result = await accountsAPI.syncUpstreamModels(id)
      if (!isCurrent()) return null
      const ids = normalizeAntigravityModelsForDisplay(result.models)
      localAccountModels.value = ids
      showCatalogWarnings(result.warnings)
      return ids
    } catch (error) {
      if (!isCurrent()) return null
      localAccountModelsError.value = error && typeof error === 'object' && 'message' in error && typeof error.message === 'string'
        ? error.message : t('admin.accounts.syncUpstreamModelsFailed')
      appStore.showError(localAccountModelsError.value)
      return null
    } finally {
      if (isCurrent()) {
        localAccountModelsLoading.value = false
        accountModelsRequest = null
      }
    }
  })()
  accountModelsRequest = request
  return request
}

const availableOptions = computed(() => {
  if (scopedAccountId.value) {
    return (availableAccountModels.value || []).map(model => ({ value: model, label: model }))
  }
  if (normalizedPlatforms.value.length === 0) {
    return allModels
  }

  const allowedModels = new Set<string>()
  for (const platform of normalizedPlatforms.value) {
    for (const model of getModelsByPlatform(platform)) {
      allowedModels.add(model)
    }
  }

  return allModels.filter(model => allowedModels.has(model.value))
})

const filteredModels = computed(() => {
  const query = searchQuery.value.toLowerCase().trim()
  if (!query) return availableOptions.value
  return availableOptions.value.filter(
    m => m.value.toLowerCase().includes(query) || m.label.toLowerCase().includes(query)
  )
})

const toggleDropdown = () => {
  if (showDropdown.value) {
    closeDropdown()
    return
  }

  updateDropdownPosition()
  showDropdown.value = true
  if (scopedAccountId.value) void loadAvailableAccountModels()
  startPositionTracking()
  nextTick(updateDropdownPosition)
}

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as Node
  if (!containerRef.value?.contains(target) && !dropdownRef.value?.contains(target)) {
    closeDropdown()
  }
}

const handleEscape = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && showDropdown.value) {
    closeDropdown()
  }
}

const removeModel = (model: string) => {
  emit('update:modelValue', props.modelValue.filter(m => m !== model))
}

const toggleModel = (model: string) => {
  if (props.modelValue.includes(model)) {
    removeModel(model)
  } else {
    if (scopedAccountId.value && !availableAccountModels.value?.includes(model)) return
    emit('update:modelValue', [...props.modelValue, model])
  }
}

const copyModelId = async (model: string) => {
  await copyToClipboard(model)
}

const addCustom = () => {
  const model = customModel.value.trim()
  if (!model) return
  if (props.modelValue.includes(model)) {
    appStore.showInfo(t('admin.accounts.modelExists'))
    return
  }
  emit('update:modelValue', [...props.modelValue, model])
  customModel.value = ''
}

const handleEnter = () => {
  if (!isComposing.value) addCustom()
}

const fillRelated = async () => {
  if (scopedAccountId.value) {
    const generation = accountModelsGeneration
    const models = await loadAvailableAccountModels()
    if (!models || generation !== accountModelsGeneration) return
    emit('update:modelValue', [...new Set([...props.modelValue, ...models])])
    return
  }
  const newModels = [...props.modelValue]
  for (const platform of normalizedPlatforms.value) {
    for (const model of getModelsByPlatform(platform)) {
      if (!newModels.includes(model)) {
        newModels.push(model)
      }
    }
  }
  emit('update:modelValue', newModels)
}

const syncUpstreamModels = async () => {
  if (isSyncingUpstream.value) return
  if (!props.accountId && !props.syncCredentials) return

  const generation = accountModelsGeneration
  const id = props.accountId
  isSyncingUpstream.value = true
  try {
    let result
    if (scopedAccountId.value) {
      const models = await loadAvailableAccountModels(true)
      if (!models || generation !== accountModelsGeneration) return
      result = { models }
    } else if (props.accountId) {
      result = await accountsAPI.syncUpstreamModels(props.accountId)
    } else if (props.syncCredentials) {
      result = await accountsAPI.syncUpstreamModelsPreview(props.syncCredentials as SyncUpstreamPreviewParams)
    } else {
      return
    }
    if (generation !== accountModelsGeneration || props.accountId !== id) return
    // Account-scoped loads already report warnings once for all coalesced callers.
    if (!scopedAccountId.value) showCatalogWarnings(result.warnings)

    const rawUpstreamModels = result.models.map(model => model.trim()).filter(Boolean)
    const upstreamModels = isAntigravitySync.value && !scopedAccountId.value
      ? normalizeAntigravityModelsForDisplay(rawUpstreamModels)
      : rawUpstreamModels
    if (upstreamModels.length === 0) {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsEmpty'))
      return
    }

    const newModels = [...props.modelValue]
    let addedCount = 0
    for (const model of upstreamModels) {
      if (!newModels.includes(model)) {
        newModels.push(model)
        addedCount += 1
      }
    }

    emit('update:modelValue', newModels)
    if (addedCount > 0) {
      appStore.showSuccess(t('admin.accounts.syncUpstreamModelsSuccess', { count: addedCount, total: upstreamModels.length }))
    } else {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsNoChanges', { count: upstreamModels.length }))
    }
  } catch (error) {
    if (generation !== accountModelsGeneration || props.accountId !== id) return
    const message = error instanceof Error ? error.message : t('admin.accounts.syncUpstreamModelsFailed')
    appStore.showError(t('admin.accounts.syncUpstreamModelsError', { message }))
  } finally {
    if (generation === accountModelsGeneration) isSyncingUpstream.value = false
  }
}

function showCatalogWarnings(warnings: SyncUpstreamModelsResult['warnings']) {
  const warningKeys: Record<string, string> = {
    upstream_model_list_mapping_fallback: 'admin.accounts.syncUpstreamModelsMappingFallback',
    upstream_model_metadata_partial: 'admin.accounts.syncUpstreamModelsMetadataPartial',
    upstream_model_metadata_incomplete: 'admin.accounts.syncUpstreamModelsMetadataIncomplete',
    upstream_model_metadata_too_large: 'admin.accounts.syncUpstreamModelsMetadataTooLarge'
  }
  for (const warning of warnings ?? []) {
    const message = warningKeys[warning.code] ? t(warningKeys[warning.code]) : warning.message
    if (message) appStore.showWarning(message)
  }
}

const clearAll = () => {
  emit('update:modelValue', [])
}

function invalidateAccountModels() {
  accountModelsGeneration += 1
  accountModelsRequest = null
  localAccountModels.value = null
  localAccountModelsLoading.value = false
  localAccountModelsError.value = ''
  isSyncingUpstream.value = false
}

watch([() => props.accountId, scopedAccountId], () => {
  invalidateAccountModels()
  if (showDropdown.value && scopedAccountId.value) void loadAvailableAccountModels()
})

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleEscape)
})

onUnmounted(() => {
  invalidateAccountModels()
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleEscape)
  stopPositionTracking()
})

</script>
