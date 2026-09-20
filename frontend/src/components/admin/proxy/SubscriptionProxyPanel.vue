<template>
  <div class="space-y-6">
    <section class="border-b border-gray-200 pb-6 dark:border-dark-700">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.proxies.subscriptions.sourcesTitle') }}</h2>
          <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.proxies.subscriptions.sourcesHint') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="preview">
            <Icon name="search" size="sm" />
            <span>{{ previewing ? t('admin.proxies.subscriptions.previewing') : t('admin.proxies.subscriptions.preview') }}</span>
          </button>
          <button type="button" class="btn btn-secondary" :disabled="busy || storedSourceCount === 0" @click="refresh">
            <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" />
            <span>{{ t('admin.proxies.subscriptions.refresh') }}</span>
          </button>
          <button type="button" class="btn btn-primary" :disabled="!canApply" @click="requestApply">
            <Icon name="check" size="sm" />
            <span>{{ applying ? t('admin.proxies.subscriptions.applying') : t('admin.proxies.subscriptions.applyCount', { count: selectedIds.size }) }}</span>
          </button>
        </div>
      </div>

      <div v-if="!loading && !agentAvailable" class="mt-4 flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-300">
        <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
        <span>{{ t('admin.proxies.subscriptions.agentUnavailable') }}</span>
      </div>
      <div v-if="lastError" class="mt-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">{{ lastError }}</div>
      <div v-if="previewStale" class="mt-3 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-300">{{ t('admin.proxies.subscriptions.previewStale') }}</div>

      <div class="mt-5 grid gap-4 lg:grid-cols-2">
        <div class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <label class="block" for="subscription-url-batch">
            <span class="input-label">{{ t('admin.proxies.subscriptions.multiUrlLabel') }}</span>
            <textarea id="subscription-url-batch" v-model="urlBatchInput" rows="4" class="input mt-1 font-mono text-xs" :placeholder="t('admin.proxies.subscriptions.multiUrlPlaceholder')" :disabled="busy"></textarea>
          </label>
          <div class="flex justify-end">
            <button type="button" class="btn btn-secondary" :disabled="busy || !urlBatchInput.trim()" @click="addURLBatch">
              <Icon name="plus" size="sm" /><span>{{ t('admin.proxies.subscriptions.addUrls') }}</span>
            </button>
          </div>
        </div>

        <div class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <label class="block" for="subscription-content-paste">
            <span class="input-label">{{ t('admin.proxies.subscriptions.pasteContentLabel') }}</span>
            <textarea id="subscription-content-paste" v-model="pastedInput" rows="4" class="input mt-1 font-mono text-xs" :placeholder="t('admin.proxies.subscriptions.contentPlaceholder')" :disabled="busy"></textarea>
          </label>
          <div class="flex justify-end">
            <button type="button" class="btn btn-secondary" :disabled="busy || !pastedInput.trim()" @click="addPastedInput">
              <Icon name="plus" size="sm" /><span>{{ t('admin.proxies.subscriptions.addContent') }}</span>
            </button>
          </div>
        </div>

        <div class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700 lg:col-span-2">
          <span class="input-label">{{ t('admin.proxies.subscriptions.localFileLabel') }}</span>
          <div class="flex min-h-28 items-center justify-between gap-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 px-4 py-3 dark:border-dark-600 dark:bg-dark-800">
            <div class="min-w-0 text-sm text-gray-600 dark:text-gray-300">
              <div>{{ t('admin.proxies.subscriptions.localFileHint') }}</div>
              <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">YAML / YML / TXT · 2 MiB</div>
            </div>
            <button type="button" class="btn btn-secondary shrink-0" :disabled="busy" @click="fileInput?.click()">
              <Icon name="upload" size="sm" /><span>{{ t('common.chooseFile') }}</span>
            </button>
          </div>
          <input ref="fileInput" type="file" class="hidden" multiple accept=".yaml,.yml,.txt,text/plain,application/x-yaml" @change="handleFiles" />
        </div>
      </div>

      <div class="mt-5 space-y-4">
        <div v-if="sources.length === 0" class="rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ t('admin.proxies.subscriptions.noSources') }}</div>
        <div v-for="(source, index) in sources" :key="source.key" class="border-t border-gray-100 pt-4 dark:border-dark-700">
          <div class="grid gap-3 lg:grid-cols-[10rem_minmax(0,1fr)_13rem_auto] lg:items-end">
            <label class="block min-w-0">
              <span class="input-label">{{ t('admin.proxies.subscriptions.name') }}</span>
              <input v-model="source.name" class="input mt-1 w-full" :placeholder="t('admin.proxies.subscriptions.defaultName', { index: index + 1 })" :disabled="busy" @input="invalidatePreview" />
            </label>
            <label class="block min-w-0">
              <span class="input-label">{{ source.kind === 'url' ? t('admin.proxies.subscriptions.url') : t('admin.proxies.subscriptions.content') }}</span>
              <input v-if="source.kind === 'url'" v-model="source.url" class="input mt-1 w-full font-mono text-xs" type="url" autocomplete="off" :placeholder="source.maskedURL || t('admin.proxies.subscriptions.keepConfigured')" :disabled="busy" @input="invalidatePreview" />
              <textarea v-else v-model="source.input" rows="3" class="input mt-1 w-full font-mono text-xs" :placeholder="source.configured ? t('admin.proxies.subscriptions.keepConfigured') : t('admin.proxies.subscriptions.contentPlaceholder')" :disabled="busy" @input="invalidatePreview"></textarea>
            </label>
            <label class="block min-w-0">
              <span class="input-label">{{ t('admin.proxies.subscriptions.userAgent') }}</span>
              <input v-model="source.userAgent" class="input mt-1 w-full" autocomplete="off" placeholder="XIASS-Proxy-Agent/1" :disabled="busy" @input="invalidatePreview" />
            </label>
            <button type="button" class="btn btn-secondary h-10 px-3 text-red-600 dark:text-red-400" :disabled="busy" :title="t('admin.proxies.subscriptions.removeSource')" @click="removeSource(index)"><Icon name="trash" size="sm" /></button>
            <label class="block min-w-0 lg:col-span-2">
              <span class="input-label">{{ t('admin.proxies.subscriptions.protocolFilter') }}</span>
              <input v-model="source.protocols" class="input mt-1 w-full" :placeholder="t('admin.proxies.subscriptions.protocolPlaceholder')" :disabled="busy" @input="invalidatePreview" />
            </label>
            <label class="block min-w-0 lg:col-span-2">
              <span class="input-label">{{ t('admin.proxies.subscriptions.excludeKeywords') }}</span>
              <input v-model="source.excludes" class="input mt-1 w-full" :placeholder="t('admin.proxies.subscriptions.excludePlaceholder')" :disabled="busy" @input="invalidatePreview" />
            </label>
          </div>
          <label class="mt-3 flex items-center gap-2 text-sm text-amber-700 dark:text-amber-400">
            <input type="checkbox" class="checkbox" :checked="source.allowInsecureTLS" :disabled="busy" data-test="allow-insecure-tls" @change="requestTLSChange(source, $event)" />
            <span>{{ t('admin.proxies.subscriptions.allowInsecureTLS') }}</span>
          </label>
        </div>
      </div>
    </section>

    <section>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.proxies.subscriptions.nodesTitle') }}</h2>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.subscriptions.selectedSummary', { selected: selectedIds.size, total: nodes.length }) }}</p>
        </div>
        <div class="flex w-full flex-wrap items-center gap-2 lg:w-auto lg:justify-end">
          <div class="relative min-w-48 flex-1 lg:w-60 lg:flex-none">
            <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input v-model="search" class="input w-full pl-9" :placeholder="t('admin.proxies.subscriptions.searchNodes')" />
          </div>
          <select v-model="sourceFilter" class="input min-w-36 flex-1 lg:w-44 lg:flex-none">
            <option value="">{{ t('admin.proxies.subscriptions.allSources') }}</option>
            <option v-for="source in nodeSources" :key="source.id" :value="source.id">{{ source.name }}</option>
          </select>
          <select v-model="protocolFilter" class="input min-w-32 flex-1 lg:w-40 lg:flex-none">
            <option value="">{{ t('admin.proxies.subscriptions.allProtocols') }}</option>
            <option v-for="protocol in nodeProtocols" :key="protocol" :value="protocol">{{ protocol.toUpperCase() }}</option>
          </select>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || filteredNodes.length === 0" @click="selectVisible">{{ t('admin.proxies.subscriptions.selectAll') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || filteredNodes.length === 0" @click="invertVisible">{{ t('admin.proxies.subscriptions.invert') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || selectedIds.size === 0" @click="clearSelection">{{ t('admin.proxies.subscriptions.clear') }}</button>
        </div>
      </div>

      <div class="mt-3 hidden border-y border-gray-200 dark:border-dark-700 md:block">
        <table class="w-full table-fixed divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400">
            <tr>
              <th class="w-12 px-3 py-3 text-left"><input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" :checked="allVisibleSelected" :indeterminate="someVisibleSelected" :disabled="busy || filteredNodes.length === 0" @change="toggleVisible" /></th>
              <th class="px-3 py-3 text-left">{{ t('admin.proxies.subscriptions.node') }}</th>
              <th class="w-48 px-3 py-3 text-left">{{ t('admin.proxies.subscriptions.source') }}</th>
              <th class="w-32 px-3 py-3 text-left">{{ t('admin.proxies.subscriptions.protocol') }}</th>
              <th class="w-36 px-3 py-3 text-left">{{ t('admin.proxies.subscriptions.xiassProxy') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="node in filteredNodes" :key="node.id" class="hover:bg-gray-50 dark:hover:bg-dark-800/70">
              <td class="px-3 py-3"><input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" :checked="selectedIds.has(node.id)" :disabled="busy || node.missing" @change="toggleNode(node.id)" /></td>
              <td class="min-w-0 px-3 py-3 font-medium text-gray-900 dark:text-white"><div class="truncate" :title="node.name">{{ node.name }}</div></td>
              <td class="min-w-0 px-3 py-3 text-gray-500 dark:text-gray-400"><div class="truncate">{{ node.source_name || node.source_id }}</div></td>
              <td class="px-3 py-3"><span class="badge badge-gray uppercase">{{ node.protocol }}</span></td>
              <td class="px-3 py-3 font-mono text-xs text-gray-500 dark:text-gray-400">{{ node.proxy_id ? `#${node.proxy_id}` : t('admin.proxies.subscriptions.createOnApply') }}</td>
            </tr>
            <tr v-if="filteredNodes.length === 0"><td colspan="5" class="px-4 py-10 text-center text-gray-500 dark:text-gray-400">{{ emptyNodesText }}</td></tr>
          </tbody>
        </table>
      </div>

      <div class="mt-3 space-y-2 md:hidden">
        <label v-for="node in filteredNodes" :key="node.id" class="block rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900" :class="selectedIds.has(node.id) && 'border-primary-300 bg-primary-50/40 dark:border-primary-700 dark:bg-primary-900/10'">
          <div class="flex items-start gap-3">
            <input type="checkbox" class="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600" :checked="selectedIds.has(node.id)" :disabled="busy || node.missing" @change="toggleNode(node.id)" />
            <div class="min-w-0 flex-1">
              <div class="break-words text-sm font-medium text-gray-900 dark:text-white">{{ node.name }}</div>
              <div class="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                <span class="text-gray-400">{{ t('admin.proxies.subscriptions.source') }}</span><span class="min-w-0 break-words text-right text-gray-600 dark:text-gray-300">{{ node.source_name || node.source_id }}</span>
                <span class="text-gray-400">{{ t('admin.proxies.subscriptions.protocol') }}</span><span class="text-right uppercase text-gray-600 dark:text-gray-300">{{ node.protocol }}</span>
                <span class="text-gray-400">{{ t('admin.proxies.subscriptions.xiassProxy') }}</span><span class="text-right font-mono text-gray-600 dark:text-gray-300">{{ node.proxy_id ? `#${node.proxy_id}` : t('admin.proxies.subscriptions.createOnApply') }}</span>
              </div>
            </div>
          </div>
        </label>
        <div v-if="filteredNodes.length === 0" class="rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ emptyNodesText }}</div>
      </div>

      <p v-if="updatedAt" class="mt-2 text-right text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.subscriptions.lastSync', { time: formatDateTime(updatedAt) }) }}</p>
    </section>

    <ConfirmDialog :show="showDestructiveApply" :title="t('admin.proxies.subscriptions.destructiveTitle')" :message="destructiveMessage" :confirm-text="t('admin.proxies.subscriptions.destructiveConfirm')" :cancel-text="t('common.cancel')" danger @confirm="confirmApply" @cancel="showDestructiveApply = false" />
    <ConfirmDialog :show="Boolean(pendingTLSSourceKey)" :title="t('admin.proxies.subscriptions.tlsConfirmTitle')" :message="t('admin.proxies.subscriptions.tlsConfirmMessage')" :confirm-text="t('common.confirm')" :cancel-text="t('common.cancel')" danger @confirm="confirmTLSChange" @cancel="pendingTLSSourceKey = ''" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { ProxySubscriptionNode, ProxySubscriptionOverview, ProxySubscriptionSource } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'

type SourceKind = 'url' | 'input'
type SourceForm = { key: string; id: string; name: string; kind: SourceKind; url: string; input: string; maskedURL: string; configured: boolean; userAgent: string; protocols: string; excludes: string; allowInsecureTLS: boolean }

const MAX_SOURCES = 16
const MAX_INPUT_BYTES = 2 << 20
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const sources = reactive<SourceForm[]>([])
const nodes = ref<ProxySubscriptionNode[]>([])
const selectedIds = reactive(new Set<string>())
const search = ref('')
const sourceFilter = ref('')
const protocolFilter = ref('')
const urlBatchInput = ref('')
const pastedInput = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const loading = ref(false)
const previewing = ref(false)
const applying = ref(false)
const refreshing = ref(false)
const agentAvailable = ref(false)
const updatedAt = ref('')
const lastError = ref('')
const previewID = ref('')
const previewSignature = ref('')
const hadPreview = ref(false)
const showDestructiveApply = ref(false)
const pendingTLSSourceKey = ref('')

const busy = computed(() => loading.value || previewing.value || applying.value || refreshing.value)
const storedSourceCount = computed(() => sources.filter(source => source.configured).length)
const currentSignature = computed(() => JSON.stringify(sourcePayload()))
const previewCurrent = computed(() => Boolean(previewID.value) && previewSignature.value === currentSignature.value)
const previewStale = computed(() => hadPreview.value && !previewCurrent.value)
const canApply = computed(() => !busy.value && agentAvailable.value && previewCurrent.value)
const filteredNodes = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  return nodes.value.filter(node => (!sourceFilter.value || node.source_id === sourceFilter.value) && (!protocolFilter.value || node.protocol === protocolFilter.value) && (!keyword || `${node.name} ${node.source_name || ''} ${node.protocol}`.toLowerCase().includes(keyword)))
})
const nodeSources = computed(() => {
  const seen = new Map<string, string>()
  nodes.value.forEach(node => seen.set(node.source_id, node.source_name || node.source_id))
  return [...seen].map(([id, name]) => ({ id, name }))
})
const nodeProtocols = computed(() => [...new Set(nodes.value.map(node => node.protocol))].sort())
const selectableVisibleNodes = computed(() => filteredNodes.value.filter(node => !node.missing))
const allVisibleSelected = computed(() => selectableVisibleNodes.value.length > 0 && selectableVisibleNodes.value.every(node => selectedIds.has(node.id)))
const someVisibleSelected = computed(() => !allVisibleSelected.value && selectableVisibleNodes.value.some(node => selectedIds.has(node.id)))
const emptyNodesText = computed(() => nodes.value.length === 0 ? t('admin.proxies.subscriptions.previewFirst') : t('common.noOptionsFound'))
const destructiveMessage = computed(() => sources.length === 0 ? t('admin.proxies.subscriptions.removeAllConfirm') : t('admin.proxies.subscriptions.disableAllConfirm'))

function newKey(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `source-${Date.now()}-${Math.random()}` }
function makeSource(kind: SourceKind, name = '', value = ''): SourceForm {
  return { key: newKey(), id: '', name, kind, url: kind === 'url' ? value : '', input: kind === 'input' ? value : '', maskedURL: '', configured: false, userAgent: '', protocols: '', excludes: '', allowInsecureTLS: false }
}
function splitList(value: string): string[] { return [...new Set(value.split(/[,，\n]/).map(item => item.trim()).filter(Boolean))] }
function sourcePayload(): ProxySubscriptionSource[] {
  return sources.map((source, index) => ({ id: source.id.trim(), name: source.name.trim() || t('admin.proxies.subscriptions.defaultName', { index: index + 1 }), url: source.kind === 'url' ? source.url.trim() : undefined, input: source.kind === 'input' ? source.input.trim() : undefined, user_agent: source.userAgent.trim(), include_protocols: splitList(source.protocols), exclude_keywords: splitList(source.excludes), allow_insecure_tls: source.allowInsecureTLS }))
}
function updateStatus(overview: ProxySubscriptionOverview) { agentAvailable.value = overview.agent_available; updatedAt.value = overview.updated_at || ''; lastError.value = overview.last_error || '' }
function replaceSelection(nextNodes: ProxySubscriptionNode[], fallback?: Set<string>) {
  selectedIds.clear()
  nextNodes.forEach(node => { if (!node.missing && (fallback ? fallback.has(node.id) : node.selected)) selectedIds.add(node.id) })
}
function loadOverview(overview: ProxySubscriptionOverview) {
  const nextSources: SourceForm[] = overview.sources.map(source => ({ key: source.id || newKey(), id: source.id, name: source.name || '', kind: source.source_kind === 'input' ? 'input' : 'url', url: '', input: '', maskedURL: source.url || '', configured: source.configured === true, userAgent: source.user_agent || '', protocols: (source.include_protocols || []).join(', '), excludes: (source.exclude_keywords || []).join(', '), allowInsecureTLS: source.allow_insecure_tls === true }))
  sources.splice(0, sources.length, ...nextSources)
  nodes.value = overview.nodes || []
  replaceSelection(nodes.value)
  updateStatus(overview)
  previewID.value = ''; previewSignature.value = ''; hadPreview.value = false
}
function loadPreview(overview: ProxySubscriptionOverview, submitted: ProxySubscriptionSource[]) {
  const previous = [...sources]
  sources.splice(0, sources.length, ...overview.sources.map((source, index) => {
    const draft = previous[index]
    return { key: draft?.key || source.id || newKey(), id: source.id, name: source.name || draft?.name || '', kind: source.source_kind === 'input' ? 'input' : draft?.kind || 'url', url: submitted[index]?.url || '', input: submitted[index]?.input || '', maskedURL: source.url || draft?.maskedURL || '', configured: source.configured === true, userAgent: source.user_agent || draft?.userAgent || '', protocols: (source.include_protocols || []).join(', '), excludes: (source.exclude_keywords || []).join(', '), allowInsecureTLS: source.allow_insecure_tls === true }
  }))
  nodes.value = overview.nodes || []
  replaceSelection(nodes.value)
  updateStatus(overview)
  previewID.value = overview.preview_id || ''
  previewSignature.value = currentSignature.value
  hadPreview.value = true
}
function invalidatePreview() { if (previewID.value || hadPreview.value) { previewID.value = ''; previewSignature.value = ''; hadPreview.value = true } }

function addURLBatch() {
  const candidates = urlBatchInput.value.split(/\r?\n/).map(value => value.trim()).filter(Boolean)
  const seen = new Set(sources.filter(source => source.kind === 'url' && source.url.trim()).map(source => source.url.trim()))
  let added = 0; let duplicate = 0
  for (const url of candidates) {
    if (seen.has(url)) { duplicate++; continue }
    if (sources.length >= MAX_SOURCES) break
    seen.add(url); sources.push(makeSource('url', '', url)); added++
  }
  if (candidates.length > added + duplicate) appStore.showWarning(t('admin.proxies.subscriptions.sourceLimit', { count: MAX_SOURCES }))
  else if (duplicate > 0) appStore.showInfo(t('admin.proxies.subscriptions.urlsAddedWithDuplicates', { added, duplicate }))
  if (added > 0) invalidatePreview()
  urlBatchInput.value = ''
}
function addPastedInput() {
  const input = pastedInput.value.trim()
  if (!input) return
  if (sources.length >= MAX_SOURCES) { appStore.showWarning(t('admin.proxies.subscriptions.sourceLimit', { count: MAX_SOURCES })); return }
  if (new TextEncoder().encode(input).byteLength > MAX_INPUT_BYTES) { appStore.showError(t('admin.proxies.subscriptions.pastedContentTooLarge')); return }
  if (sources.some(source => source.kind === 'input' && source.input.trim() === input)) { appStore.showInfo(t('admin.proxies.subscriptions.duplicateContent')); return }
  sources.push(makeSource('input', '', input))
  invalidatePreview()
  pastedInput.value = ''
}
async function handleFiles(event: Event) {
  const target = event.target as HTMLInputElement
  const files = [...(target.files || [])]
  const existingInputs = new Set(sources.filter(source => source.kind === 'input').map(source => source.input))
  let added = 0
  for (const file of files) {
    if (sources.length >= MAX_SOURCES) break
    if (file.size > MAX_INPUT_BYTES) { appStore.showError(t('admin.proxies.subscriptions.fileTooLarge', { name: file.name })); continue }
    const input = await file.text()
    if (!input.trim() || existingInputs.has(input)) continue
    existingInputs.add(input)
    sources.push(makeSource('input', file.name.replace(/\.(yaml|yml|txt)$/i, ''), input)); added++
  }
  if (files.length > added && sources.length >= MAX_SOURCES) appStore.showWarning(t('admin.proxies.subscriptions.sourceLimit', { count: MAX_SOURCES }))
  if (added > 0) invalidatePreview()
  target.value = ''
}
function removeSource(index: number) { sources.splice(index, 1); invalidatePreview() }

function requestTLSChange(source: SourceForm, event: Event) {
  const checkbox = event.target as HTMLInputElement
  if (busy.value) { checkbox.checked = source.allowInsecureTLS; return }
  if (checkbox.checked) { checkbox.checked = false; pendingTLSSourceKey.value = source.key }
  else { source.allowInsecureTLS = false; invalidatePreview() }
}
function confirmTLSChange() {
  const source = sources.find(item => item.key === pendingTLSSourceKey.value)
  if (source && !busy.value) { source.allowInsecureTLS = true; invalidatePreview() }
  pendingTLSSourceKey.value = ''
}
function subscriptionError(error: any, fallbackKey: string): string {
  if ((error?.reason || error?.code) === 'INVALID_SUBSCRIPTION_URL') return t('admin.proxies.subscriptions.invalidUrl')
  if (typeof error?.message === 'string' && error.message.includes('insecure TLS is not allowed')) return t('admin.proxies.subscriptions.insecureTLSRejected')
  return error?.message || t(fallbackKey)
}

async function load() {
  loading.value = true
  try { loadOverview(await adminAPI.proxies.getSubscriptions()) }
  catch (error: any) { appStore.showError(subscriptionError(error, 'admin.proxies.subscriptions.loadFailed')) }
  finally { loading.value = false }
}
async function preview() {
  const submitted = sourcePayload()
  previewing.value = true
  try { const overview = await adminAPI.proxies.previewSubscriptions(submitted); loadPreview(overview, submitted); appStore.showSuccess(t('admin.proxies.subscriptions.previewSuccess', { count: nodes.value.length })) }
  catch (error: any) { previewID.value = ''; previewSignature.value = ''; hadPreview.value = true; appStore.showError(subscriptionError(error, 'admin.proxies.subscriptions.previewFailed')) }
  finally { previewing.value = false }
}
function requestApply() { if (canApply.value) { if (sources.length === 0 || selectedIds.size === 0) showDestructiveApply.value = true; else void apply() } }
function confirmApply() { showDestructiveApply.value = false; void apply() }
async function apply() {
  if (!canApply.value || !previewID.value) return
  const selectedCount = selectedIds.size
  applying.value = true
  try { const overview = await adminAPI.proxies.applySubscriptions(sourcePayload(), [...selectedIds], previewID.value); loadOverview(overview); appStore.showSuccess(t('admin.proxies.subscriptions.applySuccess', { count: selectedCount })); emit('changed') }
  catch (error: any) { previewID.value = ''; previewSignature.value = ''; hadPreview.value = true; appStore.showError(subscriptionError(error, 'admin.proxies.subscriptions.applyFailed')) }
  finally { applying.value = false }
}
async function refresh() {
  const draftSelection = new Set(selectedIds)
  refreshing.value = true
  invalidatePreview()
  try { const overview = await adminAPI.proxies.refreshSubscriptions(); nodes.value = overview.nodes || []; replaceSelection(nodes.value, draftSelection.size > 0 ? draftSelection : undefined); updateStatus(overview); appStore.showSuccess(t('admin.proxies.subscriptions.refreshSuccess')); emit('changed') }
  catch (error: any) { appStore.showError(subscriptionError(error, 'admin.proxies.subscriptions.refreshFailed')) }
  finally { refreshing.value = false }
}

function toggleNode(id: string) { if (!busy.value) { if (selectedIds.has(id)) selectedIds.delete(id); else selectedIds.add(id) } }
function selectVisible() { selectableVisibleNodes.value.forEach(node => selectedIds.add(node.id)) }
function invertVisible() { selectableVisibleNodes.value.forEach(node => toggleNode(node.id)) }
function clearSelection() { selectedIds.clear() }
function toggleVisible() { if (allVisibleSelected.value) selectableVisibleNodes.value.forEach(node => selectedIds.delete(node.id)); else selectVisible() }

onMounted(load)
</script>
