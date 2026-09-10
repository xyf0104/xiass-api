<template>
  <div class="preview-shell" data-testid="result-thumbnail">
    <iframe v-if="preview" ref="frame" :srcdoc="preview" :style="frameStyle" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="title" data-testid="result-preview" />
    <span v-else role="status" class="text-xs text-gray-500">{{ failed ? t('admin.accounts.pelicanBenchmark.resultFailed') : t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPelicanResult } from '@/api/admin/pelicanBenchmark'

const props = defineProps<{ id: string; title: string }>()
const { t } = useI18n()
const preview = ref('')
const failed = ref(false)
const frame = ref<HTMLIFrameElement>()
const height = ref(690)
const frameStyle = computed(() => {
  const scale = Math.min(220 / 1100, 138 / height.value)
  return { height: `${height.value}px`, transform: `scale(${scale})`, left: `${(220 - 1100 * scale) / 2}px` }
})
function measure(event: MessageEvent) {
  if (!frame.value || event.source !== frame.value.contentWindow || event.data?.type !== 'pelican-preview-size') return
  const value = event.data.height
  if (typeof value === 'number' && Number.isFinite(value)) height.value = Math.max(height.value, Math.min(20000, Math.ceil(value)))
}
let request: AbortController | undefined
watch(() => props.id, async id => {
  request?.abort()
  const controller = new AbortController()
  request = controller
  preview.value = ''
  failed.value = false
  height.value = 690
  try {
    const result = await getPelicanResult(id, controller.signal)
    if (controller.signal.aborted) return
    if (result.id !== id || result.status !== 'succeeded' || !result.html?.trim()) throw new Error('Invalid result')
    // Generated scripts remain in an opaque origin; no network or parent access.
    const policy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none';"
    const sizing = `<script>addEventListener('load',()=>{const report=()=>parent.postMessage({type:'pelican-preview-size',height:Math.max(document.documentElement.scrollHeight,document.body?.scrollHeight||0)},'*');report();new ResizeObserver(report).observe(document.body||document.documentElement)});<\/script>`
    preview.value = `<meta http-equiv="Content-Security-Policy" content="${policy}">${result.html}${sizing}`
  } catch {
    if (!controller.signal.aborted) failed.value = true
  }
}, { immediate: true })
onMounted(() => window.addEventListener('message', measure))
onUnmounted(() => {
  request?.abort()
  window.removeEventListener('message', measure)
})
</script>

<style scoped>
.preview-shell { width: 220px; height: 138px; max-width: 100%; overflow: hidden; position: relative; display: grid; place-items: center; border: 1px solid #64748b; border-radius: 6px; background: white; }
iframe { position: absolute; top: 0; left: 0; width: 1100px; height: 690px; border: 0; transform: scale(.2); transform-origin: top left; }
</style>
