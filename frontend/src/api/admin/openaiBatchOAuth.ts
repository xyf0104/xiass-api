import { apiClient } from '../client'

export interface BatchOAuthConfig {
  group_ids: number[]
  proxy_id: number | null
  pool_id: number | null
  concurrency: number
  priority: number
  codex_fingerprint_mode: 'off' | 'device' | 'session' | 'full'
}

export interface BatchOAuthTask {
  task_id: string
  email: string
  status: 'queued' | 'running' | 'ready' | 'completed' | 'failed' | 'blocked' | 'canceled'
  stage: string
  reason?: string
  account_id?: number
  restart_count: number
  requires_sms_confirmation: boolean
  created_at: string
  expires_at: string
}

export interface BatchOAuthLogin {
  password: string
  totp_secret: string
}

export interface BatchOAuthSMS {
  task: BatchOAuthTask
  sms: { status: string; number: string; expires_at: string } | null
}

const path = '/admin/openai/batch-oauth/tasks'
export const batchOAuthAPI = {
  async list() {
    return (await apiClient.get<{ items: BatchOAuthTask[]; max_concurrency: number; max_restarts: number }>(path)).data
  },
  async create(input: BatchOAuthConfig & BatchOAuthLogin & { email: string; idempotency_key: string }) {
    return (await apiClient.post<BatchOAuthTask>(path, input)).data
  },
  async complete(id: string) {
    return (await apiClient.post<BatchOAuthTask>(`${path}/${encodeURIComponent(id)}/complete`)).data
  },
  async cancel(id: string) {
    return (await apiClient.post<BatchOAuthTask>(`${path}/${encodeURIComponent(id)}/cancel`, { confirmed: true })).data
  },
  async restart(id: string, login?: BatchOAuthLogin) {
    return (await apiClient.post<BatchOAuthTask>(`${path}/${encodeURIComponent(id)}/restart`, { ...login, confirmed: true })).data
  },
  async sms(id: string, action: 'check' | 'acquire' | 'change' | 'cancel') {
    const url = `${path}/${encodeURIComponent(id)}/sms`
    return action === 'check'
      ? (await apiClient.get<BatchOAuthSMS>(url)).data
      : (await apiClient.post<BatchOAuthSMS>(`${url}/${action}`, { confirmed: true })).data
  }
}
