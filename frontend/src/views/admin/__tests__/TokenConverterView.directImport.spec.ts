import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TokenConverterView from '../TokenConverterView.vue'

const { authState, showErrorMock } = vi.hoisted(() => ({
  authState: { isAuthenticated: true, isAdmin: true },
  showErrorMock: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => authState }))
vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: showErrorMock, showSuccess: vi.fn() }),
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() }),
}))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: vi.fn() }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
}))
vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { list: vi.fn(), exportData: vi.fn() } },
}))
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'zh' },
      t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

const passthrough = { template: '<div><slot /></div>' }
const directImportDialogStub = {
  props: ['show', 'payload', 'accountCount'],
  template: '<div v-if="show" data-testid="direct-import-dialog-stub">{{ accountCount }}</div>',
}

function mountView() {
  return mount(TokenConverterView, {
    global: {
      stubs: {
        AppLayout: passthrough,
        PublicToolLayout: passthrough,
        AdminAccountPicker: true,
        OpenAIReauthorizationPanel: true,
        DirectAccountImportDialog: directImportDialogStub,
        TotpStepUpDialog: true,
        Icon: true,
      },
    },
  })
}

async function enterConvertibleAccount(wrapper: ReturnType<typeof mountView>): Promise<void> {
  const input = wrapper.get('[data-test="token-converter-input"]')
  await input.setValue(JSON.stringify({
    access_token: 'access-token',
    refresh_token: 'refresh-token',
    email: 'account@example.com',
  }))
  await vi.advanceTimersByTimeAsync(200)
  await flushPromises()
}

describe('TokenConverterView direct import', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    authState.isAuthenticated = true
    authState.isAdmin = true
  })

  afterEach(() => vi.useRealTimers())

  it('offers direct import only after an administrator has a valid XIASS result', async () => {
    const wrapper = mountView()
    expect(wrapper.get('[data-test="open-direct-import"]').attributes('disabled')).toBeDefined()

    await enterConvertibleAccount(wrapper)
    const importButton = wrapper.get('[data-test="open-direct-import"]')
    expect(importButton.attributes('disabled')).toBeUndefined()

    await importButton.trigger('click')
    expect(wrapper.get('[data-testid="direct-import-dialog-stub"]').text()).toBe('1')
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  it('never renders direct import for a regular signed-in user', async () => {
    authState.isAdmin = false
    const wrapper = mountView()
    await enterConvertibleAccount(wrapper)

    expect(wrapper.find('[data-test="open-direct-import"]').exists()).toBe(false)
  })
})
