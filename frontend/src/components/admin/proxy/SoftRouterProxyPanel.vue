<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-auto">
    <div class="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(340px,420px)]">
      <section class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">软路由代理配置</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              OpenWrt 通过 FRP 主动反连，公网 SOCKS 端口由 Nowind 提供用户名密码认证。
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <button class="btn btn-secondary" :disabled="loading" @click="loadOverview">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button class="btn btn-secondary" :disabled="saving" @click="reconcile">
              <Icon name="sync" size="md" class="mr-2" />
              同步监听
            </button>
            <button class="btn btn-primary" :disabled="saving" @click="saveConfig">
              <Icon name="check" size="md" class="mr-2" />
              保存配置
            </button>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <label class="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
            <input v-model="configForm.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
            <span class="text-sm text-gray-700 dark:text-gray-200">启用公网代理节点</span>
          </label>
          <div>
            <label class="input-label">公网域名/IP</label>
            <input v-model="configForm.public_host" class="input" placeholder="api.example.com" />
          </div>
          <div>
            <label class="input-label">Nowind 内部访问地址</label>
            <input v-model="configForm.upstream_host" class="input" placeholder="127.0.0.1" />
          </div>
          <div>
            <label class="input-label">监听地址</label>
            <input v-model="configForm.gateway_listen_host" class="input" placeholder="0.0.0.0" />
          </div>
          <div>
            <label class="input-label">FRP 服务地址</label>
            <input v-model="configForm.frp_server_host" class="input" placeholder="api.example.com" />
          </div>
          <div>
            <label class="input-label">FRP 控制端口</label>
            <input v-model.number="configForm.frp_server_port" type="number" min="1" max="65535" class="input" />
          </div>
          <div>
            <label class="input-label">Raw FRP 端口起止</label>
            <div class="grid grid-cols-2 gap-2">
              <input v-model.number="configForm.raw_port_start" type="number" min="1" max="65535" class="input" />
              <input v-model.number="configForm.raw_port_end" type="number" min="1" max="65535" class="input" />
            </div>
          </div>
          <div>
            <label class="input-label">公网 SOCKS 端口起止</label>
            <div class="grid grid-cols-2 gap-2">
              <input v-model.number="configForm.public_port_start" type="number" min="1" max="65535" class="input" />
              <input v-model.number="configForm.public_port_end" type="number" min="1" max="65535" class="input" />
            </div>
          </div>
          <div>
            <label class="input-label">默认用户名</label>
            <input v-model="configForm.default_username" class="input" autocomplete="off" />
          </div>
          <div>
            <label class="input-label">默认密码</label>
            <input v-model="configForm.default_password" type="password" class="input" autocomplete="new-password" />
          </div>
          <div>
            <label class="input-label">Agent 拉取间隔（秒）</label>
            <input v-model.number="configForm.agent_poll_seconds" type="number" min="5" class="input" />
          </div>
          <div class="lg:col-span-3">
            <label class="input-label">FRP Token</label>
            <input v-model="configForm.frp_token" type="password" class="input" autocomplete="new-password" />
          </div>
        </div>
      </section>

      <section class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
        <div class="mb-4 flex items-center justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">OpenWrt Agent</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">在软路由 LuCI 里填入面板地址和 token 后会自动上报节点。</p>
          </div>
          <button class="btn btn-primary" @click="openAgentDialog()">
            <Icon name="plus" size="md" class="mr-2" />
            新建
          </button>
        </div>

        <div v-if="overview.agents.length === 0" class="rounded-lg border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
          暂无 Agent
        </div>
        <div v-else class="space-y-3">
          <div
            v-for="agent in overview.agents"
            :key="agent.id"
            class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <span class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ agent.name }}</span>
                  <span :class="['badge', agent.status === 'online' ? 'badge-success' : 'badge-gray']">{{ agent.status || 'offline' }}</span>
                </div>
                <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ agent.hostname || '-' }} · {{ agent.last_seen_at ? formatDateTime(agent.last_seen_at) : '未上报' }}
                </div>
              </div>
              <div class="flex shrink-0 items-center gap-1">
                <button class="rounded p-1 text-gray-400 hover:text-primary-600" title="复制 Token" @click="copy(agent.token || '')">
                  <Icon name="copy" size="sm" />
                </button>
                <button class="rounded p-1 text-gray-400 hover:text-primary-600" title="编辑" @click="openAgentDialog(agent)">
                  <Icon name="edit" size="sm" />
                </button>
                <button class="rounded p-1 text-gray-400 hover:text-amber-600" title="重置 Token" @click="rotateAgent(agent)">
                  <Icon name="refresh" size="sm" />
                </button>
                <button class="rounded p-1 text-gray-400 hover:text-red-600" title="删除" @click="deleteAgent(agent)">
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </div>
            <code class="mt-2 block truncate rounded bg-gray-100 px-2 py-1 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-300">{{ agent.token || '-' }}</code>
          </div>
        </div>
      </section>
    </div>

    <section class="rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
        <div>
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">PassWall SOCKS 节点</h2>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">新增 OpenWrt SOCKS 节点后，等待 Agent 上报即可在这里配置公网端口和认证。</p>
        </div>
        <button class="btn btn-secondary" @click="openMappingDialog()">
          <Icon name="plus" size="md" class="mr-2" />
          手动映射
        </button>
      </div>

      <div class="overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="px-4 py-3 text-left">节点</th>
              <th class="px-4 py-3 text-left">OpenWrt</th>
              <th class="px-4 py-3 text-left">公网代理</th>
              <th class="px-4 py-3 text-left">认证</th>
              <th class="px-4 py-3 text-left">状态</th>
              <th class="px-4 py-3 text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-if="overview.nodes.length === 0 && overview.mappings.length === 0">
              <td colspan="6" class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">暂无上报节点</td>
            </tr>
            <tr v-for="row in rows" :key="row.key">
              <td class="px-4 py-3">
                <div class="font-medium text-gray-900 dark:text-white">{{ row.name }}</div>
                <div class="mt-0.5 text-xs text-gray-500">{{ row.node?.node_ref || row.mapping?.name || '-' }}</div>
              </td>
              <td class="px-4 py-3">
                <code class="code text-xs">127.0.0.1:{{ row.openwrtPort }}</code>
                <div class="mt-1 text-xs text-gray-500">HTTP {{ row.node?.http_port || '-' }}</div>
              </td>
              <td class="px-4 py-3">
                <div v-if="row.mapping" class="space-y-1">
                  <div class="flex items-center gap-1.5">
                    <code class="code text-xs">{{ configForm.public_host || '-' }}:{{ row.mapping.public_port }}</code>
                    <button class="rounded p-0.5 text-gray-400 hover:text-primary-600" @click="copy(row.mapping.public_url || '')">
                      <Icon name="copy" size="sm" />
                    </button>
                  </div>
                  <div class="text-xs text-gray-500">raw {{ row.mapping.raw_remote_port }} · proxy #{{ row.mapping.proxy_id || '-' }}</div>
                </div>
                <span v-else class="text-gray-400">未配置</span>
              </td>
              <td class="px-4 py-3">
                <div v-if="row.mapping" class="text-xs">
                  <div class="text-gray-700 dark:text-gray-200">{{ row.mapping.username }}</div>
                  <div class="font-mono text-gray-500">{{ row.mapping.password ? '••••••••' : '-' }}</div>
                </div>
                <span v-else class="text-gray-400">-</span>
              </td>
              <td class="px-4 py-3">
                <div class="flex flex-col gap-1">
                  <span :class="['badge', row.listenStatus === 'listening' ? 'badge-success' : 'badge-gray']">{{ row.listenStatus }}</span>
                  <span v-if="row.mapping" :class="['badge', row.mapping.enabled ? 'badge-primary' : 'badge-gray']">{{ row.mapping.status }}</span>
                </div>
              </td>
              <td class="px-4 py-3 text-right">
                <div class="flex justify-end gap-1">
                  <button v-if="!row.mapping" class="btn btn-secondary btn-sm" @click="openMappingDialog(row.node || undefined)">
                    配置
                  </button>
                  <button v-else class="rounded p-1.5 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" @click="openMappingDialog(row.node || undefined, row.mapping)">
                    <Icon name="edit" size="sm" />
                  </button>
                  <button v-if="row.mapping" class="rounded p-1.5 text-gray-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20" @click="deleteMapping(row.mapping)">
                    <Icon name="trash" size="sm" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <BaseDialog :show="showAgentDialog" :title="editingAgent ? '编辑 Agent' : '新建 Agent'" width="normal" @close="closeAgentDialog">
      <form id="agent-form" class="space-y-4" @submit.prevent="saveAgent">
        <div>
          <label class="input-label">名称</label>
          <input v-model="agentForm.name" class="input" required />
        </div>
        <div>
          <label class="input-label">备注</label>
          <textarea v-model="agentForm.description" rows="3" class="input" />
        </div>
      </form>
      <template #footer>
        <button class="btn btn-secondary" @click="closeAgentDialog">取消</button>
        <button type="submit" form="agent-form" class="btn btn-primary" :disabled="saving">保存</button>
      </template>
    </BaseDialog>

    <BaseDialog :show="showMappingDialog" :title="editingMapping ? '编辑代理节点' : '配置代理节点'" width="wide" @close="closeMappingDialog">
      <form id="mapping-form" class="grid grid-cols-1 gap-4 md:grid-cols-2" @submit.prevent="saveMapping">
        <div>
          <label class="input-label">Agent</label>
          <Select v-model="mappingForm.agent_id" :options="agentOptions" />
        </div>
        <div>
          <label class="input-label">OpenWrt 节点</label>
          <Select v-model="mappingForm.node_id" :options="nodeOptions" clearable />
        </div>
        <div>
          <label class="input-label">名称</label>
          <input v-model="mappingForm.name" class="input" required />
        </div>
        <div>
          <label class="input-label">OpenWrt SOCKS 端口</label>
          <input v-model.number="mappingForm.openwrt_port" type="number" min="1" max="65535" class="input" required />
        </div>
        <div>
          <label class="input-label">Raw FRP 端口</label>
          <input v-model.number="mappingForm.raw_remote_port" type="number" min="1" max="65535" class="input" />
        </div>
        <div>
          <label class="input-label">公网 SOCKS 端口</label>
          <input v-model.number="mappingForm.public_port" type="number" min="1" max="65535" class="input" />
        </div>
        <div>
          <label class="input-label">用户名</label>
          <input v-model="mappingForm.username" class="input" autocomplete="off" />
        </div>
        <div>
          <label class="input-label">密码</label>
          <input v-model="mappingForm.password" type="password" class="input" autocomplete="new-password" />
        </div>
        <label class="md:col-span-2 flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
          <input v-model="mappingForm.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
          <span class="text-sm text-gray-700 dark:text-gray-200">启用这个公网代理</span>
        </label>
      </form>
      <template #footer>
        <button class="btn btn-secondary" @click="closeMappingDialog">取消</button>
        <button type="submit" form="mapping-form" class="btn btn-primary" :disabled="saving">保存</button>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { adminAPI } from '@/api/admin'
