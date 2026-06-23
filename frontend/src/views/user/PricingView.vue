<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- 页面标题 -->
      <div>
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">模型价格</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          查看各产品的模型定价和渠道折扣信息
        </p>
      </div>

      <!-- 产品类别 Tab -->
      <div class="flex items-center gap-3 rounded-xl border border-gray-200/60 bg-white/40 p-2 dark:border-dark-700/60 dark:bg-dark-800/40 shadow-sm backdrop-blur-sm">
        <button
          v-for="cat in productCategories"
          :key="cat.platform"
          @click="activePlatform = cat.platform"
          :class="[
            'flex items-center gap-2.5 rounded-lg px-6 py-3 text-base font-semibold transition-all border duration-300 hover:-translate-y-0.5',
            activePlatform === cat.platform
              ? 'bg-primary-500 text-white border-primary-500 shadow-lg shadow-primary-500/25'
              : 'text-gray-600 border-gray-200/80 bg-gray-50/40 hover:bg-gray-100 hover:border-gray-300 hover:shadow dark:text-gray-400 dark:border-dark-700/80 dark:bg-dark-800/30 dark:hover:bg-dark-700/50 dark:hover:border-dark-600'
          ]"
        >
          <BrandIcon :name="cat.icon" class="h-5 w-5" />
          {{ cat.label }}
        </button>
      </div>



      <!-- 主要内容区 -->
      <div v-if="loading" class="flex items-center justify-center py-20">
        <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
      </div>

      <div v-else-if="activeChannel" class="space-y-0">
        <!-- 价格列表卡片 -->
        <div class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <!-- 标题栏 -->
          <div class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-200 px-6 py-4 dark:border-dark-700">
            <div class="flex items-center gap-3">
              <Icon name="chart" size="lg" class="text-primary-500" />
              <span class="text-lg font-bold text-gray-900 dark:text-white">价格列表</span>
            </div>
            <div class="flex items-center gap-4">
              <span class="text-sm text-gray-500 dark:text-gray-400">
                选择分组后，直接查看每个模型的人民币价格。
              </span>
              <div class="flex overflow-hidden rounded-lg border border-gray-300 dark:border-dark-600">
                <button
                  @click="priceMode = 'group'"
                  :class="[
                    'px-4 py-2 text-sm font-semibold transition-colors',
                    priceMode === 'group'
                      ? 'bg-primary-500 text-white'
                      : 'bg-white text-gray-600 hover:bg-gray-50 dark:bg-dark-800 dark:text-gray-400 dark:hover:bg-dark-700'
                  ]"
                >
                  分组价格
                </button>
                <button
                  @click="priceMode = 'official'"
                  :class="[
                    'px-4 py-2 text-sm font-semibold transition-colors',
                    priceMode === 'official'
                      ? 'bg-primary-500 text-white'
                      : 'bg-white text-gray-600 hover:bg-gray-50 dark:bg-dark-800 dark:text-gray-400 dark:hover:bg-dark-700'
                  ]"
                >
                  官方价格
                </button>
              </div>
            </div>
          </div>

          <!-- 分组卡片区域 -->
          <div class="flex flex-nowrap w-full overflow-x-auto gap-4 border-b border-gray-200 px-6 py-5 dark:border-dark-700 custom-scrollbar">
            <button
              v-for="group in activeGroups"
              :key="group.id"
              @click="activeGroupId = group.id"
              :class="[
                'group relative flex flex-col rounded-xl border-2 px-5 py-4 text-left transition-all duration-300 flex-1 min-w-[200px] max-w-[280px]',
                activeGroupId === group.id
                  ? 'border-primary-500 bg-primary-100/70 shadow-lg shadow-primary-500/20 dark:border-primary-500 dark:bg-primary-900/40'
                  : 'border-gray-200 bg-white hover:border-primary-400 hover:shadow-md dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-500'
              ]"
            >
              <!-- 分组名 + 折扣标签 -->
              <div class="flex items-center gap-2 whitespace-nowrap w-full overflow-hidden">
                <span class="text-base font-bold text-gray-900 dark:text-white truncate">
                  {{ group.name }}
                </span>
                <span class="rounded-full bg-primary-500 px-2 py-0.5 text-[11px] font-bold text-white shrink-0">
                  {{ formatDisplayDiscount(group) }}折
                </span>
                <!-- 选中勾选 -->
                <div
                  v-if="activeGroupId === group.id"
                  class="ml-auto flex h-6 w-6 items-center justify-center rounded-full bg-primary-500 text-white shrink-0"
                >
                  <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                </div>
              </div>
              <!-- 倍率描述 -->
              <span class="mt-2 text-sm text-gray-500 dark:text-gray-400 whitespace-nowrap truncate w-full text-left">
                {{ formatDisplayMultiplier(group) }}x 倍率 · 相当于约 {{ formatDisplayDiscount(group) }}折
              </span>
            </button>
          </div>

          <!-- 分组介绍 -->
          <div v-if="activeGroup?.description" class="border-b border-gray-200 px-6 py-4 dark:border-dark-700 bg-gray-50/50 dark:bg-dark-800/50">
            <div class="flex items-start gap-3 text-sm">
              <span class="font-bold text-amber-600 dark:text-amber-500 shrink-0">分组介绍：</span>
              <span class="text-gray-600 dark:text-gray-400 border-l-2 border-gray-300 dark:border-gray-600 pl-3 leading-relaxed">{{ activeGroup.description }}</span>
            </div>
          </div>

          <!-- 定价表格 -->
          <div class="overflow-x-auto">
            <table class="w-full">
              <thead>
                <tr class="border-b-2 border-gray-200 text-sm font-bold text-gray-700 dark:border-dark-600 dark:text-gray-300">
                  <th class="px-6 py-4 text-left">模型 ID</th>
                  <th class="px-6 py-4 text-left">输入价格</th>
                  <th class="px-6 py-4 text-left">输出价格</th>
                  <th class="px-6 py-4 text-left">缓存创建</th>
                  <th class="px-6 py-4 text-left">缓存读取</th>
                  <th class="px-6 py-4 text-right">节省幅度</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="model in activeModels"
                  :key="model.name"
                  class="border-b border-gray-100 transition-colors hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800/60"
                >
                  <!-- 模型名 -->
                  <td class="px-6 py-5">
                    <div class="flex items-center gap-3">
                      <span class="text-base font-bold text-gray-900 dark:text-white">{{ model.name }}</span>
                      <button
                        @click="copyModelId(model.name)"
                        class="rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-200 hover:text-gray-600 dark:hover:bg-dark-600 dark:hover:text-gray-300"
                        title="复制模型 ID"
                      >
                        <Icon name="clipboard" size="sm" />
                      </button>
                    </div>
                  </td>

                  <!-- 输入价格 -->
                  <td class="px-6 py-5">
                    <PriceCell
                      :base-price="model.pricing?.input_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 输出价格 -->
                  <td class="px-6 py-5">
                    <PriceCell
                      :base-price="model.pricing?.output_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 缓存创建价格 -->
                  <td class="px-6 py-5">
                    <PriceCell
                      :base-price="model.pricing?.cache_write_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 缓存读取价格 -->
                  <td class="px-6 py-5">
                    <PriceCell
                      :base-price="model.pricing?.cache_read_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 节省幅度 -->
                  <td class="px-6 py-5 text-right">
                    <span
                      v-if="savingsPercent(activeGroup?.rate_multiplier) > 0"
                      class="inline-flex items-center gap-1 text-base font-bold text-primary-500"
                    >
                      省 {{ savingsPercent(activeGroup?.rate_multiplier) }}%
                    </span>
                    <span v-else class="text-sm text-gray-400">-</span>
                  </td>
                </tr>

                <!-- 空状态 -->
                <tr v-if="activeModels.length === 0">
                  <td colspan="6" class="py-16 text-center text-base text-gray-400">
                    该分类下暂无已定价模型
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- 无数据 -->
      <div v-else class="rounded-2xl border border-gray-200 bg-white py-20 text-center dark:border-dark-700 dark:bg-dark-800">
        <Icon name="inbox" size="xl" class="mx-auto mb-4 text-gray-400" />
        <p class="text-base text-gray-500 dark:text-gray-400">暂无可用渠道数据</p>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
