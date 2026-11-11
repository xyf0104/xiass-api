<template>
  <section class="card overflow-hidden" data-testid="openai-model-priority-settings">
    <div class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
      <div class="min-w-0">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.modelPriority.modelPriorityTitle') }}</h2>
        <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.modelPriority.modelPriorityDescription') }}</p>
      </div>
      <div class="flex items-center gap-3">
        <span class="text-sm font-medium" :class="draft.enabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'">
          {{ draft.enabled ? t('admin.modelPriority.on') : t('admin.modelPriority.off') }}
        </span>
        <Toggle v-model="draft.enabled" :disabled="loading || saving" :aria-label="t('admin.modelPriority.modelPriorityTitle')" />
      </div>
    </div>

    <div v-if="loading" class="flex justify-center py-12">
      <span class="h-7 w-7 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
    </div>
    <div v-else>
      <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
        <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(12rem,16rem)_auto] lg:items-center">
          <div class="min-w-0">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.modelPriority.smartRotationTitle') }}</h3>
            <p id="model-priority-smart-rotation-help" class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">
              {{ t('admin.modelPriority.smartRotationDescription') }}
            </p>
            <p v-if="!draft.enabled" class="mt-1 text-sm text-amber-700 dark:text-amber-300">
              {{ t('admin.modelPriority.smartRotationRequiresPriority') }}
            </p>
          </div>

          <label class="block min-w-0" for="model-priority-smart-rotation-cooldown">
            <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.modelPriority.smartRotationCooldown') }}</span>
            <Select
              id="model-priority-smart-rotation-cooldown"
              class="mt-1 w-full"
              :model-value="draft.smart_rotation_cooldown_minutes"
              :options="smartRotationCooldownOptions"
              :disabled="smartRotationCooldownDisabled"
              :aria-label="t('admin.modelPriority.smartRotationCooldown')"
              aria-describedby="model-priority-smart-rotation-help"
              data-testid="model-priority-smart-rotation-cooldown"
              @update:model-value="setSmartRotationCooldown"
            />
          </label>

          <div class="flex min-h-10 items-center justify-between gap-3 lg:justify-end">
            <span
              class="text-sm font-medium"
              :class="draft.enabled && draft.smart_rotation_enabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'"
              data-testid="model-priority-smart-rotation-state"
            >
              {{ smartRotationState }}
            </span>
            <Toggle
              v-model="draft.smart_rotation_enabled"
              :disabled="smartRotationToggleDisabled"
              :aria-label="t('admin.modelPriority.smartRotationTitle')"
              aria-describedby="model-priority-smart-rotation-help"
              data-testid="model-priority-smart-rotation-toggle"
            />
          </div>
        </div>
      </div>

      <div v-if="draft.rules.length" class="divide-y divide-gray-100 dark:divide-dark-700">
        <div v-for="(rule, index) in draft.rules" :key="rule.key" class="grid gap-3 px-5 py-4 sm:px-6 lg:grid-cols-[minmax(14rem,1fr)_11rem_auto_auto] lg:items-end">
          <label class="block min-w-0" :for="`model-priority-pattern-${rule.key}`">
            <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.modelPriority.modelPattern') }}</span>
            <input
              :id="`model-priority-pattern-${rule.key}`"
              v-model.trim="rule.model_pattern"
              list="openai-model-priority-model-options"
              class="input mt-1 w-full font-mono text-sm"
              :placeholder="t('admin.modelPriority.modelPatternPlaceholder')"
              :disabled="saving"
              :data-testid="`model-priority-pattern-${index}`"
            />
          </label>
          <div class="min-w-0">
            <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.modelPriority.preferredAccounts') }}</span>
            <div class="mt-1 flex h-10 items-center text-sm font-semibold text-gray-800 dark:text-gray-200">
              {{ t('admin.modelPriority.selectedAccounts', { count: rule.account_ids.length }) }}
            </div>
          </div>
          <button type="button" class="btn btn-secondary" :disabled="saving" :data-testid="`model-priority-configure-${index}`" @click="openAccountPicker(index)">
            <Icon name="users" size="sm" />
            <span>{{ t('admin.modelPriority.configureAccounts') }}</span>
          </button>
          <button type="button" class="btn btn-secondary flex h-10 w-10 items-center justify-center p-0 text-red-600 dark:text-red-400" :disabled="saving" :title="t('admin.modelPriority.removeRule')" :aria-label="t('admin.modelPriority.removeRule')" @click="removeRule(index)">
            <Icon name="trash" size="sm" />
          </button>
        </div>
      </div>
      <div v-else class="px-5 py-8 text-center text-sm text-gray-500 dark:text-gray-400 sm:px-6">
        {{ t('admin.modelPriority.noModelPriorityRules') }}
      </div>
      <datalist id="openai-model-priority-model-options">
        <option v-for="model in modelOptions" :key="model" :value="model" />
      </datalist>
      <div class="px-5 pb-4 sm:px-6">
        <button type="button" class="btn btn-secondary" :disabled="saving" data-testid="model-priority-add" @click="addRule">
          <Icon name="plus" size="sm" /><span>{{ t('admin.modelPriority.addModelPriorityRule') }}</span>
        </button>
      </div>
      <div class="border-t border-gray-100 dark:border-dark-700" data-testid="openai-user-priority-settings">
        <div class="flex flex-wrap items-start justify-between gap-4 px-5 py-4 sm:px-6">
          <div class="min-w-0">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.modelPriority.userPriorityTitle') }}</h3>
            <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.modelPriority.userPriorityDescription') }}</p>
          </div>
          <Toggle v-model="draft.user_enabled" :disabled="saving" :aria-label="t('admin.modelPriority.userPriorityTitle')" data-testid="user-priority-toggle" />
        </div>
        <div v-if="draft.user_rules.length" class="divide-y divide-gray-100 dark:divide-dark-700">
          <div v-for="(rule, index) in draft.user_rules" :key="rule.key" class="grid gap-3 px-5 py-3 sm:px-6 lg:grid-cols-[minmax(14rem,1fr)_11rem_auto_auto] lg:items-center">
            <div class="min-w-0 truncate text-sm font-medium text-gray-900 dark:text-white" :title="userName(rule.user_id)">{{ userName(rule.user_id) }}</div>
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.modelPriority.selectedAccounts', { count: rule.account_ids.length }) }}</span>
            <button type="button" class="btn btn-secondary" :disabled="saving" :data-testid="`user-priority-configure-${index}`" @click="openAccountPicker(index, 'user')"><Icon name="users" size="sm" /><span>{{ t('admin.modelPriority.configureAccounts') }}</span></button>
            <button type="button" class="btn btn-secondary flex h-10 w-10 items-center justify-center p-0 text-red-600 dark:text-red-400" :disabled="saving" :title="t('admin.modelPriority.removeRule')" :aria-label="t('admin.modelPriority.removeRule')" @click="removeUserRule(index)"><Icon name="trash" size="sm" /></button>
          </div>
        </div>
        <div v-else class="px-5 pb-3 text-sm text-gray-500 dark:text-gray-400 sm:px-6">{{ t('admin.modelPriority.noUserRules') }}</div>
        <div class="border-t border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
          <SearchInput v-model="userSearch" :placeholder="t('admin.modelPriority.searchUsers')" :aria-label="t('admin.modelPriority.searchUsers')" class="w-full sm:max-w-sm" data-testid="user-priority-search" />
          <div v-if="userSearch.trim()" class="mt-2 max-h-40 overflow-y-auto border border-gray-200 dark:border-dark-700" data-testid="user-priority-results">
            <div v-if="userLoading" class="flex justify-center py-3"><Icon name="refresh" size="sm" class="animate-spin text-gray-500" /></div>
            <button v-for="user in userResults" :key="user.id" type="button" class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm hover:bg-gray-50 disabled:opacity-50 dark:hover:bg-dark-800" :disabled="saving || draft.user_rules.some(rule => rule.user_id === user.id)" :data-testid="`user-priority-add-${user.id}`" @click="addUserRule(user)">
              <span class="min-w-0 truncate text-gray-900 dark:text-white">{{ user.username }} <span class="text-gray-500 dark:text-gray-400">{{ user.email }}</span></span><Icon name="plus" size="sm" />
            </button>
            <div v-if="!userResults.length && !userLoading" class="px-3 py-3 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.modelPriority.noMatchingUsers') }}</div>
          </div>
        </div>
      </div>
      <div class="flex justify-end gap-3 border-t border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
        <button type="button" class="btn btn-primary" :disabled="saving || !canSave" data-testid="model-priority-save" @click="save">
          <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" />
          <span>{{ saving ? t('admin.modelPriority.saving') : t('admin.modelPriority.save') }}</span>
        </button>
      </div>
    </div>

    <BaseDialog :show="pickerRule !== null" :title="pickerTitle" width="full" @close="closeAccountPicker">
      <div class="space-y-4">
        <div class="flex flex-wrap items-center gap-3">
          <SearchInput v-model="filters.search" :placeholder="t('admin.modelPriority.searchPriorityAccounts')" class="w-full sm:w-64" />
          <Select v-model="filters.group" class="w-40" :options="groupOptions" data-testid="model-priority-group-filter" />
          <Select v-model="filters.pool" class="w-40" :options="poolOptions" data-testid="model-priority-pool-filter" />
          <Select v-model="filters.plan" class="w-40" :options="planOptions" data-testid="model-priority-plan-filter" />
          <Select v-model="filters.type" class="w-40" :options="typeOptions" data-testid="model-priority-type-filter" />
          <Select v-model="filters.status" class="w-40" :options="statusOptions" data-testid="model-priority-status-filter" />
        </div>

        <div class="flex flex-wrap items-center justify-between gap-3 border-y border-gray-100 py-3 dark:border-dark-700">
          <div class="text-sm text-gray-600 dark:text-gray-300">
            {{ t('admin.modelPriority.prioritySelectionSummary', { filtered: filteredAccounts.length, selected: pickerSelected.size }) }}
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="catalogLoading || !filteredAccounts.length" data-testid="model-priority-select-filtered" @click="selectFiltered">
              <Icon name="check" size="sm" />
              <span>{{ t('admin.modelPriority.selectFilteredAccounts') }}</span>
            </button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="catalogLoading || !filteredAccounts.length" @click="deselectFiltered">
              <Icon name="x" size="sm" />
              <span>{{ t('admin.modelPriority.deselectFilteredAccounts') }}</span>
            </button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!pickerSelected.size" data-testid="model-priority-clear-selection" @click="clearPickerSelection">
              <Icon name="trash" size="sm" />
              <span>{{ t('admin.modelPriority.clearSelectedAccounts') }}</span>
            </button>
          </div>
        </div>

        <div v-if="pickerOrder.length" class="space-y-2" data-testid="model-priority-selected-order">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.modelPriority.selectedAccountOrder') }}</h3>
          <VueDraggable v-model="pickerOrder" :animation="180" handle=".priority-drag-handle" class="space-y-2">
            <div
              v-for="(accountID, orderIndex) in pickerOrder"
              :key="accountID"
              class="flex min-h-12 items-center gap-2 rounded-md border border-gray-200 bg-white px-2 py-2 dark:border-dark-600 dark:bg-dark-900"
              :data-testid="`model-priority-selected-${accountID}`"
            >
              <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded bg-gray-100 text-xs font-semibold text-gray-700 dark:bg-dark-700 dark:text-gray-200">{{ orderIndex + 1 }}</span>
              <button
                type="button"
                class="priority-drag-handle flex h-9 w-9 shrink-0 cursor-grab items-center justify-center text-gray-400 hover:text-gray-700 active:cursor-grabbing dark:hover:text-gray-200"
                :aria-label="t('admin.modelPriority.dragPriorityAccount', { account: selectedAccountName(accountID) })"
                :title="t('admin.modelPriority.dragPriorityAccount', { account: selectedAccountName(accountID) })"
                :data-testid="`model-priority-drag-${accountID}`"
                @keydown.up.prevent="movePickerAccount(orderIndex, -1)"
                @keydown.down.prevent="movePickerAccount(orderIndex, 1)"
              >
                <Icon name="sort" size="sm" />
              </button>
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="selectedAccountName(accountID)">{{ selectedAccountName(accountID) }}</div>
                <div v-if="accountByID.get(accountID) && accountEmail(accountByID.get(accountID)!)" class="truncate text-xs text-gray-500 dark:text-gray-400">{{ accountEmail(accountByID.get(accountID)!) }}</div>
              </div>
              <div class="flex shrink-0 items-center gap-1">
                <button type="button" class="flex h-9 w-9 items-center justify-center text-gray-500 disabled:opacity-30 dark:text-gray-300" :disabled="orderIndex === 0" :aria-label="t('admin.modelPriority.movePriorityAccountUp', { account: selectedAccountName(accountID) })" :title="t('admin.modelPriority.movePriorityAccountUp', { account: selectedAccountName(accountID) })" :data-testid="`model-priority-up-${accountID}`" @click="movePickerAccount(orderIndex, -1)">
                  <Icon name="chevronUp" size="sm" />
                </button>
                <button type="button" class="flex h-9 w-9 items-center justify-center text-gray-500 disabled:opacity-30 dark:text-gray-300" :disabled="orderIndex === pickerOrder.length - 1" :aria-label="t('admin.modelPriority.movePriorityAccountDown', { account: selectedAccountName(accountID) })" :title="t('admin.modelPriority.movePriorityAccountDown', { account: selectedAccountName(accountID) })" :data-testid="`model-priority-down-${accountID}`" @click="movePickerAccount(orderIndex, 1)">
                  <Icon name="chevronDown" size="sm" />
                </button>
              </div>
            </div>
          </VueDraggable>
        </div>

        <div v-if="catalogLoading" class="flex justify-center py-16">
          <span class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
        </div>
        <div v-else class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
          <div class="max-h-[52vh] overflow-auto">
            <table class="min-w-full table-fixed divide-y divide-gray-200 text-sm dark:divide-dark-600">
              <thead class="sticky top-0 z-10 bg-gray-50 text-left text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400">
                <tr>
                  <th class="w-12 px-4 py-3">
                    <input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :checked="pageAccounts.length > 0 && pageAccounts.every(account => pickerSelected.has(account.id))" :aria-label="t('admin.modelPriority.selectCurrentPage')" @change="toggleCurrentPage" />
                  </th>
                  <th class="w-[30%] px-3 py-3">{{ t('admin.modelPriority.account') }}</th>
                  <th class="w-40 px-3 py-3">{{ t('admin.modelPriority.accountType') }}</th>
                  <th class="w-28 px-3 py-3">{{ t('admin.modelPriority.accountPlan') }}</th>
                  <th class="px-3 py-3">{{ t('admin.modelPriority.accountGroups') }}</th>
                  <th class="w-32 px-3 py-3">{{ t('admin.modelPriority.accountPool') }}</th>
                  <th class="w-24 px-3 py-3 text-right">{{ t('admin.modelPriority.accountPriority') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-900">
                <tr v-for="account in pageAccounts" :key="account.id" class="cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-800/70" :data-testid="`model-priority-account-${account.id}`" @click="toggleAccount(account.id)">
                  <td class="px-4 py-3" @click.stop>
                    <input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :checked="pickerSelected.has(account.id)" :aria-label="account.name" @change="toggleAccount(account.id)" />
                  </td>
                  <td class="px-3 py-3">
                    <div class="truncate font-medium text-gray-900 dark:text-white" :title="account.name">{{ account.name }}</div>
                    <div v-if="accountEmail(account)" class="truncate text-xs text-gray-500 dark:text-gray-400" :title="accountEmail(account)">{{ accountEmail(account) }}</div>
                  </td>
                  <td class="px-3 py-3"><PlatformTypeBadge :platform="account.platform" :type="account.type" :plan-type="accountPlan(account)" /></td>
                  <td class="px-3 py-3 text-gray-700 dark:text-gray-300">{{ accountPlanLabel(account) }}</td>
                  <td class="px-3 py-3"><div class="flex flex-wrap gap-1"><span v-for="name in accountGroupNames(account)" :key="name" class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ name }}</span><span v-if="!accountGroupNames(account).length" class="text-xs text-gray-400">-</span></div></td>
                  <td class="px-3 py-3 text-gray-600 dark:text-gray-300">{{ accountPoolName(account.id) || '-' }}</td>
                  <td class="px-3 py-3 text-right font-mono text-gray-700 dark:text-gray-300">{{ account.priority }}</td>
                </tr>
                <tr v-if="!pageAccounts.length"><td colspan="7" class="px-4 py-12 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('admin.modelPriority.noMatchingAccounts') }}</td></tr>
              </tbody>
            </table>
          </div>
          <Pagination v-if="filteredAccounts.length > pageSize" :total="filteredAccounts.length" :page="page" :page-size="pageSize" :show-page-size-selector="true" @update:page="page = $event" @update:page-size="changePageSize" />
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" data-testid="model-priority-cancel-accounts" @click="closeAccountPicker">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="catalogLoading || pickerRule === null || pickerSelected.size === 0" data-testid="model-priority-apply-accounts" @click="applyAccountPicker">
          <Icon name="check" size="sm" />
          <span>{{ t('admin.modelPriority.applySelectedAccounts') }}</span>
        </button>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { VueDraggable } from 'vue-draggable-plus'
