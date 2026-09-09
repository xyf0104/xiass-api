<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.title') }}</h1>
          <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary" :disabled="loading || saving || pairingSaving || runtimeSaving || takeoverSaving" :title="t('admin.executionNodes.refresh')" data-testid="execution-node-refresh" @click="refreshAll()">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          <span>{{ t('admin.executionNodes.refresh') }}</span>
        </button>
      </div>

      <div v-if="loading && !status" class="flex justify-center py-16">
        <span class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
      </div>

      <template v-else-if="status">
        <section class="card overflow-hidden">
          <div class="flex min-w-0 items-start gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg" :class="pairingStatus?.production_ready ? 'bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'">
              <Icon name="server" size="lg" />
            </div>
            <div class="min-w-0">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.statusTitle') }}</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ pairingStatus?.production_ready ? t('admin.executionNodes.pairingReadyHint') : pairingStatus?.paired ? t('admin.executionNodes.pairingMismatchHint') : t('admin.executionNodes.pairingNotPaired') }}</p>
            </div>
          </div>
          <div class="grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3 sm:px-6">
            <StatusTile :label="t('admin.executionNodes.localNode')" :value="status.runtime.enabled ? t('admin.executionNodes.configured') : t('admin.executionNodes.notConfigured')" :tone="status.runtime.enabled ? 'ok' : 'bad'" />
            <StatusTile :label="t('admin.executionNodes.dataConnection')" :value="status.database_reachable ? t('admin.executionNodes.healthy') : t('admin.executionNodes.unavailable')" :tone="status.database_reachable ? 'ok' : 'bad'" />
            <StatusTile :label="t('admin.executionNodes.machineConnection')" :value="status.runtime.enabled ? (status.heartbeat_store_reachable ? t('admin.executionNodes.healthy') : t('admin.executionNodes.unavailable')) : t('admin.executionNodes.notRequired')" :tone="status.runtime.enabled ? (status.heartbeat_store_reachable ? 'ok' : 'warn') : 'warn'" />
            <StatusTile :label="t('admin.executionNodes.failoverProtection')" :value="status.failover?.enabled ? (status.failover.ready ? t('admin.executionNodes.failoverReady') : t('admin.executionNodes.failoverNotReady')) : t('admin.executionNodes.notConfigured')" :tone="status.failover?.ready ? 'ok' : status.failover?.enabled ? 'bad' : 'warn'" />
          </div>
          <div v-if="status.runtime.enabled" class="mx-5 mb-5 flex items-start gap-3 rounded-lg border px-4 py-3 text-sm leading-6 sm:mx-6" :class="status.admin_write_mode === 'emergency_takeover' ? 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-300' : status.admin_write_allowed ? 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/20 dark:text-emerald-300' : 'border-sky-200 bg-sky-50 text-sky-800 dark:border-sky-900/60 dark:bg-sky-950/20 dark:text-sky-300'" data-testid="execution-node-admin-access">
            <Icon :name="status.admin_write_allowed ? 'check' : 'lock'" size="sm" class="mt-1 shrink-0" />
            <span>{{ status.admin_write_mode === 'paired_full_access' ? t('admin.executionNodes.adminWritePaired') : status.admin_write_mode === 'pairing_unavailable' ? t('admin.executionNodes.adminWritePairingUnavailable') : status.admin_write_mode === 'emergency_takeover' ? t('admin.executionNodes.adminWriteTakeover') : status.admin_write_allowed ? t('admin.executionNodes.adminWritePrimary') : t('admin.executionNodes.adminWriteSecondary') }}</span>
          </div>
        </section>

        <section v-if="!status.runtime.enabled" class="card overflow-hidden">
          <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.runtimeTitle') }}</h2>
          </div>
          <div class="bg-sky-50 px-5 py-4 dark:bg-sky-950/20 sm:px-6">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div class="flex min-w-0 items-start gap-3">
                <div>
                  <h3 class="text-sm font-semibold text-sky-900 dark:text-sky-200">{{ t('admin.executionNodes.initializeTitle') }}</h3>
                  <p class="mt-1 text-xs leading-5 text-sky-800 dark:text-sky-300">{{ t('admin.executionNodes.initializeHint') }}</p>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <span class="text-xs text-sky-800 dark:text-sky-300">{{ t('admin.executionNodes.detectedMachine', { name: localNodeID }) }}</span>
                <button type="button" class="btn btn-primary btn-sm" :disabled="runtimeSaving || !localNodeID" data-testid="execution-node-initialize" @click="initializeRuntime">
                  <Icon :name="runtimeSaving ? 'refresh' : 'server'" size="sm" :class="runtimeSaving ? 'animate-spin' : ''" />
                  <span>{{ runtimeSaving ? t('admin.executionNodes.initializing') : t('admin.executionNodes.initialize') }}</span>
                </button>
              </div>
            </div>
          </div>
        </section>

        <section class="card overflow-hidden" data-testid="execution-node-pairing">
          <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.pairingTitle') }}</h2>
              <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.pairingDescription') }}</p>
            </div>
            <div class="flex items-center gap-2 text-sm font-medium" :class="pairingStatus?.production_ready ? 'text-emerald-600 dark:text-emerald-400' : pairingStatus?.paired ? 'text-amber-600 dark:text-amber-400' : 'text-gray-500 dark:text-gray-400'">
              <span class="h-2.5 w-2.5 rounded-full" :class="pairingStatus?.production_ready ? 'bg-emerald-500' : pairingStatus?.paired ? 'bg-amber-500' : 'bg-gray-400'" />
              {{ pairingStatus?.production_ready ? t('admin.executionNodes.pairingReady') : pairingStatus?.paired ? t('admin.executionNodes.pairingIncomplete') : t('admin.executionNodes.pairingNotPaired') }}
            </div>
          </div>
          <div class="grid gap-5 p-5 lg:grid-cols-2 sm:px-6">
            <div class="space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div>
                  <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.inviteTitle') }}</h3>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.inviteHint') }}</p>
                </div>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="pairingSaving || !canGenerateInvite" :title="canGenerateInvite ? t('admin.executionNodes.generateInvite') : t('admin.executionNodes.initializeFirst')" data-testid="execution-node-generate-invite" @click="generateInvite">
                  <Icon name="key" size="sm" />
                  <span>{{ pairingInvite ? t('admin.executionNodes.regenerateInvite') : t('admin.executionNodes.generateInvite') }}</span>
                </button>
              </div>
              <div class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-300">
                {{ t('admin.executionNodes.inviteAuthorityNotice') }}
              </div>
              <div v-if="pairingInvite" class="rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-900/60 dark:bg-amber-950/20">
                <div class="flex gap-2">
                  <input :value="pairingInvite.token" readonly class="input min-w-0 flex-1 bg-white font-mono text-xs dark:bg-dark-800" data-testid="execution-node-invite-token" />
                  <button type="button" class="btn btn-secondary btn-sm shrink-0" :title="t('admin.executionNodes.copyInvite')" @click="copyInvite">
                    <Icon name="copy" size="sm" />
                    <span>{{ t('admin.executionNodes.copyInvite') }}</span>
                  </button>
                </div>
                <p class="mt-2 text-xs text-amber-800 dark:text-amber-300">{{ t('admin.executionNodes.inviteExpiry', { time: formatPairingTime(pairingInvite.expires_at) }) }}</p>
              </div>
              <p v-else class="rounded-lg border border-dashed border-gray-200 px-3 py-4 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ t('admin.executionNodes.noInvite') }}</p>
            </div>
            <div class="space-y-3">
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.joinTitle') }}</h3>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.joinHint') }}</p>
              </div>
              <div class="rounded-lg border border-sky-200 bg-sky-50 px-3 py-2 text-xs leading-5 text-sky-800 dark:border-sky-900/60 dark:bg-sky-950/20 dark:text-sky-300">
                {{ t('admin.executionNodes.joinAuthorityNotice') }}
              </div>
              <label class="block" for="execution-node-peer-url">
                <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.peerURL') }}</span>
                <input id="execution-node-peer-url" v-model.trim="pairingPeerURL" class="input mt-1 w-full" type="url" autocomplete="url" :placeholder="t('admin.executionNodes.peerURLPlaceholder')" data-testid="execution-node-peer-url" />
              </label>
              <label class="block" for="execution-node-peer-token">
                <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.inviteToken') }}</span>
                <input id="execution-node-peer-token" v-model.trim="pairingToken" class="input mt-1 w-full font-mono text-xs" type="password" autocomplete="one-time-code" data-testid="execution-node-peer-token" />
              </label>
              <div class="flex flex-wrap gap-2">
                <button type="button" class="btn btn-primary btn-sm" :disabled="pairingSaving || !pairingPeerURL || !pairingToken" data-testid="execution-node-pair" @click="pairNode">
                  <Icon :name="pairingSaving ? 'refresh' : 'link'" size="sm" :class="pairingSaving ? 'animate-spin' : ''" />
                  <span>{{ pairingSaving ? t('admin.executionNodes.pairing') : t('admin.executionNodes.pair') }}</span>
                </button>
                <button v-if="pairingStatus?.paired" type="button" class="btn btn-secondary btn-sm text-red-600 dark:text-red-400" :disabled="pairingSaving" data-testid="execution-node-unpair" @click="showUnpairConfirm = true">
                  <Icon name="x" size="sm" />
                  <span>{{ t('admin.executionNodes.unpair') }}</span>
                </button>
              </div>
            </div>
          </div>
          <div v-if="pairingStatus?.paired" class="border-t border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <p class="mt-3 text-xs leading-5" :class="pairingStatus.production_ready ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">
              {{ pairingStatus.production_ready ? t('admin.executionNodes.pairingReadyHint') : t('admin.executionNodes.pairingMismatchHint') }}
            </p>
          </div>
        </section>

        <section class="card overflow-hidden">
          <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.nodesTitle') }}</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.weightHint') }}</p>
            </div>
            <span v-if="lastSyncedAt" class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap text-xs text-gray-500 dark:text-gray-400" data-testid="execution-node-sync-status">
              <Icon name="refresh" size="xs" />
              {{ t('admin.executionNodes.syncStatus', { time: formatSyncTime(lastSyncedAt) }) }}
            </span>
          </div>
          <div class="space-y-3 p-5 sm:px-6">
            <div v-for="(node, index) in draftNodes" :key="node.key" class="rounded-lg border border-gray-200 p-4 dark:border-dark-600" :data-testid="`execution-node-row-${index}`">
              <div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_9rem] lg:items-end">
                <div class="block min-w-0">
                  <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.node') }}</span>
                  <div class="mt-1 flex min-h-10 items-center rounded-lg border border-gray-200 bg-gray-50 px-3 text-sm font-medium text-gray-900 dark:border-dark-600 dark:bg-dark-800/60 dark:text-white" :data-testid="`execution-node-label-${index}`">
                    {{ nodeLabel(node, index) }}
                  </div>
                </div>
                <label class="block min-w-0" :for="`execution-node-weight-${index}`">
                  <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.weight') }}</span>
                  <input :id="`execution-node-weight-${index}`" v-model.number="node.weight" class="input mt-1 w-full" min="0" max="1000000" step="0.1" inputmode="decimal" type="number" :disabled="saving" :data-testid="`execution-node-weight-${index}`" @input="draftDirty = true" />
                </label>
              </div>
              <div v-if="nodeStatus(node.nodeID)" class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
                <span class="inline-flex items-center gap-1.5" :class="nodeStatus(node.nodeID)!.online ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'">
                  <span class="h-2 w-2 rounded-full" :class="nodeStatus(node.nodeID)!.online ? 'bg-emerald-500' : 'bg-gray-400'" />
                  {{ nodeStatus(node.nodeID)!.online ? t('admin.executionNodes.online') : t('admin.executionNodes.offline') }}
                  <span v-if="nodeStatus(node.nodeID)!.is_local">· {{ t('admin.executionNodes.local') }}</span>
                </span>
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.accounts') }} {{ nodeStatus(node.nodeID)!.account_stats.total }} / {{ t('admin.executionNodes.schedulableAccounts') }} {{ nodeStatus(node.nodeID)!.account_stats.schedulable }}</span>
                <span class="text-gray-500 dark:text-gray-400">{{ nodeStatus(node.nodeID)!.proxy_valid ? t('admin.executionNodes.accountPathReady') : t('admin.executionNodes.accountPathInvalid') }}</span>
              </div>
            </div>
          </div>
        </section>

        <section class="card overflow-hidden">
          <div class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div class="min-w-0">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.enabled') }}</h2>
              <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.enabledHint') }}</p>
            </div>
            <div class="flex items-center gap-3">
              <span class="text-sm font-medium" :class="draftEnabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'">
                {{ draftEnabled ? t('admin.executionNodes.on') : t('admin.executionNodes.off') }}
              </span>
              <Toggle :model-value="draftEnabled" :disabled="saving || (!draftEnabled && !status.can_enable)" :aria-label="t('admin.executionNodes.enabled')" :title="t('admin.executionNodes.enabled')" data-testid="execution-node-balancing-toggle" @update:model-value="requestToggle" />
            </div>
          </div>
          <div class="flex flex-wrap items-center justify-between gap-3 px-5 py-4 sm:px-6">
            <div class="text-sm" :class="status.can_enable ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
              {{ status.can_enable ? t('admin.executionNodes.canEnable') : t('admin.executionNodes.cannotEnable') }}
            </div>
            <button type="button" class="btn btn-primary" :disabled="saving || !hasValidDraft" data-testid="execution-node-save" @click="save">
              <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" />
              <span>{{ saving ? t('admin.executionNodes.saving') : t('admin.executionNodes.save') }}</span>
            </button>
          </div>
          <div v-if="status.runtime.enabled" class="flex flex-wrap items-center justify-between gap-4 border-t border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div class="min-w-0">
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.offlineTakeover') }}</h3>
              <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.offlineTakeoverHint') }}</p>
            </div>
            <div class="flex items-center gap-3">
              <span class="text-sm font-medium" :class="status.runtime.emergency_local_egress ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'">
                {{ status.runtime.emergency_local_egress ? t('admin.executionNodes.on') : t('admin.executionNodes.off') }}
              </span>
              <Toggle :model-value="status.runtime.emergency_local_egress" :disabled="takeoverSaving" :aria-label="t('admin.executionNodes.offlineTakeover')" :title="t('admin.executionNodes.offlineTakeover')" data-testid="execution-node-takeover-toggle" @update:model-value="updateOfflineTakeover" />
            </div>
          </div>
        </section>

        <section class="rounded-lg border border-sky-200 bg-sky-50 p-5 dark:border-sky-900/60 dark:bg-sky-950/20">
          <h2 class="text-sm font-semibold text-sky-900 dark:text-sky-200">{{ t('admin.executionNodes.ingressTitle') }}</h2>
          <p class="mt-1 text-sm leading-6 text-sky-800 dark:text-sky-300">{{ t('admin.executionNodes.ingressDescription') }}</p>
        </section>

        <section class="card overflow-hidden">
          <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ hasBlockingIssues ? t('admin.executionNodes.issueTitle') : t('admin.executionNodes.statusTitle') }}</h2>
          </div>
          <div v-if="statusIssues.length" class="divide-y divide-gray-100 dark:divide-dark-700">
            <div v-for="issue in statusIssues" :key="`${issue.code}-${issue.message}`" class="flex gap-3 px-5 py-3 text-sm sm:px-6">
              <Icon :name="issue.severity === 'error' ? 'exclamationTriangle' : 'infoCircle'" size="sm" :class="issue.severity === 'error' ? 'text-red-500' : issue.severity === 'info' ? 'text-sky-500' : 'text-amber-500'" />
              <div class="min-w-0"><span class="font-medium text-gray-800 dark:text-gray-200">{{ issueText(issue) }}</span></div>
            </div>
          </div>
          <p v-else class="px-5 py-4 text-sm text-gray-500 dark:text-gray-400 sm:px-6">{{ t('admin.executionNodes.noIssues') }}</p>
        </section>
      </template>
    </div>

    <ConfirmDialog
      :show="showEnableConfirm"
      :title="t('admin.executionNodes.confirmEnableTitle')"
      :message="t('admin.executionNodes.confirmEnableMessage')"
      :confirm-text="t('admin.executionNodes.confirmEnable')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmEnable"
      @cancel="showEnableConfirm = false"
    />
    <ConfirmDialog
      :show="showUnpairConfirm"
      :title="t('admin.executionNodes.confirmUnpairTitle')"
      :message="t('admin.executionNodes.confirmUnpairMessage')"
      :confirm-text="t('admin.executionNodes.confirmUnpair')"
      :cancel-text="t('common.cancel')"
      @confirm="unpairNode"
      @cancel="showUnpairConfirm = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { ExecutionNodeAdminNode, ExecutionNodeAdminStatus, ExecutionNodePairingInvite, ExecutionNodePairingStatus } from '@/api/admin/executionNodes'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()