import type {
  SoftRouterAgent,
  SoftRouterOverview,
  SoftRouterProxyConfig,
  SoftRouterProxyMapping,
  SoftRouterSocksNode
} from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { formatDateTime } from '@/utils/format'

const emit = defineEmits<{
  (event: 'changed'): void
}>()

const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const defaultConfig = (): SoftRouterProxyConfig => ({
  enabled: false,
  public_host: '',
  gateway_listen_host: '0.0.0.0',
  upstream_host: '127.0.0.1',
  frp_server_host: '',
  frp_server_port: 7010,
  frp_token: '',
  raw_port_start: 12083,
  raw_port_end: 12150,
  public_port_start: 1101,
  public_port_end: 1120,
  default_username: '',
  default_password: '',
  agent_poll_seconds: 20,
  updated_at: ''
})

const overview = reactive<SoftRouterOverview>({
  config: defaultConfig(),
  agents: [],
  nodes: [],
  mappings: [],
  runtime: { enabled: false, listeners: {} }
})
const configForm = reactive<SoftRouterProxyConfig>(defaultConfig())
const loading = ref(false)
const saving = ref(false)

const showAgentDialog = ref(false)
const editingAgent = ref<SoftRouterAgent | null>(null)
const agentForm = reactive({ name: '', description: '' })

