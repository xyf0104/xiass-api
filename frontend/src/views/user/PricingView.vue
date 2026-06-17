<template>
  <AppLayout>
    <div class="space-y-5">
      <!-- 页面标题 -->
      <div class="flex items-center justify-between">
        <div>
          <h1 class="text-xl font-bold text-gray-900 dark:text-white">模型价格</h1>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            查看各产品的模型定价和渠道折扣信息
          </p>
        </div>
      </div>

      <!-- 产品类别 Tab -->
      <div class="flex items-center gap-1 rounded-lg border border-gray-200 bg-white p-1 dark:border-dark-700 dark:bg-dark-800">
        <button
          v-for="cat in productCategories"
          :key="cat.platform"
          @click="activePlatform = cat.platform"
          :class="[
            'flex items-center gap-2 rounded-md px-4 py-2 text-sm font-medium transition-all',
            activePlatform === cat.platform
              ? 'bg-primary-500 text-white shadow-sm'
              : 'text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-dark-700'
          ]"
        >
          <BrandIcon :name="cat.icon" class="h-4 w-4" />
          {{ cat.label }}
        </button>
      </div>

      <!-- 计价规则提示 -->
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/30 bg-amber-500/5 px-4 py-2.5">
        <div class="flex items-center gap-2 text-xs text-amber-600 dark:text-amber-400">
          <Icon name="bolt" size="sm" />
          <span class="font-medium">计价规则</span>
          <span>官方价格按 $1 = ¥7 折算 &nbsp; 分组价格 = 官方价格 × 分组倍率 × 7</span>
        </div>
        <div v-if="activeModels.length > 0" class="text-xs text-gray-500 dark:text-gray-400">
          示例：{{ activeModels[0].name }} 输入价，官方 ¥{{ formatOfficialPrice(activeModels[0].pricing?.input_price) }}，{{ activeGroups[0]?.name }} ¥{{ formatGroupPrice(activeModels[0].pricing?.input_price, activeGroups[0]?.rate_multiplier) }}
        </div>
      </div>

      <!-- 主要内容区 -->
      <div v-if="loading" class="flex items-center justify-center py-16">
        <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
      </div>

      <div v-else-if="activeChannel" class="space-y-5">
        <!-- 价格列表标题 + 切换按钮 -->
        <div class="card overflow-hidden">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-3 dark:border-dark-700">
            <div class="flex items-center gap-2">
              <Icon name="chart" size="md" class="text-gray-500" />
              <span class="font-semibold text-gray-900 dark:text-white">价格列表</span>
            </div>
            <div class="flex items-center gap-3">
              <span class="text-xs text-gray-500 dark:text-gray-400">
                选择分组后，直接查看每个模型的人民币价格。
              </span>
              <div class="flex overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
                <button
                  @click="priceMode = 'group'"
                  :class="[
                    'px-3 py-1.5 text-xs font-medium transition-colors',
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
                    'px-3 py-1.5 text-xs font-medium transition-colors',
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

          <!-- 分组卡片 -->
          <div class="flex flex-wrap gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
            <button
              v-for="group in activeGroups"
              :key="group.id"
              @click="activeGroupId = group.id"
              :class="[
                'group relative flex min-w-[180px] flex-col rounded-lg border-2 px-4 py-3 text-left transition-all',
                activeGroupId === group.id
                  ? 'border-primary-500 bg-primary-500/5 dark:border-primary-400'
                  : 'border-gray-200 bg-white hover:border-gray-300 dark:border-dark-600 dark:bg-dark-800 dark:hover:border-dark-500'
              ]"
            >
              <!-- 分组名 + 折扣标签 -->
              <div class="flex items-center gap-2">
                <span class="text-sm font-semibold text-gray-900 dark:text-white">
                  {{ group.name }}
                </span>
                <span class="rounded-full bg-primary-500 px-1.5 py-0.5 text-[10px] font-bold text-white">
                  {{ formatDiscount(group.rate_multiplier) }}折
                </span>
                <!-- 选中指示器 -->
                <div
                  v-if="activeGroupId === group.id"
                  class="ml-auto flex h-5 w-5 items-center justify-center rounded-full bg-primary-500 text-white"
                >
                  <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                </div>
              </div>
              <!-- 倍率描述 -->
              <span class="mt-1 text-[11px] text-gray-500 dark:text-gray-400">
                {{ group.rate_multiplier }}x 倍率 · 相当于约 {{ formatDiscount(group.rate_multiplier) }}折
              </span>
            </button>
          </div>

          <!-- 分组介绍 -->
          <div v-if="activeGroup?.description" class="border-b border-gray-100 px-5 py-3 dark:border-dark-700">
            <div class="flex items-start gap-2 text-xs">
              <span class="font-medium text-primary-500">分组介绍：</span>
              <span class="text-gray-600 dark:text-gray-400">｜ {{ activeGroup.description }}</span>
            </div>
          </div>

          <!-- 定价表格 -->
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-gray-100 text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400">
                  <th class="px-5 py-3 text-left font-medium">模型 ID</th>
                  <th class="px-5 py-3 text-left font-medium">输入价格</th>
                  <th class="px-5 py-3 text-left font-medium">输出价格</th>
                  <th class="px-5 py-3 text-left font-medium">缓存创建</th>
                  <th class="px-5 py-3 text-left font-medium">缓存读取</th>
                  <th class="px-5 py-3 text-right font-medium">节省幅度</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="model in activeModels"
                  :key="model.name"
                  class="border-b border-gray-50 transition-colors hover:bg-gray-50/50 dark:border-dark-700/50 dark:hover:bg-dark-800/50"
                >
                  <!-- 模型名 -->
                  <td class="px-5 py-4">
                    <div class="flex items-center gap-2">
                      <span class="font-medium text-gray-900 dark:text-white">{{ model.name }}</span>
                      <button
                        @click="copyModelId(model.name)"
                        class="rounded p-0.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-300"
                        title="复制模型 ID"
                      >
                        <Icon name="clipboard" size="xs" />
                      </button>
                    </div>
                  </td>

                  <!-- 输入价格 -->
                  <td class="px-5 py-4">
                    <PriceCell
                      :base-price="model.pricing?.input_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 输出价格 -->
                  <td class="px-5 py-4">
                    <PriceCell
                      :base-price="model.pricing?.output_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 缓存创建价格 -->
                  <td class="px-5 py-4">
                    <PriceCell
                      :base-price="model.pricing?.cache_write_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 缓存读取价格 -->
                  <td class="px-5 py-4">
                    <PriceCell
                      :base-price="model.pricing?.cache_read_price"
                      :multiplier="activeGroup?.rate_multiplier ?? 1"
                      :mode="priceMode"
                    />
                  </td>

                  <!-- 节省幅度 -->
                  <td class="px-5 py-4 text-right">
                    <span
                      v-if="savingsPercent(activeGroup?.rate_multiplier) > 0"
                      class="inline-flex items-center gap-0.5 text-xs font-semibold text-primary-500"
                    >
                      <span class="text-primary-500">省</span>
                      {{ savingsPercent(activeGroup?.rate_multiplier) }}%
                    </span>
                    <span v-else class="text-xs text-gray-400">-</span>
                  </td>
                </tr>

                <!-- 空状态 -->
                <tr v-if="activeModels.length === 0">
                  <td colspan="6" class="py-12 text-center text-sm text-gray-400">
                    该分类下暂无已定价模型
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- 无数据 -->
      <div v-else class="card py-16 text-center">
        <Icon name="inbox" size="xl" class="mx-auto mb-3 text-gray-400" />
        <p class="text-sm text-gray-500 dark:text-gray-400">暂无可用渠道数据</p>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