function browserOrigin(): string {
  if (typeof window === 'undefined' || !/^https?:$/.test(window.location.protocol)) return ''
  return window.location.origin
}

function detectedMachineName(): string {
  if (typeof window === 'undefined') return 'node1'
  const hostname = window.location.hostname.toLowerCase()
  if (!hostname || hostname === 'localhost') return 'node1'
  const candidate = `${/^\d{1,3}(?:\.\d{1,3}){3}$/.test(hostname) ? 'node-' : ''}${hostname}`.replace(/[^a-z0-9._-]/g, '-')
  if (candidate.length <= 64) return candidate || 'node1'
  let hash = 2166136261
  for (const char of candidate) hash = Math.imul(hash ^ char.charCodeAt(0), 16777619)
  return `${candidate.slice(0, 54)}-${(hash >>> 0).toString(36)}`.slice(0, 64)
}

const status = ref<ExecutionNodeAdminStatus | null>(null)
const pairingStatus = ref<ExecutionNodePairingStatus | null>(null)
const pairingInvite = ref<ExecutionNodePairingInvite | null>(null)
const pairingPeerURL = ref('')
const pairingToken = ref('')
const pairingTargetID = ref(detectedMachineName())
const pairingTargetURL = ref(browserOrigin())
const localNodeID = ref(detectedMachineName())
const loading = ref(false)
const saving = ref(false)
const pairingSaving = ref(false)
const runtimeSaving = ref(false)
const takeoverSaving = ref(false)
const showEnableConfirm = ref(false)
const showUnpairConfirm = ref(false)
const draftEnabled = ref(false)
const draftDirty = ref(false)
const lastSyncedAt = ref<number | null>(null)
let nodeSequence = 0
const healthyPollDelay = 10_000
const errorPollDelay = 3_000
let statusPollTimer: number | null = null
let refreshInFlight: Promise<void> | null = null
let refreshSilent = false
let refreshFailed = false
let refreshRevision = 0
let disposed = false