/**
 * 模型定价页面 — 对标 apikey.fun 的分组卡片 + 定价表格布局
 * 数据来源：复用 /channels/available API（需登录），按平台聚合展示
 */
import { ref, computed, watch, onMounted } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import BrandIcon from '@/components/icons/BrandIcon.vue'
import PriceCell from '@/components/pricing/PriceCell.vue'
import userChannelsAPI, { type UserAvailableChannel, type UserAvailableGroup, type UserSupportedModel } from '@/api/channels'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const appStore = useAppStore()

// ==================== 状态 ====================

const channels = ref<UserAvailableChannel[]>([])
const loading = ref(false)
const activePlatform = ref('anthropic')
const activeGroupId = ref<number | null>(null)
const priceMode = ref<'group' | 'official'>('group')

// ==================== 产品类别定义 ====================

/** 产品 Tab 配置：按平台分类 */
const productCategories = [
  { platform: 'anthropic', label: 'Claude Code', icon: 'claude' },
  { platform: 'openai', label: 'Codex', icon: 'openai' },
  { platform: 'gemini', label: 'Gemini', icon: 'gemini' },
]

// ==================== 计算属性 ====================

/** 当前选中平台对应的渠道 */
const activeChannel = computed(() => {
  for (const ch of channels.value) {
    const section = ch.platforms.find(p => p.platform === activePlatform.value)
    if (section) return { channel: ch, section }
  }
  return null
})

