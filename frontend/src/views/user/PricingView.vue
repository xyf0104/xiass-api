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
          :data-test="`platform-${cat.platform}`"
          @click="activePlatform = cat.platform"
          :class="[
            'flex items-center gap-2.5 rounded-lg px-6 py-3 text-base font-semibold transition-all border duration-300 hover:-translate-y-0.5',
            activePlatform === cat.platform
              ? 'bg-primary-500 text-white border-primary-500 shadow-lg shadow-primary-500/25'
              : 'text-gray-600 border-gray-200/80 bg-gray-50/40 hover:bg-gray-100 hover:border-gray-300 hover:shadow dark:text-gray-400 dark:border-dark-700/80 dark:bg-dark-800/30 dark:hover:bg-dark-700/50 dark:hover:border-dark-600'
          ]"
        >
          <BrandIcon v-if="cat.icon" :name="cat.icon" class="h-5 w-5" />
          <PlatformIcon v-else :platform="cat.platform" size="lg" />
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
                <span
                  v-if="isGroupPeakActive(group)"
                  class="rounded-full bg-amber-500 px-2 py-0.5 text-[11px] font-bold text-white shrink-0"
                >
                  高峰中
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
              <span
                v-if="hasPeakRate(group)"
                class="mt-1 text-xs text-amber-600 dark:text-amber-400 whitespace-nowrap truncate w-full text-left"
              >
                {{ peakRateWindow(group) }}
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
                  <th class="px-6 py-4 text-left">计费方式</th>
                  <th class="px-6 py-4 text-left">价格详情</th>
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

                  <!-- 计费方式 -->
                  <td class="px-6 py-5">
                    <span
                      class="inline-flex whitespace-nowrap rounded-md border border-gray-200 bg-gray-50 px-2.5 py-1 text-sm font-semibold text-gray-700 dark:border-dark-600 dark:bg-dark-700/60 dark:text-gray-300"
                    >
                      {{ billingModeLabel(model) }}
                    </span>
                  </td>

                  <!-- 价格详情 -->
                  <td class="px-6 py-5">
                    <div
                      v-if="priceItemsFor(model).length > 0"
                      :data-test="`price-items-${model.name}`"
                      class="flex min-w-max flex-nowrap items-start gap-8"
                    >
                      <div
                        v-for="item in priceItemsFor(model)"
                        :key="item.key"
                        :data-test="`price-${model.name}-${item.key}`"
                        class="w-[13rem] flex-none"
                      >
                        <div class="mb-1 whitespace-nowrap text-xs font-semibold text-gray-500 dark:text-gray-400">
                          {{ item.label }}
                        </div>
                        <PriceCell
                          :base-price="item.basePrice"
                          :group-base-price="item.groupBasePrice"
                          :multiplier="multiplierFor(model)"
                          :mode="priceMode"
                          :scale="item.scale"
                          :unit="item.unit"
                        />
                      </div>
                    </div>
                    <span v-else class="text-sm text-gray-400">暂无价格</span>
                  </td>

                  <!-- 节省幅度 -->
                  <td class="px-6 py-5 text-right">
                    <span
                      v-if="savingsPercent(multiplierFor(model)) > 0"
                      class="inline-flex items-center gap-1 text-base font-bold text-primary-500"
                    >
                      省 {{ savingsPercent(multiplierFor(model)) }}%
                    </span>
                    <span v-else class="text-sm text-gray-400">-</span>
                  </td>
                </tr>

                <!-- 空状态 -->
                <tr v-if="activeModels.length === 0">
                  <td colspan="4" class="py-16 text-center text-base text-gray-400">
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
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import BrandIcon from '@/components/icons/BrandIcon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import PriceCell from '@/components/pricing/PriceCell.vue'
import userChannelsAPI, {
  type UserAvailableChannel,
  type UserAvailableGroup,
  type UserPricingInterval,
  type UserSupportedModel
} from '@/api/channels'
import userGroupsAPI from '@/api/groups'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  formatPeakRateWindow,
  hasPeakRate,
  isPeakRateActive,
  serverTimezoneLabel
} from '@/utils/peak-rate'
import type { GroupPlatform } from '@/types'
import {
  getDefaultImagePreviewPrice,
  getDefaultVideoPreviewPrice
} from '@/views/admin/groupsImagePricing'

