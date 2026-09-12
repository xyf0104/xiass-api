<template>
  <BaseDialog
    :show="show" title="号池管理" width="wide"
    :close-on-escape="!busy && !confirmation" :show-close-button="!busy"
    @close="close"
  >
    <div class="min-w-0 space-y-4 text-sm text-gray-700 dark:text-gray-200" :aria-busy="loading || busy">
      <div v-if="loadError || mutationError" role="alert" class="flex flex-wrap items-center gap-2 break-words text-red-600 dark:text-red-300">
        <span class="min-w-0 flex-1">{{ loadError || mutationError }}</span>
        <button v-if="loadError" type="button" class="btn btn-secondary btn-sm" :disabled="loading || busy" data-testid="pool-retry" @click="load()">
          <Icon name="refresh" size="sm" />重试加载
        </button>
      </div>
      <p v-if="loading" role="status" class="text-gray-500 dark:text-gray-400">正在加载号池和账号…</p>
      <p v-if="notice && !loading" role="status" class="text-emerald-700 dark:text-emerald-300">{{ notice }}</p>

      <fieldset :disabled="locked" class="min-w-0 space-y-3">
        <div class="flex min-w-0 items-end gap-2">
          <label class="min-w-0 flex-1">
            <span class="input-label">号池</span>
            <select v-model="poolId" class="input w-full" data-testid="pool-select">
              <option :value="null">新建号池</option>
              <option v-for="pool in pools" :key="pool.id" :value="pool.id">{{ pool.name }} ({{ pool.account_count }})</option>
            </select>
          </label>
          <button type="button" class="btn btn-secondary h-10 w-10 shrink-0 p-0" title="新建号池" aria-label="新建号池" data-testid="pool-new" @click="newPool">
            <Icon name="plus" size="sm" />
          </button>
          <button type="button" class="btn btn-secondary h-10 w-10 shrink-0 p-0" title="刷新号池和账号" aria-label="刷新号池和账号" @click="load()">
            <Icon name="refresh" size="sm" />
          </button>
        </div>
        <form class="flex min-w-0 flex-wrap items-end gap-2" @submit.prevent="saveName">
          <label class="min-w-0 flex-1 basis-48">
            <span class="input-label">号池名称</span>
            <input v-model="name" class="input w-full" maxlength="100" required data-testid="pool-name" />
          </label>
          <button type="submit" class="btn btn-primary shrink-0" :disabled="!validName || (!!currentPool && name.trim() === currentPool.name)" data-testid="pool-save">
            <Icon :name="currentPool ? 'check' : 'plus'" size="sm" />{{ currentPool ? '保存名称' : '创建号池' }}
          </button>
          <button v-if="currentPool" type="button" class="btn btn-secondary h-10 w-10 shrink-0 p-0 text-red-600 dark:text-red-300" title="删除号池" aria-label="删除号池" data-testid="pool-delete" @click="askDelete">
            <Icon name="trash" size="sm" />
          </button>
        </form>
        <div class="min-w-0">
          <span id="account-pool-proxy-label" class="input-label">号池代理节点</span>
          <ProxySelector
            :model-value="currentPool ? currentPool.proxy_id : draftProxy" :proxies="proxies"
            :disabled="locked" aria-labelledby="account-pool-proxy-label" @update:model-value="changeProxy"
          />
        </div>
      </fieldset>

      <section v-if="currentPool" class="min-w-0 border-t border-gray-200 pt-3 dark:border-dark-700" aria-label="号池账号">
        <fieldset :disabled="locked" class="min-w-0 space-y-3">
          <div class="flex flex-wrap items-center gap-3">
            <div class="flex items-center gap-3" role="radiogroup" aria-label="账号范围">
              <label class="flex items-center gap-1.5"><input v-model="scope" type="radio" value="all" name="account-pool-scope" />全部账号</label>
              <label class="flex items-center gap-1.5"><input v-model="scope" type="radio" value="pool" name="account-pool-scope" />当前号池</label>
            </div>
            <input v-model="search" type="search" class="input min-w-0 flex-1 basis-48" aria-label="搜索账号、节点或号池" placeholder="搜索账号、节点或号池" data-testid="pool-search" />
          </div>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <label class="flex items-center gap-2">
              <input type="checkbox" :checked="allSelected" :indeterminate="selected.size > 0 && !allSelected" :disabled="!selectableAccounts.length" data-testid="pool-select-all" @change="selectAll" />
              全选可选账号 <span class="text-gray-500 dark:text-gray-400">{{ selected.size }}/{{ selectableAccounts.length }}</span>
            </label>
            <div class="flex flex-wrap gap-2">
              <button type="button" class="btn btn-primary btn-sm" :disabled="!addIds.length || addIds.length > 5000" data-testid="pool-add" @click="askMembers(false)"><Icon name="plus" size="sm" />加入号池<span v-if="addIds.length"> ({{ addIds.length }})</span></button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!removeIds.length || removeIds.length > 5000" data-testid="pool-remove" @click="askMembers(true)"><Icon name="x" size="sm" />移出号池<span v-if="removeIds.length"> ({{ removeIds.length }})</span></button>
            </div>
          </div>
          <p v-if="selected.size > 5000" role="alert" class="text-red-600 dark:text-red-300">单次最多操作 5000 个账号，请缩小选择范围。</p>
          <p v-if="!currentPool.account_count" class="text-gray-500 dark:text-gray-400">当前号池暂无账号。</p>
          <div class="max-h-[42vh] min-w-0 overflow-y-auto overflow-x-hidden border-y border-gray-200 dark:border-dark-700">
            <label v-for="account in visibleAccounts" :key="account.id" class="flex min-w-0 items-start gap-3 border-b border-gray-100 py-3 last:border-b-0 dark:border-dark-700" :data-testid="`pool-account-${account.id}`">
              <input type="checkbox" class="mt-1 shrink-0" :checked="selected.has(account.id)" :disabled="!!selectionDisabledReason(account)" :aria-label="`选择账号 ${account.name}`" @change="toggleAccount(account.id)" />
              <span class="grid min-w-0 flex-1 gap-1 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] sm:gap-x-4">
                <span class="min-w-0 break-all font-medium">{{ account.name }} <span class="font-normal text-gray-500 dark:text-gray-400">#{{ account.id }}</span></span>
                <span class="min-w-0 break-all text-gray-600 dark:text-gray-300">所属服务器：{{ account.execution_node_id?.trim() || '本机' }}</span>
                <span class="min-w-0 break-words text-xs text-gray-500 dark:text-gray-400">{{ account.platform }} · {{ planName(account) }}</span>
                <span class="min-w-0 break-all text-xs" :class="membership.get(account.id)?.id === poolId ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-500 dark:text-gray-400'">号池：{{ membership.get(account.id)?.name || '未分配' }}</span>
                <span class="min-w-0 break-all text-xs text-gray-500 dark:text-gray-400 sm:col-span-2">代理出口：{{ proxyName(account.proxy_id) }}</span>
                <span v-if="selectionDisabledReason(account)" class="min-w-0 break-words text-xs text-amber-700 dark:text-amber-300 sm:col-span-2">{{ selectionDisabledReason(account) }}</span>
              </span>
            </label>
            <p v-if="!visibleAccounts.length && !loading" class="py-6 text-center text-gray-500 dark:text-gray-400">{{ search ? '没有匹配的账号' : '暂无账号' }}</p>
          </div>
        </fieldset>
      </section>
      <p v-else-if="!loading && !pools.length && !loadError" class="text-gray-500 dark:text-gray-400">暂无号池。</p>
    </div>
    <template #footer>
      <div class="flex items-center justify-between gap-3">
        <span role="status" class="text-sm text-gray-500 dark:text-gray-400">{{ busy ? '正在保存…' : currentPool ? `${currentPool.account_count} 个成员` : '' }}</span>
        <button type="button" class="btn btn-secondary" :disabled="busy || !!confirmation" @click="close">关闭</button>
      </div>
    </template>
  </BaseDialog>
  <ConfirmDialog
    :show="!!confirmation" :title="confirmation?.title || ''" :message="confirmation?.message || ''"
    :danger="confirmation?.kind === 'remove' || confirmation?.kind === 'delete'"
    confirm-text="确认" cancel-text="取消" @cancel="confirmation = null" @confirm="confirmAction"
  />
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import Icon from '@/components/icons/Icon.vue'
import { accountPoolsAPI, getAccountPoolAccounts, type AccountPool } from '@/api/admin/accountPools'
import type { Account, Proxy } from '@/types'