interface NodeDraft { key: number; nodeID: string; weight: number; proxyID: number }
const draftNodes = ref<NodeDraft[]>([])
const statusIssues = computed(() => status.value?.issues ?? [])
const canGenerateInvite = computed(() => Boolean(
  status.value?.runtime.enabled &&
  /^[A-Za-z0-9._-]{1,64}$/.test(status.value.runtime.node_id) &&
  status.value.runtime.default_proxy_id > 0
))
const hasBlockingIssues = computed(() => statusIssues.value.some((issue) => issue.severity === 'error'))

const StatusTile = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true }, tone: { type: String, default: 'ok' } },
  setup(props) {
    return () => h('div', { class: 'rounded-lg border border-gray-200 p-4 dark:border-dark-600' }, [
      h('div', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label),
      h('div', { class: ['mt-1 text-sm font-semibold', props.tone === 'ok' ? 'text-emerald-600 dark:text-emerald-400' : props.tone === 'warn' ? 'text-amber-600 dark:text-amber-400' : 'text-red-600 dark:text-red-400'] }, props.value)
    ])
  }
})

const hasValidDraft = computed(() => {
  const ids = new Set<string>()
  const proxyIDs = new Set<number>()
  let positive = false
  for (const node of draftNodes.value) {
    const id = node.nodeID.trim()
    const weight = Number(node.weight)
    if (!/^[A-Za-z0-9._-]{1,64}$/.test(id) || ids.has(id) || !Number.isFinite(weight) || weight < 0 || weight > 1_000_000) return false
    ids.add(id)
    positive ||= weight > 0
    if (draftEnabled.value && (!Number.isInteger(node.proxyID) || node.proxyID <= 0 || proxyIDs.has(node.proxyID))) return false
    if (node.proxyID > 0) proxyIDs.add(node.proxyID)
  }
  return ids.size > 0 && positive
})