const showMappingDialog = ref(false)
const editingMapping = ref<SoftRouterProxyMapping | null>(null)
const mappingForm = reactive({
  agent_id: null as number | null,
  node_id: null as number | null,
  name: '',
  openwrt_port: 0,
  raw_remote_port: 0,
  public_port: 0,
  username: '',
  password: '',
  enabled: true
})

const rows = computed(() => {
  const usedMappings = new Set<number>()
  const fromNodes = overview.nodes.map((node) => {
    const mapping = overview.mappings.find((item) => item.node_id === node.id) || null
    if (mapping) usedMappings.add(mapping.id)
    return {
      key: `node-${node.id}`,
      node,
      mapping,
      name: node.name,
      openwrtPort: node.openwrt_port,
      listenStatus: node.listen_status || 'unknown'
    }
  })
  const manual = overview.mappings
    .filter((mapping) => !usedMappings.has(mapping.id))
    .map((mapping) => ({
      key: `mapping-${mapping.id}`,
      node: null,
      mapping,
      name: mapping.name,
      openwrtPort: mapping.openwrt_port,
      listenStatus: 'manual'
    }))
  return [...fromNodes, ...manual]
})

const agentOptions = computed(() =>
  overview.agents.map((agent) => ({ label: agent.name, value: agent.id }))
)

