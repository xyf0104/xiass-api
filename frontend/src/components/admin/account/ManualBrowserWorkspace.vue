<template>
  <section class="min-w-0 overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
    <header class="flex flex-col gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700 lg:flex-row lg:items-center lg:justify-between">
      <div class="min-w-0">
        <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">临时浏览器</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">独立浏览器会话 · {{ active ? `${proxyName || '服务器直连'}${egressIP ? ` · 出口 ${egressIP}` : ''}` : '选择出口后启动' }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <label for="manual-browser-proxy" class="text-sm text-gray-600 dark:text-gray-300">出口</label>
        <select id="manual-browser-proxy" v-model="selectedProxyID" class="input min-w-[190px] max-w-full" :disabled="loading || active" data-testid="manual-browser-proxy">
          <option value="">直连服务器</option>
          <option v-for="proxy in proxies" :key="proxy.id" :value="String(proxy.id)">{{ proxy.name }}</option>
        </select>
        <button v-if="!active" type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="!configured || loading" data-testid="manual-browser-open" @click="emit('open', selectedProxyID ? Number(selectedProxyID) : null)">
          <Icon :name="loading ? 'refresh' : 'server'" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
          <span>{{ loading ? '正在启动...' : '打开浏览器' }}</span>
        </button>
        <template v-else>
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="collapsed = !collapsed">{{ collapsed ? '重新打开面板' : '收起面板' }}</button>
          <button type="button" class="btn btn-danger" :disabled="loading" data-testid="manual-browser-end" @click="showEndConfirm = true">结束会话</button>
        </template>
      </div>
    </header>

    <p v-if="error || localError" role="alert" class="border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">{{ localError || error }}</p>
    <p v-if="!configured" class="px-4 py-5 text-sm text-amber-700 dark:text-amber-300">服务器 Chromium 尚未启用。</p>

    <div v-if="active && !collapsed" class="browser-shell" data-testid="manual-browser-frame">
      <div class="browser-toolbar">
        <button type="button" class="browser-icon" title="后退" aria-label="后退" @click="act('back')"><Icon name="arrowLeft" size="sm" /></button>
        <button type="button" class="browser-icon" title="前进" aria-label="前进" @click="act('forward')"><Icon name="arrowRight" size="sm" /></button>
        <button type="button" class="browser-icon" title="刷新网页" aria-label="刷新网页" @click="act('reload')"><Icon name="refresh" size="sm" /></button>
        <form class="browser-address" @submit.prevent="navigate">
          <input v-model="address" type="text" inputmode="url" autocomplete="url" aria-label="网址" placeholder="输入网址" />
          <button type="submit" title="打开网址" aria-label="打开网址"><Icon name="arrowRight" size="sm" /></button>
        </form>
        <button type="button" class="browser-icon" title="新建标签" aria-label="新建标签" @click="act('new-tab')"><Icon name="plus" size="sm" /></button>
        <button type="button" class="browser-icon" title="关闭当前标签" aria-label="关闭当前标签" @click="act('close-tab')"><Icon name="x" size="sm" /></button>
      </div>
      <div class="browser-tabs" aria-label="浏览器标签">
        <button v-for="tab in tabs" :key="tab.index" type="button" :class="['browser-tab', { active: tab.index === activeTab }]" :title="tab.url" @click="selectTab(tab.index)">{{ tabTitle(tab.url) }}</button>
      </div>
      <div class="browser-viewport-wrap">
        <div v-if="!screenshotURL" class="browser-loading">正在连接浏览器...</div>
        <img
          v-else
          :src="screenshotURL"
          class="browser-screen"
          alt="临时浏览器画面"
          tabindex="0"
          draggable="false"
          @click="screenClick"
          @mousemove="screenMove"
          @wheel.prevent="screenWheel"
          @focus="keyboardInput?.focus({ preventScroll: true })"
          @contextmenu.prevent="screenClick"
        />
        <textarea ref="keyboardInput" class="browser-keyboard" aria-label="浏览器键盘输入" autocomplete="off" autocapitalize="off" :spellcheck="false" @keydown="screenKeydown" @input="screenInput" @compositionend="screenInput" @paste="screenPaste" />
      </div>
      <footer class="browser-status">
        <span>{{ currentURL || 'about:blank' }}</span>
        <span>会话保留至 {{ expiresLabel }}</span>
      </footer>
    </div>

    <BaseDialog :show="showEndConfirm" title="结束临时浏览器会话" width="narrow" @close="showEndConfirm = false">
      <p class="text-sm text-gray-600 dark:text-gray-300">将关闭所有浏览器标签并删除此会话的 Cookie 和站点数据。此操作不可撤销。</p>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showEndConfirm = false">取消</button>
        <button type="button" class="btn btn-danger" data-testid="manual-browser-confirm-end" @click="endSession">结束并清除</button>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Icon } from '@/components/icons'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { teamChildAPI, type ManualBrowserSession } from '@/api/admin/teamChild'