const appStore = useAppStore()

// ==================== 状态 ====================

const channels = ref<UserAvailableChannel[]>([])
const userGroupRates = ref<Record<number, number>>({})
const loading = ref(false)
const activePlatform = ref('anthropic')
const activeGroupId = ref<number | null>(null)
const priceMode = ref<'group' | 'official'>('group')
const clock = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | null = null

type DisplayBillingMode = 'token' | 'per_request' | 'image' | 'video'

type PricingGroup = UserAvailableGroup & {
  image_rate_independent?: boolean
  image_rate_multiplier?: number
  image_price_1k?: number | null
  image_price_2k?: number | null
  image_price_4k?: number | null
  video_rate_independent?: boolean
  video_rate_multiplier?: number
  video_price_480p?: number | null
  video_price_720p?: number | null
  video_price_1080p?: number | null
}

interface DisplayPriceItem {
  key: string
  label: string
  basePrice: number | null
  groupBasePrice?: number | null
  scale: number
  unit: string
}

// ==================== 产品类别定义 ====================

/** 产品 Tab 配置：按平台分类 */
const productCategories: { platform: GroupPlatform; label: string; icon?: string }[] = [
  { platform: 'anthropic', label: 'Claude Code', icon: 'claude' },
  { platform: 'openai', label: 'Codex', icon: 'openai' },
  { platform: 'gemini', label: 'Gemini', icon: 'gemini' },
  { platform: 'antigravity', label: 'Antigravity', icon: 'antigravity' },
  { platform: 'grok', label: 'Grok' }
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
const activeGroups = computed((): PricingGroup[] => {
  if (!activeChannel.value) return []
  return [...activeChannel.value.section.groups]
    .map(group => group as PricingGroup)
    .sort((a, b) => baseMultiplierForGroup(a) - baseMultiplierForGroup(b))
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
function baseMultiplierForGroup(group: PricingGroup): number {
  const userRate = userGroupRates.value[group.id]
  return typeof userRate === 'number' && Number.isFinite(userRate) && userRate >= 0
    ? userRate
    : group.rate_multiplier
}

function isGroupPeakActive(group: PricingGroup): boolean {
  void clock.value
  return group.subscription_type === 'subscription' && isPeakRateActive(
    group,
    appStore.cachedPublicSettings?.server_utc_offset
  )
}

function effectiveTextMultiplier(group: PricingGroup): number {
  const base = baseMultiplierForGroup(group)
  if (!isGroupPeakActive(group)) return base
  return base * (normalizedPrice(group.peak_rate_multiplier) ?? 1)
}

function formatDisplayMultiplier(group: PricingGroup): string {
  return effectiveTextMultiplier(group).toString()
}

function formatDisplayDiscount(group: PricingGroup): string {
  // 折扣计算：(groupPrice / officialPrice) * 10
  // officialPrice 包含 *7 的换算，groupPrice 是真实的，所以折扣是 (multiplier / 7) * 10
  const discount = (effectiveTextMultiplier(group) / 7) * 10
  return discount % 1 === 0 ? discount.toFixed(0) : discount.toFixed(1)
}

function peakRateWindow(group: PricingGroup): string {
  return formatPeakRateWindow(
    group,
    serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset)
  )
}

function savingsPercent(multiplier?: number): number {
  if (!multiplier) return 0
  const ratio = multiplier / 7
  if (ratio >= 1) return 0
  return Math.round((1 - ratio) * 100)
}

function normalizedPrice(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
}

function isGrokVideoModel(modelName: string): boolean {
  return modelName.trim().toLowerCase().startsWith('grok-imagine-video')
}

function isGrokImageModel(modelName: string): boolean {
  const normalized = modelName.trim().toLowerCase()
  return normalized.startsWith('grok-imagine') && !isGrokVideoModel(normalized)
}

function billingModeFor(model: UserSupportedModel): DisplayBillingMode {
  if (isGrokVideoModel(model.name)) return 'video'
  if (isGrokImageModel(model.name)) return 'image'

  const mode = model.pricing?.billing_mode as string | undefined
  if (mode === 'per_request' || mode === 'image' || mode === 'video') return mode
  return 'token'
}

function billingModeLabel(model: UserSupportedModel): string {
  switch (billingModeFor(model)) {
    case 'per_request':
      return '按次计费'
    case 'image':
      return '按图片计费'
    case 'video':
      return '按视频时长计费'
    default:
      return '按 Token 计费'
  }
}

function multiplierFor(model: UserSupportedModel): number {
  const group = activeGroup.value
  if (!group) return 1

  const mode = billingModeFor(model)
  if (mode === 'image' && group.image_rate_independent) {
    return normalizedPrice(group.image_rate_multiplier) ?? baseMultiplierForGroup(group)
  }
  if (mode === 'video' && group.video_rate_independent) {
    return normalizedPrice(group.video_rate_multiplier) ?? baseMultiplierForGroup(group)
  }
  if (mode === 'image' || mode === 'video') return baseMultiplierForGroup(group)
  return effectiveTextMultiplier(group)
}

function tokenItem(
  key: string,
  label: string,
  basePrice: number | null | undefined
): DisplayPriceItem {
  return {
    key,
    label,
    basePrice: normalizedPrice(basePrice),
    scale: 1_000_000,
    unit: '/ 1M tokens'
  }
}

function requestItem(
  key: string,
  label: string,
  basePrice: number | null | undefined,
  unit: string,
  groupBasePrice?: number | null
): DisplayPriceItem {
  return {
    key,
    label,
    basePrice: normalizedPrice(basePrice),
    groupBasePrice: normalizedPrice(groupBasePrice),
    scale: 1,
    unit
  }
}

function intervalLabel(interval: UserPricingInterval): string {
  if (interval.tier_label) return interval.tier_label
  const max = interval.max_tokens == null ? '∞' : interval.max_tokens.toLocaleString()
  return `${interval.min_tokens.toLocaleString()} - ${max} tokens`
}

function tokenPriceItems(model: UserSupportedModel): DisplayPriceItem[] {
  const pricing = model.pricing
  if (!pricing) return []

  const items = [
    tokenItem('input', '输入', pricing.input_price),
    tokenItem('output', '输出', pricing.output_price),
    tokenItem('cache-write', '缓存创建', pricing.cache_write_price),
    tokenItem('cache-read', '缓存读取', pricing.cache_read_price)
  ]
  if ((normalizedPrice(pricing.image_output_price) ?? 0) > 0) {
    items.push(tokenItem('image-output', '图片输出', pricing.image_output_price))
  }

  for (const [index, interval] of (pricing.intervals ?? []).entries()) {
    const prefix = `区间 ${intervalLabel(interval)}`
    const intervalPrices = [
      tokenItem(`interval-${index}-input`, `${prefix} · 输入`, interval.input_price),
      tokenItem(`interval-${index}-output`, `${prefix} · 输出`, interval.output_price),
      tokenItem(`interval-${index}-cache-write`, `${prefix} · 缓存创建`, interval.cache_write_price),
      tokenItem(`interval-${index}-cache-read`, `${prefix} · 缓存读取`, interval.cache_read_price)
    ]
    items.push(...intervalPrices.filter(item => item.basePrice != null))
  }
  return items
}

function perRequestPriceItems(model: UserSupportedModel): DisplayPriceItem[] {
  const pricing = model.pricing
  if (!pricing) return []

  const items: DisplayPriceItem[] = []
  if (normalizedPrice(pricing.per_request_price) != null) {
    items.push(requestItem('request', '每次请求', pricing.per_request_price, '/ 次'))
  }
  for (const [index, interval] of (pricing.intervals ?? []).entries()) {
    if (normalizedPrice(interval.per_request_price) == null) continue
    items.push(requestItem(
      `interval-${index}`,
      intervalLabel(interval),
      interval.per_request_price,
      '/ 次'
    ))
  }
  return items
}

function grokImageDefaultPrice(modelName: string, tier: '1k' | '2k' | '4k'): number | null {
  const normalized = modelName.trim().toLowerCase()
  if (normalized === 'grok-imagine-image-quality') {
    return tier === '1k' ? 0.05 : 0.07
  }
  return getDefaultImagePreviewPrice('grok', `image_price_${tier}`)
}

function imagePriceItems(model: UserSupportedModel): DisplayPriceItem[] {
  const group = activeGroup.value
  const pricing = model.pricing

  if (isGrokImageModel(model.name)) {
    return (['1k', '2k', '4k'] as const).map(tier => requestItem(
      `image-${tier}`,
      tier.toUpperCase(),
      grokImageDefaultPrice(model.name, tier),
      '/ 张',
      group?.[`image_price_${tier}`]
    ))
  }

  const intervalItems = (pricing?.intervals ?? [])
    .filter(interval => normalizedPrice(interval.per_request_price) != null)
    .map((interval, index) => requestItem(
      `interval-${index}`,
      intervalLabel(interval),
      interval.per_request_price,
      '/ 张'
    ))
  if (intervalItems.length > 0) return intervalItems

  const configuredPrice = normalizedPrice(pricing?.per_request_price)
    ?? normalizedPrice(pricing?.image_output_price)
  if (configuredPrice != null) {
    return [requestItem('image', '每张图片', configuredPrice, '/ 张', group?.image_price_1k)]
  }

  const platform = model.platform || activePlatform.value
  return (['1k', '2k', '4k'] as const).map(tier => requestItem(
    `image-${tier}`,
    tier.toUpperCase(),
    getDefaultImagePreviewPrice(platform, `image_price_${tier}`),
    '/ 张',
    group?.[`image_price_${tier}`]
  ))
}

function videoDefaultPrice(modelName: string, resolution: '480p' | '720p' | '1080p'): number | null {
  if (modelName.trim().toLowerCase().startsWith('grok-imagine-video-1.5')) {
    if (resolution === '480p') return 0.08
    if (resolution === '720p') return 0.14
  }
  return getDefaultVideoPreviewPrice('grok', `video_price_${resolution}`)
}

function videoPriceItems(model: UserSupportedModel): DisplayPriceItem[] {
  const group = activeGroup.value
  const isVideo15 = model.name.trim().toLowerCase().startsWith('grok-imagine-video-1.5')
  const resolutions = isVideo15
    ? (['480p', '720p', '1080p'] as const)
    : (['480p', '720p'] as const)

  return resolutions.map(resolution => requestItem(
    `video-${resolution}`,
    resolution,
    videoDefaultPrice(model.name, resolution),
    '/ 秒',
    group?.[`video_price_${resolution}`]
  ))
}

function priceItemsFor(model: UserSupportedModel): DisplayPriceItem[] {
  switch (billingModeFor(model)) {
    case 'per_request':
      return perRequestPriceItems(model)
    case 'image':
      return imagePriceItems(model)
    case 'video':
      return videoPriceItems(model)
    default:
      return tokenPriceItems(model)
  }
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
    const [availableChannels, rates] = await Promise.all([
      userChannelsAPI.getAvailable(),
      userGroupsAPI.getUserGroupRates().catch((error: unknown) => {
        console.error('Failed to load user group rates:', error)
        return {} as Record<number, number>
      })
    ])
    channels.value = availableChannels
    userGroupRates.value = rates
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

onMounted(() => {
  void loadChannels()
  clockTimer = setInterval(() => {
    clock.value = Date.now()
  }, 30_000)
})

onUnmounted(() => {
  if (clockTimer !== null) clearInterval(clockTimer)
})
</script>
