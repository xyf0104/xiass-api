<template>
  <div class="min-w-0 max-w-full text-right" data-testid="candy-result">
    <button v-if="compact" type="button" class="flex h-11 w-11 items-center justify-center text-2xl" :title="`${t(`${prefix}.testNames.candy`)}: ${verdictLabel}${run.answer == null ? '' : ` · ${run.answer}`}`" :aria-label="`${t(`${prefix}.testNames.candy`)}: ${verdictLabel} · ${t(`${prefix}.candyFullAnswer`)}`" data-testid="candy-details" @click="expanded = true">{{ run.passed === true ? '✅' : run.passed === false ? '❌' : '—' }}</button>
    <template v-else>
    <p class="flex flex-wrap items-center justify-end gap-1.5 text-sm font-semibold" :class="verdictClass" :aria-label="verdictLabel">
      <span aria-hidden="true">{{ run.passed === true ? '✅' : run.passed === false ? '❌' : '—' }}</span>
      <span>{{ verdictLabel }}</span>
    </p>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400" data-testid="candy-answer">{{ run.answer == null ? t(`${prefix}.candyNoAnswer`) : t(`${prefix}.candyAnswer`, { answer: run.answer }) }}</p>
    <button type="button" class="mt-1 min-h-10 text-xs font-medium text-primary-600 hover:underline dark:text-primary-400" data-testid="candy-details" @click="expanded = true">{{ t(`${prefix}.candyFullAnswer`) }}</button>
    </template>
    <BaseDialog :show="expanded" :title="`${run.account_name}: ${t(`${prefix}.testNames.candy`)}`" width="wide" :z-index="60" @close="expanded = false">
      <p class="mb-3 break-all text-xs text-gray-500 dark:text-gray-400">{{ run.model }}</p>
      <p v-if="loading" role="status" class="py-6 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-else-if="failed" role="alert" class="space-y-3 text-sm text-red-600 dark:text-red-400">
        <p>{{ t(`${prefix}.answerLoadFailed`) }}</p>
        <button type="button" class="btn btn-secondary" data-testid="candy-detail-retry" @click="loadAnswer">{{ t(`${prefix}.retry`) }}</button>
      </div>
      <p v-else class="whitespace-pre-wrap text-sm leading-relaxed text-gray-900 [overflow-wrap:anywhere] dark:text-gray-100" data-testid="candy-output">{{ output || t(`${prefix}.candyEmptyOutput`) }}</p>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getPelicanResult, type PelicanBenchmarkRun } from '@/api/admin/pelicanBenchmark'

const props = defineProps<{ run: PelicanBenchmarkRun; compact?: boolean }>()
const { t } = useI18n()
const prefix = 'admin.accounts.pelicanBenchmark'
const expanded = ref(false)
const loading = ref(false)
const failed = ref(false)
const output = ref('')
let loaded = false
let request: AbortController | undefined
const verdictLabel = computed(() => t(`${prefix}.${props.run.passed === true ? 'candyPassed' : props.run.passed === false ? 'candyFailed' : 'candyUngraded'}`))
const verdictClass = computed(() => props.run.passed === true ? 'text-emerald-700 dark:text-emerald-400' : props.run.passed === false ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400')

async function loadAnswer() {
  request?.abort()
  const controller = new AbortController()
  request = controller
  const id = props.run.id
  loading.value = true
  failed.value = false
  try {
    const result = await getPelicanResult(id, controller.signal)
    if (controller.signal.aborted || id !== props.run.id) return
    if (result.id !== id || result.status !== 'succeeded' || result.test_type !== 'candy') throw new Error('invalid_candy_result')
    output.value = result.text_output ?? ''
    loaded = true
  } catch {
    if (!controller.signal.aborted && id === props.run.id) failed.value = true
  } finally {
    if (!controller.signal.aborted && id === props.run.id) loading.value = false
  }
}

watch(expanded, show => {
  if (show && !loaded) void loadAnswer()
  if (!show) {
    request?.abort()
    loading.value = false
  }
})
watch(() => props.run.id, () => {
  request?.abort()
  expanded.value = false
  loaded = false
  output.value = ''
  failed.value = false
})
onUnmounted(() => request?.abort())
</script>
