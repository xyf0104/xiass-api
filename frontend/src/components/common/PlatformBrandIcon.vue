<template>
  <span
    :class="['platform-brand-icon', sizeClass]"
    :style="{ background: brand.background }"
    :aria-label="brand.label"
    :data-platform-brand="platform"
    role="img"
  >
    <ModelIcon
      :model="brand.model"
      :size="iconSize"
      :color="brand.color"
      :path-colors="brand.pathColors"
    />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import type { GroupPlatform } from '@/types'

type BrandStyle = {
  label: string
  model: string
  background: string
  color: string
  pathColors?: string[]
}

const props = withDefaults(defineProps<{
  platform: GroupPlatform
  size?: 'sm' | 'md' | 'lg'
}>(), {
  size: 'md'
})

// Official logo paths are rendered by ModelIcon. These avatar treatments keep
// each mark readable in both themes while preserving the vendors' brand colors.
const brands: Record<GroupPlatform, BrandStyle> = {
  anthropic: {
    label: 'Claude',
    model: 'anthropic',
    background: '#2F211D',
    color: '#D97757'
  },
  openai: {
    label: 'OpenAI',
    model: 'openai',
    background: '#0D2A24',
    color: '#10A37F'
  },
  gemini: {
    label: 'Gemini',
    model: 'gemini',
    background: 'linear-gradient(135deg, #08B962 0%, #3186FF 54%, #F94543 100%)',
    color: '#FFFFFF'
  },
  antigravity: {
    label: 'Antigravity',
    model: 'antigravity',
    background: 'linear-gradient(135deg, #00B95C 0%, #3186FF 52%, #FC413D 100%)',
    color: '#FFFFFF'
  },
  grok: {
    label: 'Grok',
    model: 'grok',
    background: '#0B0B0B',
    color: '#FFFFFF'
  },
  kimi: {
    label: 'Kimi',
    model: 'kimi',
    background: '#111318',
    color: '#FFFFFF',
    pathColors: ['#1783FF', '#FFFFFF']
  },
  zhipu: {
    label: 'Zhipu GLM',
    model: 'zhipu',
    background: '#EEF1FF',
    color: '#3859FF'
  },
  deepseek: {
    label: 'DeepSeek',
    model: 'deepseek',
    background: '#EDF1FF',
    color: '#4D6BFE'
  },
  minimax: {
    label: 'MiniMax',
    model: 'minimax',
    background: '#111318',
    color: '#FFFFFF'
  },
  opencode_go: {
    label: 'OpenCode Go',
    model: 'opencode_go',
    background: '#EEF2F7',
    color: '#334155'
  },
  composite: {
    label: 'Composite',
    model: 'composite',
    background: '#EEF2F7',
    color: '#475569'
  }
}

const brand = computed(() => brands[props.platform] ?? brands.composite)

const sizeClass = computed(() => ({
  sm: 'h-7 w-7 rounded-md',
  md: 'h-8 w-8 rounded-md',
  lg: 'h-9 w-9 rounded-lg'
})[props.size])

const iconSize = computed(() => ({ sm: '17px', md: '20px', lg: '22px' })[props.size])
</script>

<style scoped>
.platform-brand-icon {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.12), 0 1px 2px rgb(15 23 42 / 0.12);
}
</style>
