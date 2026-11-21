import { apiClient } from './client'

export interface WeekendDiscountSettings {
  enabled: boolean
  mode?: 'weekends' | 'custom'
  starts_at: string | null
  ends_at: string | null
}

export interface WeekendDiscountStatus extends WeekendDiscountSettings {
  timezone: string
  discount_multiplier: number
  applied_multiplier: number
  active: boolean
  server_time: string
  next_change_at: string | null
}

export async function getWeekendDiscount(admin = false): Promise<WeekendDiscountStatus> {
  const { data } = await apiClient.get<WeekendDiscountStatus>(admin
    ? '/admin/settings/weekend-discount' : '/channels/weekend-discount', { headers: { 'Cache-Control': 'no-cache' } })
  return data
}

export async function saveWeekendDiscount(settings: WeekendDiscountSettings): Promise<WeekendDiscountStatus> {
  const { data } = await apiClient.put<WeekendDiscountStatus>('/admin/settings/weekend-discount', settings)
  return data
}