function nodeStatus(nodeID: string): ExecutionNodeAdminNode | undefined {
  return (status.value?.nodes ?? []).find((node) => node.node_id === nodeID.trim())
}

function nodeLabel(node: NodeDraft, index: number): string {
  const current = nodeStatus(node.nodeID)
  if (current?.is_local || node.nodeID.trim() === localNodeID.value.trim()) return t('admin.executionNodes.localNode')
  return t('admin.executionNodes.otherNode', { number: index + 1 })
}

function issueText(issue: ExecutionNodeAdminStatus['issues'][number]): string {
  const key = `admin.executionNodes.issues.${issue.code}`
  const translated = t(key)
  return translated === key ? issue.message : translated
}

function syncDraft(next: ExecutionNodeAdminStatus): void {
  draftEnabled.value = next.balancing_enabled
  if (next.runtime.node_id) {
    localNodeID.value = next.runtime.node_id
    pairingTargetID.value = next.runtime.node_id
  }
  draftNodes.value = (next.nodes ?? []).map((node) => ({ key: ++nodeSequence, nodeID: node.node_id, weight: node.weight, proxyID: node.proxy_id || 0 }))
  if (!draftNodes.value.length) {
    // Before a second machine joins, this is the first/source machine.
    draftNodes.value = [{ key: ++nodeSequence, nodeID: localNodeID.value, weight: 9, proxyID: 0 }]
  }
  if (!pairingTargetURL.value) pairingTargetURL.value = browserOrigin()
  draftDirty.value = false
}

