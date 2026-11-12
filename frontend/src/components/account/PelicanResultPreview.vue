<template>
  <div class="preview-shell" data-testid="result-thumbnail">
    <iframe v-if="preview" :key="`${preview.id}:${attempt}`" ref="previewFrame" src="/api/v1/pelican-preview" :data-result-id="preview.id" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="title" data-testid="result-preview" @load="renderPreview" />
    <span v-else role="status" class="text-xs text-gray-500">{{ failed ? t('admin.accounts.pelicanBenchmark.resultFailed') : t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPelicanResult } from '@/api/admin/pelicanBenchmark'

const props = defineProps<{ id: string; title: string }>()
const { t } = useI18n()
const preview = ref<{ id: string; html: string }>()
const previewFrame = ref<HTMLIFrameElement>()
const failed = ref(false)
const attempt = ref(0)
const delivered = new WeakMap<HTMLIFrameElement, string | null>()
const deliveryWindows = new WeakMap<HTMLIFrameElement, { start: number; count: number }>()
let request: AbortController | undefined
let loadTimeout: ReturnType<typeof setTimeout> | undefined
let deliveryTimer: ReturnType<typeof setTimeout> | undefined
let pendingDelivery: { frame: HTMLIFrameElement; documentID: string | null } | undefined
function clearPendingDelivery() {
  if (deliveryTimer !== undefined) clearTimeout(deliveryTimer)
  deliveryTimer = undefined
  pendingDelivery = undefined
}
function clearLoadTimeout() {
  if (loadTimeout !== undefined) clearTimeout(loadTimeout)
  loadTimeout = undefined
}
function awaitAcceptance() {
  clearLoadTimeout()
  const pending = preview.value
  loadTimeout = setTimeout(() => {
    loadTimeout = undefined
    if (!pending || preview.value !== pending) return
    if (attempt.value < 1) {
      attempt.value++
      awaitAcceptance()
    } else {
      preview.value = undefined
      failed.value = true
    }
  }, 15_000)
}
function sendHTML(frame: HTMLIFrameElement, documentID: string | null) {
  const result = preview.value
  if (!result || frame !== previewFrame.value || frame.dataset.resultId !== result.id || !frame.contentWindow) return
  if (delivered.has(frame) && delivered.get(frame) === documentID) return
  const now = Date.now()
  const budget = deliveryWindows.get(frame)
  if (budget && now - budget.start < 1000) {
    if (budget.count >= 4) {
      pendingDelivery = { frame, documentID }
      if (deliveryTimer === undefined) deliveryTimer = setTimeout(() => {
        const pending = pendingDelivery
        clearPendingDelivery()
        if (pending) sendHTML(pending.frame, pending.documentID)
      }, 1000 - (now - budget.start))
      return
    }
    budget.count++
  } else {
    deliveryWindows.set(frame, { start: now, count: 1 })
  }
  clearPendingDelivery()
  delivered.set(frame, documentID)
  awaitAcceptance()
  frame.contentWindow.postMessage({ type: 'xiass-pelican-render', html: result.html, ...(documentID ? { document_id: documentID } : {}) }, '*')
}
function onPreviewMessage(event: MessageEvent) {
  const frame = previewFrame.value
  if (!frame || event.source !== frame.contentWindow || event.origin !== 'null') return
  const message = event.data
  if (message?.type === 'xiass-pelican-ready' && typeof message.document_id === 'string' && /^[a-f0-9]{32}$/.test(message.document_id)) {
    // Moving a keyed row reloads its document without replacing the iframe.
    // Track delivery per document, so sorting cannot leave an empty shell.
    sendHTML(frame, message.document_id)
  } else if (message?.type === 'xiass-pelican-render-accepted' && delivered.has(frame) &&
    (delivered.get(frame) === null || delivered.get(frame) === message.document_id)) {
    clearLoadTimeout()
  }
}
window.addEventListener('message', onPreviewMessage)
function renderPreview(event: Event) {
  const frame = event.target as HTMLIFrameElement
  if (frame !== previewFrame.value || !frame.contentWindow) return
  // Support an older shell during upgrades. The probe recovers an early ready
  // message; the consumed renderer ignores it after document.write's load.
  if (!delivered.has(frame)) sendHTML(frame, null)
  frame.contentWindow.postMessage({ type: 'xiass-pelican-probe' }, '*')
}
watch(() => props.id, async id => {
  request?.abort()
  clearLoadTimeout()
  clearPendingDelivery()
  const controller = new AbortController()
  request = controller
  preview.value = undefined
  failed.value = false
  attempt.value = 0
  try {
    const result = await getPelicanResult(id, controller.signal)
    if (controller.signal.aborted) return
    if (result.id !== id || result.status !== 'succeeded' || !result.html?.trim()) throw new Error('Invalid result')
    // A navigated sandbox document has its own CSP. srcdoc would inherit the
    // application's nonce-only script policy and silently block generated JS.
    preview.value = { id: result.id, html: result.preview_html?.trim() ? result.preview_html : result.html }
    awaitAcceptance()
  } catch {
    if (!controller.signal.aborted) failed.value = true
  }
}, { immediate: true })
onUnmounted(() => {
  request?.abort()
  clearLoadTimeout()
  clearPendingDelivery()
  window.removeEventListener('message', onPreviewMessage)
})
</script>

<style scoped>
.preview-shell { width: 220px; height: 138px; flex-shrink: 0; overflow: hidden; position: relative; display: grid; place-items: center; outline: 1px solid #64748b; border-radius: 6px; background: white; }
iframe { position: absolute; top: 0; left: 0; width: 1100px; height: 690px; border: 0; transform: scale(.2); transform-origin: top left; }
</style>
