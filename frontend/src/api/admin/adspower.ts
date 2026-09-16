import { apiClient } from '../client'

export interface OpenAIAdsPowerBinding {
  version: 1
  device_id: string
  profile_id: string
  profile_no?: string
  profile_name?: string
  environment_key: string
  proxy_type?: string
  proxy_host?: string
  proxy_port?: string
  proxy_exit_ip?: string
  webrtc_disabled: boolean
  fingerprint_randomized: boolean
  fingerprint_slot?: number
  bound_at?: string
  last_verified_at?: string
  last_launched_at?: string
}

export interface OpenAIAdsPowerLaunchRequest {
  account_id?: number
  session_id: string
  auth_url: string
  profile_label?: string
}

export interface OpenAIAdsPowerLaunchResult {
  helper_url: string
  expires_at: string
  delivery?: 'queued' | 'local'
}

export interface OpenAIAdsPowerPairingResult {
  helper_url: string
  environment_key: string
  expires_at: string
}

export const adsPowerAPI = {
  async launch(payload: OpenAIAdsPowerLaunchRequest): Promise<OpenAIAdsPowerLaunchResult> {
    return (await apiClient.post<OpenAIAdsPowerLaunchResult>('/admin/openai/adspower/launch-tickets', payload)).data
  },

  async pairHelper(environmentKey: string): Promise<OpenAIAdsPowerPairingResult> {
    return (await apiClient.post<OpenAIAdsPowerPairingResult>('/admin/openai/adspower/helpers/pairing-tickets', {
      environment_key: environmentKey,
    })).data
  },

  async unbind(accountID: number): Promise<{ account_id: number; unbound: boolean }> {
    return (await apiClient.delete<{ account_id: number; unbound: boolean }>(
      `/admin/openai/accounts/${encodeURIComponent(String(accountID))}/adspower-binding`
    )).data
  },

  async claim(accountID: number, sessionID: string): Promise<OpenAIAdsPowerBinding> {
    return (await apiClient.post<OpenAIAdsPowerBinding>(
      `/admin/openai/accounts/${encodeURIComponent(String(accountID))}/adspower-binding/claim`,
      { session_id: sessionID }
    )).data
  }
}

export default adsPowerAPI