async function initializeRuntime(): Promise<void> {
  runtimeSaving.value = true
  invalidateStatusRefresh()
  try {
    await adminAPI.executionNodes.initializeRuntime(localNodeID.value)
    appStore.showSuccess(t('admin.executionNodes.initializeAccepted'))
    await refreshAfterMutation()
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.executionNodes.issues', t('admin.executionNodes.initializeFailed')))
  } finally {
    runtimeSaving.value = false
    scheduleStatusPoll()
  }
}

async function updateOfflineTakeover(enabled: boolean): Promise<void> {
  takeoverSaving.value = true
  invalidateStatusRefresh()
  try {
    await adminAPI.executionNodes.updateOfflineTakeover(enabled)
    appStore.showSuccess(enabled ? t('admin.executionNodes.offlineTakeoverEnabled') : t('admin.executionNodes.offlineTakeoverDisabled'))
    await refreshAfterMutation()
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.executionNodes.issues', t('admin.executionNodes.offlineTakeoverFailed')))
  } finally {
    takeoverSaving.value = false
    scheduleStatusPoll()
  }
}

function statusPollDelay(): number {
  const current = status.value
  const pairing = pairingStatus.value
  if (refreshFailed || !current || !pairing || !current.database_reachable) return errorPollDelay

  // A single-node installation or an unpaired setup is not a failed pair.
  // Once routing or pairing is active, all of its health signals matter.
  const pairingExpected = current.balancing_enabled || pairing.paired
  if ((current.issues ?? []).some((issue) => (
    (issue.severity === 'error' || issue.severity === 'warning') &&
    !(issue.code === 'PAIRING_NOT_READY' && !pairingExpected)
  ))) return errorPollDelay
  if (current.runtime.enabled && !current.heartbeat_store_reachable) return errorPollDelay
  if (pairing.state_error) return errorPollDelay
  if (pairingExpected && (!current.runtime.enabled || !pairing.paired || !pairing.production_ready ||
    !pairing.protocol_compatible || !pairing.database_shared || !pairing.redis_shared || !pairing.auth_compatible)) return errorPollDelay
  if (current.runtime.enabled && (current.nodes ?? []).some((node) => (
    (node.is_local || (pairingExpected && node.weight > 0)) && (!node.online || !node.proxy_valid)
  ))) return errorPollDelay
  return healthyPollDelay
}

