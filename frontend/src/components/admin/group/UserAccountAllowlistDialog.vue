<template>
  <BaseDialog
    :show="show"
    :title="text('admin.groups.userAccountAllowlist.title', '用户可调用账号')"
    width="wide"
    @close="emit('close')"
  >
    <div v-if="group && user" class="space-y-4">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg bg-gray-50 px-4 py-3 text-sm dark:bg-dark-700">
        <span class="inline-flex items-center gap-1.5 font-medium text-gray-900 dark:text-white">
          <Icon name="user" size="sm" class="text-primary-500" />
          {{ user.username || user.email }}
        </span>
        <span class="text-gray-300 dark:text-dark-500">|</span>
        <span class="text-gray-600 dark:text-gray-400">{{ user.email }}</span>
        <span class="text-gray-300 dark:text-dark-500">|</span>
        <span class="text-gray-600 dark:text-gray-400">{{ group.name }}</span>
      </div>

      <div class="grid gap-3 sm:grid-cols-2">
        <div class="rounded-lg border border-gray-200 px-3 py-2.5 dark:border-dark-600">
          <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <Icon name="users" size="sm" />
            {{ text('admin.groups.userAccountAllowlist.currentConcurrency', '当前并发') }}
          </div>
          <div class="mt-1 flex items-baseline gap-1.5">
            <span class="font-mono text-lg font-semibold text-gray-900 dark:text-white">{{ currentUserConcurrency }}</span>
            <span class="text-xs text-gray-400 dark:text-gray-500">{{ text('admin.groups.userAccountAllowlist.requests', '个请求') }}</span>
          </div>
        </div>

        <div class="rounded-lg border border-gray-200 px-3 py-2.5 dark:border-dark-600">
          <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <Icon name="grid" size="sm" />
            {{ text('admin.groups.userAccountAllowlist.activeAccounts', '当前调用账号') }}
          </div>
          <div class="mt-1 min-h-6">
            <div v-if="activeAccountNames.length > 0 || waitingConcurrency > 0" class="flex flex-wrap gap-1.5">
              <span
                v-for="account in activeAccountNames"
                :key="account.id"
                class="inline-flex max-w-full items-center gap-1 rounded-md bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
                :title="`${account.name} ×${account.userConcurrency}`"
                :data-test="`allowlist-active-account-${account.id}`"
              >
                <Icon name="bolt" size="xs" />
                <span class="truncate">{{ account.name }}</span>
                <span class="shrink-0 font-mono text-current/70">×{{ account.userConcurrency }}</span>
              </span>
              <span
                v-if="waitingConcurrency > 0"
                class="inline-flex items-center gap-1 rounded-md bg-gray-100 px-1.5 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
                data-test="allowlist-waiting-account"
              >
                <Icon name="clock" size="xs" />
                {{ text('admin.groups.userAccountAllowlist.waitingForAccount', '等待账号槽位') }}
                <span class="font-mono text-current/70">×{{ waitingConcurrency }}</span>
              </span>
            </div>
            <span v-else class="text-sm text-gray-400 dark:text-gray-500">{{ text('admin.groups.userAccountAllowlist.noActiveAccounts', '暂无活跃调用') }}</span>
          </div>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-3 border-b border-gray-200 pb-3 dark:border-dark-600">
        <label class="inline-flex cursor-pointer items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300">
          <input
            ref="selectAllInput"
            data-test="allowlist-select-all"
            type="checkbox"
            :checked="allSelected"
            :disabled="loading || saving || availableCandidates.length === 0"
            class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700"
            @change="toggleAll"
          />
          {{ text('common.selectAll', '全选') }}
        </label>
        <span class="text-xs text-gray-400 dark:text-gray-500">
          {{ selectedAvailableCount }}/{{ availableCandidates.length }} {{ text('admin.groups.userAccountAllowlist.availableAccounts', '个正常可调度账号') }}
        </span>
        <span v-if="selectedUnavailableCount > 0" class="text-xs text-amber-600 dark:text-amber-300">
          {{ text('admin.groups.userAccountAllowlist.unavailableSelected', '另有 {count} 个已选账号暂不可用').replace('{count}', String(selectedUnavailableCount)) }}
        </span>
        <span
          class="ml-auto inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium"
          :class="isUsingOriginalScheduling
            ? 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
            : 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'"
        >
          <Icon :name="isUsingOriginalScheduling ? 'sync' : 'checkCircle'" size="xs" />
          {{ isUsingOriginalScheduling
            ? text('admin.groups.userAccountAllowlist.originalScheduling', '原版调度')
            : text('admin.groups.userAccountAllowlist.restrictedScheduling', '已指定账号') }}
        </span>
      </div>

      <div v-if="loading" class="flex justify-center py-10">
        <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
      </div>

      <div v-else-if="candidates.length === 0" class="py-10 text-center text-sm text-gray-400 dark:text-gray-500">
        {{ text('admin.groups.userAccountAllowlist.noSchedulableAccounts', '该分组暂无可配置账号') }}
      </div>

      <div v-else class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
        <div class="max-h-[420px] overflow-auto">
          <table class="w-full min-w-[620px] text-sm">
            <thead class="sticky top-0 z-[1] bg-gray-50 dark:bg-dark-700">
              <tr class="border-b border-gray-200 dark:border-dark-600">
                <th class="w-12 px-3 py-2 text-center">
                  <span class="sr-only">{{ text('admin.groups.userAccountAllowlist.allow', '允许') }}</span>
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ text('admin.groups.userAccountAllowlist.account', '账号') }}
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ text('admin.groups.userAccountAllowlist.status', '状态') }}
                </th>
                <th class="px-3 py-2 text-right text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ text('admin.groups.userAccountAllowlist.accountConcurrency', '账号并发') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr
                v-for="account in candidates"
                :key="account.id"
                class="transition-colors hover:bg-gray-50 dark:hover:bg-dark-700/50"
                :class="isSelected(account.id) ? 'bg-primary-50/40 dark:bg-primary-900/10' : ''"
              >
                <td class="px-3 py-2.5 text-center">
                  <label class="inline-flex cursor-pointer items-center justify-center">
                    <input
                      type="checkbox"
                      :data-test="`allowlist-account-${account.id}`"
                      :checked="isSelected(account.id)"
                      :disabled="saving || (!account.available && !isSelected(account.id))"
                      class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700"
                      @change="toggleAccount(account.id)"
                    />
                    <span class="sr-only">{{ text('admin.groups.userAccountAllowlist.allowAccount', '允许调用该账号') }}</span>
                  </label>
                </td>
                <td class="min-w-0 px-3 py-2.5">
                  <div class="flex min-w-0 items-center gap-2">
                    <span class="truncate font-medium text-gray-900 dark:text-white" :title="account.name">{{ account.name }}</span>
                    <span class="shrink-0 text-xs text-gray-400 dark:text-gray-500">#{{ account.id }}</span>
                  </div>
                </td>
                <td class="px-3 py-2.5">
                  <div class="flex flex-wrap items-center gap-1.5">
                    <span
                      v-if="isActive(account.id)"
                      class="inline-flex items-center gap-1 rounded-md bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
                    >
                      <Icon name="bolt" size="xs" />
                      {{ text('admin.groups.userAccountAllowlist.inUse', '调用中') }}
                    </span>
                    <span v-else-if="account.available" class="inline-flex items-center gap-1 rounded-md bg-green-100 px-1.5 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-300">
                      <Icon name="checkCircle" size="xs" />
                      {{ text('admin.groups.userAccountAllowlist.normal', '正常') }}
                    </span>
                    <span v-else class="inline-flex items-center gap-1 rounded-md bg-gray-100 px-1.5 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-400">
                      <Icon name="exclamationCircle" size="xs" />
                      {{ text('admin.groups.userAccountAllowlist.unavailable', '暂不可用') }}
                    </span>
                    <span v-if="isSelected(account.id)" class="text-xs text-primary-600 dark:text-primary-400">
                      {{ text('admin.groups.userAccountAllowlist.allowed', '已允许') }}
                    </span>
                  </div>
                </td>
                <td class="px-3 py-2.5 text-right">
                  <span
                    class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-mono text-xs font-medium"
                    :class="accountConcurrencyClass(account)"
                  >
                    <Icon name="grid" size="xs" />
                    {{ accountConcurrency(account) }}<span class="text-current/50">/</span>{{ account.concurrency > 0 ? account.concurrency : '∞' }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <p v-if="restricted && selectedAccountIds.length === 0 && !loading" class="flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-300">
        <Icon name="exclamationCircle" size="sm" />
        {{ text('admin.groups.userAccountAllowlist.denyAllAccounts', '保存后，该用户将无法调用此分组中的任何账号。') }}
      </p>
    </div>

    <template #footer>
    <div class="flex flex-wrap items-center gap-3">
      <button
        type="button"
        data-test="allowlist-restore-original"
        class="btn btn-secondary mr-auto"
        :disabled="loading || saving || isUsingOriginalScheduling"
        @click="handleRestore"
      >
        <Icon name="sync" size="sm" class="mr-1.5" />
        {{ text('admin.groups.userAccountAllowlist.restoreOriginal', '恢复原版调度') }}
      </button>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">
        {{ text('common.cancel', '取消') }}
      </button>
      <button
        type="button"
        data-test="allowlist-save"
        class="btn btn-primary"
        :disabled="saveDisabled"
        @click="handleSave"
      >
        <Icon v-if="saving" name="refresh" size="sm" class="mr-1.5 animate-spin" />
        <Icon v-else name="check" size="sm" class="mr-1.5" />
        {{ saving ? text('common.saving', '保存中') : text('common.save', '保存') }}
      </button>
    </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminGroup, AdminUser } from '@/types'
