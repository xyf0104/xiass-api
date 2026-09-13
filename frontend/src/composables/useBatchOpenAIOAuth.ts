import { computed, onScopeDispose, ref } from 'vue'
import { batchOAuthAPI, type BatchOAuthConfig, type BatchOAuthLogin, type BatchOAuthTask } from '@/api/admin/openaiBatchOAuth'
import type { AccountCredentialRow } from '@/features/token-converter/accountCredentials'
import { normalizeBase32Secret } from '@/features/token-converter/totp'

export interface OAuthQueueRow {
  key: string
  email: string
  task?: BatchOAuthTask
  localStatus?: 'pending' | 'starting' | 'uncertain' | 'canceled'
  retryPending?: boolean
  error?: string
  number?: string
  automaticState?: string
  automaticAfter?: number
}

const automaticRestartLimits: Record<string, number> = {
  sms_timeout: 2,
  sms_confirmation_timeout: 2,
  task_expired: 2,
  captcha_required: 1,
  email_code_required: 1,
  account_blocked: 1,
  manual_challenge: 1,
  navigation_timeout: 1,
  browser_context_lost: 1,
  page_interaction_failed: 1,
}

export function batchTaskActive(task?: BatchOAuthTask) {
  return !!task && ['queued', 'running', 'ready'].includes(task.status)
}

export function batchTaskWillAutoRestart(task?: BatchOAuthTask) {
  return !!task && ['failed', 'blocked'].includes(task.status) && !task.account_id
    && task.restart_count < (automaticRestartLimits[task.reason || ''] || 0)
}

