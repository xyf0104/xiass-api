<template>
  <div class="multi-proxy-selector">
    <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
      <div class="flex items-center gap-2 text-sm">
        <span class="font-medium text-gray-800 dark:text-dark-100">
          {{ t('admin.accounts.multiProxy.configured', { count: modelValue.length }) }}
        </span>
        <span class="text-gray-400 dark:text-dark-500">·</span>
        <span class="font-mono text-xs text-gray-600 dark:text-dark-300">
          {{ t('admin.accounts.multiProxy.totalCapacity', { count: totalCapacity }) }}
        </span>
      </div>
      <div class="relative min-w-44 flex-1 sm:max-w-64">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400" />
        <input
          v-model="query"
          type="search"
          class="input h-9 pl-8"
          :placeholder="t('admin.proxies.searchProxies')"
          :disabled="disabled"
        />
      </div>
    </div>

    <div class="max-h-72 overflow-y-auto rounded-md border border-gray-200 dark:border-dark-600">
      <label
        v-for="proxy in filteredProxies"
        :key="proxy.id"
        class="grid cursor-pointer grid-cols-[20px_minmax(0,1fr)_84px] items-center gap-2 border-b border-gray-100 px-3 py-2.5 last:border-b-0 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800/70"
        :class="disabled && 'cursor-not-allowed opacity-60'"
      >
        <input
          type="checkbox"
          class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          :checked="isSelected(proxy.id)"
          :disabled="disabled || proxy.status !== 'active'"
          @change="toggleProxy(proxy.id)"
        />
        <span class="min-w-0">
          <span class="flex min-w-0 items-center gap-2">
            <span class="truncate text-sm font-medium text-gray-800 dark:text-dark-100">{{ proxy.name }}</span>
            <span class="shrink-0 font-mono text-[11px] uppercase text-gray-400">{{ proxy.protocol }}</span>
          </span>
          <span class="block break-all font-mono text-xs text-gray-500 dark:text-dark-400">
            {{ proxyAddress(proxy) }}
          </span>
        </span>
        <span v-if="isSelected(proxy.id)" class="flex items-center gap-1">
          <input
            :value="capacityFor(proxy.id)"
            type="number"
            min="1"
            max="10000"
            class="input h-8 w-16 px-2 text-center font-mono text-xs"
            :disabled="disabled"
            :aria-label="t('admin.accounts.multiProxy.proxyCapacity', { name: proxy.name })"
            @click.stop
            @input="updateCapacity(proxy.id, $event)"
          />
          <span class="text-xs text-gray-400">{{ t('admin.accounts.multiProxy.slots') }}</span>
        </span>
        <span v-else class="text-right text-xs text-gray-400">{{ proxy.status }}</span>
      </label>

      <div v-if="missingBindings.length > 0" class="border-t border-amber-200 bg-amber-50/70 dark:border-amber-900/50 dark:bg-amber-950/20">
        <div
          v-for="binding in missingBindings"
          :key="binding.proxy_id"
          class="grid grid-cols-[20px_minmax(0,1fr)_84px] items-center gap-2 px-3 py-2.5"
        >
          <button
            type="button"
            class="text-gray-400 hover:text-red-500"
            :disabled="disabled"
            :title="t('admin.accounts.multiProxy.remove')"
            @click="removeProxy(binding.proxy_id)"
          >
            <Icon name="x" size="sm" />
          </button>
          <span class="text-sm text-amber-800 dark:text-amber-300">
            {{ t('admin.accounts.multiProxy.missingProxy', { id: binding.proxy_id }) }}
          </span>
          <span class="text-right font-mono text-xs text-amber-700 dark:text-amber-400">
            0/{{ binding.max_concurrency }}
          </span>
        </div>
      </div>

      <div v-if="filteredProxies.length === 0 && missingBindings.length === 0" class="px-3 py-8 text-center text-sm text-gray-500 dark:text-dark-400">
        {{ t('common.noOptionsFound') }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { AccountProxyBindingInput, Proxy } from '@/types'

const props = withDefaults(defineProps<{
  modelValue: AccountProxyBindingInput[]
  proxies: Proxy[]
  disabled?: boolean
}>(), {
  disabled: false
})

const emit = defineEmits<{
  'update:modelValue': [value: AccountProxyBindingInput[]]
}>()

const { t } = useI18n()
const query = ref('')

const selectedByID = computed(() => new Map(props.modelValue.map((binding) => [binding.proxy_id, binding])))
const proxyIDs = computed(() => new Set(props.proxies.map((proxy) => proxy.id)))
const totalCapacity = computed(() => props.modelValue.reduce((sum, binding) => sum + Math.max(1, binding.max_concurrency || 1), 0))
const missingBindings = computed(() => props.modelValue.filter((binding) => !proxyIDs.value.has(binding.proxy_id)))

const filteredProxies = computed(() => {
  const normalized = query.value.trim().toLowerCase()
  if (!normalized) return props.proxies
  return props.proxies.filter((proxy) =>
    proxy.name.toLowerCase().includes(normalized) ||
    proxy.host.toLowerCase().includes(normalized) ||
    String(proxy.port).includes(normalized)
  )
})

const proxyAddress = (proxy: Proxy) => {
  const host = proxy.host.includes(':') && !proxy.host.startsWith('[') ? `[${proxy.host}]` : proxy.host
  return `${host}:${proxy.port}`
}

const isSelected = (proxyID: number) => selectedByID.value.has(proxyID)
const capacityFor = (proxyID: number) => selectedByID.value.get(proxyID)?.max_concurrency ?? 1

const normalize = (bindings: AccountProxyBindingInput[]) => bindings
  .map((binding) => ({
    proxy_id: binding.proxy_id,
    max_concurrency: Math.max(1, Math.min(10000, Math.trunc(Number(binding.max_concurrency) || 1)))
  }))
  .sort((a, b) => a.proxy_id - b.proxy_id)

const toggleProxy = (proxyID: number) => {
  if (props.disabled) return
  if (isSelected(proxyID)) {
    removeProxy(proxyID)
    return
  }
  emit('update:modelValue', normalize([...props.modelValue, { proxy_id: proxyID, max_concurrency: 1 }]))
}

const removeProxy = (proxyID: number) => {
  if (props.disabled) return
  emit('update:modelValue', props.modelValue.filter((binding) => binding.proxy_id !== proxyID))
}

const updateCapacity = (proxyID: number, event: Event) => {
  const value = Number((event.target as HTMLInputElement).value)
  emit('update:modelValue', normalize(props.modelValue.map((binding) =>
    binding.proxy_id === proxyID
      ? { ...binding, max_concurrency: value }
      : binding
  )))
}
</script>
