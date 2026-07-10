import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { UserAvailableChannel, UserSupportedModelPricing } from '@/api/channels'
import PricingView from '../PricingView.vue'

const getAvailable = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())

vi.mock('@/api/channels', () => ({
  default: { getAvailable },
  userChannelsAPI: { getAvailable }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: vi.fn(),
    showError
  })
}))

function pricing(
  overrides: Partial<UserSupportedModelPricing> = {}
): UserSupportedModelPricing {
  return {
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    ...overrides
  }
}

function grokChannelFixture(): UserAvailableChannel[] {
  return [{
    name: 'Grok Channel',
    description: '',
    platforms: [{
      platform: 'grok',
      groups: [{
        id: 9,
        name: 'Grok 家宽',
        description: '媒体与对话模型',
        platform: 'grok',
        subscription_type: 'standard',
        rate_multiplier: 3.5,
        peak_rate_enabled: false,
        peak_start: '',
        peak_end: '',
        peak_rate_multiplier: 1,
        is_exclusive: false,
        cost_ratio: null
      }],
      supported_models: [
        {
          name: 'grok-4',
          platform: 'grok',
          pricing: pricing({
            input_price: 0.000001,
            output_price: 0.000002
          })
        },
        {
          name: 'grok-search',
          platform: 'grok',
          pricing: pricing({
            billing_mode: 'per_request',
            per_request_price: 0.5,
            intervals: [{
              min_tokens: 0,
              max_tokens: null,
              tier_label: '深度搜索',
              input_price: null,
              output_price: null,
              cache_write_price: null,
              cache_read_price: null,
              per_request_price: 0.75
            }]
          })
        },
        {
          name: 'grok-imagine-image',
          platform: 'grok',
          pricing: null
        },
        {
          name: 'grok-imagine-video-1.5',
          platform: 'grok',
          pricing: null
        }
      ]
    }]
  }]
}

describe('PricingView', () => {
  it('keeps existing product tabs and renders all Grok billing modes in CNY', async () => {
    getAvailable.mockResolvedValue(grokChannelFixture())

    const wrapper = mount(PricingView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          Icon: { template: '<span />' },
          BrandIcon: { template: '<span />' },
          PlatformIcon: { template: '<span />' }
        }
      }
    })
    await flushPromises()

    for (const platform of ['anthropic', 'openai', 'gemini', 'antigravity', 'grok']) {
      expect(wrapper.find(`[data-test="platform-${platform}"]`).exists()).toBe(true)
    }

    expect(wrapper.text()).toContain('按 Token 计费')
    expect(wrapper.text()).toContain('按次计费')
    expect(wrapper.text()).toContain('按图片计费')
    expect(wrapper.text()).toContain('按视频时长计费')
    expect(wrapper.text()).toContain('深度搜索')

    expect(wrapper.get('[data-test="price-grok-4-input"]').text()).toContain('¥3.50')
    expect(wrapper.get('[data-test="price-grok-search-request"]').text()).toContain('¥1.75')
    expect(wrapper.get('[data-test="price-grok-imagine-image-image-1k"]').text()).toContain('¥0.07')
    expect(wrapper.get('[data-test="price-grok-imagine-video-1.5-video-480p"]').text()).toContain('¥0.28')
    expect(wrapper.get('[data-test="price-grok-imagine-video-1.5-video-720p"]').text()).toContain('¥0.49')
    expect(wrapper.get('[data-test="price-grok-imagine-video-1.5-video-1080p"]').text()).toContain('¥0.875')
    expect(wrapper.text()).toContain('/ 次')
    expect(wrapper.text()).toContain('/ 张')
    expect(wrapper.text()).toContain('/ 秒')
    expect(wrapper.text()).not.toContain('$')

    const officialButton = wrapper.findAll('button').find(button => button.text() === '官方价格')
    expect(officialButton).toBeDefined()
    await officialButton!.trigger('click')
    expect(wrapper.get('[data-test="price-grok-4-input"]').text()).toContain('¥7')
    expect(wrapper.get('[data-test="price-grok-imagine-video-1.5-video-480p"]').text()).toContain('¥0.56')
  })
})