import { adminAPI } from '@/api/admin'
import type { OpenAIModelPriorityRule, OpenAIModelPrioritySettings, OpenAIUserPriorityRule } from '@/api/admin/settings'
import type { Account, AdminGroup, AdminUser } from '@/types'
import type { AccountPool } from '@/api/admin/accountPools'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/common/Pagination.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import { getModelsByPlatform } from '@/composables/useModelWhitelist'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'

interface DraftRule extends OpenAIModelPriorityRule { key: number }
interface DraftUserRule extends OpenAIUserPriorityRule { key: number }
interface DraftSettings {
  enabled: boolean
  user_enabled: boolean
  user_rules: DraftUserRule[]
  smart_rotation_enabled: boolean
  smart_rotation_cooldown_minutes: number
  rules: DraftRule[]
}

const SMART_ROTATION_DEFAULT_COOLDOWN_MINUTES = 30
const SMART_ROTATION_COOLDOWN_OPTIONS = [1, 5, 15, 30, 60, 120, 1440]

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const catalogLoading = ref(false)
const catalogLoaded = ref(false)
const accounts = ref<Account[]>([])
const groups = ref<AdminGroup[]>([])
const pools = ref<AccountPool[]>([])
const usersByID = ref<Map<number, AdminUser>>(new Map())
const userResults = ref<AdminUser[]>([])
const userSearch = ref('')
const userLoading = ref(false)
const draft = reactive<DraftSettings>({
  enabled: false,
  user_enabled: false,
  user_rules: [],
  smart_rotation_enabled: false,
  smart_rotation_cooldown_minutes: SMART_ROTATION_DEFAULT_COOLDOWN_MINUTES,
  rules: []
})
const pickerRuleIndex = ref<number | null>(null)
const pickerUserRuleIndex = ref<number | null>(null)
const pickerSelected = ref<Set<number>>(new Set())
const pickerOrder = ref<number[]>([])
const page = ref(1)
const pageSize = ref(20)
let ruleSequence = 0
let pickerRequestSequence = 0
let userSearchSequence = 0

