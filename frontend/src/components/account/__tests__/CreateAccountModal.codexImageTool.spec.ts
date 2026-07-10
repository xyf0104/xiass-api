import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { createAccountMock, authIsSimpleMode } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  authIsSimpleMode: { value: true }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    }
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false })
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue({})
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: {
      type: [String, Number, Boolean, null],
      default: ''
    },
    options: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <select
      v-bind="$attrs"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option v-for="option in options" :key="option.value" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `
})

function mountModal() {
  return mount(CreateAccountModal, {
    props: {
      show: true,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        Select: SelectStub,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: true,
        ModelWhitelistSelector: true,
        QuotaLimitCard: true,
        OAuthAuthorizationFlow: true
      }
    }
  })
}

async function selectOpenAIAPIKey(wrapper: ReturnType<typeof mountModal>) {
  const openAIButton = wrapper.findAll('button').find((button) => button.text().trim() === 'OpenAI')
  expect(openAIButton).toBeDefined()
  await openAIButton!.trigger('click')
  await nextTick()

  const accountTypeButtons = wrapper.get('[data-tour="account-form-type"]').findAll('button')
  expect(accountTypeButtons).toHaveLength(2)
  await accountTypeButtons[1].trigger('click')
  await nextTick()

  await wrapper.get('[data-tour="account-form-name"]').setValue('OpenAI Key')
  await wrapper.get('input[type="password"]').setValue('sk-test')
}

async function submitAndReadExtra(wrapper: ReturnType<typeof mountModal>) {
  createAccountMock.mockClear()
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()

  expect(createAccountMock).toHaveBeenCalledTimes(1)
  return createAccountMock.mock.calls[0]?.[0]?.extra as Record<string, unknown>
}

describe('CreateAccountModal Codex image tool policy', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    createAccountMock.mockReset()
    createAccountMock.mockResolvedValue({})
  })

  it('submits the same four policy modes supported by the edit form', async () => {
    const wrapper = mountModal()
    await selectOpenAIAPIKey(wrapper)

    const inheritedExtra = await submitAndReadExtra(wrapper)
    expect(inheritedExtra).not.toHaveProperty('codex_image_generation_bridge')
    expect(inheritedExtra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
    expect(inheritedExtra).not.toHaveProperty('codex_image_generation_bridge_enabled')

    await wrapper.get('[data-testid="codex-image-tool-enabled"]').trigger('click')
    const enabledExtra = await submitAndReadExtra(wrapper)
    expect(enabledExtra.codex_image_generation_bridge).toBe(true)
    expect(enabledExtra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')

    await wrapper.get('[data-testid="codex-image-tool-disabled"]').trigger('click')
    const disabledExtra = await submitAndReadExtra(wrapper)
    expect(disabledExtra.codex_image_generation_bridge).toBe(false)
    expect(disabledExtra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')

    await wrapper.get('[data-testid="codex-image-tool-block"]').trigger('click')
    const blockedExtra = await submitAndReadExtra(wrapper)
    expect(blockedExtra.codex_image_generation_explicit_tool_policy).toBe('strip')
    expect(blockedExtra).not.toHaveProperty('codex_image_generation_bridge')

    await wrapper.get('[data-testid="codex-image-tool-inherit"]').trigger('click')
    const resetExtra = await submitAndReadExtra(wrapper)
    expect(resetExtra).not.toHaveProperty('codex_image_generation_bridge')
    expect(resetExtra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })
})