const props = defineProps<{ show: boolean; proxies: Proxy[] }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const pools = ref<AccountPool[]>([])
const accounts = ref<Account[]>([])
const poolId = ref<number | null>(null)
const name = ref('号池分组1')
const draftProxy = ref<number | null>(null)
const search = ref('')
const scope = ref<'all' | 'pool'>('all')
const selected = ref(new Set<number>())
const loading = ref(false)
const busy = ref(false)
const ready = ref(false)
const loadError = ref('')
const mutationError = ref('')
const notice = ref('')
let loadController: AbortController | undefined
let generation = 0

type Confirmation = { title: string; message: string; poolId: number } & (
  { kind: 'proxy'; proxyId: number | null } |
  { kind: 'add' | 'remove'; accountIds: number[] } |
  { kind: 'delete' }
)
const confirmation = ref<Confirmation | null>(null)
const currentPool = computed(() => pools.value.find(pool => pool.id === poolId.value))
const locked = computed(() => loading.value || busy.value || !ready.value || !!confirmation.value)
const validName = computed(() => {
  const value = name.value.trim()
  return value.length > 0 && [...value].length <= 100 && [...value].every(character => {
    const code = character.charCodeAt(0)
    return code > 31 && (code < 127 || code > 159)
  })
})
const membership = computed(() => {
  const result = new Map<number, AccountPool>()
  for (const pool of pools.value) for (const id of pool.account_ids) result.set(id, pool)
  return result
})
const visibleAccounts = computed(() => {
  const query = search.value.trim().toLowerCase()
  return accounts.value.filter(account => {
    if (scope.value === 'pool' && membership.value.get(account.id)?.id !== poolId.value) return false
    return !query || [account.id, account.name, account.platform, planName(account), proxyName(account.proxy_id), account.execution_node_id, membership.value.get(account.id)?.name]
      .join(' ').toLowerCase().includes(query)
  })
})
const selectableAccounts = computed(() => visibleAccounts.value.filter(account => !selectionDisabledReason(account)))
const allSelected = computed(() => selectableAccounts.value.length > 0 && selectableAccounts.value.every(account => selected.value.has(account.id)))
const addIds = computed(() => [...selected.value].filter(id => membership.value.get(id)?.id !== poolId.value))
const removeIds = computed(() => [...selected.value].filter(id => membership.value.get(id)?.id === poolId.value))