const filters = reactive({ search: '', group: '', pool: '', plan: '', type: '', status: '' })
const modelOptions = getModelsByPlatform('openai')
const groupByID = computed(() => new Map(groups.value.map(group => [group.id, group.name])))
const accountByID = computed(() => new Map(accounts.value.map(account => [account.id, account])))
const poolByAccountID = computed(() => {
  const result = new Map<number, AccountPool>()
  for (const pool of pools.value) for (const accountID of pool.account_ids || []) result.set(accountID, pool)
  return result
})

const groupOptions = computed(() => [{ value: '', label: t('admin.accounts.allGroups') }, ...groups.value.map(group => ({ value: String(group.id), label: group.name }))])
const poolOptions = computed(() => [{ value: '', label: t('admin.modelPriority.allAccountPools') }, ...pools.value.map(pool => ({ value: String(pool.id), label: pool.name }))])
const typeOptions = computed(() => [
  { value: '', label: t('admin.accounts.allTypes') },
  { value: 'oauth', label: t('admin.accounts.oauthType') },
  { value: 'setup-token', label: t('admin.accounts.setupToken') },
  { value: 'apikey', label: t('admin.accounts.apiKey') }
])
const statusOptions = computed(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'available', label: t('admin.modelPriority.availableAccounts') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'error', label: t('admin.accounts.status.error') }
])
const planOptions = computed(() => {
  const values = new Set(accounts.value.map(accountPlan).filter(Boolean))
  return [{ value: '', label: t('admin.modelPriority.allPlans') }, ...Array.from(values).sort().map(value => ({ value, label: planLabel(value) }))]
})
const smartRotationState = computed(() => {
  if (!draft.enabled) return t('admin.modelPriority.smartRotationInactive')
  return draft.smart_rotation_enabled ? t('admin.modelPriority.on') : t('admin.modelPriority.off')
})
const smartRotationToggleDisabled = computed(() => loading.value || saving.value || !draft.enabled)
const smartRotationCooldownDisabled = computed(() => smartRotationToggleDisabled.value || !draft.smart_rotation_enabled)
const smartRotationCooldownOptions = computed(() => {
  const values = new Set(SMART_ROTATION_COOLDOWN_OPTIONS)
  if (isValidSmartRotationCooldown(draft.smart_rotation_cooldown_minutes)) values.add(draft.smart_rotation_cooldown_minutes)
  return Array.from(values)
    .sort((left, right) => left - right)
    .map(value => ({
      value,
      label: value === 1440
        ? t('admin.modelPriority.smartRotationCooldownDay')
        : t('admin.modelPriority.smartRotationCooldownMinutes', { count: value })
    }))
})

