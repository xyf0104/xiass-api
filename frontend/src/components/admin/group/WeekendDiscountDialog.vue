<template>
  <BaseDialog :show="true" title="八折活动" width="normal" @close="$emit('close')">
    <form class="space-y-5" @submit.prevent="save">
      <p v-if="loading" role="status" class="text-sm text-gray-500">加载活动设置…</p>
      <template v-else-if="loaded">
        <div class="flex items-center justify-between gap-4">
          <label for="weekend-enabled" class="font-medium text-gray-900 dark:text-gray-100">启用八折活动</label>
          <input id="weekend-enabled" v-model="enabled" type="checkbox" role="switch" class="h-6 w-6 accent-primary-500" :disabled="saving" />
        </div>
        <fieldset :disabled="saving" class="space-y-2">
          <legend class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">活动模式</legend>
          <div class="grid grid-cols-2 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
            <label v-for="option in modes" :key="option.value" class="relative flex min-h-11 cursor-pointer items-center justify-center gap-2 rounded-md px-2 text-sm font-medium" :class="mode === option.value ? 'bg-white text-orange-700 shadow-sm dark:bg-dark-600 dark:text-orange-400' : 'text-gray-600 dark:text-gray-300'">
              <input v-model="mode" type="radio" name="discount-mode" :value="option.value" class="peer sr-only" />
              <span class="absolute inset-0 rounded-md peer-focus-visible:ring-2 peer-focus-visible:ring-orange-500" aria-hidden="true"></span>
              <Icon :name="option.icon" size="sm" aria-hidden="true" />{{ option.label }}
            </label>
          </div>
        </fieldset>
        <p class="border-y border-gray-200 py-3 text-sm leading-6 text-gray-600 dark:border-dark-600 dark:text-gray-300">
          <span class="inline-flex items-center gap-1.5 font-semibold text-orange-700 dark:text-orange-400"><DiscountIcon />全部分组 · 原价 × 80%</span><br />
          {{ mode === 'custom' ? '自选活动时段，周一至周日均可生效' : '北京时间每周六 00:00 至周一 00:00' }}
        </p>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="min-w-0 text-sm text-gray-700 dark:text-gray-300" for="weekend-start">活动开始（北京时间）
            <input id="weekend-start" v-model="start" type="datetime-local" class="input mt-2 min-h-11 w-full min-w-0" :disabled="saving" />
            <span class="mt-1 block text-xs text-gray-500">{{ mode === 'custom' ? '留空：保存后立即开始' : '留空：从最近的周末开始' }}</span>
          </label>
          <label class="min-w-0 text-sm text-gray-700 dark:text-gray-300" for="weekend-end">活动结束（北京时间）
            <input id="weekend-end" v-model="end" type="datetime-local" class="input mt-2 min-h-11 w-full min-w-0" :disabled="saving" />
            <span class="mt-1 block text-xs text-gray-500">留空：持续至手动关闭</span>
          </label>
        </div>
        <p v-if="savedStatus" class="text-sm text-gray-600 dark:text-gray-300">已保存状态：{{ savedStatus.active ? '八折生效中' : !savedStatus.enabled ? '已关闭' : savedStatus.ends_at && !savedStatus.next_change_at ? '活动已结束' : savedStatus.mode === 'custom' ? '已预约，等待活动开始' : '已开启，等待周末或活动开始' }}</p>
      </template>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <div class="flex flex-wrap justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <button v-if="loaded" type="button" class="btn btn-secondary mr-auto min-h-11 text-orange-700 dark:text-orange-400" :disabled="saving" @click="startNow"><Icon name="play" size="sm" class="mr-1.5" aria-hidden="true" />现在开始</button>
        <button type="button" class="btn btn-secondary min-h-11" :disabled="saving" @click="$emit('close')">关闭</button>
        <button v-if="!loaded && !loading" type="button" class="btn btn-secondary min-h-11" @click="load">重试</button>
        <button type="submit" class="btn btn-primary min-h-11" :disabled="!loaded || loading || saving">{{ saving ? '保存中…' : '保存活动' }}</button>
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import DiscountIcon from '@/components/common/DiscountIcon.vue'
import { getWeekendDiscount, saveWeekendDiscount, type WeekendDiscountStatus } from '@/api/weekendDiscount'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
import { updateWeekendDiscountStatus } from '@/composables/useWeekendDiscount'

defineEmits<{ (e: 'close'): void }>()
const app = useAppStore()
const enabled = ref(false)
const mode = ref<'weekends' | 'custom'>('weekends')
const modes = [
  { value: 'weekends', label: '每周末（默认）', icon: 'calendar' },
  { value: 'custom', label: '自定义时段', icon: 'clock' }
] as const
const start = ref('')
const end = ref('')
const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const savedStatus = ref<WeekendDiscountStatus | null>(null)
const localTime = (value: string | null) => value ? new Date(Date.parse(value) + 8 * 3_600_000).toISOString().slice(0, 16) : ''
const apiTime = (value: string) => value ? new Date(`${value}:00+08:00`).toISOString() : null

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await getWeekendDiscount(true)
    savedStatus.value = data
    enabled.value = data.enabled
    mode.value = data.mode ?? 'weekends'
    start.value = localTime(data.starts_at)
    end.value = localTime(data.ends_at)
    loaded.value = true
  } catch (err) { error.value = extractApiErrorMessage(err, '加载活动失败') }
  finally { loading.value = false }
}

async function save() {
  error.value = ''
  if (start.value && end.value && end.value <= start.value) { error.value = '结束时间必须晚于开始时间'; return }
  saving.value = true
  try {
    savedStatus.value = await saveWeekendDiscount({ enabled: enabled.value, mode: mode.value, starts_at: apiTime(start.value), ends_at: apiTime(end.value) })
    updateWeekendDiscountStatus(savedStatus.value)
    app.showSuccess('八折活动已保存')
  } catch (err) { error.value = extractApiErrorMessage(err, '保存活动失败') }
  finally { saving.value = false }
}

async function startNow() {
  error.value = ''
  saving.value = true
  try {
    const current = await getWeekendDiscount(true)
    if (end.value && Date.parse(apiTime(end.value)!) <= Date.parse(current.server_time)) {
      error.value = '结束时间已过，请选择未来的时间或留空'
      return
    }
    savedStatus.value = await saveWeekendDiscount({ enabled: true, mode: 'custom', starts_at: current.server_time, ends_at: apiTime(end.value) })
    updateWeekendDiscountStatus(savedStatus.value)
    enabled.value = true
    mode.value = 'custom'
    start.value = localTime(savedStatus.value.starts_at)
    app.showSuccess('八折活动已立即开启')
  } catch (err) { error.value = extractApiErrorMessage(err, '开启活动失败') }
  finally { saving.value = false }
}
onMounted(load)
</script>
