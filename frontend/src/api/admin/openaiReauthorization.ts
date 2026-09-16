import { apiClient } from '../client'
import type { BatchOAuthSMS, BatchOAuthTask } from './openaiBatchOAuth'
import type { Account } from '@/types'

export interface OpenAIReauthorizationTask extends BatchOAuthTask {
  mode: 'reauthorization'
  target_account_id: number
  reauthorization_number?: number
}

const path = '/admin/openai/reauthorization/tasks'
const accountsPath = '/admin/openai/reauthorization/accounts'

export type OpenAIReauthorizationRiskLevel = 'first' | 'cooldown' | 'repeated' | 'blocked' | 'success' | 'failed' | 'unknown' | 'history'

export interface OpenAIReauthorizationAccountStatus {
  account: Account
  current_needs_reauthorization: boolean
  current_authorization_number: number
  has_history: boolean
  has_attempted: boolean
  has_reauthorized: boolean
  attempt_count: number
  success_count: number
  first_attempt_at?: string
  last_attempt_at?: string
  first_succeeded_at?: string
  last_succeeded_at?: string
  successful_authorization_times?: string[]
  last_result?: string
  last_reason?: string
  last_result_at?: string
  history_source?: string
  history_confidence?: 'exact' | 'inferred' | 'unknown'
  legacy_evidence_count?: number
  cooldown_until?: string
  cooldown_remaining_seconds: number
  seconds_since_first_reauthorization?: number
  can_start: boolean
  requires_risk_confirmation: boolean
  risk_level: OpenAIReauthorizationRiskLevel
}

export const openAIReauthorizationAPI = {
  async list() {
    return (await apiClient.get<{ items: OpenAIReauthorizationTask[]; max_concurrency: number; max_restarts: number }>(path)).data
  },
  async accounts(accountIDs: number[] = []) {
    const query = accountIDs.length ? `?account_ids=${encodeURIComponent(accountIDs.join(','))}` : ''
    return (await apiClient.get<{ items: OpenAIReauthorizationAccountStatus[]; cooldown_seconds: number }>(`${accountsPath}${query}`)).data
  },
  async start(accountID: number, acknowledgeRisk = false, browserMode?: 'server' | 'adspower') {
    return (await apiClient.post<OpenAIReauthorizationTask>(path, {
      account_id: accountID,
      confirmed: true,
      acknowledged_second_reauthorization_risk: acknowledgeRisk,
      ...(browserMode ? { browser_mode: browserMode } : {}),
    })).data
  },
  async complete(id: string) {
    return (await apiClient.post<OpenAIReauthorizationTask>(`${path}/${encodeURIComponent(id)}/complete`)).data
  },
  async cancel(id: string) {
    return (await apiClient.post<OpenAIReauthorizationTask>(`${path}/${encodeURIComponent(id)}/cancel`, { confirmed: true })).data
  },
  async restart(id: string, acknowledgeRisk = false, browserMode?: 'server' | 'adspower') {
    return (await apiClient.post<OpenAIReauthorizationTask>(`${path}/${encodeURIComponent(id)}/restart`, {
      confirmed: true,
      acknowledged_second_reauthorization_risk: acknowledgeRisk,
      ...(browserMode ? { browser_mode: browserMode } : {}),
    })).data
  },
  async launchAdsPower(id: string) {
    return (await apiClient.post<{ helper_url: string; expires_at: string }>(`${path}/${encodeURIComponent(id)}/adspower-launch`)).data
  },
  async remove(id: string) {
    return (await apiClient.delete<{ task_id: string; account_id?: number }>(`${path}/${encodeURIComponent(id)}`)).data
  },
  async sms(id: string, action: 'check' | 'acquire' | 'change' | 'cancel') {
    const url = `${path}/${encodeURIComponent(id)}/sms`
    return action === 'check'
      ? (await apiClient.get<BatchOAuthSMS>(url)).data
      : (await apiClient.post<BatchOAuthSMS>(`${url}/${action}`, { confirmed: true })).data
  }
}

export default openAIReauthorizationAPI
