<template>
  <DarkVideoBackground blurred />
  <div class="app-layout openai-account-workbench mx-auto w-full min-w-0 space-y-4 p-3 sm:p-4 md:p-6" data-testid="openai-reauthorization-view">
    <header class="oauth-workbench-heading">
      <button type="button" class="oauth-icon-button oauth-workbench-back" title="返回账号管理" aria-label="返回账号管理" @click="backToAccounts">
        <Icon name="arrowLeft" size="sm" :stroke-width="2" />
      </button>
      <h1 class="text-2xl font-semibold text-gray-950 dark:text-white">XIASS工作台</h1>
    </header>

    <nav class="oauth-workbench-nav" aria-label="XIASS 工作台导航">
      <div class="oauth-workbench-primary">
        <div class="oauth-workbench-tablist" role="tablist" aria-label="OpenAI OAuth 账号工作台">
          <button
          type="button"
          role="tab"
          :aria-selected="activeWorkspace === 'batch'"
          class="workbench-tab"
          :class="activeWorkspace === 'batch' ? 'workbench-tab-active' : 'workbench-tab-idle'"
          data-testid="batch-workspace-tab"
          @click="activeWorkspace = 'batch'"
        >
          <Icon name="userPlus" size="sm" :stroke-width="2" />
          <span>批量添加</span>
        </button>
        <button
          type="button"
          role="tab"
          :aria-selected="activeWorkspace === 'reauthorization'"
          class="workbench-tab"
          :class="activeWorkspace === 'reauthorization' ? 'workbench-tab-active' : 'workbench-tab-idle'"
          data-testid="reauthorization-workspace-tab"
          @click="activeWorkspace = 'reauthorization'"
        >
          <span class="reauthorization-tab-icon" aria-hidden="true">
            <Icon name="shield" size="sm" class="reauthorization-tab-shield" :stroke-width="2" />
            <Icon name="refresh" size="xs" class="reauthorization-tab-refresh" :stroke-width="2.4" />
          </span>
          <span>401 重新授权</span>
          <span class="workbench-tab-count">{{ reauthorizationAccounts.length }}</span>
        </button>
        <button
          type="button"
          role="tab"
          :aria-selected="activeWorkspace === 'history'"
          class="workbench-tab"
          :class="activeWorkspace === 'history' ? 'workbench-tab-active' : 'workbench-tab-idle'"
          data-testid="authorization-history-workspace-tab"
          @click="activeWorkspace = 'history'"
        >
          <Icon name="clock" size="sm" :stroke-width="2" />
          <span>授权历史</span>
          <span class="workbench-tab-count">{{ historyAccounts.length }}</span>
        </button>
        <button
          type="button"
          role="tab"
          :aria-selected="activeWorkspace === 'credentials'"
          class="workbench-tab"
          :class="activeWorkspace === 'credentials' ? 'workbench-tab-active' : 'workbench-tab-idle'"
          data-testid="credential-library-workspace-tab"
          @click="activeWorkspace = 'credentials'"
          >
            <Icon name="key" size="sm" :stroke-width="2" />
            <span>账号库</span>
          </button>
        </div>
      </div>
      <div class="oauth-workbench-actions">
        <div class="authorization-mode-selector" role="radiogroup" aria-label="当前授权浏览器">
          <span class="authorization-mode-label">授权方式</span>
          <button
            type="button"
            role="radio"
            class="authorization-mode-option"
            :class="authorizationBrowserMode === 'server' ? 'authorization-mode-option-active' : ''"
            :aria-checked="authorizationBrowserMode === 'server'"
            data-testid="authorization-browser-mode-server"
            @click="setAuthorizationBrowserMode('server')"
          >
            <Icon name="server" size="sm" :stroke-width="2" />
            <span>内置浏览器</span>
            <span v-if="authorizationBrowserMode === 'server'" class="authorization-mode-current">当前</span>
          </button>
          <button
            type="button"
            role="radio"
            class="authorization-mode-option"
            :class="authorizationBrowserMode === 'adspower' ? 'authorization-mode-option-active' : ''"
            :aria-checked="authorizationBrowserMode === 'adspower'"
            data-testid="authorization-browser-mode-adspower"
            @click="setAuthorizationBrowserMode('adspower')"
          >
            <Icon name="globe" size="sm" :stroke-width="2" />
            <span>Ads 指纹浏览器</span>
            <span v-if="authorizationBrowserMode === 'adspower'" class="authorization-mode-current">当前</span>
          </button>
        </div>
        <button type="button" class="authorization-mode-settings" data-testid="adspower-helper-settings" title="安装和配置 Ads 指纹浏览器" @click="showAdsPowerSetup = true">
          <Icon name="cog" size="sm" :stroke-width="2" />
          <span>Ads 设置</span>
        </button>
      </div>
      <div class="team-child-entry">
        <button type="button" class="team-child-entry-button" data-testid="team-child-creation-entry" @click="openTeamChildCreation">
          <span class="team-child-entry-icon"><Icon name="users" size="sm" :stroke-width="2" /></span>
          <span>创建 Team 子号</span>
          <Icon name="arrowRight" size="xs" :stroke-width="2" />
        </button>
      </div>
    </nav>

    <div v-if="adsPowerHelperMissing" class="rounded-md border border-red-200 bg-red-50/95 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/80 dark:text-red-200" role="alert" data-testid="reauthorization-adspower-helper-missing">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p>本次 Ads 授权已停止：未检测到可用的 XIASS AdsPower 助手或 AdsPower。安装后请在“Ads 设置”填写 API Key，并使用与账号所属 XIASS 服务器出口 IP 一致的代理节点。</p>
        <div class="flex flex-wrap gap-2">
          <a class="btn btn-secondary btn-sm" :href="adsPowerHelperMacDownloadURL"><Icon name="download" size="sm" />macOS 安装包</a>
          <a class="btn btn-secondary btn-sm" :href="adsPowerHelperWindowsDownloadURL"><Icon name="download" size="sm" />Windows 安装包</a>
          <button type="button" class="btn btn-primary btn-sm" @click="showAdsPowerSetup = true"><Icon name="cog" size="sm" />Ads 设置</button>
        </div>
      </div>
    </div>

    <section v-if="activeWorkspace === 'reauthorization' || activeWorkspace === 'history'" class="oauth-workbench-filterbar" aria-label="授权账号筛选">
      <SearchInput
        v-model="workbenchSearch"
        class="min-w-0 flex-1 sm:max-w-sm"
        placeholder="搜索账号名称、邮箱、ID、节点或号池"
        data-testid="authorization-account-search"
      />
      <Select
        v-model="workbenchPoolFilter"
        class="w-full sm:w-48"
        :options="workbenchPoolOptions"
        data-testid="authorization-account-pool-filter"
      />
      <span class="oauth-filter-summary">显示 {{ activeWorkspace === 'history' ? filteredHistoryAccounts.length : filteredReauthorizationAccounts.length }} / {{ activeWorkspace === 'history' ? historyAccounts.length : reauthorizationAccounts.length }}</span>
      <button v-if="workbenchFiltersActive" type="button" class="btn btn-secondary btn-sm" data-testid="authorization-filter-reset" @click="resetWorkbenchFilters">
        <Icon name="x" size="sm" :stroke-width="2" />
        <span>清除筛选</span>
      </button>
    </section>

    <section v-show="activeWorkspace === 'batch'" class="oauth-workbench-surface min-w-0 overflow-hidden">
        <header class="oauth-module-header">
          <div class="flex items-center gap-2">
            <span class="oauth-module-icon"><Icon name="userPlus" size="sm" :stroke-width="2" /></span>
            <h2 class="text-base font-semibold text-gray-950 dark:text-white">批量添加账号</h2>
          </div>
        </header>
        <div v-if="batchOptionsError" class="border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/25 dark:text-red-300" role="alert">{{ batchOptionsError }}</div>
        <div v-if="batchOptionsLoading" class="flex min-h-48 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
          <Icon name="refresh" size="sm" class="animate-spin" />
          正在读取账号配置
        </div>
        <div v-else class="px-4 py-5 sm:px-5">
          <BatchOpenAIOAuthModal
            embedded
            :groups="groups"
            :proxies="proxies"
            :browser-mode="authorizationBrowserMode"
            :show-browser-mode-selector="false"
            @created="handleBatchAccountCreated"
          />
        </div>
    </section>

    <section v-if="activeWorkspace === 'reauthorization'" class="oauth-workbench-surface min-w-0 overflow-hidden">
        <header class="oauth-module-header flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div class="flex min-w-0 flex-wrap items-center gap-2.5">
            <div class="flex items-center gap-2">
              <span class="oauth-module-icon"><Icon name="refresh" size="sm" :stroke-width="2" /></span>
              <h2 class="text-base font-semibold text-gray-950 dark:text-white">401 重新授权</h2>
            </div>
            <span class="oauth-summary-chip"><b>{{ filteredReauthorizationAccounts.length }}</b> 待处理</span>
            <span class="oauth-summary-chip oauth-summary-chip-active"><b>{{ visibleActiveCount }}</b> 进行中</span>
            <span v-if="pendingCount" class="oauth-summary-chip oauth-summary-chip-warning"><b>{{ pendingCount }}</b> 高风险</span>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <button type="button" class="btn btn-secondary flex items-center gap-2" :disabled="loading || refreshing" @click="refreshAll">
              <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" :stroke-width="2" />
              <span>刷新</span>
            </button>
            <button type="button" class="btn btn-primary flex items-center gap-2" data-testid="reauthorize-all" :disabled="!batchStartableAccounts.length || startingAll" @click="requestStartAll">
              <Icon name="play" size="sm" :stroke-width="2" />
              <span>{{ startingAll ? '正在启动' : `一键授权${batchStartableAccounts.length ? ` (${batchStartableAccounts.length})` : ''}` }}</span>
            </button>
          </div>
        </header>

        <div v-if="operationNotice" class="border-b border-green-200 bg-green-50 px-4 py-3 text-sm text-green-700 dark:border-green-900/60 dark:bg-green-950/20 dark:text-green-300" role="status">{{ operationNotice }}</div>
        <div v-if="loadError" role="alert" class="border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/25 dark:text-red-300">{{ loadError }}</div>

        <div v-if="loading" class="flex min-h-48 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
          <Icon name="refresh" size="sm" class="animate-spin" />
          正在读取待授权账号
        </div>

        <div v-else-if="!filteredReauthorizationAccounts.length" class="oauth-empty-state">
          <Icon name="checkCircle" size="lg" class="text-green-500" :stroke-width="2" />
          <p>{{ reauthorizationAccounts.length ? '当前筛选下没有待重授权账号' : '当前没有待重授权账号' }}</p>
        </div>

        <div v-else class="oauth-account-list">
          <article v-for="account in filteredReauthorizationAccounts" :key="account.id" class="oauth-account-row" :data-testid="`reauthorization-account-${account.id}`">
            <div class="oauth-account-identity">
              <div class="flex min-w-0 items-center gap-2">
                <span class="oauth-account-avatar">{{ accountAvatar(account) }}</span>
                <div class="min-w-0">
                  <p class="truncate text-sm font-semibold text-gray-950 dark:text-white">{{ account.name }}</p>
                  <p class="truncate text-xs text-gray-500 dark:text-gray-400">#{{ account.id }} · {{ accountEmail(account) }}</p>
                </div>
              </div>
              <div class="mt-2 flex flex-wrap items-center gap-1.5">
                <span v-if="account.execution_node_id" class="oauth-mini-tag">{{ account.execution_node_id }}</span>
                <span class="oauth-mini-tag" :class="loginMethodClass(account)">{{ loginMethodLabel(account) }}</span>
                <span v-if="adsPowerBindingLabel(account)" class="oauth-mini-tag oauth-mini-tag-fingerprint">{{ adsPowerBindingLabel(account) }}</span>
              </div>
            </div>

            <div class="oauth-account-progress">
              <div class="flex min-w-0 items-center justify-between gap-3">
                <div class="flex min-w-0 items-center gap-2">
                  <span class="oauth-round-badge" :class="authorizationRoundClass(account)" :data-testid="`reauthorization-history-${account.id}`">{{ authorizationRoundLabel(account) }}</span>
                  <p class="truncate text-sm font-medium" :class="statusClass(account)">{{ statusLabel(account) }}</p>
                </div>
                <span class="shrink-0 text-xs text-gray-500 dark:text-gray-400">{{ elapsedText(account) }}</span>
              </div>
              <div class="oauth-progress-track">
                <div class="h-full rounded-full transition-[width] duration-300" :class="progressClass(account)" :style="{ width: `${progressPercent(account)}%` }" />
              </div>
              <div class="mt-1.5 flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ stepLabel(account) }}</p>
              </div>
              <p v-if="reauthorizationRowNotice(account)" class="mt-1.5 break-words text-xs font-semibold" :class="reauthorizationRowNoticeClass(account)" :data-testid="statusFor(account)?.risk_level === 'cooldown' ? `reauthorization-cooldown-${account.id}` : undefined">
                {{ reauthorizationRowNotice(account) }}
              </p>
              <p v-if="failureText(account)" class="mt-1 break-words text-sm text-red-600 dark:text-red-400" role="alert">{{ failureText(account) }}</p>
            </div>

            <div class="oauth-account-actions">
              <button v-if="canRecoverAccountState(account)" type="button" class="btn btn-secondary btn-sm flex items-center gap-1.5" :disabled="busyAccountIDs.has(account.id)" :data-testid="`recover-reauthorization-account-${account.id}`" @click="recoverAccountState(account)">
                <Icon name="refresh" size="sm" :class="busyAccountIDs.has(account.id) ? 'animate-spin' : ''" :stroke-width="2" />
                <span>恢复状态</span>
              </button>
              <button v-if="isActive(account)" type="button" class="btn btn-secondary btn-sm flex items-center gap-1.5" :disabled="busyAccountIDs.has(account.id)" @click="stopAccount(account)">
                <Icon name="x" size="sm" :stroke-width="2" />
                <span>停止</span>
              </button>
              <button v-else-if="!accountLoginMethod(account)" type="button" class="btn btn-secondary btn-sm flex items-center gap-1.5" @click="activeWorkspace = 'credentials'">
                <Icon name="key" size="sm" :stroke-width="2" />
                <span>补充登录资料</span>
              </button>
              <button v-else-if="canStart(account)" type="button" class="btn btn-primary btn-sm flex items-center gap-1.5" :class="statusFor(account)?.requires_risk_confirmation ? 'btn-danger' : ''" :disabled="busyAccountIDs.has(account.id) || activeCount >= maxConcurrency" :data-testid="`start-reauthorization-${account.id}`" @click="requestStartAccount(account)">
                <Icon name="play" size="sm" :class="busyAccountIDs.has(account.id) ? 'animate-pulse' : ''" :stroke-width="2" />
                <span>{{ retryLabel(account) }}</span>
              </button>
              <span v-else-if="taskFor(account)?.status === 'completed'" class="flex items-center gap-1.5 text-sm font-medium text-green-600 dark:text-green-400">
                <Icon name="check" size="sm" :stroke-width="2.5" />授权成功
              </span>
              <button type="button" class="oauth-delete-button" title="从账号管理和账号库完整删除" :aria-label="`删除账号 ${account.name}`" :disabled="deletingAccountIDs.has(account.id)" :data-testid="`delete-reauthorization-account-${account.id}`" @click="requestDeleteAccount(account)">
                <Icon name="trash" size="sm" :class="deletingAccountIDs.has(account.id) ? 'animate-pulse' : ''" :stroke-width="2" />
              </button>
            </div>
          </article>
        </div>
    </section>

    <section v-if="activeWorkspace === 'history'" class="oauth-workbench-surface min-w-0 overflow-hidden" data-testid="authorization-history-module">
      <header class="oauth-module-header flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div class="flex items-center gap-2">
          <span class="oauth-module-icon"><Icon name="clock" size="sm" :stroke-width="2" /></span>
          <h2 class="text-base font-semibold text-gray-950 dark:text-white">授权历史</h2>
          <span class="oauth-summary-chip"><b>{{ historyAccounts.length }}</b> 条</span>
        </div>
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <div class="oauth-history-filters" role="group" aria-label="授权次数筛选">
            <button
              v-for="option in historyFilterOptions"
              :key="option.value"
              type="button"
              class="oauth-history-filter"
              :class="historyFilter === option.value ? 'oauth-history-filter-active' : ''"
              :aria-pressed="historyFilter === option.value"
              :data-testid="`history-filter-${option.value}`"
              @click="historyFilter = option.value"
            >
              <span>{{ option.label }}</span>
              <b>{{ historyFilterCount(option.value) }}</b>
            </button>
          </div>
          <button type="button" class="oauth-icon-button" title="刷新授权历史" aria-label="刷新授权历史" :disabled="loading || refreshing" @click="refreshAll">
            <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" :stroke-width="2" />
          </button>
        </div>
      </header>

      <div v-if="loadError" role="alert" class="border-b border-red-200 bg-red-50/80 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/25 dark:text-red-300">{{ loadError }}</div>
      <div v-if="loading" class="flex min-h-48 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="refresh" size="sm" class="animate-spin" />
        正在读取授权历史
      </div>
      <div v-else-if="!filteredHistoryAccounts.length" class="oauth-empty-state">
        <Icon name="clock" size="lg" class="text-gray-400" :stroke-width="2" />
        <p>{{ historyAccounts.length ? '当前筛选下没有账号' : '暂无授权历史' }}</p>
      </div>
      <div v-else class="oauth-history-list">
        <article v-for="account in filteredHistoryAccounts" :key="account.id" class="oauth-history-row" :data-testid="`authorization-history-account-${account.id}`">
          <div class="oauth-account-identity">
            <div class="flex min-w-0 items-center gap-2">
              <span class="oauth-account-avatar">{{ accountAvatar(account) }}</span>
              <div class="min-w-0">
                <p class="truncate text-sm font-semibold text-gray-950 dark:text-white">{{ account.name }}</p>
                <p class="truncate text-xs text-gray-500 dark:text-gray-400">#{{ account.id }} · {{ accountEmail(account) }}</p>
              </div>
            </div>
          </div>
          <div class="min-w-0">
            <div class="space-y-1.5">
              <div v-for="(entry, index) in historyAuthorizationTimeline(account)" :key="`${entry.label}-${entry.time}`" class="flex min-w-0 flex-wrap items-center gap-2">
                <span class="oauth-round-badge" :class="historyResultClass(account)">{{ entry.label }}</span>
                <time v-if="entry.time" class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ entry.time }}</time>
                <span v-if="index === historyAuthorizationTimeline(account).length - 1 && historyPendingAuthorizationLabel(account)" class="text-xs font-semibold text-red-600 dark:text-red-300" :data-testid="`history-pending-authorization-${account.id}`">
                  {{ historyPendingAuthorizationLabel(account) }}
                </span>
              </div>
            </div>
            <p v-if="historyNextAuthorizationLabel(account)" class="mt-1 text-xs font-semibold text-amber-700 dark:text-amber-300">{{ historyNextAuthorizationLabel(account) }}</p>
          </div>
          <div class="flex items-center justify-end gap-2">
            <span class="oauth-history-result" :class="historyResultClass(account)">{{ historyResultLabel(account) }}</span>
            <button type="button" class="oauth-delete-button" title="从账号管理和账号库完整删除" :aria-label="`删除账号 ${account.name}`" :disabled="deletingAccountIDs.has(account.id)" :data-testid="`delete-history-account-${account.id}`" @click="requestDeleteAccount(account)">
              <Icon name="trash" size="sm" :class="deletingAccountIDs.has(account.id) ? 'animate-pulse' : ''" :stroke-width="2" />
            </button>
          </div>
        </article>
      </div>
    </section>

    <section v-show="activeWorkspace === 'credentials'" class="oauth-workbench-surface min-w-0 overflow-hidden">
      <OpenAIOAuthCredentialLibraryPanel
        :active="activeWorkspace === 'credentials'"
        @updated="handleCredentialLibraryUpdated"
      />
    </section>

    <ConfirmDialog
      :show="Boolean(pendingDeleteAccount)"
      title="删除 401 账号"
      :message="deleteConfirmationMessage"
      confirm-text="删除账号"
      cancel-text="取消"
      danger
      @confirm="confirmDeleteAccount"
      @cancel="pendingDeleteAccount = null"
    />
    <ConfirmDialog
      :show="Boolean(pendingAuthorization)"
      :title="authorizationConfirmationTitle"
      :message="authorizationConfirmationMessage"
      :confirm-text="authorizationConfirmationButton"
      cancel-text="取消"
      :danger="authorizationConfirmationDanger"
      @confirm="confirmAuthorization"
      @cancel="pendingAuthorization = null"
    />
    <AdsPowerHelperSetupDialog
      :show="showAdsPowerSetup"
      :server-origin="adsPowerServerOrigin"
      :environment-key="adsPowerEnvironmentKey"
      @close="showAdsPowerSetup = false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Icon } from '@/components/icons'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DarkVideoBackground from '@/components/common/DarkVideoBackground.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import BatchOpenAIOAuthModal from '@/components/account/BatchOpenAIOAuthModal.vue'