import type { UserGroupAccountAllowlistCandidate } from '@/api/admin/groups'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

interface Props {
  show: boolean
  group: AdminGroup | null
  user: AdminUser | null
  /** Current candidates plus any persisted selection that is temporarily unavailable. */
  candidates: UserGroupAccountAllowlistCandidate[]
  /** Accounts that currently hold one or more live requests for this user. */
  activeAccountIds: number[]
  /** Occupied request slots per active account for this user. */
  activeAccountConcurrency: Record<string, number>
  /** False means original scheduling; true may intentionally have zero selected accounts. */
  restricted: boolean
  allowedAccountIds: number[]
  loading: boolean
  saving: boolean
}

const props = defineProps<Props>()

const emit = defineEmits<{
  close: []
  /** Persist the explicit allowed account IDs. An empty list means deny all. */
  save: [accountIds: number[]]
  /** Clear the explicit allowlist and restore the original group scheduling behavior. */
  restore: []
}>()

const { t, te } = useI18n()
const selectedAccountIds = ref<number[]>([])
const selectAllInput = ref<HTMLInputElement | null>(null)

const availableCandidates = computed(() => props.candidates.filter((account) => account.available))
const availableAccountIds = computed(() => availableCandidates.value.map((account) => account.id))
const activeAccountIdSet = computed(() => new Set(props.activeAccountIds))
const selectedAccountIdSet = computed(() => new Set(selectedAccountIds.value))

