import { computed, ref } from 'vue'
import { executionNodesAPI, type ExecutionNodeAdminStatus } from '@/api/admin/executionNodes'

const status = ref<ExecutionNodeAdminStatus | null>(null)
const loading = ref(false)
let pending: Promise<ExecutionNodeAdminStatus | null> | null = null

async function loadExecutionNodeAdminAccess(force = false): Promise<ExecutionNodeAdminStatus | null> {
  if (!force && status.value) return status.value
  if (!force && pending) return pending

  loading.value = true
  pending = executionNodesAPI.getStatus()
    .then((next) => {
      status.value = next
      return next
    })
    .catch(() => status.value)
    .finally(() => {
      loading.value = false
      pending = null
    })
  return pending
}

export function useExecutionNodeAdminAccess() {
  const sharedWriteAllowed = computed(() => status.value?.admin_write_allowed !== false)
  const sharedReadOnly = computed(() => status.value?.runtime.enabled === true && !sharedWriteAllowed.value)

  return {
    executionNodeStatus: status,
    executionNodeAccessLoading: loading,
    sharedWriteAllowed,
    sharedReadOnly,
    loadExecutionNodeAdminAccess
  }
}
