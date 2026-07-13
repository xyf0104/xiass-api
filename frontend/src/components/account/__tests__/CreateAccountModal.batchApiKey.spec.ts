import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const {
  authIsSimpleMode,
  createAccountMock,
  showErrorMock,
  showInfoMock,
  showSuccessMock,
  showWarningMock
} = vi.hoisted(() => ({
  authIsSimpleMode: { value: true },
  createAccountMock: vi.fn(),
  showErrorMock: vi.fn(),
  showInfoMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showWarningMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showInfo: showInfoMock,
    showSuccess: showSuccessMock,
    showWarning: showWarningMock
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

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelectorStub',
  props: {
    syncCredentials: {
      type: Object,
      default: undefined
    }
  },
  setup(props) {
    return () => {
      const credentials = props.syncCredentials as Record<string, unknown> | undefined
      return h('div', {
        'data-testid': 'model-sync-credentials',
        'data-api-key': String(credentials?.api_key || ''),
        'data-base-url': String(credentials?.base_url || '')
      })
    }
  }
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
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
        QuotaLimitCard: true,
        OAuthAuthorizationFlow: true
      }
    }
  })
}

type ModalWrapper = ReturnType<typeof mountModal>

async function selectPlatform(wrapper: ModalWrapper, platform: 'OpenAI' | 'Grok') {
  const button = wrapper
    .findAll('button')
    .find((candidate) => candidate.text().trim() === platform)
  expect(button).toBeDefined()
  await button!.trigger('click')
  await nextTick()

  if (platform === 'Grok') {
    await wrapper.get('[data-testid="grok-account-type-api-key"]').trigger('click')
  } else {
    const accountTypeButtons = wrapper.get('[data-tour="account-form-type"]').findAll('button')
    expect(accountTypeButtons).toHaveLength(2)
    await accountTypeButtons[1]!.trigger('click')
  }
  await nextTick()
}

async function enterBatch(wrapper: ModalWrapper, value: string) {
  await wrapper.get('[data-testid="api-key-batch-mode-toggle"]').trigger('click')
  await nextTick()
  await wrapper.get('[data-testid="api-key-batch-input"]').setValue(value)
  await nextTick()
}

async function addHeaderOverride(wrapper: ModalWrapper, name: string, value: string) {
  await wrapper.get('[data-testid="header-override-toggle"]').trigger('click')
  await wrapper.get('[data-testid="header-override-add-row"]').trigger('click')
  await nextTick()
  await wrapper.get('[data-testid="header-override-name-0"]').setValue(name)
  await wrapper.get('[data-testid="header-override-value-0"]').setValue(value)
}

async function submit(wrapper: ModalWrapper) {
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
}

describe('CreateAccountModal batch API keys', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    createAccountMock.mockReset()
    createAccountMock.mockResolvedValue({})
    showErrorMock.mockReset()
    showInfoMock.mockReset()
    showSuccessMock.mockReset()
    showWarningMock.mockReset()
  })

  it('writes validated header overrides to every parsed account', async () => {
    const randomSpy = vi.spyOn(Math, 'random').mockReturnValue(0.99)
    const wrapper = mountModal()
    await selectPlatform(wrapper, 'OpenAI')
    await enterBatch(
      wrapper,
      'Alpha Primary sk-alpha-account-key-123456\nBeta Secondary sk-beta-account-key-654321'
    )

    const syncCredentials = wrapper.get('[data-testid="model-sync-credentials"]')
    expect(syncCredentials.attributes('data-api-key')).toBe('sk-beta-account-key-654321')
    expect(syncCredentials.attributes('data-base-url')).toBe('https://api.openai.com')

    await addHeaderOverride(wrapper, 'X-Batch-Client', 'codex-test')
    await submit(wrapper)

    expect(createAccountMock).toHaveBeenCalledTimes(2)
    const payloads = createAccountMock.mock.calls.map(([payload]) => payload)
    expect(payloads.map((payload) => [payload.name, payload.credentials.api_key])).toEqual([
      ['Alpha Primary', 'sk-alpha-account-key-123456'],
      ['Beta Secondary', 'sk-beta-account-key-654321']
    ])
    for (const payload of payloads) {
      expect(payload.credentials).toMatchObject({
        header_override_enabled: true,
        header_overrides: {
          'x-batch-client': 'codex-test'
        }
      })
    }
    randomSpy.mockRestore()
    wrapper.unmount()
  })

  it('rejects invalid header overrides before creating any account', async () => {
    const wrapper = mountModal()
    await selectPlatform(wrapper, 'OpenAI')
    await enterBatch(wrapper, 'Alpha sk-alpha-account-key-123456\nBeta sk-beta-account-key-654321')
    await addHeaderOverride(wrapper, 'Authorization', 'Bearer blocked')
    await submit(wrapper)

    expect(showErrorMock).toHaveBeenCalledWith('admin.accounts.headerOverride.blockedName')
    expect(createAccountMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('aborts the whole batch when temporary unschedulable rules are invalid', async () => {
    const wrapper = mountModal()
    await selectPlatform(wrapper, 'OpenAI')
    await enterBatch(wrapper, 'Alpha sk-alpha-account-key-123456\nBeta sk-beta-account-key-654321')
    await wrapper.get('[data-testid="temp-unsched-toggle"]').trigger('click')
    await submit(wrapper)

    expect(showErrorMock).toHaveBeenCalledWith(
      'admin.accounts.tempUnschedulable.rulesInvalid'
    )
    expect(createAccountMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps the official xAI base URL for Grok batches', async () => {
    const wrapper = mountModal()
    await selectPlatform(wrapper, 'Grok')
    await enterBatch(wrapper, 'Grok One xai-first-account-key-123456\nGrok Two xai-second-account-key-654321')
    await submit(wrapper)

    expect(createAccountMock).toHaveBeenCalledTimes(2)
    for (const [payload] of createAccountMock.mock.calls) {
      expect(payload).toMatchObject({
        platform: 'grok',
        type: 'apikey',
        credentials: {
          base_url: 'https://api.x.ai/v1'
        }
      })
    }
    wrapper.unmount()
  })
})
