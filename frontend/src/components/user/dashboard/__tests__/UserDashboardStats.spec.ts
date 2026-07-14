import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UserDashboardStats from '../UserDashboardStats.vue'

const messages: Record<string, string> = {
  'dashboard.balance': 'Balance',
  'dashboard.apiKeys': 'API Keys',
  'dashboard.todayRequests': 'Today Requests',
  'dashboard.todayCost': 'Today Cost',
  'dashboard.todayTokens': 'Today Tokens',
  'dashboard.totalTokens': 'Total Tokens',
  'dashboard.input': 'Input',
  'dashboard.output': 'Output',
  'dashboard.cacheHitRate': 'Cache hit rate',
  'dashboard.performance': 'Performance',
  'dashboard.avgResponse': 'Avg Response',
  'dashboard.averageTime': 'average time',
  'common.available': 'available',
  'common.active': 'active',
  'common.total': 'Total',
  'dashboard.actual': 'Actual',
  'dashboard.standard': 'Standard',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const stats = {
  total_api_keys: 1,
  active_api_keys: 1,
  total_requests: 10,
  total_input_tokens: 200,
  total_output_tokens: 50,
  total_cache_creation_tokens: 10,
  total_cache_read_tokens: 90,
  total_tokens: 350,
  total_cost: 1,
  total_actual_cost: 1,
  today_requests: 2,
  today_input_tokens: 100,
  today_output_tokens: 20,
  today_cache_creation_tokens: 20,
  today_cache_read_tokens: 80,
  today_tokens: 220,
  today_cost: 0.5,
  today_actual_cost: 0.5,
  average_duration_ms: 120,
  rpm: 1,
  tpm: 2,
  by_platform: [],
}

describe('UserDashboardStats', () => {
  it('shows cache hit rates for today and total prompt tokens', () => {
    const wrapper = mount(UserDashboardStats, {
      props: {
        stats,
        balance: 0,
        isSimple: false,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('Cache hit rate:')
    expect(text).toContain('40.0%')
    expect(text).toContain('30.0%')
  })
})