import AdsPowerHelperSetupDialog from '@/components/admin/account/AdsPowerHelperSetupDialog.vue'
import OpenAIOAuthCredentialLibraryPanel from '@/components/admin/account/OpenAIOAuthCredentialLibraryPanel.vue'
import { accountsAPI, executionNodesAPI, groupsAPI, proxiesAPI } from '@/api/admin'
import { accountPoolsAPI, buildAccountPoolLookup, type AccountPool } from '@/api/admin/accountPools'
import {
  openAIReauthorizationAPI,
  type OpenAIReauthorizationAccountStatus,
  type OpenAIReauthorizationTask,
} from '@/api/admin/openaiReauthorization'
import type { Account, AdminGroup, Proxy } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  adsPowerHelperMacDownloadURL,
  adsPowerHelperUnavailableMessage,
  adsPowerHelperWindowsDownloadURL,
  isAdsPowerHelperAvailable,
} from '@/utils/adspowerHelper'

const route = useRoute()
const router = useRouter()
const accounts = ref<Account[]>([])
const accountStatuses = ref<OpenAIReauthorizationAccountStatus[]>([])
const tasks = ref<OpenAIReauthorizationTask[]>([])
const loading = ref(true)
const refreshing = ref(false)
const startingAll = ref(false)
const loadError = ref('')
const maxConcurrency = ref(3)
const maxRestarts = ref(2)
const now = ref(Date.now())
const queuedAccountIDs = ref<number[]>([])
const busyAccountIDs = ref(new Set<number>())
const deletingAccountIDs = ref(new Set<number>())
const localErrors = ref(new Map<number, string>())
const groups = ref<AdminGroup[]>([])
const proxies = ref<Proxy[]>([])
const accountPools = ref<AccountPool[]>([])
const batchOptionsLoading = ref(true)
const batchOptionsError = ref('')
const operationNotice = ref('')
const pendingDeleteAccount = ref<Account | null>(null)
const workbenchSearch = ref('')
const workbenchPoolFilter = ref('')
const showAdsPowerSetup = ref(false)
const adsPowerHelperMissing = ref(false)
const adsPowerServerOrigin = window.location.origin
const adsPowerEnvironmentKey = ref(defaultAdsPowerEnvironmentKey())
type AuthorizationBrowserMode = 'server' | 'adspower'
const authorizationBrowserMode = ref<AuthorizationBrowserMode>('server')
const queuedAuthorizationBrowserMode = ref<AuthorizationBrowserMode>('server')
const pendingAuthorization = ref<{ accounts: Account[]; batch: boolean; browserMode: AuthorizationBrowserMode } | null>(null)
type WorkbenchWorkspace = 'batch' | 'reauthorization' | 'history' | 'credentials'
type HistoryFilter = 'all' | 'unauthorized' | 'first' | 'second'
const activeWorkspace = ref<WorkbenchWorkspace>(initialWorkspace())
const historyFilter = ref<HistoryFilter>('all')
const historyFilterOptions: Array<{ value: HistoryFilter; label: string }> = [
  { value: 'all', label: '全部' },
  { value: 'unauthorized', label: '未授权' },
  { value: 'first', label: '第一次授权' },
  { value: 'second', label: '第二次授权' },
]
const completingTaskIDs = new Set<string>()
const smsTaskIDs = new Set<string>()
const historyRefreshedTaskEvents = new Set<string>()
let pollTimer: ReturnType<typeof setTimeout> | null = null
let disposed = false

