<template>
  <div class="preview-shell" data-testid="result-thumbnail">
    <iframe v-if="preview" :key="preview.id" ref="previewFrame" src="/api/v1/pelican-preview" :data-result-id="preview.id" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="title" data-testid="result-preview" @load="renderPreview" />
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
const delivered = new WeakSet<HTMLIFrameElement>()
let request: AbortController | undefined
let loadTimeout: ReturnType<typeof setTimeout> | undefined
function clearLoadTimeout() {
  if (loadTimeout !== undefined) clearTimeout(loadTimeout)
  loadTimeout = undefined
}
function onPreviewAccepted(event: MessageEvent) {
  const frame = previewFrame.value
  if (!frame || !delivered.has(frame) || event.source !== frame.contentWindow || event.origin !== 'null' || event.data?.type !== 'xiass-pelican-render-accepted') return
  clearLoadTimeout()
}
window.addEventListener('message', onPreviewAccepted)
function renderPreview(event: Event) {
  const frame = event.target as HTMLIFrameElement
  const result = preview.value
  if (!result || frame.dataset.resultId !== result.id || delivered.has(frame) || !frame.contentWindow) return
  // The shell has an opaque origin. Send only the already-authorized HTML to
  // this frame, once; document.write in the shell may trigger another load.
  delivered.add(frame)
  frame.contentWindow.postMessage({ type: 'xiass-pelican-render', html: result.html }, '*')
}
watch(() => props.id, async id => {
  request?.abort()
  clearLoadTimeout()
  const controller = new AbortController()
  request = controller
  preview.value = undefined
  failed.value = false
  try {
    const result = await getPelicanResult(id, controller.signal)
    if (controller.signal.aborted) return
    if (result.id !== id || result.status !== 'succeeded' || !result.html?.trim()) throw new Error('Invalid result')
    // A navigated sandbox document has its own CSP. srcdoc would inherit the
    // application's nonce-only script policy and silently block generated JS.
    preview.value = { id: result.id, html: result.preview_html?.trim() ? result.preview_html : result.html }
    const pending = preview.value
    loadTimeout = setTimeout(() => {
      if (preview.value !== pending) return
      preview.value = undefined
      failed.value = true
      loadTimeout = undefined
    }, 15_000)
  } catch {
    if (!controller.signal.aborted) failed.value = true
  }
}, { immediate: true })
onUnmounted(() => {
  request?.abort()
  clearLoadTimeout()
  window.removeEventListener('message', onPreviewAccepted)
})
</script>

<style scoped>
.preview-shell { width: 220px; height: 138px; flex-shrink: 0; overflow: hidden; position: relative; display: grid; place-items: center; outline: 1px solid #64748b; border-radius: 6px; background: white; }
iframe { position: absolute; top: 0; left: 0; width: 1100px; height: 690px; border: 0; transform: scale(.2); transform-origin: top left; }
</style>