import type { Proxy } from '@/types'

const props = defineProps<{
  configured: boolean
  sessionId: string
  loading: boolean
  error: string
  proxies: Proxy[]
  expiresAt: string
  initialProxyId: number | null
  initialProxyName: string
  initialEgressIp: string
}>()

const emit = defineEmits<{
  open: [proxyID: number | null]
  end: []
}>()

const selectedProxyID = ref('')
const screenshotURL = ref('')
const address = ref('')
const currentURL = ref('')
const proxyName = ref('')
const egressIP = ref('')
const localError = ref('')
const collapsed = ref(false)
const showEndConfirm = ref(false)
const tabs = ref<Array<{ index: number; url: string }>>([])
const activeTab = ref(0)
const keyboardInput = ref<HTMLTextAreaElement | null>(null)
let disposed = false
let commandQueue: Promise<void> = Promise.resolve()
let lastTabsAt = 0
let screenshotTimer: ReturnType<typeof setTimeout> | null = null
let screenshotInFlight = false
let revokedURLs: string[] = []
let lastMouseMoveAt = 0
function revokeObjectURL(url: string) {
  if (typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(url)
}
const active = computed(() => Boolean(props.sessionId))
const expiresLabel = computed(() => {
  if (!props.expiresAt) return '12 小时'
  const value = new Date(props.expiresAt)
  return Number.isNaN(value.getTime()) ? '12 小时' : value.toLocaleString()
})

watch(() => props.sessionId, (sessionID) => {
  selectedProxyID.value = props.initialProxyId ? String(props.initialProxyId) : ''
  proxyName.value = props.initialProxyName
  egressIP.value = props.initialEgressIp
  collapsed.value = false
  localError.value = ''
  for (const url of revokedURLs) revokeObjectURL(url)
  revokedURLs = []
  screenshotURL.value = ''
  tabs.value = []
  if (sessionID) {
    void refreshScreenshot()
    scheduleScreenshot()
    void refreshTabs()
  } else {
    stopScreenshotPolling()
  }
}, { immediate: true })

watch(() => props.initialProxyId, value => { selectedProxyID.value = value ? String(value) : '' })

function scheduleScreenshot() {
  stopScreenshotPolling()
  if (disposed || !active.value || collapsed.value) return
  screenshotTimer = setTimeout(async () => {
    await refreshScreenshot()
    scheduleScreenshot()
  }, 900)
}

function stopScreenshotPolling() {
  if (screenshotTimer) clearTimeout(screenshotTimer)
  screenshotTimer = null
}

async function refreshScreenshot() {
  if (disposed || document.hidden || !props.sessionId || screenshotInFlight || collapsed.value) return
  const sessionID = props.sessionId
  screenshotInFlight = true
  try {
    const nextURL = await teamChildAPI.manualBrowserScreenshotURL(sessionID)
    if (disposed || props.sessionId !== sessionID) { revokeObjectURL(nextURL); return }
    revokedURLs.push(nextURL)
    screenshotURL.value = nextURL
    if (revokedURLs.length > 3) revokeObjectURL(revokedURLs.shift()!)
    if (Date.now() - lastTabsAt > 4000) { lastTabsAt = Date.now(); void refreshTabs() }
  } catch {
    localError.value = '浏览器画面连接中断，请稍后重试或重新打开面板。'
  } finally {
    screenshotInFlight = false
  }
}

async function refreshTabs() {
  if (!props.sessionId) return
  try {
    const result = await teamChildAPI.manualBrowserCommand<{ tabs: Array<{ index: number; url: string }>; active_index: number }>(props.sessionId, 'tabs')
    tabs.value = result.tabs || []
    activeTab.value = Number(result.active_index || 0)
    const active = tabs.value[activeTab.value]
    if (active) {
      currentURL.value = active.url
      if (document.activeElement?.getAttribute('aria-label') !== '网址') address.value = active.url === 'about:blank' ? '' : active.url
    }
  } catch {
    localError.value = '浏览器会话已失效，请重新打开。'
  }
}

async function navigate() {
  let target = address.value.trim()
  if (!target) return
  if (!/^[a-zA-Z][a-zA-Z\d+.-]*:/.test(target)) target = `https://${target}`
  await command('navigate', { url: target })
}

async function command(action: string, payload?: unknown) {
  if (!props.sessionId) return
  const sessionID = props.sessionId
  const run = async () => {
    if (disposed || props.sessionId !== sessionID) return
  try {
    const result = await teamChildAPI.manualBrowserCommand<ManualBrowserSession & { current_url?: string }>(sessionID, action, payload)
    if (result.current_url) {
      currentURL.value = result.current_url
      address.value = result.current_url === 'about:blank' ? '' : result.current_url
    }
    if (result.egress_ip) egressIP.value = result.egress_ip
    if (['navigate', 'select-tab'].includes(action) || ['new-tab', 'close-tab', 'back', 'forward', 'reload'].includes(String((payload as { type?: string } | undefined)?.type || ''))) {
      await refreshTabs()
    }
    localError.value = ''
  } catch {
    localError.value = '浏览器操作失败，可能是网页暂时无响应。'
  }
  }
  commandQueue = commandQueue.then(run, run)
  return commandQueue
}

function act(type: string) { return command('action', { type }) }
function tabTitle(url: string) {
  if (!url || url === 'about:blank') return '新标签页'
  try { return new URL(url).hostname } catch { return url.slice(0, 24) }
}
function selectTab(index: number) { return command('select-tab', { index }) }

function screenCoordinates(event: MouseEvent) {
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  return { x: Math.round((event.clientX - rect.left) * 1280 / rect.width), y: Math.round((event.clientY - rect.top) * 800 / rect.height) }
}
function screenClick(event: MouseEvent) {
  keyboardInput.value?.focus({ preventScroll: true })
  const { x, y } = screenCoordinates(event)
  void command('action', { type: 'click', x, y, button: event.button === 2 ? 'right' : 'left', click_count: event.detail })
}
function screenMove(event: MouseEvent) {
  const now = Date.now()
  if (now - lastMouseMoveAt < 200) return
  lastMouseMoveAt = now
  const { x, y } = screenCoordinates(event)
  void teamChildAPI.manualBrowserCommand(props.sessionId, 'action', { type: 'move', x, y }).catch(() => undefined)
}
function screenWheel(event: WheelEvent) {
  void command('action', { type: 'wheel', delta_x: event.deltaX, delta_y: event.deltaY })
}
function screenKeydown(event: KeyboardEvent) {
  if (event.isComposing || event.key === 'Process' || ['Control', 'Meta', 'Alt', 'Shift'].includes(event.key)) return
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'v') return
  const modifiers = [event.ctrlKey || event.metaKey ? 'Control' : '', event.shiftKey ? 'Shift' : '', event.altKey ? 'Alt' : ''].filter(Boolean)
  if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) return
  if (event.key === 'Tab') event.preventDefault()
  if (modifiers.length || event.key === 'Enter' || event.key === 'Tab' || event.key.startsWith('Arrow') || ['Backspace', 'Delete', 'Escape', 'Space'].includes(event.key)) {
    event.preventDefault()
    const key = event.key.length === 1 ? event.key.toLowerCase() : event.key === ' ' ? 'Space' : event.key
    void command('action', { type: 'press', key: [...modifiers, key].join('+') })
  }
}
function screenInput(event: Event) {
  if ((event as InputEvent).isComposing) return
  const input = event.target as HTMLTextAreaElement
  const text = input.value
  input.value = ''
  if (text) void command('action', { type: 'type', text })
}
function screenPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData('text')
  if (text) {
    event.preventDefault()
    void command('action', { type: 'type', text })
  }
}

