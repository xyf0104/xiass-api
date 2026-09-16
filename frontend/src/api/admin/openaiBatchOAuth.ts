import { apiClient } from '../client'

export interface BatchOAuthConfig {
  group_ids: number[]
  proxy_id: number | null
  pool_id: number | null
  concurrency: number
  priority: number
  codex_fingerprint_mode: 'off' | 'device' | 'session' | 'full'
  browser_mode?: 'server' | 'adspower'
}

export interface BatchOAuthTask {
  task_id: string
  email: string
  login_method: 'password' | 'email_code'
  status: 'queued' | 'running' | 'ready' | 'completed' | 'failed' | 'blocked' | 'canceled'
  stage: string
  reason?: string
  account_id?: number
  restart_count: number
  browser_mode?: 'server' | 'adspower'
  requires_sms_confirmation: boolean
  created_at: string
  expires_at: string
  finished_at?: string
}

export interface BatchOAuthPasswordLogin {
  login_method: 'password'
  password: string
  totp_secret: string
}

export interface BatchOAuthEmailCodeLogin {
  login_method: 'email_code'
  email_code_token: string
}

export type BatchOAuthLogin = BatchOAuthPasswordLogin | BatchOAuthEmailCodeLogin

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
  async remove(id: string) {
    return (await apiClient.delete<{ task_id: string; account_id?: number }>(`${path}/${encodeURIComponent(id)}`)).data
  },
  async cancel(id: string) {
    return (await apiClient.post<BatchOAuthTask>(`${path}/${encodeURIComponent(id)}/cancel`, { confirmed: true })).data
  },
  async restart(id: string, login?: BatchOAuthLogin, browserMode?: 'server' | 'adspower') {
    return (await apiClient.post<BatchOAuthTask>(`${path}/${encodeURIComponent(id)}/restart`, {
      ...login,
      confirmed: true,
      ...(browserMode ? { browser_mode: browserMode } : {}),
    })).data
  },
  async launchAdsPower(id: string) {
    return (await apiClient.post<{ helper_url: string; expires_at: string }>(`${path}/${encodeURIComponent(id)}/adspower-launch`)).data
  },
  async sms(id: string, action: 'check' | 'acquire' | 'change' | 'cancel') {
    const url = `${path}/${encodeURIComponent(id)}/sms`
    return action === 'check'
      ? (await apiClient.get<BatchOAuthSMS>(url)).data
      : (await apiClient.post<BatchOAuthSMS>(`${url}/${action}`, { confirmed: true })).data
  }
}