function isValidSmartRotationCooldown(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= 1440
}

function normalizeSmartRotationCooldown(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  return isValidSmartRotationCooldown(parsed) ? parsed : SMART_ROTATION_DEFAULT_COOLDOWN_MINUTES
}

function setSmartRotationCooldown(value: string | number | boolean | null): void {
  draft.smart_rotation_cooldown_minutes = normalizeSmartRotationCooldown(value)
}

function accountPlan(account: Account): string {
  const value = account.credentials?.plan_type ?? account.parent_plan_type ?? ''
  return typeof value === 'string' ? value.trim().toLowerCase() : ''
}

function planLabel(value: string): string {
  switch (value.toLowerCase()) {
    case 'plus': return 'Plus'
    case 'pro': case 'chatgpt_pro': return 'Pro'
    case 'team': return 'Team'
    case 'free': case 'basic': return 'Free'
    default: return value || '-'
  }
}

function accountPlanLabel(account: Account): string { return planLabel(accountPlan(account)) }
function accountCurrentlyAvailable(account: Account): boolean {
  if (account.status !== 'active' || !account.schedulable) return false
  const now = Date.now()
  for (const value of [account.rate_limit_reset_at, account.overload_until, account.temp_unschedulable_until]) {
    if (value && new Date(value).getTime() > now) return false
  }
  return true
}
function accountEmail(account: Account): string {
  const extra = account.extra as Record<string, unknown> | undefined
  const value = extra?.email_address ?? extra?.email ?? account.credentials?.email ?? account.parent_email ?? ''
  return typeof value === 'string' ? value : ''
}
function accountGroupNames(account: Account): string[] {
  const ids = account.group_ids ?? account.groups?.map(group => group.id) ?? []
  return ids.map(id => groupByID.value.get(id) ?? `#${id}`)
}
function accountPoolName(accountID: number): string { return poolByAccountID.value.get(accountID)?.name ?? '' }