const activeAccountNames = computed(() =>
  props.candidates
    .filter((account) => activeAccountIdSet.value.has(account.id))
    .map((account) => ({
      ...account,
      userConcurrency: Math.max(
        0,
        Number(props.activeAccountConcurrency[String(account.id)]) || 1,
      ),
    }))
)

const currentUserConcurrency = computed(() =>
  props.user?.current_concurrency ?? props.activeAccountIds.length
)

const assignedConcurrency = computed(() => {
  if (Object.keys(props.activeAccountConcurrency).length > 0) {
    return Object.values(props.activeAccountConcurrency).reduce(
      (sum, value) => sum + Math.max(0, Number(value) || 0),
      0,
    )
  }
  return props.activeAccountIds.length
})

const waitingConcurrency = computed(() =>
  Math.max(0, currentUserConcurrency.value - assignedConcurrency.value)
)

const allSelected = computed(() =>
  availableAccountIds.value.length > 0 && availableAccountIds.value.every((id) => selectedAccountIdSet.value.has(id))
)

const selectedAvailableCount = computed(() =>
  availableAccountIds.value.filter((id) => selectedAccountIdSet.value.has(id)).length
)

const selectedUnavailableCount = computed(() =>
  props.candidates.filter((account) => !account.available && selectedAccountIdSet.value.has(account.id)).length
)

