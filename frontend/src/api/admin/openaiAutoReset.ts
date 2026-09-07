import { apiClient } from '../client'
import type { OpenAIAutoResetConfig } from '@/types'

export async function getOpenAIAutoReset(id: number): Promise<OpenAIAutoResetConfig> {
  const { data } = await apiClient.get<OpenAIAutoResetConfig>(`/admin/openai/accounts/${id}/auto-reset`)
  return data
}

export async function setOpenAIAutoReset(id: number, config: Pick<OpenAIAutoResetConfig, 'enabled' | 'threshold_5h' | 'threshold_7d'>): Promise<OpenAIAutoResetConfig> {
  const { data } = await apiClient.put<OpenAIAutoResetConfig>(`/admin/openai/accounts/${id}/auto-reset`, config)
  return data
}
