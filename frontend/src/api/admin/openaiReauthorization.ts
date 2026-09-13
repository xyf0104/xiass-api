import { apiClient } from '../client'
import type { BatchOAuthSMS, BatchOAuthTask } from './openaiBatchOAuth'

export interface OpenAIReauthorizationTask extends BatchOAuthTask {
  mode: 'reauthorization'
  target_account_id: number
}

const path = '/admin/openai/reauthorization/tasks'

export const openAIReauthorizationAPI = {
  async list() {
    return (await apiClient.get<{ items: OpenAIReauthorizationTask[]; max_concurrency: number; max_restarts: number }>(path)).data
  },
  async start(accountID: number) {
    return (await apiClient.post<OpenAIReauthorizationTask>(path, { account_id: accountID })).data
  },
  async complete(id: string) {
    return (await apiClient.post<OpenAIReauthorizationTask>(`${path}/${encodeURIComponent(id)}/complete`)).data
  },
  async cancel(id: string) {
    return (await apiClient.post<OpenAIReauthorizationTask>(`${path}/${encodeURIComponent(id)}/cancel`, { confirmed: true })).data
  },
  async restart(id: string) {
    return (await apiClient.post<OpenAIReauthorizationTask>(`${path}/${encodeURIComponent(id)}/restart`, { confirmed: true })).data
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