const partiallySelected = computed(() =>
  selectedAvailableCount.value > 0 && !allSelected.value
)

const isUsingOriginalScheduling = computed(() => !props.restricted)
const saveDisabled = computed(() => props.loading || props.saving)

function text(key: string, fallback: string): string {
  return te(key) ? t(key) : fallback
}

function hydrateSelection() {
  if (!props.restricted) {
    selectedAccountIds.value = [...availableAccountIds.value]
    return
  }
  const candidateIDs = new Set(props.candidates.map((account) => account.id))
  selectedAccountIds.value = props.allowedAccountIds.filter((id) => candidateIDs.has(id))
}

watch(
  () => [
    props.show,
    props.group?.id,
    props.user?.id,
    props.loading,
    props.restricted,
    props.candidates.map((account) => `${account.id}:${account.available ? 1 : 0}`).join(','),
    props.allowedAccountIds.join(','),
  ],
  ([show]) => {
    if (show) hydrateSelection()
  },
  { immediate: true }
)

watch(
  [allSelected, partiallySelected, selectAllInput],
  async () => {
    await nextTick()
    if (selectAllInput.value) selectAllInput.value.indeterminate = partiallySelected.value
  },
  { immediate: true }
)

function isSelected(accountId: number): boolean {
  return selectedAccountIdSet.value.has(accountId)
}

function isActive(accountId: number): boolean {
  return activeAccountIdSet.value.has(accountId)
}

function toggleAccount(accountId: number) {
  const account = props.candidates.find((candidate) => candidate.id === accountId)
  if (!account || (!account.available && !isSelected(accountId))) return
  selectedAccountIds.value = isSelected(accountId)
    ? selectedAccountIds.value.filter((id) => id !== accountId)
    : [...selectedAccountIds.value, accountId]
}

function toggleAll() {
  const available = new Set(availableAccountIds.value)
  const unavailableSelections = selectedAccountIds.value.filter((id) => !available.has(id))
  selectedAccountIds.value = allSelected.value
    ? unavailableSelections
    : [...availableAccountIds.value, ...unavailableSelections]
}

function accountConcurrency(account: UserGroupAccountAllowlistCandidate): number {
  return account.current_concurrency ?? 0
}

function accountConcurrencyClass(account: UserGroupAccountAllowlistCandidate): string {
  const used = accountConcurrency(account)
  if (account.concurrency > 0 && used >= account.concurrency) {
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  }
  if (used > 0) {
    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  }
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
}

function handleSave() {
  if (saveDisabled.value) return
  const selected = new Set(selectedAccountIds.value)
  // Keeping every currently available account selected is the default
  // scheduler behavior. Do not persist a snapshot that would exclude future
  // accounts added to this group; only an actual subset is an allowlist.
  if (selectedAvailableCount.value === availableAccountIds.value.length && selectedUnavailableCount.value === 0) {
    emit('restore')
    return
  }
  emit('save', props.candidates.map((account) => account.id).filter((id) => selected.has(id)))
}

function handleRestore() {
  if (props.loading || props.saving || isUsingOriginalScheduling.value) return
  selectedAccountIds.value = [...availableAccountIds.value]
  emit('restore')
}
</script>
