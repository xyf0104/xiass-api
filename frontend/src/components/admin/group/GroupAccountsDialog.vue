<template>
  <BaseDialog
    :show="show"
    :title="text('admin.groups.groupAccounts.title', '分组账号')"
    width="wide"
    @close="emit('close')"
  >
    <div v-if="group" class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-2 rounded-md bg-gray-50 px-4 py-3 text-sm dark:bg-dark-700">
        <div class="flex min-w-0 items-center gap-2">
          <span class="truncate font-medium text-gray-900 dark:text-white">{{ group.name }}</span>
          <span class="shrink-0 text-gray-400 dark:text-gray-500">#{{ group.id }}</span>
        </div>
        <span class="text-gray-500 dark:text-gray-400">
          {{ text('admin.groups.groupAccounts.selected', '已选') }}
          <strong class="text-gray-900 dark:text-white">{{ selectedAccountIds.length }}</strong>
          {{ text('admin.groups.accountsUnit', '个账号') }}
          <span class="mx-1">·</span>
          {{ text('admin.groups.groupAccounts.schedulable', '当前可调度') }}
          <strong class="text-emerald-600 dark:text-emerald-400">{{ selectedSchedulableCount }}</strong>
        </span>
      </div>

      <div class="flex flex-col gap-3 sm:flex-row sm:items-center">
        <label class="relative min-w-0 flex-1">
          <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            v-model="searchQuery"
            data-test="group-accounts-search"
            type="search"
            class="input w-full pl-9"
            :placeholder="text('admin.groups.groupAccounts.searchPlaceholder', '搜索账号名称或 ID...')"
            :disabled="loading"
            @keydown.enter.prevent="applySearchNow"
          />
        </label>
        <label class="inline-flex shrink-0 cursor-pointer items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
          <input
            ref="selectVisibleInput"
            data-test="group-accounts-select-visible"
            type="checkbox"
            class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700"
            :checked="allVisibleSelected"
            :disabled="loading || saving || visibleAccounts.length === 0"
            @change="toggleVisibleAccounts"
          />
          {{ text('admin.groups.groupAccounts.selectVisible', '全选当前列表') }}
        </label>
      </div>

      <div v-if="riskAccountIds.length > 0" class="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2.5 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200" data-test="group-accounts-mixed-risk">
        <Icon name="exclamationCircle" size="sm" class="mt-0.5 shrink-0" />
        <span>{{ text('admin.groups.groupAccounts.mixedRisk', '这些账号可能让同一渠道混用不同平台。再次点击保存表示确认继续。') }}</span>
      </div>

      <div v-if="loading" class="flex justify-center py-10">
        <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
      </div>
      <div v-else-if="visibleAccounts.length === 0" class="py-12 text-center text-sm text-gray-400 dark:text-gray-500">
        {{ searchQuery.trim() ? text('admin.groups.groupAccounts.noResults', '没有找到匹配账号') : text('admin.groups.groupAccounts.noAccounts', '没有可配置的账号') }}
      </div>
      <div v-else class="overflow-hidden rounded-md border border-gray-200 dark:border-dark-600">
        <div class="max-h-[min(56vh,520px)] overflow-auto">
          <table class="w-full min-w-[560px] text-sm">
            <thead class="sticky top-0 z-[1] bg-gray-50 dark:bg-dark-700">
              <tr class="border-b border-gray-200 dark:border-dark-600">
                <th class="w-12 px-3 py-2 text-center">
                  <span class="sr-only">{{ text('common.select', '选择') }}</span>
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ text('admin.groups.groupAccounts.account', '账号') }}
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ text('admin.groups.groupAccounts.platform', '平台 / 类型') }}
                </th>
                <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ text('admin.groups.groupAccounts.status', '可调度状态') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr
                v-for="account in visibleAccounts"
                :key="account.id"
                class="transition-colors hover:bg-gray-50 dark:hover:bg-dark-700/50"
                :class="isSelected(account.id) ? 'bg-primary-50/40 dark:bg-primary-900/10' : ''"
              >
                <td class="px-3 py-2.5 text-center">
                  <input
                    type="checkbox"
                    :data-test="`group-account-${account.id}`"
                    :checked="isSelected(account.id)"
                    :disabled="saving"
                    class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700"
                    @change="toggleAccount(account.id)"
                  />
                </td>
                <td class="min-w-0 px-3 py-2.5">
                  <div class="flex min-w-0 items-center gap-2">
                    <span class="truncate font-medium text-gray-900 dark:text-white" :title="account.name">{{ account.name }}</span>
                    <span class="shrink-0 font-mono text-xs text-gray-400 dark:text-gray-500">#{{ account.id }}</span>
                    <span v-if="isSelected(account.id)" class="shrink-0 rounded bg-primary-50 px-1.5 py-0.5 text-[11px] font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
                      {{ text('admin.groups.groupAccounts.selectedTag', '已选') }}
                    </span>
                  </div>
                </td>
                <td class="whitespace-nowrap px-3 py-2.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ text(`admin.groups.platforms.${account.platform}`, account.platform) }}
                  <span class="mx-1 text-gray-300 dark:text-dark-500">·</span>
                  {{ account.type }}
                </td>
                <td class="px-3 py-2.5 text-right">
                  <span
                    class="inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium"
                    :class="isSchedulable(account)
                      ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/25 dark:text-emerald-300'
                      : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'"
                  >
                    {{ isSchedulable(account) ? text('admin.groups.groupAccounts.ready', '可调度') : text('admin.groups.groupAccounts.unavailable', '暂不可调度') }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="candidateLoading" class="flex justify-center border-t border-gray-100 py-3 dark:border-dark-600">
          <Icon name="refresh" size="sm" class="animate-spin text-primary-500" />
        </div>
        <div v-else-if="canLoadMore" class="border-t border-gray-100 p-2 text-center dark:border-dark-600">
          <button type="button" class="btn btn-secondary px-3 py-1.5 text-xs" data-test="group-accounts-load-more" :disabled="saving" @click="loadMore">
            {{ text('admin.groups.groupAccounts.loadMore', '加载更多') }}
          </button>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <span class="text-xs text-gray-500 dark:text-gray-400">
          {{ text('admin.groups.groupAccounts.changeCount', '待变更') }}: {{ changeCount }}
        </span>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">
            {{ text('common.cancel', '取消') }}
          </button>
          <button
            type="button"
            data-test="group-accounts-save"
            class="btn btn-primary"
            :disabled="loading || saving || changeCount === 0"
            @click="saveSelection"
          >
            <Icon v-if="saving" name="refresh" size="sm" class="mr-1.5 animate-spin" />
            <Icon v-else name="check" size="sm" class="mr-1.5" />
            {{ saving ? text('common.saving', '保存中') : riskAccountIds.length > 0 ? text('admin.groups.groupAccounts.confirmRisk', '确认并保存') : text('common.save', '保存') }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { Account, AdminGroup } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const PAGE_SIZE = 50
const MEMBER_PAGE_SIZE = 100
const SEARCH_DEBOUNCE_MS = 300

const props = defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const { t, te } = useI18n()
const appStore = useAppStore()
const loading = ref(false)
const candidateLoading = ref(false)
const saving = ref(false)
const searchQuery = ref('')
const candidateRows = ref<Account[]>([])
const accountCache = ref<Account[]>([])
const selectedAccountIds = ref<number[]>([])
const originalAccountIds = ref<number[]>([])
const candidatePage = ref(0)
const candidatePages = ref(0)
const riskAccountIds = ref<number[]>([])
let searchTimer: ReturnType<typeof setTimeout> | null = null
let loadRevision = 0
let candidateRevision = 0

const selectedSet = computed(() => new Set(selectedAccountIds.value))
const normalizedSearch = computed(() => searchQuery.value.trim().toLocaleLowerCase())
const selectVisibleInput = ref<HTMLInputElement | null>(null)
const visibleAccounts = computed(() => {
  const query = normalizedSearch.value
  const matches = (account: Account) => !query ||
    account.name.toLocaleLowerCase().includes(query) ||
    String(account.id).includes(query)
  const selected = accountCache.value.filter((account) => selectedSet.value.has(account.id) && matches(account))
  const merged = new Map<number, Account>()
  for (const account of selected) merged.set(account.id, account)
  // Candidate rows already match the server-side query, which may include fields
  // (such as email) that are intentionally not returned in the account DTO.
  for (const account of candidateRows.value) merged.set(account.id, account)
  return [...merged.values()].sort((a, b) => {
    const aSelected = selectedSet.value.has(a.id)
    const bSelected = selectedSet.value.has(b.id)
    return Number(bSelected) - Number(aSelected) || a.name.localeCompare(b.name) || a.id - b.id
  })
})
const allVisibleSelected = computed(() => visibleAccounts.value.length > 0 && visibleAccounts.value.every((account) => selectedSet.value.has(account.id)))
const partiallyVisibleSelected = computed(() =>
  visibleAccounts.value.some((account) => selectedSet.value.has(account.id)) && !allVisibleSelected.value,
)
const canLoadMore = computed(() => candidatePage.value < candidatePages.value)
const changeCount = computed(() => {
  const original = new Set(originalAccountIds.value)
  const selected = selectedSet.value
  let count = 0
  for (const id of original) if (!selected.has(id)) count++
  for (const id of selected) if (!original.has(id)) count++
  return count
})
const selectedSchedulableCount = computed(() =>
  accountCache.value.filter((account) => selectedSet.value.has(account.id) && isSchedulable(account)).length,
)

function text(key: string, fallback: string): string {
  return te(key) ? t(key) : fallback
}

function upsertAccounts(accounts: Account[]) {
  const byID = new Map(accountCache.value.map((account) => [account.id, account]))
  for (const account of accounts) byID.set(account.id, account)
  accountCache.value = [...byID.values()]
}

async function fetchGroupMembers(groupID: number, revision: number): Promise<Account[]> {
  const items: Account[] = []
  let page = 1
  let pages = 1
  do {
    const response = await adminAPI.accounts.list(page, MEMBER_PAGE_SIZE, { group: String(groupID) })
    if (revision !== loadRevision) return []
    items.push(...response.items)
    pages = response.pages || 1
    page++
  } while (page <= pages)
  return items
}

async function fetchCandidatePage(page: number, append: boolean, query: string) {
  const group = props.group
  if (!group) return
  const revision = ++candidateRevision
  candidateLoading.value = true
  try {
    const response = await adminAPI.accounts.list(page, PAGE_SIZE, {
      ...(group.platform === 'composite' ? {} : { platform: group.platform }),
      ...(query ? { search: query } : {}),
    })
    if (revision !== candidateRevision || props.group?.id !== group.id) return
    candidateRows.value = append
      ? [...new Map([...candidateRows.value, ...response.items].map((account) => [account.id, account])).values()]
      : response.items
    upsertAccounts(response.items)
    candidatePage.value = response.page
    candidatePages.value = response.pages
  } catch (error: unknown) {
    if (revision === candidateRevision) {
      appStore.showError(extractApiErrorMessage(error, text('admin.groups.groupAccounts.loadFailed', '加载账号失败')))
    }
  } finally {
    if (revision === candidateRevision) candidateLoading.value = false
  }
}

async function loadDialogData() {
  const group = props.group
  if (!group) return
  const revision = ++loadRevision
  candidateRevision++
  loading.value = true
  candidateRows.value = []
  accountCache.value = []
  selectedAccountIds.value = []
  originalAccountIds.value = []
  riskAccountIds.value = []
  candidatePage.value = 0
  candidatePages.value = 0
  try {
    const [members, candidates] = await Promise.all([
      fetchGroupMembers(group.id, revision),
      adminAPI.accounts.list(1, PAGE_SIZE, group.platform === 'composite' ? {} : { platform: group.platform }),
    ])
    if (revision !== loadRevision || props.group?.id !== group.id) return
    const memberIDs = [...new Set(members.map((account) => account.id))]
    originalAccountIds.value = memberIDs
    selectedAccountIds.value = [...memberIDs]
    upsertAccounts([...members, ...candidates.items])
    candidateRows.value = candidates.items
    candidatePage.value = candidates.page
    candidatePages.value = candidates.pages
  } catch (error: unknown) {
    if (revision === loadRevision) appStore.showError(extractApiErrorMessage(error, text('admin.groups.groupAccounts.loadFailed', '加载账号失败')))
  } finally {
    if (revision === loadRevision) loading.value = false
  }
}

watch(
  () => [props.show, props.group?.id] as const,
  ([show]) => {
    if (!show) {
      loadRevision++
      candidateRevision++
      return
    }
    if (searchTimer) clearTimeout(searchTimer)
    searchTimer = null
    searchQuery.value = ''
    void loadDialogData()
  },
  { immediate: true },
)

watch(searchQuery, (value) => {
  if (!props.show) return
  if (searchTimer) clearTimeout(searchTimer)
  const query = value.trim()
  const revision = ++candidateRevision
  candidateRows.value = []
  candidatePage.value = 0
  candidatePages.value = 0
  candidateLoading.value = false
  searchTimer = setTimeout(() => {
    searchTimer = null
    if (revision !== candidateRevision) return
    void fetchCandidatePage(1, false, query)
  }, SEARCH_DEBOUNCE_MS)
})

watch([allVisibleSelected, partiallyVisibleSelected], async () => {
  await nextTick()
  if (selectVisibleInput.value) selectVisibleInput.value.indeterminate = partiallyVisibleSelected.value
}, { immediate: true })

function applySearchNow() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = null
  candidateRows.value = []
  candidatePage.value = 0
  candidatePages.value = 0
  void fetchCandidatePage(1, false, searchQuery.value.trim())
}

function loadMore() {
  if (!canLoadMore.value || candidateLoading.value) return
  void fetchCandidatePage(candidatePage.value + 1, true, searchQuery.value.trim())
}

function isSelected(accountID: number): boolean {
  return selectedSet.value.has(accountID)
}

function clearRiskConfirmation() {
  riskAccountIds.value = []
}

function toggleAccount(accountID: number) {
  selectedAccountIds.value = isSelected(accountID)
    ? selectedAccountIds.value.filter((id) => id !== accountID)
    : [...selectedAccountIds.value, accountID]
  clearRiskConfirmation()
}

function toggleVisibleAccounts() {
  const visibleIDs = new Set(visibleAccounts.value.map((account) => account.id))
  selectedAccountIds.value = allVisibleSelected.value
    ? selectedAccountIds.value.filter((id) => !visibleIDs.has(id))
    : [...new Set([...selectedAccountIds.value, ...visibleIDs])]
  clearRiskConfirmation()
}

function isFuture(value: string | null | undefined): boolean {
  if (!value) return false
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) && timestamp > Date.now()
}

