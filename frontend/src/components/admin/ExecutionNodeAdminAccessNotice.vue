<template>
  <div
    v-if="visible"
    class="flex items-start gap-3 rounded-lg border border-sky-200 bg-sky-50 px-4 py-3 text-sm leading-6 text-sky-800 dark:border-sky-900/60 dark:bg-sky-950/20 dark:text-sky-300"
    data-testid="execution-node-shared-access-notice"
  >
    <Icon name="lock" size="sm" class="mt-1 shrink-0" />
    <div>
      <p class="font-medium">
        {{ t('admin.executionNodes.sharedAccess.readOnlyTitle') }}
      </p>
      <p class="text-xs leading-5 opacity-90">
        {{ t('admin.executionNodes.sharedAccess.readOnlyDescription') }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useExecutionNodeAdminAccess } from '@/composables/useExecutionNodeAdminAccess'

const { t } = useI18n()
const {
  executionNodeStatus,
  sharedReadOnly,
  loadExecutionNodeAdminAccess
} = useExecutionNodeAdminAccess()

const visible = computed(() => executionNodeStatus.value?.runtime.enabled === true && sharedReadOnly.value)

onMounted(() => {
  void loadExecutionNodeAdminAccess()
})
</script>