const nodeOptions = computed(() =>
  overview.nodes.map((node) => ({
    label: `${node.name} (${node.openwrt_port})`,
    value: node.id
  }))
)

function nextFreePort(start: number, end: number, used: number[]) {
  const taken = new Set(used.filter((port) => port > 0))
  for (let port = start; port <= end; port += 1) {
    if (!taken.has(port)) return port
  }
  return 0
}

function mappingPortFilter(mapping: SoftRouterProxyMapping) {
  return !editingMapping.value || mapping.id !== editingMapping.value.id
}

function usedRawPorts() {
  return overview.mappings.filter(mappingPortFilter).map((mapping) => mapping.raw_remote_port)
}

function usedPublicPorts() {
  return overview.mappings.filter(mappingPortFilter).map((mapping) => mapping.public_port)
}

function nextFreeRawPort() {
  return nextFreePort(
    configForm.raw_port_start,
    configForm.raw_port_end,
    usedRawPorts()
  )
}

function nextFreePublicPort() {
  return nextFreePort(
    configForm.public_port_start,
    configForm.public_port_end,
    usedPublicPorts()
  )
}

function ensureMappingFreePorts() {
  if (editingMapping.value) return
  mappingForm.raw_remote_port = nextFreeRawPort()
  mappingForm.public_port = nextFreePublicPort()
}

function selectedNode() {
  const nodeID = Number(mappingForm.node_id || 0)
  return overview.nodes.find((node) => node.id === nodeID) || null
}

function assignConfig(config: SoftRouterProxyConfig) {
  Object.assign(configForm, defaultConfig(), config)
  Object.assign(overview.config, defaultConfig(), config)
}

async function loadOverview() {
  loading.value = true
  try {
    const data = await adminAPI.proxies.getSoftRouterOverview()
    assignConfig(data.config)
    overview.agents = data.agents || []
    overview.nodes = data.nodes || []
    overview.mappings = data.mappings || []
    overview.runtime = data.runtime || { enabled: false, listeners: {} }
  } catch (error: any) {
    appStore.showError(error?.message || '加载代理节点失败')
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  try {
    await adminAPI.proxies.updateSoftRouterConfig(configForm)
    appStore.showSuccess('配置已保存')
    await loadOverview()
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || '保存配置失败')
  } finally {
    saving.value = false
  }
}

async function reconcile() {
  saving.value = true
  try {
    await adminAPI.proxies.reconcileSoftRouter()
    appStore.showSuccess('监听已同步')
    await loadOverview()
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || '同步失败')
  } finally {
    saving.value = false
  }
}

function openAgentDialog(agent?: SoftRouterAgent) {
  editingAgent.value = agent || null
  agentForm.name = agent?.name || 'OpenWrt'
  agentForm.description = agent?.description || ''
  showAgentDialog.value = true
}