function proxyName(id: number | null): string {
  return id === null ? '直连（无代理）' : props.proxies.find(proxy => proxy.id === id)?.name || `代理 #${id}`
}

function planName(account: Account): string {
  const plan = account.credentials?.plan_type || account.parent_plan_type
  return typeof plan === 'string' ? plan : account.type
}

function selectionDisabledReason(account: Account): string {
  const flags = account as Account & { credential_shadow?: boolean; read_only?: boolean }
  if (account.parent_account_id != null || flags.credential_shadow === true || account.extra?.credential_shadow === true) {
    return '影子账号 · 不可选择'
  }
  if (flags.read_only === true || account.extra?.read_only === true) return '只读账号 · 不可选择'
  return ''
}

function resetDraft() {
  let number = 1
  while (pools.value.some(pool => pool.name === `号池分组${number}`)) number++
  name.value = currentPool.value?.name ?? `号池分组${number}`
  draftProxy.value = null
  selected.value = new Set()
  mutationError.value = ''
}

function newPool() {
  if (locked.value) return
  poolId.value = null
  resetDraft()
  notice.value = ''
}

function errorMessage(error: unknown): string {
  const detail = error as { reason?: string; code?: string; message?: string } | null
  if (detail?.reason === 'ACCOUNT_POOL_NAME_TAKEN' || detail?.code === 'ACCOUNT_POOL_NAME_TAKEN' || detail?.message?.includes('pool name already exists')) {
    return '号池名称已存在，请使用其他名称。'
  }
  return detail?.message || '操作失败，请重试。'
}

async function load(preferredId: number | null = poolId.value) {
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loading.value = true
  ready.value = false
  loadError.value = ''
  selected.value = new Set()
  try {
    const result = await accountPoolsAPI.list(controller.signal)
    const rows = await getAccountPoolAccounts(controller.signal)
    if (controller.signal.aborted || !props.show) return
    pools.value = result.items
    accounts.value = rows
    poolId.value = result.items.some(pool => pool.id === preferredId) ? preferredId : result.items[0]?.id ?? null
    resetDraft()
    ready.value = true
  } catch (error) {
    if (!controller.signal.aborted && props.show) loadError.value = `加载失败：${errorMessage(error)}`
  } finally {
    if (loadController === controller) loading.value = false
  }
}

