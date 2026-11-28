<template>
  <section class="space-y-4" :aria-busy="loading" data-testid="visitor-records">
    <div class="flex flex-wrap items-end gap-3">
      <div class="min-w-0 flex-1 basis-full sm:basis-64">
        <label for="visitor-search" class="input-label">{{ t('admin.settings.visitors.search') }}</label>
        <div class="relative">
          <Icon name="search" size="md" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            id="visitor-search"
            v-model.trim="filters.q"
            type="search"
            class="input pl-10"
            :placeholder="t('admin.settings.visitors.searchPlaceholder')"
            @keydown.enter.prevent="search"
          />
        </div>
      </div>
      <div class="min-w-0 w-full sm:w-auto">
        <label for="visitor-start-time" class="input-label">{{ t('admin.settings.visitors.startTime') }}</label>
        <input
          id="visitor-start-time"
          v-model="filters.start"
          type="datetime-local"
          class="input"
          :aria-invalid="invalidTimeRange"
          :aria-describedby="invalidTimeRange ? 'visitor-time-error' : undefined"
          @keydown.enter.prevent="search"
        />
      </div>
      <div class="min-w-0 w-full sm:w-auto">
        <label for="visitor-end-time" class="input-label">{{ t('admin.settings.visitors.endTime') }}</label>
        <input
          id="visitor-end-time"
          v-model="filters.end"
          type="datetime-local"
          class="input"
          :aria-invalid="invalidTimeRange"
          :aria-describedby="invalidTimeRange ? 'visitor-time-error' : undefined"
          @keydown.enter.prevent="search"
        />
      </div>
      <div class="flex items-center gap-2">
        <button type="button" class="btn btn-primary" :disabled="loading || invalidTimeRange" @click="search">
          <Icon name="search" size="sm" class="mr-1.5" />
          {{ t('common.search') }}
        </button>
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="loading"
          :aria-label="t('common.refresh')"
          :title="t('common.refresh')"
          @click="loadRecords"
        >
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
    </div>

    <p v-if="invalidTimeRange" id="visitor-time-error" role="alert" class="text-sm text-red-600 dark:text-red-400">
      {{ t('admin.settings.visitors.invalidTimeRange') }}
    </p>

    <div v-if="loadFailed" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
      <span>{{ t('admin.settings.visitors.loadFailed') }}</span>
      <button type="button" class="btn btn-secondary btn-sm" @click="loadRecords">
        <Icon name="refresh" size="sm" class="mr-1.5" />
        {{ t('common.retry') }}
      </button>
    </div>

    <div v-else>
      <p v-if="loading" role="status" class="mb-3 text-sm text-gray-500 dark:text-gray-400">
        {{ t('common.loading') }}
      </p>
      <DataTable :columns="columns" :data="records" :loading="loading" row-key="id">
        <template #cell-created_at="{ value }">
          <time :datetime="value" class="whitespace-nowrap text-gray-600 dark:text-gray-300">
            {{ formatDateTime(value, undefined, locale) || '-' }}
          </time>
        </template>
        <template #cell-visitor="{ row }">
          <div class="max-w-xs whitespace-normal break-all font-medium text-gray-900 dark:text-white">
            {{ row.actor_email || row.client_ip || '-' }}
          </div>
          <div v-if="row.actor_email && row.client_ip" class="mt-1 max-w-xs whitespace-normal break-all text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.visitors.clientIp') }} {{ row.client_ip }}
          </div>
        </template>
        <template #cell-path="{ value }">
          <span class="block max-w-sm whitespace-normal break-all font-mono text-sm text-gray-700 dark:text-gray-300">{{ value || '-' }}</span>
        </template>
        <template #empty>
          <p class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.visitors.empty') }}
          </p>
        </template>
      </DataTable>
      <fieldset v-if="total > 0" :disabled="loading" class="min-w-0">
        <Pagination
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="changePage"
          @update:pageSize="changePageSize"
        />
      </fieldset>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { auditAPI, type AuditLog, type AuditLogQuery } from '@/api/admin/audit'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import type { Column } from '@/components/common/types'
import Icon from '@/components/icons/Icon.vue'
import { getPersistedPageSize, setPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatDateTime, parseDateTimeLocalInput } from '@/utils/format'

const { t, locale } = useI18n()
const filters = reactive({ q: '', start: '', end: '' })
const appliedFilters = ref<AuditLogQuery>({})
const records = ref<AuditLog[]>([])
const page = ref(1)
const pageSize = ref(getPersistedPageSize())
const total = ref(0)
const loading = ref(false)
const loadFailed = ref(false)
let requestId = 0

const startTime = computed(() => parseDateTimeLocalInput(filters.start))
const endTime = computed(() => parseDateTimeLocalInput(filters.end))
const invalidTimeRange = computed(() =>
  (filters.start !== '' && startTime.value === null) ||
  (filters.end !== '' && endTime.value === null) ||
  (startTime.value !== null && endTime.value !== null && startTime.value > endTime.value)
)
const columns = computed<Column[]>(() => [
  { key: 'created_at', label: t('admin.settings.visitors.visitTime') },
  { key: 'visitor', label: t('admin.settings.visitors.visitor') },
  { key: 'path', label: t('admin.settings.visitors.page') }
])

async function loadRecords() {
  const currentRequest = ++requestId
  loading.value = true
  loadFailed.value = false
  try {
    const response = await auditAPI.list({
      ...appliedFilters.value,
      action: 'site.visit',
      page: page.value,
      page_size: pageSize.value
    })
    if (currentRequest !== requestId) return

    const lastPage = Math.max(1, Math.ceil(response.total / pageSize.value))
    if (page.value > lastPage) {
      page.value = lastPage
      await loadRecords()
      return
    }
    records.value = response.items
    total.value = response.total
  } catch {
    if (currentRequest === requestId) loadFailed.value = true
  } finally {
    if (currentRequest === requestId) loading.value = false
  }
}

function search() {
  if (invalidTimeRange.value) return
  appliedFilters.value = {
    q: filters.q || undefined,
    start_time: startTime.value === null ? undefined : new Date(startTime.value * 1000).toISOString(),
    end_time: endTime.value === null ? undefined : new Date(endTime.value * 1000).toISOString()
  }
  page.value = 1
  void loadRecords()
}

function changePage(value: number) {
  page.value = value
  void loadRecords()
}

function changePageSize(value: number) {
  pageSize.value = value
  setPersistedPageSize(value)
  page.value = 1
  void loadRecords()
}

onMounted(loadRecords)
onBeforeUnmount(() => { requestId++ })
</script>