function isSchedulable(account: Account): boolean {
  const nowSeconds = Date.now() / 1000
  const expired = typeof account.expires_at === 'number' && account.expires_at <= nowSeconds
  return account.status === 'active' && account.schedulable !== false &&
    !(expired && account.auto_pause_on_expired !== false) &&
    !isFuture(account.rate_limit_reset_at) && !isFuture(account.overload_until) &&
    !isFuture(account.temp_unschedulable_until)
}

async function mapWithLimit<T, R>(items: T[], limit: number, worker: (item: T) => Promise<R>): Promise<R[]> {
  const results = new Array<R>(items.length)
  let cursor = 0
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (cursor < items.length) {
      const index = cursor++
      results[index] = await worker(items[index])
    }
  }))
  return results
}

async function reloadMembersAfterPartialSave(groupID: number) {
  const revision = ++loadRevision
  const members = await fetchGroupMembers(groupID, revision)
  if (revision !== loadRevision || props.group?.id !== groupID) return
  upsertAccounts(members)
  const ids = [...new Set(members.map((account) => account.id))]
  originalAccountIds.value = ids
  selectedAccountIds.value = ids
}

async function saveSelection() {
  const group = props.group
  if (!group || saving.value || changeCount.value === 0) return
  saving.value = true
  try {
    const initiallySelected = new Set(originalAccountIds.value)
    const desiredSelected = selectedSet.value
    const changedIDs = [...new Set([...initiallySelected, ...desiredSelected])]
      .filter((id) => initiallySelected.has(id) !== desiredSelected.has(id))

    const plans = await mapWithLimit(changedIDs, 5, async (id) => {
      const account = await adminAPI.accounts.getById(id)
      const currentGroupIDs = account.group_ids ?? account.groups?.map((item) => item.id)
      if (!currentGroupIDs) throw new Error(`无法确认账号 ${id} 的现有分组，已停止保存以防覆盖其他分组`)
      const next = new Set(currentGroupIDs)
      if (desiredSelected.has(id)) next.add(group.id)
      else next.delete(group.id)
      return { id, platform: account.platform, groupIDs: [...next] }
    })

    const riskChecks = await mapWithLimit(plans, 5, async (plan) => {
      if (plan.groupIDs.length === 0) return { id: plan.id, hasRisk: false }
      const result = await adminAPI.accounts.checkMixedChannelRisk({
        account_id: plan.id,
        platform: plan.platform,
        group_ids: plan.groupIDs,
      })
      return { id: plan.id, hasRisk: result.has_risk === true }
    })
    const riskyIDs = riskChecks.filter((item) => item.hasRisk).map((item) => item.id)
    const plannedPlatforms = new Set([
      ...accountCache.value
        .filter((account) => selectedSet.value.has(account.id))
        .map((account) => account.platform),
      ...plans
        .filter((plan) => desiredSelected.has(plan.id))
        .map((plan) => plan.platform),
    ])
    if (plannedPlatforms.has('anthropic') && plannedPlatforms.has('antigravity')) {
      riskyIDs.push(...plans.map((plan) => plan.id))
    }
    const uniqueRiskyIDs = [...new Set(riskyIDs)]
    if (uniqueRiskyIDs.length > 0 && riskAccountIds.value.length === 0) {
      riskAccountIds.value = uniqueRiskyIDs
      return
    }

    const riskySet = new Set(uniqueRiskyIDs)
    const outcomes = await mapWithLimit(plans, 4, async (plan) => {
      try {
        await adminAPI.accounts.update(plan.id, {
          group_ids: plan.groupIDs,
          ...(riskySet.has(plan.id) ? { confirm_mixed_channel_risk: true } : {}),
        })
        return { id: plan.id, error: null as unknown }
      } catch (error: unknown) {
        return { id: plan.id, error }
      }
    })
    const failed = outcomes.filter((result) => result.error !== null)
    if (failed.length > 0) {
      await reloadMembersAfterPartialSave(group.id)
      const detail = extractApiErrorMessage(failed[0].error, '')
      const prefix = text('admin.groups.groupAccounts.partialSave', '已保存 {saved} 个，{failed} 个失败。请检查后重试。')
        .replace('{saved}', String(outcomes.length - failed.length))
        .replace('{failed}', String(failed.length))
      appStore.showError(detail ? `${prefix} ${detail}` : prefix)
      return
    }

    appStore.showSuccess(text('admin.groups.groupAccounts.saved', '分组账号已更新'))
    emit('saved')
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, text('admin.groups.groupAccounts.saveFailed', '保存分组账号失败')))
  } finally {
    saving.value = false
  }
}

onUnmounted(() => {
  if (searchTimer) clearTimeout(searchTimer)
  loadRevision++
  candidateRevision++
})
</script>