function closeAgentDialog() {
  showAgentDialog.value = false
  editingAgent.value = null
}

async function saveAgent() {
  saving.value = true
  try {
    if (editingAgent.value) {
      await adminAPI.proxies.updateSoftRouterAgent(editingAgent.value.id, agentForm)
    } else {
      await adminAPI.proxies.createSoftRouterAgent(agentForm)
    }
    appStore.showSuccess('Agent 已保存')
    closeAgentDialog()
    await loadOverview()
  } catch (error: any) {
    appStore.showError(error?.message || '保存 Agent 失败')
  } finally {
    saving.value = false
  }
}

async function rotateAgent(agent: SoftRouterAgent) {
  saving.value = true
  try {
    const updated = await adminAPI.proxies.rotateSoftRouterAgentToken(agent.id)
    appStore.showSuccess('Token 已重置')
    if (updated.token) copy(updated.token)
    await loadOverview()
  } catch (error: any) {
    appStore.showError(error?.message || '重置 Token 失败')
  } finally {
    saving.value = false
  }
}

async function deleteAgent(agent: SoftRouterAgent) {
  saving.value = true
  try {
    await adminAPI.proxies.deleteSoftRouterAgent(agent.id)
    appStore.showSuccess('Agent 已删除')
    await loadOverview()
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || '删除 Agent 失败')
  } finally {
    saving.value = false
  }
}

function openMappingDialog(node?: SoftRouterSocksNode, mapping?: SoftRouterProxyMapping) {
  editingMapping.value = mapping || null
  mappingForm.agent_id = mapping?.agent_id || node?.agent_id || overview.agents[0]?.id || null
  mappingForm.node_id = mapping?.node_id || node?.id || null
  mappingForm.name = mapping?.name || node?.name || ''
  mappingForm.openwrt_port = mapping?.openwrt_port || node?.openwrt_port || 0
  mappingForm.raw_remote_port = mapping?.raw_remote_port || nextFreeRawPort()
  mappingForm.public_port = mapping?.public_port || nextFreePublicPort()
  mappingForm.username = mapping?.username || configForm.default_username || ''
  mappingForm.password = mapping?.password || configForm.default_password || ''
  mappingForm.enabled = mapping?.enabled ?? true
  if (!editingMapping.value) ensureMappingFreePorts()
  showMappingDialog.value = true
}

function closeMappingDialog() {
  showMappingDialog.value = false
  editingMapping.value = null
}

async function saveMapping() {
  saving.value = true
  try {
    const payload = {
      agent_id: Number(mappingForm.agent_id || 0),
      node_id: mappingForm.node_id,
      name: mappingForm.name,
      openwrt_port: Number(mappingForm.openwrt_port || 0),
      raw_remote_port: Number(mappingForm.raw_remote_port || 0),
      public_port: Number(mappingForm.public_port || 0),
      username: mappingForm.username,
      password: mappingForm.password,
      enabled: mappingForm.enabled
    }
    if (editingMapping.value) {
      await adminAPI.proxies.updateSoftRouterMapping(editingMapping.value.id, payload)
    } else {
      await adminAPI.proxies.createSoftRouterMapping(payload)
    }
    appStore.showSuccess('代理节点已保存')
    closeMappingDialog()
    await loadOverview()
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || '保存代理节点失败')
  } finally {
    saving.value = false
  }
}

watch(
  () => mappingForm.node_id,
  () => {
    if (!showMappingDialog.value || editingMapping.value) return
    const node = selectedNode()
    if (!node) return
    mappingForm.agent_id = node.agent_id
    mappingForm.name = node.name
    mappingForm.openwrt_port = node.openwrt_port
    ensureMappingFreePorts()
  }
)

async function deleteMapping(mapping: SoftRouterProxyMapping) {
  saving.value = true
  try {
    await adminAPI.proxies.deleteSoftRouterMapping(mapping.id)
    appStore.showSuccess('代理节点已删除')
    await loadOverview()
    emit('changed')
  } catch (error: any) {
    appStore.showError(error?.message || '删除代理节点失败')
  } finally {
    saving.value = false
  }
}

function copy(value: string) {
  if (!value) return
  copyToClipboard(value, '已复制')
}

onMounted(loadOverview)
</script>
