import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copied: ref(false),
    copyToClipboard: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => {
        if (key === 'admin.accounts.oauth.authMethod') return '授权方式'
        return key
      }
    })
  }
})

import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'

const mountFlow = (authUrl = '') =>
  mount(OAuthAuthorizationFlow, {
    props: {
      addMethod: 'oauth',
      platform: 'openai',
      authUrl,
      initialInputMethod: 'manual',
      showManualOption: true,
      showRefreshTokenOption: true,
      showMobileRefreshTokenOption: true,
      showCodexSessionImportOption: true,
      showAgentIdentityOption: true,
      showCodexPatOption: true,
      showAdsPowerOption: true
    },
    global: {
      stubs: {
        Icon: true,
        PixlabSMSReceiver: true
      }
    }
  })

describe('OAuthAuthorizationFlow mobile layout', () => {
  it('uses a shrinkable single-column method list without horizontal overflow', () => {
    const wrapper = mountFlow()
    const root = wrapper.get('[data-testid="oauth-authorization-flow"]')
    const options = wrapper.get('[data-testid="oauth-method-options"]')

    expect(root.classes()).toEqual(
      expect.arrayContaining(['min-w-0', 'overflow-x-hidden', 'p-3', 'sm:p-4'])
    )
    expect(options.classes()).toEqual(
      expect.arrayContaining(['grid', 'grid-cols-1', 'sm:flex', 'sm:flex-wrap'])
    )
    expect(wrapper.text()).toContain('授权方式')
    for (const option of options.findAll('label')) {
      expect(option.classes()).toContain('min-w-0')
    }
  })

  it('stacks a long authorization URL and copy button on mobile', () => {
    const wrapper = mountFlow(
      'https://auth.example.com/oauth/authorize?this=is-a-deliberately-long-mobile-url'
    )
    const urlRow = wrapper.get('[data-testid="oauth-url-row"]')

    expect(urlRow.classes()).toEqual(
      expect.arrayContaining(['min-w-0', 'flex-col', 'sm:flex-row', 'sm:items-center'])
    )
    expect(urlRow.get('input').classes()).toEqual(
      expect.arrayContaining(['w-full', 'min-w-0', 'sm:flex-1'])
    )
    expect(urlRow.get('button').classes()).toEqual(
      expect.arrayContaining(['self-end', 'sm:self-auto'])
    )
  })

  it('offers the account-bound AdsPower launcher after generating an OpenAI URL', async () => {
    const wrapper = mountFlow('https://auth.openai.com/oauth/authorize?state=test')
    const launch = wrapper.get('[data-testid="launch-adspower-browser"]')

    expect(launch.text()).toContain('用固定指纹浏览器打开')
    expect(launch.classes()).toEqual(expect.arrayContaining(['w-full', 'sm:w-auto']))
    await launch.trigger('click')
    expect(wrapper.emitted('launch-adspower')).toHaveLength(1)
  })
})