/** 当前平台下的分组列表（已按倍率从小到大排序） */
const activeGroups = computed((): (UserAvailableGroup & { description?: string })[] => {
  if (!activeChannel.value) return []
  return [...activeChannel.value.section.groups].sort((a, b) => a.rate_multiplier - b.rate_multiplier)
})

watch(activeGroups, (groups) => {
  if (groups.length > 0 && !groups.find(g => g.id === activeGroupId.value)) {
    activeGroupId.value = groups[0].id
  }
}, { immediate: true })

/** 当前选中的分组 */
const activeGroup = computed(() => {
  return activeGroups.value.find(g => g.id === activeGroupId.value) ?? activeGroups.value[0]
})

/** 当前平台下的模型列表 */
const activeModels = computed((): UserSupportedModel[] => {
  if (!activeChannel.value) return []
  return activeChannel.value.section.supported_models
})

// ==================== 格式化方法 ====================

/**
 * 在纯人民币系统中，用户的 rate_multiplier 就是最终展示的倍率。
 * 不需要通过 cost_ratio 进行换算了。
 */
function formatDisplayMultiplier(group: { rate_multiplier: number; cost_ratio?: number | null }): string {
  return group.rate_multiplier.toString()
}

function formatDisplayDiscount(group: { rate_multiplier: number; cost_ratio?: number | null }): string {
  // 折扣计算：(groupPrice / officialPrice) * 10
  // officialPrice 包含 *7 的换算，groupPrice 是真实的，所以折扣是 (multiplier / 7) * 10
  const discount = (group.rate_multiplier / 7) * 10
  return discount % 1 === 0 ? discount.toFixed(0) : discount.toFixed(1)
}

function savingsPercent(multiplier?: number): number {
  if (!multiplier) return 0
  const ratio = multiplier / 7
  if (ratio >= 1) return 0
  return Math.round((1 - ratio) * 100)
}

async function copyModelId(modelId: string) {
  try {
    await navigator.clipboard.writeText(modelId)
    appStore.showSuccess(`已复制: ${modelId}`)
  } catch {
    appStore.showError('复制失败')
  }
}

// ==================== 数据加载 ====================

async function loadChannels() {
  loading.value = true
  try {
    channels.value = await userChannelsAPI.getAvailable()
    if (channels.value.length > 0) {
      const firstPlatform = channels.value[0]?.platforms?.[0]?.platform
      if (firstPlatform) {
        activePlatform.value = firstPlatform
      }
    }
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, '加载定价数据失败'))
  } finally {
    loading.value = false
  }
}

onMounted(loadChannels)
</script>