const activeStatuses = new Set(['queued', 'running', 'ready'])
const terminalFailureStatuses = new Set(['failed', 'blocked', 'canceled'])
const restrictionReasons = new Set(['account_banned', 'account_deleted_or_disabled'])

function initialWorkspace(): WorkbenchWorkspace {
  const raw = Array.isArray(route.query.workspace) ? route.query.workspace[0] : route.query.workspace
  if (raw === 'batch' || raw === 'reauthorization' || raw === 'history' || raw === 'credentials') return raw
  return 'reauthorization'
}

function defaultAdsPowerEnvironmentKey(): string {
  const hostname = window.location.hostname.toLowerCase()
  if (hostname.startsWith('api2.')) return 'api2'
  if (hostname.startsWith('api.')) return 'api'
  return 'api'
}

const taskByAccountID = computed(() => {
  const result = new Map<number, OpenAIReauthorizationTask>()
  for (const task of tasks.value) {
    if (!task.target_account_id) continue
    const current = result.get(task.target_account_id)
    if (!current || Date.parse(task.created_at) >= Date.parse(current.created_at)) result.set(task.target_account_id, task)
  }
  return result
})

const statusByAccountID = computed(() => new Map(accountStatuses.value.map(item => [item.account.id, item])))
const accountPoolLookup = computed(() => buildAccountPoolLookup(accountPools.value))
const workbenchPoolOptions = computed(() => [
  { value: '', label: '全部号池' },
  { value: '__none__', label: '未加入号池' },
  ...accountPools.value.map(pool => ({ value: String(pool.id), label: `${pool.name} (${pool.account_count})` })),
])
const workbenchFiltersActive = computed(() => workbenchSearch.value.trim() !== '' || workbenchPoolFilter.value !== '')

const activeCount = computed(() => tasks.value.filter(task => activeStatuses.has(task.status)).length + busyAccountIDs.value.size)
const reauthorizationAccounts = computed(() => accounts.value
  .filter(account => {
    const status = statusFor(account)
    const task = taskFor(account)
    return Boolean(status?.current_needs_reauthorization || (task && activeStatuses.has(task.status)) || localErrors.value.has(account.id))
  })
  .sort((left, right) => {
    const rank = (account: Account) => {
      const task = taskFor(account)
      if (task && activeStatuses.has(task.status)) return 0
      const risk = statusFor(account)?.risk_level
      if (risk === 'blocked') return 2
      if (risk === 'cooldown' || risk === 'repeated' || risk === 'unknown') return 1
      return 0
    }
    return rank(left) - rank(right) || left.id - right.id
  }))
const filteredReauthorizationAccounts = computed(() => reauthorizationAccounts.value.filter(workbenchAccountMatches))
const historyAccounts = computed(() => accountStatuses.value
  .filter(status => status.has_history || status.has_attempted || status.attempt_count > 0 || status.success_count > 0 || Boolean(status.last_result))
  .sort((left, right) => historyTimestamp(right) - historyTimestamp(left) || right.account.id - left.account.id)
  .map(status => status.account))
const filterMatchedHistoryAccounts = computed(() => historyAccounts.value.filter(workbenchAccountMatches))
const filteredHistoryAccounts = computed(() => historyFilter.value === 'all'
  ? filterMatchedHistoryAccounts.value
  : filterMatchedHistoryAccounts.value.filter(account => historyAuthorizationBucket(account) === historyFilter.value))
const visibleActiveCount = computed(() => filteredReauthorizationAccounts.value.filter(account => activeStatuses.has(taskFor(account)?.status || '')).length)
const pendingCount = computed(() =>
  filteredReauthorizationAccounts.value.filter(account => {
    const risk = statusFor(account)?.risk_level
    return risk === 'cooldown' || risk === 'repeated' || risk === 'blocked' || risk === 'unknown'
  }).length
)
const batchStartableAccounts = computed(() => filteredReauthorizationAccounts.value.filter(account => canStart(account) && statusFor(account)?.risk_level === 'first'))

const deleteConfirmationMessage = computed(() => {
  const account = pendingDeleteAccount.value
  if (!account) return ''
  return `#${account.id} ${account.name} 将从账号管理中完整删除。所属分组、账号登录资料中的邮箱、密码、2FA 和邮箱验证码 Token 会同时删除，此操作不可撤销。`
})

const authorizationConfirmationDanger = computed(() => {
  const pending = pendingAuthorization.value
  return Boolean(pending && !pending.batch && statusFor(pending.accounts[0])?.requires_risk_confirmation)
})

const authorizationConfirmationTitle = computed(() => {
  const pending = pendingAuthorization.value
  if (!pending) return ''
  if (pending.batch) return `确认批量授权 ${pending.accounts.length} 个账号`
  const status = statusFor(pending.accounts[0])
  if (status?.risk_level === 'cooldown') return '高风险：冷静期内再次授权'
  if (status?.risk_level === 'blocked') return '账号已停用或封禁'
  return status?.requires_risk_confirmation ? '高风险：再次 401 重新授权' : '确认首次 401 重新授权'
})

const authorizationConfirmationMessage = computed(() => {
  const pending = pendingAuthorization.value
  if (!pending) return ''
  const browserLabel = pending.browserMode === 'adspower' ? 'Ads 指纹浏览器' : '内置浏览器'
  if (pending.batch) {
    return `本次使用 ${browserLabel}。即将启动 ${pending.accounts.length} 个尚未成功重授权过的 401 账号。系统最多同时处理 ${maxConcurrency.value} 个，不会把第二次掉授权的高风险账号加入批量队列。`
  }
  const account = pending.accounts[0]
  const status = statusFor(account)
  const browserNote = pending.browserMode === 'adspower'
    ? ` 本次使用 ${browserLabel}，并会打开该账号固定的 AdsPower 环境。`
    : ` 本次使用 ${browserLabel}。`
  if (status?.risk_level === 'cooldown') {
    const nextAt = nextAuthorizationAt(status)
    const recommendation = nextAt ? `，建议等到 ${formatHistoryDate(nextAt)}` : ''
    return `#${account.id} ${account.name} 仍在 7 天冷静期内，剩余 ${formatDuration(status.cooldown_remaining_seconds)}${recommendation}。现在继续会强制启动第 ${status.current_authorization_number} 次授权，存在较高封号风险；仅在你已确认风险时继续。${browserNote}`
  }
  if (status?.risk_level === 'blocked') {
    return `#${account.id} ${account.name} 的 OpenAI 页面已明确显示“${accountRestrictionLabel(account)}”，不能重新授权。${browserNote}`
  }
  if (status?.requires_risk_confirmation) {
    return `#${account.id} ${account.name} 已经成功进行过 401 重新授权，现在是第 ${status.current_authorization_number} 次掉授权。建议不要再授权，继续可能导致封号。仅在你已确认风险时继续。${browserNote}`
  }
  return `#${account.id} ${account.name} 将开始第一次 401 重新授权。授权结果、失败原因和是否封号都会保存在本工作台。${browserNote}`
})

const authorizationConfirmationButton = computed(() => {
  if (pendingAuthorization.value?.browserMode === 'adspower') return authorizationConfirmationDanger.value ? '我已知风险，打开固定环境' : '确认并打开固定环境'
  return authorizationConfirmationDanger.value ? '我已知风险，继续授权' : '确认开始'
})

function queryAccountIDs(): number[] {
  const raw = Array.isArray(route.query.account_ids) ? route.query.account_ids[0] : route.query.account_ids
  if (typeof raw !== 'string') return []
  return [...new Set(raw.split(',').map(value => Number(value.trim())).filter(Number.isSafeInteger).filter(value => value > 0))]
}

