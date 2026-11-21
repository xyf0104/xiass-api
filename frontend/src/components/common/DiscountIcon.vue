<template>
  <span class="discount-icon" :class="`discount-icon--${size}`" aria-hidden="true" data-test="discount-icon" />
</template>

<script setup lang="ts">
withDefaults(defineProps<{ size?: 'xs' | 'sm' | 'md' }>(), { size: 'sm' })
</script>

<style scoped>
.discount-icon {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 1px solid #fdba74;
  border-radius: 5px 4px 6px 4px;
  background: #fff7ed;
  color: #ea580c;
  font-family: ui-rounded, "PingFang SC", "Microsoft YaHei", sans-serif;
  font-size: 12px;
  font-weight: 900;
  line-height: 1;
  letter-spacing: 0;
  vertical-align: middle;
  transform: rotate(-7deg);
}

.discount-icon::before {
  content: "\6298";
}

.discount-icon--xs {
  width: 16px;
  height: 16px;
  font-size: 11px;
}

.discount-icon--md {
  width: 20px;
  height: 20px;
  font-size: 13px;
}

.dark .discount-icon {
  border-color: #c2410c;
  background: #431407;
  color: #fb923c;
}
</style>
