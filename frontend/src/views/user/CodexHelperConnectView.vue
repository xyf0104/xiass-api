<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-3xl px-4 py-8 sm:px-6">
      <div class="mb-6">
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
          {{ t('codexHelper.title') }}
        </h1>
        <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">
          {{ t('codexHelper.description') }}
        </p>
      </div>

      <div
        v-if="errorMessage"
        class="mb-5 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ errorMessage }}
      </div>

      <div
        v-if="loading"
        class="flex min-h-48 items-center justify-center text-gray-500 dark:text-gray-400"
      >
        <Icon name="refresh" size="lg" class="mr-2 animate-spin" />
        {{ t('common.loading') }}
      </div>

      <div v-else-if="connection && compatibleKeys.length" class="space-y-3">
        <div
          v-for="key in compatibleKeys"
          :key="key.id"
          class="flex flex-col gap-4 rounded-lg border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-600 dark:bg-dark-800 sm:flex-row sm:items-center sm:justify-between"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span>
              <span class="badge badge-success">{{ t('common.active') }}</span>
            </div>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ key.group?.name }} · <code>{{ maskApiKey(key.key) }}</code>
            </p>
          </div>
          <button type="button" class="btn btn-primary flex-shrink-0" @click="connectKey(key)">
            <Icon name="check" size="sm" class="mr-2" />
            {{ t('codexHelper.useThisKey') }}
          </button>
        </div>
      </div>

      <div
        v-else-if="!loading && !errorMessage"
        class="rounded-lg border border-gray-200 bg-white p-6 text-center dark:border-dark-600 dark:bg-dark-800"
      >
        <p class="font-medium text-gray-900 dark:text-white">{{ t('codexHelper.noKeys') }}</p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('codexHelper.noKeysHint') }}</p>
        <RouterLink to="/keys" class="btn btn-primary mt-4 inline-flex">
          {{ t('codexHelper.manageKeys') }}
        </RouterLink>
      </div>

      <div class="mt-6 rounded-lg border border-blue-100 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
        {{ t('codexHelper.securityNote') }}
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { authAPI, keysAPI } from '@/api'
import type { ApiKey } from '@/types'
import { maskApiKey } from '@/utils/maskApiKey'
import {
  buildCodexHelperCallback,
  isCodexCompatibleKey,
  parseCodexHelperConnection,
  type CodexHelperConnection
} from '@/utils/codexHelper'

const route = useRoute()
const { t } = useI18n()
const loading = ref(true)
const errorMessage = ref('')
const compatibleKeys = ref<ApiKey[]>([])
const connection = ref<CodexHelperConnection | null>(null)
const apiBaseUrl = ref('')

function queryString(value: unknown): string | undefined {
  if (typeof value === 'string') return value
  if (Array.isArray(value) && typeof value[0] === 'string') return value[0]
  return undefined
}

async function loadKeys(): Promise<ApiKey[]> {
  const items: ApiKey[] = []
  let page = 1
  let pages = 1
  do {
    const response = await keysAPI.list(page, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
    items.push(...response.items)
    pages = Math.min(response.pages || 1, 20)
    page += 1
  } while (page <= pages)
  return items
}

async function initialize() {
  try {
    connection.value = parseCodexHelperConnection(
      queryString(route.query.callback),
      queryString(route.query.state)
    )
    const [settings, keys] = await Promise.all([authAPI.getPublicSettings(), loadKeys()])
    apiBaseUrl.value = settings.api_base_url || window.location.origin
    compatibleKeys.value = keys.filter(isCodexCompatibleKey)
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : t('codexHelper.loadFailed')
  } finally {
    loading.value = false
  }
}

function connectKey(key: ApiKey) {
  if (!connection.value) return
  try {
    window.location.assign(buildCodexHelperCallback(connection.value, apiBaseUrl.value, key))
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : t('codexHelper.connectFailed')
  }
}

onMounted(initialize)
</script>