async function loadAccounts() {
  const result = await openAIReauthorizationAPI.accounts(queryAccountIDs())
  accountStatuses.value = result.items.map(normalizeUntrackedAuthorizationStatus)
  accounts.value = accountStatuses.value.map(item => item.account)
}

function normalizeUntrackedAuthorizationStatus(status: OpenAIReauthorizationAccountStatus): OpenAIReauthorizationAccountStatus {
  if (status.last_result !== 'legacy_unknown' || status.success_count > 0 || status.attempt_count > 0) return status
  return {
    ...status,
    has_history: false,
    has_attempted: false,
    last_result: '',
    last_reason: '',
    history_source: 'xiass_tracking',
    history_confidence: 'exact',
    can_start: status.current_needs_reauthorization,
    requires_risk_confirmation: false,
    risk_level: status.current_needs_reauthorization ? 'first' : 'history',
  }
}

async function loadBatchOptions() {
  batchOptionsLoading.value = true
  batchOptionsError.value = ''
  try {
    const [availableGroups, availableProxies, availablePools] = await Promise.all([
      groupsAPI.getAll('openai'),
      proxiesAPI.getAll(),
      accountPoolsAPI.list(),
    ])
    groups.value = availableGroups
    proxies.value = availableProxies
    accountPools.value = availablePools.items
  } catch (error) {
    batchOptionsError.value = extractApiErrorMessage(error, '读取分组或代理配置失败。')
  } finally {
    batchOptionsLoading.value = false
  }
}

async function loadAdsPowerEnvironment() {
  try {
    const status = await executionNodesAPI.getStatus()
    const nodeID = String(status.runtime?.node_id || status.runtime?.legacy_unassigned_node_id || '').trim()
    if (nodeID) adsPowerEnvironmentKey.value = nodeID
  } catch {
    // The built-in authorization path remains available when node metadata cannot be read.
  }
}

function workbenchAccountMatches(account: Account): boolean {
  const pool = accountPoolLookup.value[String(account.id)]
  if (workbenchPoolFilter.value === '__none__') {
    if (pool) return false
  } else if (workbenchPoolFilter.value && pool?.id !== Number(workbenchPoolFilter.value)) {
    return false
  }
  const query = workbenchSearch.value.trim().toLowerCase()
  if (!query) return true
  const values = [
    account.id,
    `#${account.id}`,
    account.name,
    accountEmail(account),
    account.notes || '',
    account.execution_node_id || '',
    pool?.name || '',
  ]
  return values.some(value => String(value).toLowerCase().includes(query))
}

function resetWorkbenchFilters() {
  workbenchSearch.value = ''
  workbenchPoolFilter.value = ''
}

function taskFor(account: Account): OpenAIReauthorizationTask | undefined {
  const task = taskByAccountID.value.get(account.id)
  const status = statusByAccountID.value.get(account.id)
  // Completed tasks remain in the process-local store for up to 24 hours. If
  // the account has since returned to 401, that success belongs to the previous
  // authorization round and must not replace the current pending state.
  if (task?.status === 'completed' && status?.current_needs_reauthorization) return undefined
  return task
}

function statusFor(account: Account): OpenAIReauthorizationAccountStatus | undefined {
  return statusByAccountID.value.get(account.id)
}

function accountEmail(account: Account): string {
  const email = typeof account.credentials?.email === 'string' ? account.credentials.email.trim() : ''
  return email || account.name
}

function accountAvatar(account: Account): string {
  const source = (account.name || accountEmail(account)).trim()
  return (source.match(/[a-z0-9]/i)?.[0] || source.charAt(0) || '?').toUpperCase()
}

function adsPowerBindingLabel(account: Account): string {
  const value = (account.extra as Record<string, unknown> | undefined)?.xiass_openai_adspower_binding
  if (!value || typeof value !== 'object') return ''
  const binding = value as Record<string, unknown>
  const environment = typeof binding.environment_key === 'string' ? binding.environment_key.trim() : ''
  const profileNo = typeof binding.profile_no === 'string' ? binding.profile_no.trim() : ''
  if (!environment) return ''
  return `固定环境${profileNo ? ` #${profileNo}` : ''} · ${environment}`
}

function accountLoginMethod(account: Account): 'password' | 'email_code' | '' {
  const status = account.credentials_status
  if (status?.has_xiass_openai_oauth_reauth_email !== true) return ''
  if (status?.has_xiass_openai_oauth_reauth_email_code_token_encrypted === true) return 'email_code'
  if (status?.has_xiass_openai_oauth_reauth_password_encrypted === true) return 'password'
  return ''
}

function loginMethodLabel(account: Account): string {
  const method = accountLoginMethod(account)
  if (method === 'email_code') return '邮箱验证码'
  if (method === 'password') return '密码 + 2FA'
  return '未保存登录资料'
}

function loginMethodClass(account: Account): string {
  return accountLoginMethod(account)
    ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/30 dark:text-emerald-300'
    : 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-300'
}

function canStart(account: Account): boolean {
  if (busyAccountIDs.value.has(account.id)) return false
  if (!accountLoginMethod(account)) return false
  const status = statusFor(account)
  if (!status) return false
  if (status.risk_level === 'blocked') return false
  if (!status.can_start && status.risk_level !== 'cooldown') return false
  const task = taskFor(account)
  if (!task) return true
  if (task.account_id) return task.reason === 'account_state_recovery_failed'
  if (task.status === 'completed' || activeStatuses.has(task.status)) return false
	return true
}

function canRecoverAccountState(account: Account): boolean {
  if (busyAccountIDs.value.has(account.id) || isActive(account)) return false
  const status = statusFor(account)
  if (!status || status.risk_level === 'blocked') return false
  const task = taskFor(account)
  return account.status === 'error' || Boolean(task && terminalFailureStatuses.has(task.status))
}

async function recoverAccountState(account: Account) {
  if (!canRecoverAccountState(account)) return
  setBusy(account.id, true)
  try {
    await accountsAPI.clearError(account.id)
    operationNotice.value = `#${account.id} ${account.name} 的可恢复错误状态已清理。`
    const next = new Map(localErrors.value)
    next.delete(account.id)
    localErrors.value = next
  } catch (error) {
    const next = new Map(localErrors.value)
    next.set(account.id, extractApiErrorMessage(error, '恢复账号状态失败。'))
    localErrors.value = next
  } finally {
    setBusy(account.id, false)
    await Promise.all([syncTasks(false), loadAccounts()])
  }
}

function isActive(account: Account): boolean {
  const task = taskFor(account)
  return Boolean(task && activeStatuses.has(task.status))
}

function retryLabel(account: Account): string {
  const task = taskFor(account)
  if (busyAccountIDs.value.has(account.id)) return '正在启动'
  if (task?.reason === 'account_state_recovery_failed') return '重试恢复状态'
  if (statusFor(account)?.risk_level === 'cooldown' || statusFor(account)?.risk_level === 'blocked' || statusFor(account)?.requires_risk_confirmation) return '继续授权'
  return task && terminalFailureStatuses.has(task.status) ? '重试本次授权' : '开始授权'
}

const stageDetails: Record<string, { step: number; label: string }> = {
  queued: { step: 1, label: '等待独立隐私上下文' },
  external_browser: { step: 1, label: '等待 Ads 指纹浏览器完成登录，成功后自动关闭' },
  opening: { step: 1, label: '打开 OpenAI OAuth 授权页' },
  login: { step: 1, label: '进入 OpenAI 登录' },
  email: { step: 2, label: '填写登录邮箱' },
  password: { step: 3, label: '填写保存的登录密码' },
  totp: { step: 4, label: '填写实时 2FA 验证码' },
  email_code_waiting: { step: 3, label: '查询邮箱验证码' },
  email_code_submitting: { step: 3, label: '提交邮箱验证码' },
  phone_required: { step: 4, label: 'OpenAI 要求额外手机号验证' },
  phone_submitting: { step: 4, label: '提交手机号' },
  sms_waiting: { step: 4, label: '等待短信验证码' },
  sms_submitting: { step: 4, label: '提交短信验证码' },
  profile: { step: 5, label: '填写姓名和年龄' },
  workspace: { step: 6, label: '确认个人工作空间并点击继续' },
  callback_waiting: { step: 7, label: '等待 localhost OAuth 回调' },
  callback_received: { step: 7, label: '已取得 OAuth 回调' },
  completed: { step: 8, label: '新 OAuth 凭据已保存到原账号' },
  failed: { step: 8, label: '授权任务失败' },
  blocked: { step: 8, label: 'OpenAI 阻止了当前授权' },
  canceled: { step: 8, label: '任务已停止' }
}

const reasonLabels: Record<string, string> = {
  invalid_credentials: '邮箱、密码或登录后的账号身份未通过验证。',
  invalid_totp: '2FA 验证码未通过验证。',
  authenticator_required: 'OpenAI 要求 2FA，但该账号没有可用的已保存密钥。',
  account_blocked: '未知错误，可以恢复状态后重试。',
  account_banned: 'OpenAI 页面明确显示账号已封禁，不能重新授权。',
  account_deleted_or_disabled: 'OpenAI 页面明确显示账号已删除或停用，系统不会自动重试。',
  unknown_error: '未知错误，可以恢复状态后重试。',
  captcha_required: 'OpenAI 要求完成人机验证。',
  email_code_required: 'OpenAI 要求邮箱验证码，当前自动流程未继续。',
  email_code_timeout: '等待邮箱验证码超过 2 分钟，任务已停止。',
  email_code_access_denied: '保存的邮箱验证码 Token 已失效或与邮箱不匹配。',
  email_code_unavailable: '邮箱验证码服务暂时不可用，任务已停止。',
  invalid_email_code: 'OpenAI 拒绝了邮箱验证码。',
  reauthorization_phone_required: '旧版助手未继续处理手机号验证，请使用最新版重新授权。',
  proxy_unavailable: '账号代理无法用于浏览器授权。',
  navigation_timeout: '打开 OpenAI 授权页超时。',
  browser_context_lost: '独立隐私浏览器上下文意外关闭。',
  page_interaction_failed: 'OpenAI 页面控件发生变化或操作失败。',
  sms_channel_selection_failed: '无法确认已选择短信验证，未继续发送验证码。',
  oauth_exchange_failed: 'OAuth 回调换取凭据失败。',
  oauth_identity_mismatch: '授权完成后的 OpenAI 身份与目标账号不一致。',
  account_update_failed: 'OAuth 已完成，但凭据未能保存到原账号。',
  account_configuration_changed: '授权期间账号的所属服务器或代理发生变化，请按当前配置重新授权。',
  account_state_recovery_failed: '新 OAuth 凭据已保存，但账号错误状态尚未清理，请重试恢复状态。',
  sms_timeout: '等待短信验证码超过 3 分钟。',
  sms_confirmation_timeout: '等待手机号处理超时。',
  phone_rejected: 'OpenAI 拒绝了当前手机号。',
  task_expired: '授权任务已过期。',
  manual_challenge: 'OpenAI 页面出现了当前自动化未识别的验证步骤。',
  reauthorization_history_unavailable: '401 重新授权历史无法保存，系统已为安全停止任务。'
}

