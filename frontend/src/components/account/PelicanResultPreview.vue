<template>
  <div class="result-preview-actions">
    <div ref="thumbnail" class="preview-shell" data-testid="result-thumbnail">
      <iframe v-if="preview && (!usesThree || inView)" class="thumbnail-frame" :srcdoc="preview" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="title" data-testid="result-preview" />
      <span v-else role="status" class="text-xs text-gray-500">{{ failed ? t(`${prefix}.resultFailed`) : t('common.loading') }}</span>
      <button v-if="preview" type="button" class="preview-open" :title="t(`${prefix}.enlarge`)" :aria-label="`${title}: ${t(`${prefix}.enlarge`)}`" data-testid="enlarge-preview" @click="expanded = true" />
    </div>
    <div v-if="preview" class="preview-toolbar">
      <button type="button" class="btn btn-secondary preview-action" :title="t(`${prefix}.enlarge`)" :aria-label="t(`${prefix}.enlarge`)" @click="expanded = true"><Icon name="eye" size="sm" /></button>
      <button type="button" class="btn btn-secondary preview-action" :title="t(`${prefix}.openInBrowser`)" :aria-label="t(`${prefix}.openInBrowser`)" data-testid="open-preview" @click="openInBrowser"><Icon name="externalLink" size="sm" /></button>
      <button type="button" class="btn btn-secondary preview-action" :title="t(`${prefix}.downloadHTML`)" :aria-label="t(`${prefix}.downloadHTML`)" data-testid="download-preview" @click="downloadHTML"><Icon name="download" size="sm" /></button>
    </div>
    <p v-if="popupBlocked" role="alert" class="max-w-56 text-xs text-red-600 dark:text-red-400">{{ t(`${prefix}.popupBlocked`) }}</p>
  </div>
  <BaseDialog v-if="expanded" :show="expanded" :title="title" width="full" :z-index="60" @close="expanded = false">
    <iframe class="enlarged-frame" :srcdoc="enlargedPreview" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="title" data-testid="enlarged-preview" />
    <template #footer>
      <div class="flex flex-wrap justify-end gap-2">
        <button type="button" class="btn btn-secondary min-h-11" @click="openInBrowser"><Icon name="externalLink" size="sm" />{{ t(`${prefix}.openInBrowser`) }}</button>
        <button type="button" class="btn btn-primary min-h-11" @click="downloadHTML"><Icon name="download" size="sm" />{{ t(`${prefix}.downloadHTML`) }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPelicanResult } from '@/api/admin/pelicanBenchmark'
import { buildPelicanPreviewDocument, buildPelicanStandaloneDocument } from '@/utils/pelicanPreviewDocument'
import { preparePelicanHTML } from '@/utils/pelicanThreeRuntime'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ id: string; title: string }>()
const { t } = useI18n()
const prefix = 'admin.accounts.pelicanBenchmark'
const preview = ref('')
const preparedHTML = ref('')
const failed = ref(false)
const expanded = ref(false)
const popupBlocked = ref(false)
const thumbnail = ref<HTMLElement>()
const inView = ref(typeof IntersectionObserver === 'undefined')
const usesThree = computed(() => preparedHTML.value.includes('data-xiass-three-runtime'))
const enlargedPreview = computed(() => buildPelicanPreviewDocument(preparedHTML.value, { fit: false }))
let request: AbortController | undefined
let observer: IntersectionObserver | undefined

watch(thumbnail, element => {
  observer?.disconnect()
  if (!element || typeof IntersectionObserver === 'undefined') return
  // Browser WebGL context limits must not evict visible 3D results when a
  // history page contains many scenes. Ordinary HTML stays mounted directly.
  observer = new IntersectionObserver(entries => {
    inView.value = entries.some(entry => entry.isIntersecting)
  }, { rootMargin: '120px' })
  observer.observe(element)
}, { flush: 'post' })

function documentURL() {
  const html = buildPelicanStandaloneDocument(preparedHTML.value, props.title)
  const url = URL.createObjectURL(new Blob([html], { type: 'text/html;charset=utf-8' }))
  // Let the new tab/download consume the document even if this modal closes.
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
  return url
}

function openInBrowser() {
  if (!preparedHTML.value) return
  const tab = window.open('about:blank', '_blank')
  popupBlocked.value = !tab
  if (!tab) return
  tab.opener = null
  tab.location.replace(documentURL())
}

function downloadHTML() {
  if (!preparedHTML.value) return
  const link = document.createElement('a')
  link.href = documentURL()
  link.download = `xiass-test-${props.id.replace(/[^a-zA-Z0-9_-]/g, '_').slice(0, 80)}.html`
  document.body.appendChild(link)
  link.click()
  link.remove()
}

watch(() => props.id, async id => {
  request?.abort()
  const controller = new AbortController()
  request = controller
  preview.value = ''
  preparedHTML.value = ''
  failed.value = false
  expanded.value = false
  popupBlocked.value = false
  try {
    const result = await getPelicanResult(id, controller.signal)
    if (controller.signal.aborted) return
    if (result.id !== id || result.status !== 'succeeded' || !result.html?.trim()) throw new Error('Invalid result')
    const html = await preparePelicanHTML(result.preview_html?.trim() ? result.preview_html : result.html)
    if (controller.signal.aborted) return
    preparedHTML.value = html
    preview.value = buildPelicanPreviewDocument(html)
  } catch {
    if (!controller.signal.aborted) failed.value = true
  }
}, { immediate: true })
onUnmounted(() => {
  request?.abort()
  observer?.disconnect()
})
</script>

<style scoped>
.result-preview-actions { display: grid; gap: 4px; justify-items: center; max-width: 100%; }
.preview-shell { width: 220px; height: 138px; flex-shrink: 0; overflow: hidden; position: relative; display: grid; place-items: center; outline: 1px solid #64748b; border-radius: 6px; background: white; }
.thumbnail-frame { position: absolute; top: 0; left: 0; width: 1100px; height: 690px; border: 0; transform: scale(.2); transform-origin: top left; pointer-events: none; }
.preview-open { position: absolute; inset: 0; cursor: zoom-in; background: transparent; border: 0; border-radius: inherit; }
.preview-open:focus-visible { outline: 3px solid var(--color-primary-500, #0ea5e9); outline-offset: -3px; }
.preview-toolbar { display: flex; align-items: center; justify-content: center; gap: 8px; }
.preview-action { width: 44px; height: 44px; padding: 0; }
.enlarged-frame { display: block; width: 100%; height: 65vh; height: 65dvh; min-height: 240px; border: 0; background: white; }
</style>
