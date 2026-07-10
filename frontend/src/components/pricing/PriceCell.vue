<template>
  <div v-if="basePrice != null || groupBasePrice != null">
    <!-- 分组价格模式 -->
    <template v-if="mode === 'group'">
      <div class="font-mono">
        <span class="text-lg font-bold text-primary-500">
          ¥{{ groupPriceFormatted }}
        </span>
        <span class="ml-1 text-sm text-gray-400 dark:text-gray-500">{{ unit }}</span>
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
        <span class="ml-1 text-sm text-gray-400 dark:text-gray-500">{{ unit }}</span>
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

const props = withDefaults(defineProps<{
  /** 官方基准单价。 */
  basePrice?: number | null
  /** 分组有独立媒体基准价时使用；未提供则沿用官方基准价。 */
  groupBasePrice?: number | null
  /** 分组倍率。 */
  multiplier: number
  /** 显示模式：group=分组价格, official=官方价格。 */
  mode: 'group' | 'official'
  /** 单价缩放；Token 价格按百万换算，按次/图片/视频保持 1。 */
  scale?: number
  /** 价格单位文案。 */
  unit?: string
}>(), {
  basePrice: null,
  groupBasePrice: null,
  scale: 1_000_000,
  unit: '/ 1M tokens'
})

const USD_TO_CNY = 7

const officialPriceFormatted = computed(() => {
  if (props.basePrice == null) return '-'
  const price = props.basePrice * props.scale * USD_TO_CNY
  return formatPrice(price)
})

const groupPriceFormatted = computed(() => {
  const basePrice = props.groupBasePrice ?? props.basePrice
  if (basePrice == null) return '-'
  const price = basePrice * props.scale * props.multiplier
  return formatPrice(price)
})

/**
 * 格式化价格：低于 1 元时保留有效小数，较大金额保持紧凑。
 */
function formatPrice(value: number): string {
  if (value === 0) return '0'
  if (value < 1) return value.toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
  if (value % 1 === 0) return value.toFixed(0)
  return value.toFixed(2)
}
</script>