function statusLabel(account: Account): string {
  if (localErrors.value.has(account.id)) return '启动失败'
  const task = taskFor(account)
  if (!task) return '等待授权'
  if (task.status === 'completed') return '授权成功'
  if (task.status === 'failed' || task.status === 'blocked') return '授权失败'
  if (task.status === 'canceled') return '已停止'
  return stageDetails[task.stage]?.label || '正在授权'
}

function stepLabel(account: Account): string {
  const task = taskFor(account)
  if (!task) return '尚未启动'
  const detail = stageDetails[task.stage] || stageDetails[task.status]
  return detail ? `第 ${Math.min(detail.step, 8)}/8 步 · ${detail.label}` : '正在读取实际授权状态'
}

function failureText(account: Account): string {
  const local = localErrors.value.get(account.id)
  if (local) return local
  const task = taskFor(account)
  if (!task || !terminalFailureStatuses.has(task.status)) return ''
  return reasonLabels[task.reason || ''] || 'OpenAI OAuth 重新授权未完成。'
}

function progressPercent(account: Account): number {
  const task = taskFor(account)
  if (!task) return 0
  if (task.status === 'completed') return 100
  const detail = stageDetails[task.stage] || stageDetails[task.status]
  return detail ? Math.max(8, Math.min(100, Math.round(detail.step / 8 * 100))) : 8
}

function progressClass(account: Account): string {
  const task = taskFor(account)
  if (localErrors.value.has(account.id) || (task && terminalFailureStatuses.has(task.status))) return 'bg-red-500'
  if (task?.status === 'completed') return 'bg-green-500'
  return 'bg-primary-500'
}

function statusClass(account: Account): string {
  const task = taskFor(account)
  if (localErrors.value.has(account.id) || (task && terminalFailureStatuses.has(task.status))) return 'text-red-600 dark:text-red-400'
  if (task?.status === 'completed') return 'text-green-600 dark:text-green-400'
  if (task && activeStatuses.has(task.status)) return 'text-primary-600 dark:text-primary-400'
  return 'text-gray-800 dark:text-gray-200'
}

function elapsedText(account: Account): string {
  const task = taskFor(account)
  if (!task?.created_at) return '0 秒'
  const end = task.finished_at ? Date.parse(task.finished_at) : now.value
  const start = Date.parse(task.created_at)
  return `${Math.max(0, Math.round((end - start) / 1000))} 秒`
}

function formatDuration(totalSeconds: number): string {
  const seconds = Math.max(0, Math.floor(totalSeconds || 0))
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟`
  if (minutes > 0) return `${minutes} 分钟`
  return `${seconds} 秒`
}

function formatHistoryDate(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(date)
}

function historyTimestamp(status: OpenAIReauthorizationAccountStatus): number {
  const value = status.last_succeeded_at || status.last_result_at || status.last_attempt_at || status.first_succeeded_at || status.first_attempt_at
  const timestamp = value ? Date.parse(value) : 0
  return Number.isFinite(timestamp) ? timestamp : 0
}

function historyAuthorizationBucket(account: Account): Exclude<HistoryFilter, 'all'> {
  const status = statusFor(account)
  const successCount = status?.success_count || 0
  if (successCount >= 2 || (status?.current_needs_reauthorization && status.current_authorization_number >= 2)) return 'second'
  if (successCount === 1) return 'first'
  return 'unauthorized'
}

function historyFilterCount(filter: HistoryFilter): number {
  if (filter === 'all') return filterMatchedHistoryAccounts.value.length
  return filterMatchedHistoryAccounts.value.filter(account => historyAuthorizationBucket(account) === filter).length
}

function authorizationRoundLabel(account: Account): string {
  const status = statusFor(account)
  if (!status) return '第 1 次授权'
  return `第 ${Math.max(1, status.current_authorization_number || 1)} 次授权`
}

function authorizationRoundClass(account: Account): string {
  const risk = statusFor(account)?.risk_level
  if (risk === 'blocked' || risk === 'repeated') return 'oauth-tone-danger'
  if (risk === 'cooldown' || risk === 'unknown' || risk === 'failed') return 'oauth-tone-warning'
  return 'oauth-tone-info'
}

function authorizationRiskHint(account: Account): string {
  const status = statusFor(account)
  if (!status) return ''
  if (status.risk_level === 'blocked') return accountRestrictionLabel(account)
  if (status.risk_level === 'repeated') return '高风险，需二次确认'
  if (status.risk_level === 'unknown') return ''
  if (status.risk_level === 'cooldown') {
    const nextAt = nextAuthorizationAt(status)
    return nextAt ? `最早 ${formatHistoryDate(nextAt)}` : `冷却 ${formatDuration(status.cooldown_remaining_seconds)}`
  }
  return ''
}

function authorizationRiskHintClass(account: Account): string {
  return statusFor(account)?.risk_level === 'blocked' || statusFor(account)?.risk_level === 'repeated'
    ? 'text-red-600 dark:text-red-300'
    : 'text-amber-700 dark:text-amber-300'
}

function reauthorizationRowNotice(account: Account): string {
  const status = statusFor(account)
  if (!status) return ''
  if (status.risk_level === 'blocked' || restrictionReasons.has(taskFor(account)?.reason || '')) return accountRestrictionLabel(account)
  if (status.risk_level === 'cooldown') {
    const nextAt = nextAuthorizationAt(status)
    const recommendation = nextAt ? `，建议 ${formatHistoryDate(nextAt)}` : ''
    return `冷静期剩余 ${formatDuration(status.cooldown_remaining_seconds)}${recommendation}`
  }
  return authorizationRiskHint(account)
}

function accountRestrictionLabel(account: Account): string {
  const reason = taskFor(account)?.reason || statusFor(account)?.last_reason || ''
  if (reason === 'account_deleted_or_disabled') return '账号已删除或停用'
  if (reason === 'account_banned') return '账号已封禁'
  return '未知错误'
}

function reauthorizationRowNoticeClass(account: Account): string {
  return authorizationRiskHintClass(account)
}

function historySequenceLabel(account: Account): string {
  const status = statusFor(account)
  if (!status) return '历史记录'
  if (status.success_count > 0) return `第 ${status.success_count} 次授权`
  if (status.attempt_count > 0) return `第 ${status.attempt_count} 次尝试`
  return '历史记录'
}

function historyRecordedAt(account: Account): string {
  const status = statusFor(account)
  if (!status) return ''
  const value = status.last_succeeded_at || status.last_result_at || status.last_attempt_at || status.first_succeeded_at || status.first_attempt_at
  return formatHistoryDate(value)
}

function successfulAuthorizationTimes(status: OpenAIReauthorizationAccountStatus): string[] {
  const values = [
    ...(status.successful_authorization_times || []),
    status.first_succeeded_at || '',
    status.last_succeeded_at || '',
  ]
    .map(value => ({ value, timestamp: Date.parse(value) }))
    .filter(item => item.value && Number.isFinite(item.timestamp))
    .sort((left, right) => left.timestamp - right.timestamp)
  return [...new Map(values.map(item => [item.timestamp, item.value])).values()]
}

function historyAuthorizationTimeline(account: Account): Array<{ label: string; time: string }> {
  const status = statusFor(account)
  if (!status) return [{ label: '历史记录', time: '' }]
  const values = successfulAuthorizationTimes(status)
  if (status.success_count >= 2) {
    const last = values.at(-1) || status.last_succeeded_at || status.first_succeeded_at
    return [{ label: `第 ${status.success_count} 次授权`, time: formatHistoryDate(last) }]
  }
  if (status.success_count === 1) {
    const first = values[0] || status.first_succeeded_at || status.last_succeeded_at
    return [{ label: '第 1 次授权', time: formatHistoryDate(first) }]
  }
  return [{ label: historySequenceLabel(account), time: historyRecordedAt(account) }]
}

function historyPendingAuthorizationLabel(account: Account): string {
  const status = statusFor(account)
  if (!status
    || !status.current_needs_reauthorization
    || status.current_authorization_number < 2
    || status.risk_level === 'blocked'
    || status.success_count >= status.current_authorization_number) return ''
  return `待第 ${status.current_authorization_number} 次授权`
}

function nextAuthorizationAt(status: OpenAIReauthorizationAccountStatus): string {
  if (status.cooldown_until) return status.cooldown_until
  const anchor = status.last_succeeded_at || status.first_succeeded_at
  if (!anchor) return ''
  const timestamp = Date.parse(anchor)
  if (!Number.isFinite(timestamp)) return ''
  return new Date(timestamp + 7 * 86400_000).toISOString()
}

function historyNextAuthorizationLabel(account: Account): string {
  const status = statusFor(account)
  if (!status || !status.current_needs_reauthorization || status.current_authorization_number < 2 || status.risk_level === 'blocked') return ''
  const nextAt = nextAuthorizationAt(status)
  if (status.risk_level === 'repeated') return `第 ${status.current_authorization_number} 次授权已满 7 天，继续需二次确认`
  return nextAt ? `第 ${status.current_authorization_number} 次授权建议等待 7 天，推荐：${formatHistoryDate(nextAt)}` : ''
}

function historyResultLabel(account: Account): string {
  const status = statusFor(account)
  if (!status) return '未知'
  if (status.last_result === 'blocked' || status.risk_level === 'blocked') return accountRestrictionLabel(account)
  // A prior successful authorization must not mask a new 401 round. The
  // current credential state is authoritative for the result shown here;
  // historical success remains visible in the timeline and counters.
  if (status.current_needs_reauthorization) {
    return status.current_authorization_number > 1
      ? `待第 ${status.current_authorization_number} 次授权`
      : '待重新授权'
  }
  if (status.last_result === 'success' || status.has_reauthorized) return '成功'
  if (status.last_result === 'canceled') return '已停止'
  if (status.last_result === 'failed' || status.risk_level === 'failed') return '失败'
  return status.history_confidence === 'unknown' ? '待确认' : '已记录'
}

function historyResultClass(account: Account): string {
  const result = historyResultLabel(account)
  if (result === '成功') return 'oauth-tone-success'
  if (result === '账号已删除或停用' || result === '账号已封禁' || result === '失败') return 'oauth-tone-danger'
  if (result === '已停止' || result === '待确认' || result === '待重新授权' || result.startsWith('待第 ')) return 'oauth-tone-warning'
  return 'oauth-tone-info'
}

function setBusy(accountID: number, busy: boolean) {
  const next = new Set(busyAccountIDs.value)
  if (busy) next.add(accountID)
  else next.delete(accountID)
  busyAccountIDs.value = next
}

function setDeleting(accountID: number, deleting: boolean) {
  const next = new Set(deletingAccountIDs.value)
  if (deleting) next.add(accountID)
  else next.delete(accountID)
  deletingAccountIDs.value = next
}

function requestDeleteAccount(account: Account) {
  operationNotice.value = ''
  pendingDeleteAccount.value = account
}

async function confirmDeleteAccount() {
  const account = pendingDeleteAccount.value
  if (!account || deletingAccountIDs.value.has(account.id)) return
  pendingDeleteAccount.value = null
  setDeleting(account.id, true)
  loadError.value = ''
  operationNotice.value = ''
  try {
    const task = taskFor(account)
    if (task && activeStatuses.has(task.status)) {
      mergeTask(await openAIReauthorizationAPI.cancel(task.task_id))
    }
    await accountsAPI.delete(account.id)
    accounts.value = accounts.value.filter(candidate => candidate.id !== account.id)
    accountStatuses.value = accountStatuses.value.filter(candidate => candidate.account.id !== account.id)
    queuedAccountIDs.value = queuedAccountIDs.value.filter(id => id !== account.id)
    tasks.value = tasks.value.filter(candidate => candidate.target_account_id !== account.id)
    if (task) {
      try {
        await openAIReauthorizationAPI.remove(task.task_id)
      } catch {
        // The account and saved login information are already deleted.
      }
    }
    operationNotice.value = `#${account.id} ${account.name} 已从账号管理和密码库删除。`
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, `删除 #${account.id} ${account.name} 失败。`)
  } finally {
    setDeleting(account.id, false)
  }
}