export function useBatchOpenAIOAuth(onCreated: () => void) {
  const rows = ref<OAuthQueueRow[]>([])
  const error = ref('')
  const loading = ref(true)
  const started = ref(false)
  const busyKeys = ref(new Set<string>())
  // Login material never enters a reactive store, URL or browser persistence.
  const secrets = new Map<string, BatchOAuthLogin>()
  const announced = new Set<number>()
  let config: BatchOAuthConfig | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false
  let syncing = false
  let stopping = false
  const activeCount = computed(() => rows.value.filter(r => batchTaskActive(r.task) || r.localStatus === 'starting' || r.localStatus === 'uncertain').length)
  const pendingCount = computed(() => rows.value.filter(r => r.localStatus === 'pending' || r.retryPending).length)
  const hasWork = computed(() => activeCount.value > 0 || pendingCount.value > 0 || busyKeys.value.size > 0 || rows.value.some(row => row.task?.requires_sms_confirmation))

  function update(row: OAuthQueueRow, task: BatchOAuthTask) {
    if (disposed) return
    row.task = task
    row.localStatus = undefined
    if (task.account_id && !announced.has(task.account_id)) {
      announced.add(task.account_id)
      onCreated()
    }
    if (['completed', 'canceled'].includes(task.status)) secrets.delete(row.key)
  }

  async function operation(row: OAuthQueueRow, action: () => Promise<void>) {
    if (disposed || busyKeys.value.has(row.key)) return
    busyKeys.value.add(row.key)
    row.error = ''
    try { await action() } catch {
      // Never surface an HTTP client's request payload or external login page.
      row.error = '操作未确认，请刷新状态后重试。'
    } finally { busyKeys.value.delete(row.key) }
  }

  async function automatic(row: OAuthQueueRow, state: string, delayMs: number, action: () => Promise<void>) {
    if (row.automaticState !== state) {
      row.automaticState = state
      row.automaticAfter = Date.now() + delayMs
      return
    }
    if ((row.automaticAfter || 0) > Date.now()) return
    row.automaticAfter = Date.now() + delayMs
    await action()
  }

  async function create(row: OAuthQueueRow) {
    const login = secrets.get(row.key)
    if (!config || !login || disposed) return
    row.localStatus = 'starting'
    await operation(row, async () => {
      try {
        const task = await batchOAuthAPI.create({ ...config!, group_ids: [...config!.group_ids], ...login, email: row.email, idempotency_key: row.key })
        update(row, task)
      } catch {
        // Ambiguous delivery keeps its slot; explicit retry reuses the same key.
        row.localStatus = 'uncertain'
        throw new Error('unconfirmed')
      }
    })
  }

  async function sync() {
    if (disposed || syncing) return
    syncing = true
    try {
      const result = await batchOAuthAPI.list()
      if (disposed) return
      error.value = ''
      const latestByEmail = new Map<string, BatchOAuthTask>()
      for (const task of result.items) latestByEmail.set(task.email.trim().toLowerCase(), task)
      const latestTasks = [...latestByEmail.values()]
      for (const task of latestTasks) {
        let row = rows.value.find(r => r.task?.task_id === task.task_id)
        if (!row) {
          // Do not infer identity from email after a lost create response.
          if (rows.value.some(r => r.email === task.email && ['starting', 'uncertain'].includes(r.localStatus || ''))) continue
          row = rows.value.find(r => r.email.trim().toLowerCase() === task.email.trim().toLowerCase() && !r.localStatus)
          if (!row) {
            row = { key: task.task_id, email: task.email, task }
            rows.value.push(row)
          }
        }
        if (!busyKeys.value.has(row.key)) update(row, task)
      }
      const visibleTaskIDs = new Set(latestTasks.map(task => task.task_id))
      rows.value = rows.value.filter(row => !row.task || Boolean(row.localStatus) || visibleTaskIDs.has(row.task.task_id))
      for (const row of rows.value) {
        if (disposed || stopping) break
        if (!row.task || busyKeys.value.has(row.key)) continue
        if (row.task.status === 'ready' && !row.error) {
          await operation(row, async () => update(row, await batchOAuthAPI.complete(row.task!.task_id)))
        } else if (secrets.has(row.key) && batchTaskWillAutoRestart(row.task)) {
          const state = `restart:${row.task.restart_count}:${row.task.reason}`
          await automatic(row, state, 10000, async () => retry(row))
        } else if (row.task.status === 'running' && row.task.stage === 'phone_required') {
          if (row.task.reason === 'phone_rejected') {
            const state = `change:${row.task.restart_count}:${row.number || ''}`
            await automatic(row, state, 1000, async () => sms(row, 'change'))
          } else if (!row.number) {
            const state = `acquire:${row.task.restart_count}`
            await automatic(row, state, 1000, async () => ensurePhone(row))
          }
        } else if (row.task.status === 'running' && row.task.stage === 'sms_waiting') {
          await pollSMS(row)
        }
      }
      if (!stopping) {
        for (const row of rows.value) {
          if (disposed || stopping) break
          if (!row.retryPending) continue
          if (row.localStatus !== 'uncertain' && activeCount.value >= 3) break
          row.retryPending = false
          if (row.localStatus === 'uncertain') await create(row)
          else await restart(row)
        }
        for (const row of rows.value) {
          if (disposed || stopping || activeCount.value >= 3) break
          if (row.localStatus === 'pending') await create(row)
        }
      }
    } catch {
      if (!disposed) error.value = '暂时无法获取授权状态，未自动重建或重复添加账号。'
    } finally {
      loading.value = false
      syncing = false
      if (!disposed) timer = setTimeout(() => void sync(), hasWork.value ? 1000 : 10000)
    }
  }

  function start(credentials: AccountCredentialRow[], settings: BatchOAuthConfig) {
    if (started.value || disposed || loading.value || error.value || hasWork.value) return
    config = { ...settings, group_ids: [...settings.group_ids] }
    const existing = new Set(rows.value.filter(r => batchTaskActive(r.task)).map(r => r.email.toLowerCase()))
    for (const credential of credentials) {
      const email = credential.account.toLowerCase()
      if (existing.has(email)) continue
      existing.add(email)
      const key = crypto.randomUUID()
      secrets.set(key, { password: credential.password, totp_secret: normalizeBase32Secret(credential.twoFactor) })
      rows.value.push({ key, email, localStatus: 'pending' })
    }
    started.value = true
    clearTimeout(timer)
    void sync()
  }

  async function pollSMS(row: OAuthQueueRow) {
    await operation(row, async () => {
      const result = await batchOAuthAPI.sms(row.task!.task_id, 'check')
      update(row, result.task)
      row.number = result.sms?.number
    })
  }

  async function ensurePhone(row: OAuthQueueRow) {
    if (!row.task) return
    await operation(row, async () => {
      let result = await batchOAuthAPI.sms(row.task!.task_id, 'check')
      update(row, result.task)
      row.number = result.sms?.number
      if (!result.sms?.number && row.task?.status === 'running' && row.task.stage === 'phone_required') {
        result = await batchOAuthAPI.sms(row.task.task_id, 'acquire')
        update(row, result.task)
        row.number = result.sms?.number
      }
    })
  }

  async function sms(row: OAuthQueueRow, action: 'acquire' | 'change' | 'cancel') {
    if (!row.task) return
    await operation(row, async () => {
      const result = await batchOAuthAPI.sms(row.task!.task_id, action)
      update(row, result.task)
      row.number = result.sms?.number
    })
  }

  async function cancel(row: OAuthQueueRow) {
    if (row.retryPending) {
      row.retryPending = false
      return
    }
    if (row.localStatus === 'pending') {
      row.localStatus = 'canceled'
      secrets.delete(row.key)
      return
    }
    if (row.localStatus === 'uncertain') await create(row)
    if (!row.task || busyKeys.value.has(row.key)) return
    await operation(row, async () => {
      update(row, await batchOAuthAPI.cancel(row.task!.task_id))
      secrets.delete(row.key)
    })
  }

  async function cancelAll() {
    stopping = true
    try {
      for (const row of rows.value) {
        if (row.retryPending || row.localStatus || batchTaskActive(row.task) || row.task?.requires_sms_confirmation) await cancel(row)
      }
    } finally { stopping = false }
  }

  async function restart(row: OAuthQueueRow, login?: BatchOAuthLogin) {
    if (!row.task || row.task.account_id || row.task.restart_count >= 2) return
    const retryLogin = login || secrets.get(row.key)
    if (!retryLogin) {
      row.error = '登录信息已从浏览器内存清除，请删除记录后重新粘贴账号、密码和 2FA。'
      return
    }
    await operation(row, async () => {
      update(row, await batchOAuthAPI.restart(row.task!.task_id, retryLogin))
      row.number = undefined
    })
  }

  function retry(row: OAuthQueueRow) {
    if (disposed || row.retryPending) return
    if (!secrets.has(row.key)) {
      row.error = '登录信息已从浏览器内存清除，请删除记录后重新粘贴账号、密码和 2FA。'
      return
    }
    if (row.localStatus !== 'uncertain' && (!row.task || row.task.account_id || row.task.restart_count >= 2)) return
    row.error = ''
    row.retryPending = true
    clearTimeout(timer)
    if (!syncing) void sync()
  }

  async function complete(row: OAuthQueueRow) {
    if (!row.task) return
    await operation(row, async () => update(row, await batchOAuthAPI.complete(row.task!.task_id)))
  }

  async function remove(row: OAuthQueueRow) {
    if (!row.task || !['completed', 'failed', 'blocked', 'canceled'].includes(row.task.status)) return
    await operation(row, async () => {
      await batchOAuthAPI.remove(row.task!.task_id)
      secrets.delete(row.key)
      rows.value = rows.value.filter(candidate => candidate !== row)
      if (rows.value.length === 0) {
        started.value = false
        config = undefined
      }
    })
  }

  function dispose() {
    disposed = true
    clearTimeout(timer)
    secrets.clear()
    config = undefined
  }
  onScopeDispose(dispose)
  void sync()
  return { rows, error, loading, started, busyKeys, activeCount, pendingCount, hasWork, start, sms, cancel, cancelAll, retry, complete, remove,
    hasSecret: (row: OAuthQueueRow) => secrets.has(row.key), refresh: async () => { clearTimeout(timer); await sync() } }
}
