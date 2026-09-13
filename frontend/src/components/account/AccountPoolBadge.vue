<template>
  <span
    class="account-pool-badge inline-flex w-fit max-w-full items-center truncate rounded border px-1.5 py-0.5 text-[11px] font-medium leading-4"
    :style="badgeStyle"
    :title="`号池：${pool.name}`"
  >
    {{ pool.name }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { AccountPool } from '@/api/admin/accountPools'

const props = defineProps<{ pool: Pick<AccountPool, 'id' | 'name'> }>()
const hue = computed(() => Math.round((Math.abs(props.pool.id) * 137.508 + 17) % 360))
const badgeStyle = computed(() => ({ '--account-pool-hue': String(hue.value) }))
</script>

<style scoped>
.account-pool-badge {
  border-color: hsl(var(--account-pool-hue) 58% 78%);
  background-color: hsl(var(--account-pool-hue) 78% 95%);
  color: hsl(var(--account-pool-hue) 62% 29%);
}

:global(.dark) .account-pool-badge {
  border-color: hsl(var(--account-pool-hue) 42% 42% / 0.8);
  background-color: hsl(var(--account-pool-hue) 38% 18% / 0.78);
  color: hsl(var(--account-pool-hue) 76% 76%);
}
</style>