function clearStatusPoll(): void {
  if (statusPollTimer !== null) window.clearTimeout(statusPollTimer)
  statusPollTimer = null
}

function scheduleStatusPoll(): void {
  clearStatusPoll()
  if (disposed || document.visibilityState !== 'visible' || refreshInFlight ||
    saving.value || pairingSaving.value || runtimeSaving.value || takeoverSaving.value) return
  statusPollTimer = window.setTimeout(() => {
    statusPollTimer = null
    void refreshAll(true)
  }, statusPollDelay())
}

function invalidateStatusRefresh(): void {
  refreshRevision += 1
  clearStatusPoll()
}

async function refreshAfterMutation(): Promise<void> {
  // A pre-mutation read must finish (and be discarded) before the fresh read.
  await refreshInFlight
  await refreshAll()
}

function refreshAll(silent = false): Promise<void> {
  if (disposed || document.visibilityState !== 'visible') return Promise.resolve()
  clearStatusPoll()
  if (refreshInFlight) {
    refreshSilent &&= silent
    return refreshInFlight
  }
  loading.value = true
  refreshSilent = silent
  const revision = refreshRevision
  refreshInFlight = Promise.allSettled([
    adminAPI.executionNodes.getStatus(),
    adminAPI.executionNodes.getPairingStatus()
  ]).then(([statusResult, pairingResult]) => {
    if (disposed || revision !== refreshRevision) return
    refreshFailed = statusResult.status === 'rejected' || pairingResult.status === 'rejected'
    if (statusResult.status === 'fulfilled') {
      status.value = statusResult.value
      lastSyncedAt.value = Date.now()
      if (!refreshSilent || !draftDirty.value) syncDraft(statusResult.value)
    } else if (!refreshSilent) {
      appStore.showError(extractI18nErrorMessage(statusResult.reason, t, 'admin.executionNodes.issues', t('admin.executionNodes.reloadFailed')))
    }
    if (pairingResult.status === 'fulfilled') {
      pairingStatus.value = pairingResult.value
    } else if (!refreshSilent) {
      appStore.showError(extractI18nErrorMessage(pairingResult.reason, t, 'admin.executionNodes.issues', t('admin.executionNodes.pairingLoadFailed')))
    }
  }).finally(() => {
    refreshInFlight = null
    if (disposed) return
    loading.value = false
    scheduleStatusPoll()
  })
  return refreshInFlight
}