async function startAccount(account: Account, acknowledgeRisk = false, browserMode: AuthorizationBrowserMode = 'server', popup: Window | null = null) {
  if (!canStart(account)) return
  setBusy(account.id, true)
  const errors = new Map(localErrors.value)
  errors.delete(account.id)
  localErrors.value = errors
  try {
		let current = taskFor(account)
		if (current && terminalFailureStatuses.has(current.status) && current.restart_count >= maxRestarts.value) {
			await openAIReauthorizationAPI.remove(current.task_id)
			tasks.value = tasks.value.filter(candidate => candidate.task_id !== current?.task_id)
			current = undefined
		}
    const requestedBrowserMode = browserMode === 'adspower' ? 'adspower' : undefined
    const restartCurrent = (taskID: string) => requestedBrowserMode
      ? openAIReauthorizationAPI.restart(taskID, acknowledgeRisk, requestedBrowserMode)
      : openAIReauthorizationAPI.restart(taskID, acknowledgeRisk)
    const startCurrent = () => requestedBrowserMode
      ? openAIReauthorizationAPI.start(account.id, acknowledgeRisk, requestedBrowserMode)
      : openAIReauthorizationAPI.start(account.id, acknowledgeRisk)
    const task = current?.reason === 'account_state_recovery_failed' && current.account_id
      ? await openAIReauthorizationAPI.complete(current.task_id)
      : current && terminalFailureStatuses.has(current.status)
        ? await restartCurrent(current.task_id)
        : await startCurrent()
    mergeTask(task)
    if (browserMode === 'adspower' && task.browser_mode === 'adspower' && task.stage === 'external_browser') {
      const launch = await openAIReauthorizationAPI.launchAdsPower(task.task_id)
      if (launch.delivery === 'queued') {
		adsPowerHelperMissing.value = false
        popup?.close()
        operationNotice.value = `#${account.id} ${account.name} 已发送到 XIASS 常驻助手。`
		} else if (!await isAdsPowerHelperAvailable()) {
			adsPowerHelperMissing.value = true
			popup?.close()
			mergeTask(await openAIReauthorizationAPI.cancel(task.task_id))
			const next = new Map(localErrors.value)
			next.set(account.id, adsPowerHelperUnavailableMessage)
			localErrors.value = next
			return
		} else {
			adsPowerHelperMissing.value = false
			if (popup) popup.location.href = launch.helper_url
			else window.location.assign(launch.helper_url)
		}
    } else if (popup) {
      popup.close()
    }
  } catch (error) {
    popup?.close()
    const next = new Map(localErrors.value)
    next.set(account.id, extractApiErrorMessage(error, '无法启动该账号的 401 重新授权。'))
    localErrors.value = next
  } finally {
    setBusy(account.id, false)
    await Promise.all([syncTasks(false), loadAccounts()])
    void pumpQueue()
  }
}

async function stopAccount(account: Account) {
  const task = taskFor(account)
  if (!task || !isActive(account)) return
  setBusy(account.id, true)
  try {
    mergeTask(await openAIReauthorizationAPI.cancel(task.task_id))
  } catch (error) {
    const next = new Map(localErrors.value)
    next.set(account.id, extractApiErrorMessage(error, '停止授权失败。'))
    localErrors.value = next
  } finally {
    setBusy(account.id, false)
    await syncTasks(false)
  }
}

function setAuthorizationBrowserMode(browserMode: AuthorizationBrowserMode) {
  authorizationBrowserMode.value = browserMode
}

function requestStartAccount(account: Account) {
  if (!canStart(account)) return
  pendingAuthorization.value = { accounts: [account], batch: false, browserMode: authorizationBrowserMode.value }
}

function requestStartAll() {
  const candidates = batchStartableAccounts.value
  if (!candidates.length) return
  pendingAuthorization.value = { accounts: [...candidates], batch: true, browserMode: authorizationBrowserMode.value }
}

function confirmAuthorization() {
  const pending = pendingAuthorization.value
  if (!pending) return
  pendingAuthorization.value = null
  if (!pending.batch) {
    const account = pending.accounts[0]
    const popup = pending.browserMode === 'adspower' ? window.open('about:blank', '_blank') : null
    if (popup) popup.opener = null
    void startAccount(account, statusFor(account)?.requires_risk_confirmation === true, pending.browserMode, popup)
    return
  }
  queuedAccountIDs.value = pending.accounts.map(account => account.id)
  queuedAuthorizationBrowserMode.value = pending.browserMode
  startingAll.value = true
  void pumpQueue()
}

async function pumpQueue() {
  while (!disposed && queuedAccountIDs.value.length && activeCount.value < maxConcurrency.value) {
    const accountID = queuedAccountIDs.value.shift()!
    const account = accounts.value.find(candidate => candidate.id === accountID)
    if (!account || !canStart(account)) continue
    const popup = queuedAuthorizationBrowserMode.value === 'adspower' ? window.open('about:blank', '_blank') : null
    if (popup) popup.opener = null
    void startAccount(account, false, queuedAuthorizationBrowserMode.value, popup)
  }
  if (!queuedAccountIDs.value.length) startingAll.value = false
}

function mergeTask(task: OpenAIReauthorizationTask) {
  const next = tasks.value.filter(candidate => candidate.task_id !== task.task_id && candidate.target_account_id !== task.target_account_id)
  next.push(task)
  tasks.value = next
}

async function processTask(task: OpenAIReauthorizationTask) {
  if (task.status === 'ready' && !completingTaskIDs.has(task.task_id)) {
    completingTaskIDs.add(task.task_id)
    try { mergeTask(await openAIReauthorizationAPI.complete(task.task_id)) } finally { completingTaskIDs.delete(task.task_id) }
    return
  }
  if (task.status !== 'running' || smsTaskIDs.has(task.task_id)) return
  if (task.browser_mode === 'adspower') return
  if (task.stage !== 'phone_required' && task.stage !== 'sms_waiting') return
  smsTaskIDs.add(task.task_id)
  try {
    const action = task.stage === 'sms_waiting' ? 'check' : task.reason === 'phone_rejected' ? 'change' : 'acquire'
    const result = await openAIReauthorizationAPI.sms(task.task_id, action)
    mergeTask(result.task as OpenAIReauthorizationTask)
  } catch {
    // The next poll retries the current task only; other accounts keep running.
  } finally {
    smsTaskIDs.delete(task.task_id)
  }
}

async function syncTasks(showError = true) {
  try {
    const result = await openAIReauthorizationAPI.list()
    maxConcurrency.value = Math.max(1, Math.min(3, result.max_concurrency || 3))
    maxRestarts.value = Math.max(0, result.max_restarts || 0)
    tasks.value = result.items
    await Promise.all(result.items.map(processTask))
    const terminalEvents = result.items
      .filter(task => terminalFailureStatuses.has(task.status) || task.status === 'completed')
      .map(task => `${task.task_id}:${task.status}:${task.reason || ''}`)
    if (terminalEvents.some(event => !historyRefreshedTaskEvents.has(event))) {
      terminalEvents.forEach(event => historyRefreshedTaskEvents.add(event))
      await loadAccounts()
    }
    void pumpQueue()
  } catch (error) {
    if (showError) loadError.value = extractApiErrorMessage(error, '暂时无法读取 401 授权任务状态。')
  }
}

async function refreshAll() {
  refreshing.value = true
  loadError.value = ''
  try {
    await Promise.all([loadAccounts(), syncTasks()])
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, '读取待授权账号失败。')
  } finally {
    refreshing.value = false
  }
}

async function handleCredentialLibraryUpdated() {
  await loadAccounts()
}

async function handleBatchAccountCreated() {
  await loadAccounts()
}

function schedulePoll() {
  if (disposed) return
  pollTimer = setTimeout(async () => {
    now.value = Date.now()
    await syncTasks(false)
    schedulePoll()
  }, 1000)
}

function backToAccounts() {
  void router.push({ name: 'AdminAccounts' })
}

function openTeamChildCreation() {
  void router.push({ name: 'AdminTeamChildCreation' })
}

onMounted(async () => {
  window.scrollTo({ top: 0, behavior: 'auto' })
  try {
    await Promise.all([loadAccounts(), syncTasks(), loadBatchOptions(), loadAdsPowerEnvironment()])
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, '读取待授权账号失败。')
  } finally {
    loading.value = false
    schedulePoll()
  }
})

onBeforeUnmount(() => {
  disposed = true
  if (pollTimer) clearTimeout(pollTimer)
})
</script>

<style scoped>
.openai-account-workbench {
  position: relative;
  z-index: 1;
  max-width: 1440px;
  overflow-x: clip;
}

.oauth-workbench-heading,
.oauth-workbench-nav,
.oauth-workbench-filterbar,
.oauth-workbench-surface {
  width: 100%;
  max-width: 1360px;
  margin-inline: auto;
}

.oauth-workbench-heading {
  position: relative;
  display: flex;
  min-height: 4.5rem;
  align-items: center;
  justify-content: center;
  padding-inline: 0.25rem;
  text-align: center;
}

