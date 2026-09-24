import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { UserAvailableChannel, UserSupportedModelPricing } from '@/api/channels'
import PricingView from '../PricingView.vue'

const getPricing = vi.hoisted(() => vi.fn())
const getUserGroupRates = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())

vi.mock('@/api/channels', () => ({
  default: { getPricing },
  userChannelsAPI: { getPricing }
}))

vi.mock('@/api/groups', () => ({
  default: { getUserGroupRates },
  userGroupsAPI: { getUserGroupRates }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: vi.fn(),
    showError,
    cachedPublicSettings: { server_utc_offset: '+08:00' }
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
            output_price: 0.000002,
            image_output_price: 0
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
  beforeEach(() => {
    getPricing.mockReset()
    getUserGroupRates.mockReset().mockResolvedValue({})
  })

  it('keeps the compact token columns and renders all Grok price types in CNY', async () => {
    getPricing.mockResolvedValue(grokChannelFixture())

    const wrapper = mount(PricingView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          Icon: { template: '<span />' },
          PlatformBrandIcon: {
            props: ['platform'],
            template: '<span class="platform-brand-icon-stub" :data-platform="platform" />'
          }
        }
      }
    })
    await flushPromises()

    for (const platform of ['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek']) {
      const tab = wrapper.find(`[data-test="platform-${platform}"]`)
      expect(tab.exists()).toBe(true)
      expect(tab.find('.platform-brand-icon-stub').attributes('data-platform')).toBe(platform)
    }

    expect(wrapper.text()).toContain('输入价格')
    expect(wrapper.text()).toContain('输出价格')
    expect(wrapper.text()).toContain('缓存创建')
    expect(wrapper.text()).toContain('缓存读取')
    expect(wrapper.text()).not.toContain('计费方式')
    expect(wrapper.text()).not.toContain('按 Token 计费')
    expect(wrapper.text()).not.toContain('按次计费')
    expect(wrapper.text()).not.toContain('按图片计费')
    expect(wrapper.text()).not.toContain('按视频时长计费')
    expect(wrapper.text()).toContain('深度搜索')

    expect(wrapper.get('[data-test="price-grok-4-input"]').text()).toContain('¥3.50')
    expect(wrapper.find('[data-test="price-grok-4-image-output"]').exists()).toBe(false)
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

  it('applies the user-specific rate and active peak factor to text prices only', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-07-10T07:00:00Z'))
    const channels = grokChannelFixture()
    const group = channels[0]!.platforms[0]!.groups[0]!
    group.subscription_type = 'subscription'
    group.peak_rate_enabled = true
    group.peak_start = '14:00'
    group.peak_end = '18:00'
    group.peak_rate_multiplier = 2
    getPricing.mockResolvedValue(channels)
    getUserGroupRates.mockResolvedValue({ 9: 2 })

    const wrapper = mount(PricingView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          Icon: { template: '<span />' },
          PlatformBrandIcon: { template: '<span />' }
        }
      }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('高峰中')
    expect(wrapper.text()).toContain('4x 倍率')
    expect(wrapper.get('[data-test="price-grok-4-input"]').text()).toContain('¥4')
    expect(wrapper.get('[data-test="price-grok-imagine-image-image-1k"]').text()).toContain('¥0.04')

    wrapper.unmount()
    vi.useRealTimers()
  })

  it('matches base exact before final wildcard and does not multiply final fields', async () => {
	vi.useFakeTimers({ toFake: ['Date'] })
	vi.setSystemTime(new Date('2026-07-10T07:00:00Z'))
	const channels = grokChannelFixture()
	const section = channels[0]!.platforms[0]!
	const group = section.groups[0]!
	group.subscription_type = 'subscription'
	group.peak_rate_enabled = true
	group.peak_start = '14:00'
	group.peak_end = '18:00'
	group.peak_rate_multiplier = 2
	group.model_pricing = [
	  {
		models: ['grok-4'],
		billing_mode: 'token',
		price_mode: 'base',
		input_price: null,
		output_price: null,
		cache_write_price: null,
		cache_read_price: null
	  },
	  {
		models: ['grok-*'],
		billing_mode: 'token',
		price_mode: 'final',
		input_price: 0.000009,
		output_price: null,
		cache_write_price: 0,
		cache_read_price: 0.000003
	  }
	]
	section.supported_models.push({
	  name: 'grok-4-fast',
	  platform: 'grok',
	  pricing: pricing({ input_price: 0.000001, output_price: 0.000002 })
	})
	getPricing.mockResolvedValue(channels)
	getUserGroupRates.mockResolvedValue({ 9: 2 })

	const wrapper = mount(PricingView, {
	  global: {
		stubs: {
		  AppLayout: { template: '<main><slot /></main>' },
		  Icon: { template: '<span />' },
		  PlatformBrandIcon: { template: '<span />' }
		}
	  }
	})
	await flushPromises()

	// Final-price implementation details remain private; the group card keeps
	// the same public multiplier and discount presentation as other groups.
	expect(wrapper.text()).not.toContain('逐模型最终价')
	expect(wrapper.text()).not.toContain('已配置逐模型最终价')
	expect(wrapper.text()).toContain('4x 倍率')
	// Backend exact-match precedence keeps grok-4 on base pricing: 1e-6 * 4.
	expect(wrapper.get('[data-test="price-grok-4-input"]').text()).toContain('¥4')
	// The wildcard applies to grok-4-fast. Explicit final input is direct RMB,
	// while nil output inherits the base price and active 4x multiplier.
	expect(wrapper.get('[data-test="price-grok-4-fast-input"]').text()).toContain('¥9')
	expect(wrapper.get('[data-test="price-grok-4-fast-output"]').text()).toContain('¥8')
	expect(wrapper.get('[data-test="price-grok-4-fast-cache-write"]').text()).toContain('¥0')

	wrapper.unmount()
	vi.useRealTimers()
  })
})