function handleVisibilityChange(): void {
  clearStatusPoll()
  if (document.visibilityState !== 'visible') {
    invalidateStatusRefresh()
  } else if (!saving.value && !pairingSaving.value && !runtimeSaving.value && !takeoverSaving.value) {
    void resumeStatusRefresh()
  }
}

async function resumeStatusRefresh(): Promise<void> {
  await refreshInFlight
  if (saving.value || pairingSaving.value || runtimeSaving.value || takeoverSaving.value) return
  // Several visibility events during one read still coalesce into one refresh.
  await refreshAll(true)
}

async function generateInvite(): Promise<void> {
  pairingSaving.value = true
  invalidateStatusRefresh()
  try {
    pairingInvite.value = await adminAPI.executionNodes.generatePairingInvite()
    appStore.showSuccess(t('admin.executionNodes.inviteGenerated'))
    await refreshAfterMutation()
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.executionNodes.issues', t('admin.executionNodes.pairingFailed')))
  } finally {
    pairingSaving.value = false
    scheduleStatusPoll()
  }
}

async function copyInvite(): Promise<void> {
  if (!pairingInvite.value) return
  try {
    await navigator.clipboard.writeText(pairingInvite.value.token)
    appStore.showSuccess(t('admin.executionNodes.inviteCopied'))
  } catch {
    appStore.showError(t('admin.executionNodes.inviteCopyFailed'))
  }
}