/**
 * 模型定价页面 — 对标 apikey.fun 的分组卡片 + 定价表格布局
 * 数据来源：复用 /channels/available API（需登录），按平台聚合展示
 */
import { ref, computed, onMounted } from 'vue'
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

/** 产品 Tab 配置：按平台分类，与 apikey.fun 的 Claude Code / Codex / Gemini 对应 */
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

/** 当前平台下的分组列表 */
const activeGroups = computed((): (UserAvailableGroup & { description?: string })[] => {
  if (!activeChannel.value) return []
  const groups = activeChannel.value.section.groups
  // 初始化选中第一个分组
  if (groups.length > 0 && !groups.find(g => g.id === activeGroupId.value)) {
    activeGroupId.value = groups[0].id
  }
  return groups
})

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

/** 计算折扣（倍率 × 10 = 几折）*/
function formatDiscount(multiplier?: number): string {
  if (!multiplier) return '0'
  const discount = multiplier * 10
  // 对折扣取一位小数
  return discount % 1 === 0 ? discount.toFixed(0) : discount.toFixed(1)
}

/**
 * 格式化官方价格（per 1M tokens，转人民币）
 * 价格存储单位为 per token，需要乘以 1M 再乘以 7（汇率）
 */
function formatOfficialPrice(pricePerToken?: number | null): string {
  if (pricePerToken == null) return '-'
  const pricePerMillion = pricePerToken * 1_000_000 * 7
  return pricePerMillion.toFixed(2)
}

/** 格式化分组价格 */
function formatGroupPrice(pricePerToken?: number | null, multiplier?: number): string {
  if (pricePerToken == null || !multiplier) return '-'
  const pricePerMillion = pricePerToken * 1_000_000 * 7 * multiplier
  return pricePerMillion.toFixed(2)
}

/** 计算节省幅度 */
function savingsPercent(multiplier?: number): number {
  if (!multiplier || multiplier >= 1) return 0
  // 与官方价格（假设 1x 为标准）对比的节省
  // 由于系统的定价本身已经远低于官方，这里计算相对于 "官方原价" 的折扣
  // 用 (1 - multiplier * base_ratio) 近似，简化为 apikey.fun 的展示风格
  return Math.round((1 - multiplier * 0.14) * 100)
}

/** 复制模型 ID 到剪贴板 */
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
    // 默认选中第一个有数据的平台
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