const filteredAccounts = computed(() => {
  const search = filters.search.trim().toLowerCase()
  return accounts.value.filter(account => {
    if (search && !`${account.name} ${accountEmail(account)}`.toLowerCase().includes(search)) return false
    if (filters.type && account.type !== filters.type) return false
    if (filters.group && !(account.group_ids ?? account.groups?.map(group => group.id) ?? []).includes(Number(filters.group))) return false
    if (filters.pool && poolByAccountID.value.get(account.id)?.id !== Number(filters.pool)) return false
    if (filters.plan && accountPlan(account) !== filters.plan) return false
    if (filters.status === 'available' && !accountCurrentlyAvailable(account)) return false
    if (filters.status && filters.status !== 'available' && account.status !== filters.status) return false
    return true
  }).sort((left, right) => left.priority - right.priority || left.id - right.id)
})
const pageAccounts = computed(() => filteredAccounts.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
const pickerTitle = computed(() => {
  if (pickerUserRuleIndex.value !== null) {
    const rule = draft.user_rules[pickerUserRuleIndex.value]
    return t('admin.modelPriority.configureUserAccounts', { user: userName(rule?.user_id ?? 0) })
  }
  const index = pickerRuleIndex.value
  const model = index === null ? '' : draft.rules[index]?.model_pattern
  return model ? t('admin.modelPriority.configureModelAccounts', { model }) : t('admin.modelPriority.configureAccounts')
})
const pickerRule = computed(() => pickerUserRuleIndex.value !== null
  ? draft.user_rules[pickerUserRuleIndex.value] ?? null
  : pickerRuleIndex.value !== null ? draft.rules[pickerRuleIndex.value] ?? null : null)
const canSave = computed(() => {
  if (draft.enabled && draft.rules.length === 0) return false
  if (draft.user_enabled && draft.user_rules.length === 0) return false
  if (draft.user_rules.some(rule => rule.account_ids.length === 0)) return false
  const patterns = new Set<string>()
  return draft.rules.every(rule => {
    const pattern = rule.model_pattern.trim().toLowerCase()
    if (!pattern || patterns.has(pattern) || rule.account_ids.length === 0) return false
    patterns.add(pattern)
    return true
  })
})

watch(() => [filters.search, filters.group, filters.pool, filters.plan, filters.type, filters.status], () => { page.value = 1 })
watch(userSearch, (value, _, onCleanup) => {
  const sequence = ++userSearchSequence
  userResults.value = []
  userLoading.value = false
  if (!value.trim()) return
  userLoading.value = true
  const timer = window.setTimeout(async () => {
    try {
      const result = await adminAPI.users.list(1, 30, { search: value.trim() })
      if (sequence !== userSearchSequence) return
      userResults.value = result.items
      const next = new Map(usersByID.value)
      for (const user of result.items) next.set(user.id, user)
      usersByID.value = next
    } catch (error) {
      if (sequence === userSearchSequence) appStore.showError(extractApiErrorMessage(error, t('admin.modelPriority.userSearchFailed')))
    } finally {
      if (sequence === userSearchSequence) userLoading.value = false
    }
  }, 250)
  onCleanup(() => window.clearTimeout(timer))
})

function resetFilters(): void { Object.assign(filters, { search: '', group: '', pool: '', plan: '', type: '', status: '' }); page.value = 1 }
function addRule(): void { draft.rules.push({ key: ++ruleSequence, model_pattern: '', account_ids: [] }) }
function removeRule(index: number): void { draft.rules.splice(index, 1) }
function userName(userID: number): string { return usersByID.value.get(userID)?.username ?? t('admin.modelPriority.missingUser', { id: userID }) }
function addUserRule(user: AdminUser): void {
  if (draft.user_rules.some(rule => rule.user_id === user.id)) return
  draft.user_rules.push({ key: ++ruleSequence, user_id: user.id, account_ids: [], account_order: [] })
  userSearch.value = ''
}
function removeUserRule(index: number): void { draft.user_rules.splice(index, 1) }

async function loadSelectedUsers(): Promise<void> {
  const ids = draft.user_rules.map(rule => rule.user_id).filter(id => !usersByID.value.has(id))
  for (let start = 0; start < ids.length; start += 5) {
    const results = await Promise.allSettled(ids.slice(start, start + 5).map(id => adminAPI.users.getById(id)))
    const next = new Map(usersByID.value)
    for (const result of results) if (result.status === 'fulfilled') next.set(result.value.id, result.value)
    usersByID.value = next
  }
}
function toggleAccount(accountID: number): void {
  const next = new Set(pickerSelected.value)
  if (next.has(accountID)) {
    next.delete(accountID)
    pickerOrder.value = pickerOrder.value.filter(id => id !== accountID)
  } else {
    next.add(accountID)
    pickerOrder.value = [...pickerOrder.value, accountID]
  }
  pickerSelected.value = next
}
function selectFiltered(): void {
  const next = new Set(pickerSelected.value)
  const appended: number[] = []
  filteredAccounts.value.forEach(account => { if (!next.has(account.id)) appended.push(account.id); next.add(account.id) })
  pickerSelected.value = next
  pickerOrder.value = [...pickerOrder.value, ...appended]
}
function deselectFiltered(): void {
  const removed = new Set(filteredAccounts.value.map(account => account.id))
  pickerSelected.value = new Set([...pickerSelected.value].filter(id => !removed.has(id)))
  pickerOrder.value = pickerOrder.value.filter(id => !removed.has(id))
}
function clearPickerSelection(): void { pickerSelected.value = new Set(); pickerOrder.value = [] }
function toggleCurrentPage(event: Event): void {
  const checked = (event.target as HTMLInputElement).checked
  const next = new Set(pickerSelected.value)
  if (checked) {
    const appended: number[] = []
    pageAccounts.value.forEach(account => { if (!next.has(account.id)) appended.push(account.id); next.add(account.id) })
    pickerOrder.value = [...pickerOrder.value, ...appended]
  } else {
    const removed = new Set(pageAccounts.value.map(account => account.id))
    pageAccounts.value.forEach(account => next.delete(account.id))
    pickerOrder.value = pickerOrder.value.filter(id => !removed.has(id))
  }
  pickerSelected.value = next
}
function changePageSize(value: number): void { pageSize.value = value; page.value = 1 }
function selectedAccountName(accountID: number): string { return accountByID.value.get(accountID)?.name ?? t('admin.modelPriority.missingPriorityAccount', { id: accountID }) }
function movePickerAccount(index: number, offset: -1 | 1): void {
  const target = index + offset
  if (target < 0 || target >= pickerOrder.value.length) return
  const next = [...pickerOrder.value]
  ;[next[index], next[target]] = [next[target], next[index]]
  pickerOrder.value = next
}
function initialAccountOrder(rule: OpenAIModelPriorityRule | OpenAIUserPriorityRule): number[] {
  const selected = new Set(rule.account_ids)
  if (rule.account_order) {
    const ordered = rule.account_order.filter((id, index, values) => selected.has(id) && values.indexOf(id) === index)
    const included = new Set(ordered)
    return [...ordered, ...rule.account_ids.filter(id => !included.has(id))]
  }
  return [...rule.account_ids].sort((left, right) => {
    const leftPriority = accountByID.value.get(left)?.priority ?? Number.POSITIVE_INFINITY
    const rightPriority = accountByID.value.get(right)?.priority ?? Number.POSITIVE_INFINITY
    return leftPriority - rightPriority || left - right
  })
}

async function loadAccountCatalog(): Promise<void> {
  if (catalogLoaded.value || catalogLoading.value) return
  catalogLoading.value = true
  try {
    const [firstPage, loadedGroups, loadedPools] = await Promise.all([
      adminAPI.accounts.list(1, 200, { platform: 'openai', include_scheduler_score: '0', sort_by: 'id', sort_order: 'asc' }),
      adminAPI.groups.getAllIncludingInactive(),
      adminAPI.accountPools.list()
    ])
    const pages = firstPage.pages ?? Math.ceil(firstPage.total / (firstPage.page_size || 200))
    const remaining = pages > 1 ? await Promise.all(Array.from({ length: pages - 1 }, (_, index) => adminAPI.accounts.list(index + 2, 200, { platform: 'openai', include_scheduler_score: '0', sort_by: 'id', sort_order: 'asc' }))) : []
    const accountsByID = new Map<number, Account>()
    for (const account of [firstPage, ...remaining].flatMap(result => result.items)) accountsByID.set(account.id, account)
    accounts.value = Array.from(accountsByID.values())
    groups.value = loadedGroups
    pools.value = loadedPools.items
    catalogLoaded.value = true
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelPriority.modelPriorityAccountsLoadFailed')))
  } finally {
    catalogLoading.value = false
  }
}

async function openAccountPicker(index: number, kind: 'model' | 'user' = 'model'): Promise<void> {
  const ruleKey = (kind === 'user' ? draft.user_rules[index] : draft.rules[index])?.key
  if (ruleKey === undefined) return
  const requestSequence = ++pickerRequestSequence
  pickerRuleIndex.value = kind === 'model' ? index : null
  pickerUserRuleIndex.value = kind === 'user' ? index : null
  resetFilters()
  await loadAccountCatalog()
  const rule = pickerRule.value
  if (requestSequence !== pickerRequestSequence || rule?.key !== ruleKey) return
  pickerOrder.value = initialAccountOrder(rule)
  pickerSelected.value = new Set(pickerOrder.value)
}
function closeAccountPicker(): void { pickerRequestSequence++; pickerRuleIndex.value = null; pickerUserRuleIndex.value = null; pickerSelected.value = new Set(); pickerOrder.value = [] }
function applyAccountPicker(): void {
  if (pickerRule.value === null || pickerSelected.value.size === 0) return
  pickerRule.value.account_ids = [...pickerOrder.value]
  pickerRule.value.account_order = [...pickerOrder.value]
  closeAccountPicker()
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const settings = await adminAPI.settings.getOpenAIModelPrioritySettings()
    draft.enabled = settings.enabled
    draft.user_enabled = settings.user_enabled === true
    draft.user_rules = (settings.user_rules ?? []).map(rule => ({ key: ++ruleSequence, user_id: rule.user_id, account_ids: [...rule.account_ids], account_order: [...(rule.account_order ?? rule.account_ids)] }))
    void loadSelectedUsers()
    draft.smart_rotation_enabled = settings.smart_rotation_enabled === true
    draft.smart_rotation_cooldown_minutes = normalizeSmartRotationCooldown(settings.smart_rotation_cooldown_minutes)
    draft.rules = (settings.rules ?? []).map(rule => ({ key: ++ruleSequence, model_pattern: rule.model_pattern, account_ids: [...rule.account_ids], ...(rule.account_order ? { account_order: [...rule.account_order] } : {}) }))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelPriority.modelPriorityLoadFailed')))
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (!canSave.value) return
  saving.value = true
  try {
    const payload: OpenAIModelPrioritySettings = {
      enabled: draft.enabled,
      user_enabled: draft.user_enabled,
      user_rules: draft.user_rules.map(rule => ({ user_id: rule.user_id, account_ids: [...rule.account_ids], account_order: [...rule.account_order] })),
      smart_rotation_enabled: draft.smart_rotation_enabled,
      smart_rotation_cooldown_minutes: draft.smart_rotation_cooldown_minutes,
      rules: draft.rules.map(rule => ({ model_pattern: rule.model_pattern.trim(), account_ids: [...rule.account_ids], ...(rule.account_order ? { account_order: [...rule.account_order] } : {}) }))
    }
    const updated = await adminAPI.settings.updateOpenAIModelPrioritySettings(payload)
    draft.enabled = updated.enabled
    draft.user_enabled = updated.user_enabled === true
    draft.user_rules = (updated.user_rules ?? []).map(rule => ({ key: ++ruleSequence, user_id: rule.user_id, account_ids: [...rule.account_ids], account_order: [...rule.account_order] }))
    draft.smart_rotation_enabled = updated.smart_rotation_enabled ?? payload.smart_rotation_enabled ?? false
    draft.smart_rotation_cooldown_minutes = normalizeSmartRotationCooldown(updated.smart_rotation_cooldown_minutes ?? payload.smart_rotation_cooldown_minutes)
    draft.rules = updated.rules.map(rule => ({ key: ++ruleSequence, model_pattern: rule.model_pattern, account_ids: [...rule.account_ids], ...(rule.account_order ? { account_order: [...rule.account_order] } : {}) }))
    appStore.showSuccess(t('admin.modelPriority.modelPrioritySaveSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelPriority.modelPrioritySaveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(() => { void load(); void loadAccountCatalog() })
onBeforeUnmount(() => { userSearchSequence++; pickerRequestSequence++ })
</script>