async function pairNode(): Promise<void> {
  pairingSaving.value = true
  invalidateStatusRefresh()
  try {
    pairingStatus.value = await adminAPI.executionNodes.pairExecutionNode(pairingPeerURL.value, pairingToken.value, pairingTargetID.value, pairingTargetURL.value)
    pairingToken.value = ''
    if (pairingStatus.value.paired && !pairingStatus.value.production_ready) {
      appStore.showSuccess(t('admin.executionNodes.pairApplying'))
      void waitForJoinedServer()
      return
    }
    appStore.showSuccess(t('admin.executionNodes.pairSuccess'))
    await refreshAfterMutation()
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.executionNodes.issues', t('admin.executionNodes.pairingFailed')))
  } finally {
    pairingSaving.value = false
    scheduleStatusPoll()
  }
}

async function waitForJoinedServer(): Promise<void> {
  await new Promise(resolve => window.setTimeout(resolve, 3000))
  for (let attempt = 0; attempt < 45; attempt += 1) {
    try {
      const response = await fetch('/health', { cache: 'no-store' })
      if (response.ok) {
        window.location.assign('/login')
        return
      }
    } catch {
      // The application is expected to be briefly unavailable while it switches state.
    }
    await new Promise(resolve => window.setTimeout(resolve, 2000))
  }
  appStore.showError(t('admin.executionNodes.pairRecoveryTimeout'))
}

async function unpairNode(): Promise<void> {
  showUnpairConfirm.value = false
  pairingSaving.value = true
  invalidateStatusRefresh()
  try {
    await adminAPI.executionNodes.unpairExecutionNode()
    await refreshAfterMutation()
    appStore.showSuccess(t('admin.executionNodes.unpairSuccess'))
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.executionNodes.issues', t('admin.executionNodes.pairingFailed')))
  } finally {
    pairingSaving.value = false
    scheduleStatusPoll()
  }
}

function formatPairingTime(value: string): string {
  return new Date(value).toLocaleString()
}

function requestToggle(next: boolean): void {
  if (next) {
    if (!status.value?.can_enable) {
      appStore.showError(statusIssues.value.filter((issue) => issue.severity === 'error').map(issueText).join('；') || t('admin.executionNodes.cannotEnable'))
      return
    }
    showEnableConfirm.value = true
    return
  }
  draftEnabled.value = false
  draftDirty.value = true
}

function confirmEnable(): void {
  showEnableConfirm.value = false
  draftEnabled.value = true
  draftDirty.value = true
}

async function save(): Promise<void> {
  if (!hasValidDraft.value) {
    appStore.showError(draftEnabled.value ? t('admin.executionNodes.invalidProxy') : t('admin.executionNodes.invalidWeights'))
    return
  }
  const weights: Record<string, number> = {}
  const proxyIDs: Record<string, number> = {}
  for (const node of draftNodes.value) {
    const nodeID = node.nodeID.trim()
    weights[nodeID] = Number(node.weight)
    if (node.proxyID > 0) proxyIDs[nodeID] = Math.floor(node.proxyID)
  }
  saving.value = true
  invalidateStatusRefresh()
  try {
    await adminAPI.settings.updateSettings({ execution_node_balancing_enabled: draftEnabled.value, execution_node_weights: weights, execution_node_proxy_ids: proxyIDs })
    appStore.showSuccess(t('admin.executionNodes.saveSuccess'))
    await refreshAfterMutation()
  } catch (error) {
    appStore.showError(extractI18nErrorMessage(error, t, 'admin.executionNodes.issues', t('admin.executionNodes.saveFailed')))
  } finally {
    saving.value = false
    scheduleStatusPoll()
  }
}

function formatSyncTime(value: number): string {
  return new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
  void refreshAll()
})

onUnmounted(() => {
  disposed = true
  invalidateStatusRefresh()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>
