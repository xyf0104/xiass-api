<template>
  <div class="preview-shell" data-testid="result-thumbnail">
    <iframe v-if="preview" :srcdoc="preview" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="title" data-testid="result-preview" />
    <span v-else role="status" class="text-xs text-gray-500">{{ failed ? t('admin.accounts.pelicanBenchmark.resultFailed') : t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPelicanResult } from '@/api/admin/pelicanBenchmark'
import { buildPelicanPreviewDocument } from '@/utils/pelicanPreviewDocument'

const props = defineProps<{ id: string; title: string }>()
const { t } = useI18n()
const preview = ref('')
const failed = ref(false)
let request: AbortController | undefined
watch(() => props.id, async id => {
  request?.abort()
  const controller = new AbortController()
  request = controller
  preview.value = ''
  failed.value = false
  try {
    const result = await getPelicanResult(id, controller.signal)
    if (controller.signal.aborted) return
    if (result.id !== id || result.status !== 'succeeded' || !result.html?.trim()) throw new Error('Invalid result')
    preview.value = buildPelicanPreviewDocument(result.preview_html?.trim() ? result.preview_html : result.html)
  } catch {
    if (!controller.signal.aborted) failed.value = true
  }
}, { immediate: true })
onUnmounted(() => request?.abort())
</script>

<style scoped>
.preview-shell { width: 220px; height: 138px; flex-shrink: 0; overflow: hidden; position: relative; display: grid; place-items: center; outline: 1px solid #64748b; border-radius: 6px; background: white; }
iframe { position: absolute; top: 0; left: 0; width: 1100px; height: 690px; border: 0; transform: scale(.2); transform-origin: top left; }
</style>
