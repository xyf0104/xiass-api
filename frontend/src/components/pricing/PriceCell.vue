<template>
  <div v-if="basePrice != null">
    <!-- 分组价格模式 -->
    <template v-if="mode === 'group'">
      <div class="font-mono">
        <span class="text-lg font-bold text-primary-500">
          ¥{{ groupPriceFormatted }}
        </span>
        <span class="ml-1 text-sm text-gray-400 dark:text-gray-500">/ 1M tokens</span>
      </div>
      <div class="mt-1 text-xs text-gray-400 line-through dark:text-gray-600">
        官方价格 ¥{{ officialPriceFormatted }}
      </div>
    </template>
    <!-- 官方价格模式 -->
    <template v-else>
      <div class="font-mono">
        <span class="text-lg font-bold text-gray-700 dark:text-gray-300">
          ¥{{ officialPriceFormatted }}
        </span>
        <span class="ml-1 text-sm text-gray-400 dark:text-gray-500">/ 1M tokens</span>
      </div>
    </template>
  </div>
  <span v-else class="text-sm text-gray-400">-</span>
</template>

<script setup lang="ts">
/**
 * 价格单元格组件
 * 展示分组价格（高亮大字）和官方价格（删除线），与 apikey.fun 风格一致
 */
import { computed } from 'vue'

const props = defineProps<{
  /** 每 token 的基准价格（USD） */
  basePrice?: number | null
  /** 分组倍率 */
  multiplier: number
  /** 显示模式：group=分组价格, official=官方价格 */
  mode: 'group' | 'official'
}>()

const USD_TO_CNY = 7

const officialPriceFormatted = computed(() => {
  if (props.basePrice == null) return '-'
  const price = props.basePrice * 1_000_000 * USD_TO_CNY
  return formatPrice(price)
})

const groupPriceFormatted = computed(() => {
  if (props.basePrice == null) return '-'
  const price = props.basePrice * 1_000_000 * USD_TO_CNY * props.multiplier
  return formatPrice(price)
})

/**
 * 格式化价格：小于 1 显示两位小数，大于 1 显示整数或两位小数
 */
function formatPrice(value: number): string {
  if (value < 0.01) return value.toFixed(3)
  if (value < 1) return value.toFixed(2)
  if (value % 1 === 0) return value.toFixed(0)
  return value.toFixed(2)
}
</script>