.oauth-workbench-back {
  position: absolute;
  left: 0.25rem;
}

.oauth-workbench-nav,
.oauth-workbench-filterbar,
.oauth-workbench-surface {
  border: 1px solid var(--xiass-console-light-border, rgb(255 255 255 / 0.74));
  border-radius: 8px;
  background: var(--xiass-console-light-surface, rgb(255 255 255 / 0.46));
  box-shadow: 0 14px 34px rgb(71 85 105 / 0.1);
  backdrop-filter: blur(22px) saturate(125%) brightness(1.04);
  -webkit-backdrop-filter: blur(22px) saturate(125%) brightness(1.04);
}

:global(.dark .oauth-workbench-nav),
:global(.dark .oauth-workbench-filterbar),
:global(.dark .oauth-workbench-surface) {
  border-color: var(--xiass-console-border, rgb(255 255 255 / 0.13));
  background: var(--xiass-console-surface-deep, rgb(3 14 25 / 0.22));
  box-shadow: 0 14px 34px rgb(0 0 0 / 0.12);
  backdrop-filter: blur(22px) saturate(135%) brightness(1.1);
  -webkit-backdrop-filter: blur(22px) saturate(135%) brightness(1.1);
}

.oauth-workbench-filterbar {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.65rem;
  padding: 0.7rem;
}

.oauth-filter-summary {
  flex-shrink: 0;
  color: rgb(100 116 139);
  font-size: 0.72rem;
  font-variant-numeric: tabular-nums;
}

:global(.dark .oauth-filter-summary) {
  color: rgb(148 163 184);
}

.oauth-workbench-nav {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr) max-content;
  min-width: 0;
  align-items: stretch;
  column-gap: 0.75rem;
  padding: 0.4rem;
}

.oauth-workbench-primary {
  display: flex;
  min-width: 0;
  align-items: center;
  overflow-x: auto;
}

.oauth-workbench-tablist {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.35rem;
}

.oauth-workbench-actions {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: flex-end;
  gap: 0.35rem;
}

.authorization-mode-selector {
  display: inline-flex;
  min-height: 2.75rem;
  flex-shrink: 0;
  align-items: center;
  gap: 0.2rem;
  border: 1px solid rgb(148 163 184 / 0.38);
  border-radius: 7px;
  background: rgb(255 255 255 / 0.18);
  padding: 0.2rem;
}

.authorization-mode-label {
  flex-shrink: 0;
  padding-inline: 0.5rem 0.35rem;
  color: rgb(71 85 105);
  font-size: 0.72rem;
  font-weight: 700;
}

.authorization-mode-option {
  display: inline-flex;
  min-height: 2.2rem;
  flex-shrink: 0;
  align-items: center;
  gap: 0.38rem;
  border: 1px solid transparent;
  border-radius: 5px;
  padding-inline: 0.58rem;
  color: rgb(100 116 139);
  font-size: 0.76rem;
  font-weight: 700;
  transition: border-color 160ms ease, background-color 160ms ease, box-shadow 160ms ease, color 160ms ease;
}

.authorization-mode-option:hover {
  border-color: rgb(148 163 184 / 0.32);
  background: rgb(255 255 255 / 0.3);
  color: rgb(3 105 161);
}

.authorization-mode-option-active {
  border-color: rgb(14 165 233 / 0.42);
  background: rgb(14 165 233 / 0.14);
  color: rgb(3 105 161);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.18);
}

.authorization-mode-current {
  display: inline-flex;
  min-height: 1.2rem;
  align-items: center;
  border-radius: 999px;
  background: rgb(14 165 233 / 0.16);
  padding-inline: 0.38rem;
  font-size: 0.62rem;
  line-height: 1;
}

.authorization-mode-settings {
  display: inline-flex;
  min-height: 2.75rem;
  flex-shrink: 0;
  align-items: center;
  gap: 0.4rem;
  border: 1px solid rgb(148 163 184 / 0.34);
  border-radius: 6px;
  background: rgb(255 255 255 / 0.2);
  padding-inline: 0.65rem;
  color: rgb(71 85 105);
  font-size: 0.74rem;
  font-weight: 700;
}

.authorization-mode-settings:hover {
  border-color: rgb(14 165 233 / 0.46);
  background: rgb(255 255 255 / 0.32);
  color: rgb(3 105 161);
}

:global(.dark .authorization-mode-selector) {
  border-color: rgb(123 178 199 / 0.26);
  background: rgb(3 25 40 / 0.52);
}

:global(.dark .authorization-mode-label) {
  color: rgb(148 163 184);
}

:global(.dark .authorization-mode-option) {
  color: rgb(186 230 253);
}

:global(.dark .authorization-mode-option:hover) {
  border-color: rgb(123 178 199 / 0.28);
  background: rgb(8 39 56 / 0.58);
  color: rgb(224 242 254);
}

:global(.dark .authorization-mode-option-active) {
  border-color: rgb(103 232 249 / 0.52);
  background: rgb(8 145 178 / 0.24);
  color: rgb(165 243 252);
  box-shadow: 0 0 18px rgb(34 211 238 / 0.1), inset 0 0 0 1px rgb(255 255 255 / 0.035);
}

:global(.dark .authorization-mode-current) {
  background: rgb(34 211 238 / 0.15);
  color: rgb(207 250 254);
}

:global(.dark .authorization-mode-settings) {
  border-color: rgb(123 178 199 / 0.24);
  background: rgb(3 25 40 / 0.52);
  color: rgb(186 230 253);
}

:global(.dark .authorization-mode-settings:hover) {
  border-color: rgb(56 189 248 / 0.42);
  background: rgb(8 39 56 / 0.58);
  color: rgb(224 242 254);
}

.team-child-entry {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: flex-end;
  border-left: 1px solid rgb(148 163 184 / 0.22);
  padding-left: 0.75rem;
}

:global(.dark .team-child-entry) {
  border-color: rgb(123 178 199 / 0.2);
}

.team-child-entry-button {
  display: inline-flex;
  min-height: 2.75rem;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  border: 1px solid rgb(14 165 233 / 0.4);
  border-radius: 7px;
  background: rgb(14 165 233 / 0.12);
  padding-inline: 0.9rem;
  color: rgb(3 105 161);
  font-size: 0.875rem;
  font-weight: 700;
  transition: border-color 160ms ease, background-color 160ms ease, box-shadow 160ms ease, color 160ms ease;
}

.team-child-entry-button:hover {
  border-color: rgb(14 165 233 / 0.66);
  background: rgb(14 165 233 / 0.18);
  box-shadow: 0 0 18px rgb(14 165 233 / 0.14);
}

.team-child-entry-icon {
  display: inline-flex;
  width: 1.75rem;
  height: 1.75rem;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  background: rgb(14 165 233 / 0.16);
}

:global(.dark .team-child-entry-button) {
  border-color: rgb(34 211 238 / 0.42);
  background: rgb(8 145 178 / 0.18);
  color: rgb(165 243 252);
}

:global(.dark .team-child-entry-button:hover) {
  border-color: rgb(103 232 249 / 0.64);
  background: rgb(8 145 178 / 0.28);
  box-shadow: 0 0 22px rgb(34 211 238 / 0.16);
}

.workbench-tab {
  display: flex;
  min-height: 2.75rem;
  flex-shrink: 0;
  align-items: center;
  gap: 0.5rem;
  border: 1px solid transparent;
  border-radius: 6px;
  padding-inline: 0.9rem;
  font-size: 0.875rem;
  font-weight: 600;
  transition: color 160ms ease, border-color 160ms ease, background-color 160ms ease, box-shadow 160ms ease;
}

.workbench-tab-active {
  border-color: rgb(14 165 233 / 0.38);
  background: rgb(14 165 233 / 0.12);
  color: rgb(3 105 161);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.24);
}

.workbench-tab-idle {
  color: rgb(71 85 105);
}

.workbench-tab-idle:hover {
  border-color: rgb(148 163 184 / 0.28);
  background: rgb(255 255 255 / 0.32);
  color: rgb(15 23 42);
}

:global(.dark .workbench-tab-active) {
  border-color: rgb(56 189 248 / 0.42);
  background: rgb(3 105 161 / 0.28);
  color: rgb(165 243 252);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.035);
}

:global(.dark .workbench-tab-idle) {
  color: rgb(148 163 184);
}

:global(.dark .workbench-tab-idle:hover) {
  border-color: rgb(123 178 199 / 0.24);
  background: rgb(8 39 56 / 0.48);
  color: rgb(226 232 240);
}

.workbench-tab-count {
  display: inline-flex;
  min-width: 1.5rem;
  height: 1.25rem;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: rgb(148 163 184 / 0.18);
  padding-inline: 0.4rem;
  font-size: 0.68rem;
  font-variant-numeric: tabular-nums;
}

.reauthorization-tab-icon {
  position: relative;
  display: inline-flex;
  width: 1.75rem;
  height: 1.75rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(14 165 233 / 0.34);
  border-radius: 7px;
  background: rgb(14 165 233 / 0.1);
  color: rgb(2 132 199);
  transition: border-color 160ms ease, background-color 160ms ease, box-shadow 160ms ease, color 160ms ease;
}

.reauthorization-tab-shield {
  width: 1rem;
  height: 1rem;
}

.reauthorization-tab-refresh {
  position: absolute;
  right: -0.22rem;
  bottom: -0.22rem;
  width: 0.85rem;
  height: 0.85rem;
  border: 1px solid rgb(125 211 252 / 0.68);
  border-radius: 999px;
  background: rgb(3 105 161);
  padding: 0.08rem;
  color: white;
}

.workbench-tab-active .reauthorization-tab-icon {
  border-color: rgb(103 232 249 / 0.62);
  background: rgb(8 145 178 / 0.24);
  box-shadow: 0 0 0 3px rgb(14 165 233 / 0.1), 0 0 18px rgb(34 211 238 / 0.18);
  color: rgb(165 243 252);
}

:global(.dark .reauthorization-tab-icon) {
  border-color: rgb(56 189 248 / 0.34);
  background: rgb(3 105 161 / 0.22);
  color: rgb(125 211 252);
}

.oauth-module-header {
  display: flex;
  min-width: 0;
  padding: 0.9rem 1rem;
  border-bottom: 1px solid rgb(148 163 184 / 0.24);
  background: rgb(255 255 255 / 0.14);
}

:global(.dark .oauth-module-header) {
  border-color: rgb(123 178 199 / 0.2);
  background: rgb(1 16 27 / 0.44);
}

.oauth-module-icon,
.oauth-icon-button,
.oauth-delete-button {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
}

.oauth-module-icon {
  width: 2rem;
  height: 2rem;
  border-radius: 7px;
  background: rgb(14 165 233 / 0.12);
  color: rgb(2 132 199);
}