function endSession() {
  showEndConfirm.value = false
  emit('end')
}

function onVisibilityChange() {
  if (!document.hidden) { void refreshScreenshot(); scheduleScreenshot() }
}
onMounted(() => document.addEventListener('visibilitychange', onVisibilityChange))

watch(collapsed, value => {
  if (value) stopScreenshotPolling()
  else if (active.value) {
    void refreshScreenshot()
    scheduleScreenshot()
  }
})

onBeforeUnmount(() => {
  disposed = true
  document.removeEventListener('visibilitychange', onVisibilityChange)
  stopScreenshotPolling()
  for (const url of revokedURLs) revokeObjectURL(url)
  revokedURLs = []
})
</script>

<style scoped>
.browser-shell { background: #f3f4f6; }
.browser-toolbar, .browser-tabs, .browser-status { display: flex; align-items: center; gap: 6px; padding: 7px 10px; }
.browser-toolbar, .browser-tabs { border-bottom: 1px solid #d1d5db; background: #e5e7eb; }
.browser-icon { display: grid; place-items: center; width: 34px; height: 32px; flex: 0 0 34px; border: 0; border-radius: 4px; color: #374151; background: transparent; font-size: 21px; line-height: 1; }
.browser-keyboard { position: absolute; left: 0; bottom: 0; width: 1px; height: 1px; opacity: 0; pointer-events: none; }
.browser-icon:hover { background: #d1d5db; }
.browser-address { display: flex; min-width: 100px; flex: 1; overflow: hidden; border: 1px solid #cbd5e1; border-radius: 5px; background: white; }
.browser-address input { width: 100%; min-width: 0; height: 32px; border: 0; outline: 0; padding: 0 10px; color: #111827; background: white; font-size: 13px; }
.browser-address button { width: 36px; flex: 0 0 36px; display: grid; place-items: center; border: 0; color: #4b5563; background: white; }
.browser-tab { max-width: 220px; height: 30px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 0 12px; border: 0; border-radius: 4px 4px 0 0; color: #4b5563; background: transparent; font-size: 12px; }
.browser-tab.active { color: #111827; background: #f9fafb; }
.browser-viewport-wrap { position: relative; width: 100%; aspect-ratio: 16 / 10; min-height: 360px; max-height: 78vh; background: white; }
.browser-screen { display: block; width: 100%; height: 100%; object-fit: fill; outline: 0; user-select: none; }
.browser-loading { display: grid; width: 100%; height: 100%; min-height: 360px; place-items: center; color: #6b7280; }
.browser-status { justify-content: space-between; gap: 12px; overflow: hidden; color: #6b7280; background: #f9fafb; font-size: 11px; }
.browser-status span:first-child { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.browser-status span:last-child { flex: 0 0 auto; }
:deep(.btn-danger) { color: white; background: #dc2626; border-color: #dc2626; }
@media (max-width: 640px) {
  .browser-toolbar, .browser-tabs, .browser-status { padding-left: 6px; padding-right: 6px; }
  .browser-toolbar { gap: 2px; }
  .browser-icon { width: 30px; flex-basis: 30px; }
  .browser-viewport-wrap { min-height: 320px; }
}
@media (prefers-color-scheme: dark) {
  .browser-toolbar, .browser-tabs { border-color: #374151; background: #1f2937; }
  .browser-icon { color: #d1d5db; }
  .browser-icon:hover { background: #374151; }
  .browser-tab { color: #d1d5db; }
  .browser-tab.active, .browser-status { color: #e5e7eb; background: #111827; }
}
</style>