async function mutate(action: () => Promise<number | null>, message: string) {
  if (locked.value) return
  const startedGeneration = generation
  busy.value = true
  mutationError.value = ''
  notice.value = ''
  try {
    const id = await action()
    emit('updated')
    if (startedGeneration !== generation || !props.show) return
    notice.value = message
    await load(id)
  } catch (error) {
    if (startedGeneration === generation && props.show) mutationError.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

function saveName() {
  if (locked.value || !validName.value) return
  const value = name.value.trim()
  if (pools.value.some(pool => pool.id !== poolId.value && pool.name === value)) {
    mutationError.value = '号池名称已存在，请使用其他名称。'
    return
  }
  const pool = currentPool.value
  if (pool && pool.name === value) return
  const proxyId = draftProxy.value
  void mutate(async () => (pool ? await accountPoolsAPI.rename(pool.id, value) : await accountPoolsAPI.create({ name: value, proxy_id: proxyId })).id, pool ? '号池名称已保存。' : '号池已创建。')
}

function changeProxy(proxyId: number | null) {
  if (locked.value) return
  const pool = currentPool.value
  if (!pool) { draftProxy.value = proxyId; return }
  if (proxyId === pool.proxy_id) return
  confirmation.value = {
    kind: 'proxy', poolId: pool.id, proxyId, title: '覆盖号池代理',
    message: `将“${pool.name}”的代理改为${proxyName(proxyId)}，并覆盖全部 ${pool.account_count} 个现有成员的代理。后续加入的账号也将继承此代理。确认继续？`
  }
}

function askMembers(remove: boolean) {
  const pool = currentPool.value
  const ids = remove ? removeIds.value : addIds.value
  if (locked.value || !pool || !ids.length || ids.length > 5000) return
  confirmation.value = {
    kind: remove ? 'remove' : 'add', poolId: pool.id, accountIds: [...ids],
    title: remove ? '移出号池' : '加入号池并应用代理',
    message: remove
      ? `将选中的 ${ids.length} 个成员移出“${pool.name}”，账号及其当前代理保持不变。确认继续？`
      : `将选中的 ${ids.length} 个账号加入“${pool.name}”，替换原号池归属，并将其代理覆盖为${proxyName(pool.proxy_id)}。确认继续？`
  }
}

function askDelete() {
  const pool = currentPool.value
  if (locked.value || !pool) return
  confirmation.value = {
    kind: 'delete', poolId: pool.id, title: '删除号池',
    message: `删除“${pool.name}”并移出其全部 ${pool.account_count} 个成员。不会删除账号，也不会更改账号当前代理。确认删除？`
  }
}

function confirmAction() {
  const action = confirmation.value
  if (!action || busy.value || loading.value || !ready.value) return
  confirmation.value = null
  void mutate(async () => {
    if (action.kind === 'delete') { await accountPoolsAPI.delete(action.poolId); return null }
    if (action.kind === 'proxy') await accountPoolsAPI.setProxy(action.poolId, action.proxyId)
    else await accountPoolsAPI.assign(action.poolId, action.accountIds, action.kind === 'remove')
    return action.poolId
  }, action.kind === 'delete' ? '号池已删除，账号和代理已保留。' : action.kind === 'remove' ? '成员已移出，账号代理已保留。' : action.kind === 'add' ? '账号已加入号池并继承代理。' : '号池及全部现有成员的代理已更新。')
}

function toggleAccount(id: number) {
  if (locked.value || !selectableAccounts.value.some(account => account.id === id)) return
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}

function selectAll() {
  if (!locked.value) selected.value = allSelected.value ? new Set() : new Set(selectableAccounts.value.map(account => account.id))
}

function close() {
  if (!busy.value && !confirmation.value) emit('close')
}

watch(poolId, () => { resetDraft(); notice.value = '' })
watch([search, scope], () => { selected.value = new Set() })
watch(() => props.show, show => {
  generation++
  confirmation.value = null
  notice.value = ''
  mutationError.value = ''
  search.value = ''
  scope.value = 'all'
  if (show) void load()
  else { loadController?.abort(); ready.value = false; accounts.value = []; pools.value = [] }
}, { immediate: true })
onUnmounted(() => { generation++; loadController?.abort() })
</script>