:global(.dark .oauth-module-icon) {
  background: rgb(8 145 178 / 0.2);
  color: rgb(103 232 249);
}

.oauth-icon-button,
.oauth-delete-button {
  width: 2.5rem;
  height: 2.5rem;
  border: 1px solid rgb(148 163 184 / 0.38);
  border-radius: 8px;
  background: rgb(255 255 255 / 0.36);
  color: rgb(71 85 105);
  transition: border-color 160ms ease, background-color 160ms ease, color 160ms ease;
}

.oauth-icon-button:hover {
  border-color: rgb(14 165 233 / 0.5);
  color: rgb(2 132 199);
}

.oauth-delete-button {
  width: 2.25rem;
  height: 2.25rem;
  color: rgb(220 38 38);
}

.oauth-delete-button:hover {
  border-color: rgb(248 113 113 / 0.62);
  background: rgb(254 226 226 / 0.56);
}

:global(.dark .oauth-icon-button),
:global(.dark .oauth-delete-button) {
  border-color: rgb(123 178 199 / 0.26);
  background: rgb(3 25 40 / 0.72);
  color: rgb(186 230 253);
}

:global(.dark .oauth-delete-button) {
  color: rgb(252 165 165);
}

:global(.dark .oauth-delete-button:hover) {
  border-color: rgb(248 113 113 / 0.46);
  background: rgb(127 29 29 / 0.26);
}

.oauth-summary-chip,
.oauth-mini-tag,
.oauth-round-badge,
.oauth-history-result {
  display: inline-flex;
  align-items: center;
  border: 1px solid rgb(148 163 184 / 0.28);
  border-radius: 6px;
  background: rgb(255 255 255 / 0.28);
  color: rgb(71 85 105);
}

.oauth-summary-chip {
  min-height: 1.75rem;
  gap: 0.28rem;
  padding-inline: 0.55rem;
  font-size: 0.72rem;
}

.oauth-summary-chip b {
  color: rgb(15 23 42);
  font-size: 0.8rem;
  font-variant-numeric: tabular-nums;
}

.oauth-summary-chip-active {
  border-color: rgb(56 189 248 / 0.3);
  color: rgb(3 105 161);
}

.oauth-summary-chip-warning {
  border-color: rgb(245 158 11 / 0.32);
  color: rgb(180 83 9);
}

:global(.dark .oauth-summary-chip),
:global(.dark .oauth-mini-tag) {
  border-color: rgb(123 178 199 / 0.22);
  background: rgb(3 27 43 / 0.7);
  color: rgb(148 163 184);
}

:global(.dark .oauth-summary-chip b) {
  color: rgb(226 232 240);
}

:global(.dark .oauth-summary-chip-active) {
  color: rgb(125 211 252);
}

:global(.dark .oauth-summary-chip-warning) {
  color: rgb(253 230 138);
}

.oauth-history-filters {
  display: inline-flex;
  min-width: 0;
  gap: 0.22rem;
  overflow-x: auto;
  border: 1px solid rgb(148 163 184 / 0.28);
  border-radius: 8px;
  background: rgb(255 255 255 / 0.2);
  padding: 0.22rem;
}

.oauth-history-filter {
  display: inline-flex;
  min-height: 2rem;
  flex-shrink: 0;
  align-items: center;
  gap: 0.35rem;
  border: 1px solid transparent;
  border-radius: 6px;
  padding-inline: 0.62rem;
  color: rgb(71 85 105);
  font-size: 0.72rem;
  font-weight: 600;
}

.oauth-history-filter b {
  color: inherit;
  font-size: 0.68rem;
  font-variant-numeric: tabular-nums;
}

.oauth-history-filter-active {
  border-color: rgb(14 165 233 / 0.34);
  background: rgb(14 165 233 / 0.12);
  color: rgb(3 105 161);
}

:global(.dark .oauth-history-filters) {
  border-color: rgb(123 178 199 / 0.22);
  background: rgb(1 17 28 / 0.54);
}

:global(.dark .oauth-history-filter) {
  color: rgb(148 163 184);
}

:global(.dark .oauth-history-filter-active) {
  border-color: rgb(56 189 248 / 0.38);
  background: rgb(3 105 161 / 0.3);
  color: rgb(186 230 253);
}

.oauth-account-list,
.oauth-history-list {
  display: grid;
  gap: 0.55rem;
  padding: 0.75rem;
}

.oauth-account-row,
.oauth-history-row {
  display: grid;
  min-width: 0;
  align-items: center;
  gap: 1rem;
  border: 1px solid rgb(148 163 184 / 0.24);
  border-radius: 8px;
  background: rgb(255 255 255 / 0.28);
  padding: 0.9rem 1rem;
  box-shadow: inset 0 1px rgb(255 255 255 / 0.24);
}

.oauth-account-row {
  grid-template-columns: minmax(22rem, 30rem) minmax(22rem, 1fr) 12rem;
}

.oauth-history-row {
  grid-template-columns: minmax(22rem, 30rem) minmax(20rem, 1fr) 10rem;
}

:global(.dark .oauth-account-row),
:global(.dark .oauth-history-row) {
  border-color: var(--xiass-console-border, rgb(255 255 255 / 0.13));
  background: var(--xiass-console-surface, rgb(6 18 32 / 0.28));
  box-shadow: inset 0 1px rgb(255 255 255 / 0.02);
}

.oauth-account-identity,
.oauth-account-progress {
  min-width: 0;
}

.oauth-account-avatar {
  display: inline-flex;
  width: 2.4rem;
  height: 2.4rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: rgb(14 165 233 / 0.12);
  color: rgb(2 132 199);
  font-size: 0.9rem;
  font-weight: 700;
}

:global(.dark .oauth-account-avatar) {
  background: rgb(3 105 161 / 0.3);
  color: rgb(125 211 252);
}

.oauth-mini-tag {
  min-height: 1.35rem;
  padding-inline: 0.38rem;
  font-size: 0.66rem;
  font-weight: 600;
}

.oauth-round-badge,
.oauth-history-result {
  min-height: 1.55rem;
  padding-inline: 0.5rem;
  font-size: 0.7rem;
  font-weight: 700;
  white-space: nowrap;
}

.oauth-progress-track {
  height: 0.36rem;
  margin-top: 0.55rem;
  overflow: hidden;
  border-radius: 999px;
  background: rgb(148 163 184 / 0.22);
}

:global(.dark .oauth-progress-track) {
  background: rgb(148 163 184 / 0.16);
}

.oauth-account-actions {
  display: flex;
  min-width: 0;
  flex-wrap: nowrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
}

.oauth-account-actions :deep(.btn) {
  flex-shrink: 0;
  white-space: nowrap;
}

.oauth-tone-success {
  border-color: rgb(16 185 129 / 0.34);
  background: rgb(209 250 229 / 0.58);
  color: rgb(4 120 87);
}

.oauth-tone-danger {
  border-color: rgb(239 68 68 / 0.34);
  background: rgb(254 226 226 / 0.58);
  color: rgb(185 28 28);
}

.oauth-tone-warning {
  border-color: rgb(245 158 11 / 0.34);
  background: rgb(254 243 199 / 0.58);
  color: rgb(180 83 9);
}

.oauth-tone-info {
  border-color: rgb(14 165 233 / 0.32);
  background: rgb(224 242 254 / 0.52);
  color: rgb(3 105 161);
}

:global(.dark .oauth-tone-success) {
  border-color: rgb(52 211 153 / 0.28);
  background: rgb(6 78 59 / 0.34);
  color: rgb(167 243 208);
}

:global(.dark .oauth-tone-danger) {
  border-color: rgb(248 113 113 / 0.32);
  background: rgb(127 29 29 / 0.3);
  color: rgb(254 202 202);
}

:global(.dark .oauth-tone-warning) {
  border-color: rgb(251 191 36 / 0.3);
  background: rgb(120 53 15 / 0.3);
  color: rgb(253 230 138);
}

:global(.dark .oauth-tone-info) {
  border-color: rgb(56 189 248 / 0.28);
  background: rgb(3 105 161 / 0.26);
  color: rgb(186 230 253);
}

.oauth-empty-state {
  display: flex;
  min-height: 13rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.55rem;
  padding: 1rem;
  color: rgb(71 85 105);
  text-align: center;
  font-size: 0.875rem;
  font-weight: 600;
}

:global(.dark .oauth-empty-state) {
  color: rgb(148 163 184);
}

.oauth-workbench-surface :deep(.btn),
.oauth-workbench-surface :deep(.input),
.oauth-workbench-surface :deep(.select-trigger) {
  border-radius: 8px;
}

@media (max-width: 1199px) {
  .oauth-workbench-nav {
    grid-template-columns: minmax(0, 1fr) max-content;
    row-gap: 0.4rem;
  }

  .oauth-workbench-actions {
    grid-column: 1 / -1;
    grid-row: 2;
    border-top: 1px solid rgb(148 163 184 / 0.22);
    padding-top: 0.45rem;
  }

  .team-child-entry {
    border-left: 0;
    padding-left: 0;
  }

  :global(.dark .oauth-workbench-actions) {
    border-color: rgb(123 178 199 / 0.2);
  }

  .oauth-account-row,
  .oauth-history-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .oauth-account-actions,
  .oauth-history-row > :last-child {
    justify-content: flex-start;
  }
}

@media (max-width: 767px) {
  .oauth-workbench-nav {
    grid-template-columns: minmax(0, 1fr);
  }

  .oauth-workbench-primary,
  .oauth-workbench-actions {
    overflow-x: auto;
  }

  .oauth-workbench-actions {
    grid-column: 1;
    grid-row: 3;
    justify-content: flex-start;
  }

  .team-child-entry {
    grid-column: 1;
    grid-row: 2;
    justify-content: flex-end;
    border-top: 1px solid rgb(148 163 184 / 0.22);
    padding-top: 0.4rem;
  }

  :global(.dark .team-child-entry) {
    border-color: rgb(123 178 199 / 0.2);
  }
}

@media (max-width: 639px) {
  .openai-account-workbench {
    padding-inline: 0.5rem;
  }

  .oauth-workbench-heading {
    min-height: 3.75rem;
  }

  .workbench-tab {
    min-height: 2.5rem;
    padding-inline: 0.7rem;
    font-size: 0.8rem;
  }

  .oauth-module-header {
    padding-inline: 0.75rem;
  }

  .oauth-account-list,
  .oauth-history-list {
    padding: 0.5rem;
  }

  .oauth-account-row,
  .oauth-history-row {
    padding: 0.8rem;
  }

  .openai-account-workbench :deep(.btn) {
    max-width: 100%;
  }
}

@media (prefers-reduced-motion: reduce) {
  .workbench-tab,
  .oauth-icon-button,
  .oauth-delete-button {
    transition-duration: 1ms;
  }
}
</style>
