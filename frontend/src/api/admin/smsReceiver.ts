import { apiClient } from '../client'

export type SMSReceiverProvider = 'pixlab' | 'giema'

export interface SMSReceiverProviderConfig {
  giema_api_key_configured: boolean
}

export interface SMSReceiverQueueStatus {
  queued_count: number
  active_count: number
  available?: boolean
  fee_amount?: number
  balance?: number
}

export interface SMSReceiverSession {
  session_id?: string
  status: string
  number?: string
  country?: string
  code?: string
  expires_at?: string | null
  server_time?: string | null
  queued_count: number
  fee_amount?: number
  charge_state?: 'held' | 'captured' | 'released' | ''
  balance?: number
  action_available_at?: string
  number_pending?: boolean
  poll_seconds?: number
  local_stop?: boolean
}

export interface SMSReceiverAddResult {
  added_count: number
  queued_count: number
}

export interface SMSReceiverClearResult {
  deleted_count: number
  queued_count: number
}

export interface SMSReceiverCardKey {
  id: number
  card_key: string
  provider?: string
  status: string
  claim_count: number
  queue_rank: number
  last_claimed_at?: string
  created_at: string
}

export interface SMSReceiverCardKeyListResult extends SMSReceiverQueueStatus {
  card_keys: SMSReceiverCardKey[]
}

export interface SMSReceiverMemberFeeResult {
  fee_amount: number
}

export async function getStatus(): Promise<SMSReceiverQueueStatus> {
  const { data } = await apiClient.get<SMSReceiverQueueStatus>('/admin/settings/sms-receiver')
  return data
}

export async function getProviderConfig(): Promise<SMSReceiverProviderConfig> {
  const { data } = await apiClient.get<SMSReceiverProviderConfig>('/admin/settings/sms-receiver/providers')
  return { giema_api_key_configured: data?.giema_api_key_configured === true }
}

export async function updateProviderConfig(apiKey: string): Promise<SMSReceiverProviderConfig> {
  const { data } = await apiClient.put<SMSReceiverProviderConfig>('/admin/settings/sms-receiver/providers', { api_key: apiKey })
  return { giema_api_key_configured: data?.giema_api_key_configured === true }
}

export async function addCardKeys(cardKeys: string, provider?: SMSReceiverProvider): Promise<SMSReceiverAddResult> {
  const { data } = await apiClient.post<SMSReceiverAddResult>('/admin/settings/sms-receiver/card-keys', {
    card_keys: cardKeys,
    ...(provider ? { provider } : {})
  })
  return data
}

export async function clearCardKeys(): Promise<SMSReceiverClearResult> {
  const { data } = await apiClient.delete<SMSReceiverClearResult>('/admin/settings/sms-receiver/card-keys')
  return data
}

export async function listCardKeys(): Promise<SMSReceiverCardKeyListResult> {
  const { data } = await apiClient.get<SMSReceiverCardKeyListResult>('/admin/settings/sms-receiver/card-keys')
  return data
}

export async function deleteCardKey(cardKeyID: number): Promise<SMSReceiverQueueStatus> {
  const { data } = await apiClient.delete<SMSReceiverQueueStatus>(`/admin/settings/sms-receiver/card-keys/${cardKeyID}`)
  return data
}

export async function reorderCardKeys(cardKeyIDs: number[]): Promise<SMSReceiverQueueStatus> {
  const { data } = await apiClient.put<SMSReceiverQueueStatus>('/admin/settings/sms-receiver/card-keys/order', {
    card_key_ids: cardKeyIDs
  })
  return data
}

export async function updateMemberFee(memberFee: number): Promise<SMSReceiverMemberFeeResult> {
  const { data } = await apiClient.put<SMSReceiverMemberFeeResult>('/admin/settings/sms-receiver/member-fee', {
    member_fee: memberFee
  })
  return data
}

export async function redeem(): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>('/admin/settings/sms-receiver/redeem')
  return data
}

export async function resume(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/resume`)
  return data
}

export async function check(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/check`)
  return data
}

export async function change(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/change`)
  return data
}

export async function cancel(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/cancel`)
  return data
}

// Member endpoints never receive card keys or global queue totals. They use
// the same browser-safe session shape as the admin tool plus fee/balance data.
const memberBasePath = '/sms-receiver'

export const memberSMSReceiverAPI = {
  async getStatus(): Promise<SMSReceiverQueueStatus> {
    const { data } = await apiClient.get<SMSReceiverQueueStatus>(memberBasePath)
    return data
  },
  async redeem(): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/redeem`)
    return data
  },
  async resume(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/resume`)
    return data
  },
  async check(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/check`)
    return data
  },
  async change(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/change`)
    return data
  },
  async cancel(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/cancel`)
    return data
  }
}
