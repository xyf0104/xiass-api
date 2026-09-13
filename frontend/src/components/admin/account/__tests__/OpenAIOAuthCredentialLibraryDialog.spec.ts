import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIOAuthCredentialLibraryDialog from '../OpenAIOAuthCredentialLibraryDialog.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  save: vi.fn(),
}))

vi.mock('@/api/admin', () => ({ accountsAPI: { list: mocks.list } }))
vi.mock('@/api/admin/teamChild', () => ({ saveOpenAIAccountReauthorizationCredentials: mocks.save }))

function account(id: number, email: string, status: Record<string, boolean> = {}) {
  return {
    id,
    name: email,
    platform: 'openai',
    type: 'oauth',
    status: 'active',
    credentials: { email },
    credentials_status: status,
    extra: {},
    parent_account_id: null,
  }
}

const completeStatus = {
  has_xiass_openai_oauth_reauth_email: true,
  has_xiass_openai_oauth_reauth_password_encrypted: true,
  has_xiass_openai_oauth_reauth_totp_secret_encrypted: true,
}

async function mountDialog() {
  const wrapper = mount(OpenAIOAuthCredentialLibraryDialog, {
    props: { show: true },
    global: {
      stubs: {
        Icon: { template: '<span />' },
        BaseDialog: {
          props: ['show', 'title'],
          emits: ['close'],
          template: '<div v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
        },
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('OpenAIOAuthCredentialLibraryDialog', () => {
  beforeEach(() => {
    mocks.list.mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 200, pages: 0 })
    mocks.save.mockReset()
  })

  it('loads every OpenAI OAuth account page and reports saved coverage', async () => {
    mocks.list.mockImplementation(async (page: number) => ({
      items: page === 1
        ? [account(1, 'one@example.test'), account(2, 'two@example.test', completeStatus)]
        : [account(3, 'three@example.test')],
      total: 3,
      page,
      page_size: 2,
      pages: 2,
    }))

    const wrapper = await mountDialog()

    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('可管理 OpenAI OAuth 3 个')
    expect(wrapper.text()).toContain('已完整保存 1 个')
    expect(wrapper.text()).toContain('待补充 2 个')
    wrapper.unmount()
  })

  it('deduplicates input, skips complete accounts, and writes only missing encrypted fields', async () => {
    const partial = account(10, 'manual@example.test', {
      has_xiass_openai_oauth_reauth_email: true,
      has_xiass_openai_oauth_reauth_password_encrypted: false,
      has_xiass_openai_oauth_reauth_totp_secret_encrypted: true,
    })
    const complete = account(11, 'saved@example.test', completeStatus)
    mocks.list.mockResolvedValue({ items: [partial, complete], total: 2, page: 1, page_size: 200, pages: 1 })
    mocks.save.mockResolvedValue({ ...partial, credentials_status: completeStatus })
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="credential-library-input"]').setValue([
      'manual@example.test----new-password----JBSWY3DPEHPK3PXP',
      'MANUAL@example.test----ignored-password----JBSWY3DPEHPK3PXP',
      'saved@example.test----saved-password----JBSWY3DPEHPK3PXP',
      'missing@example.test----missing-password----JBSWY3DPEHPK3PXP',
    ].join('\n'))

    expect(wrapper.text()).toContain('已忽略 1 条重复内容')
    expect(wrapper.text()).toContain('可补充 1 个账号')
    expect(wrapper.text()).toContain('已完整保存，跳过')
    expect(wrapper.text()).toContain('未找到现有 OpenAI OAuth 账号')

    await wrapper.get('[data-testid="save-credential-library"]').trigger('click')
    await flushPromises()

    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(mocks.save).toHaveBeenCalledWith(10, {
      email: 'manual@example.test',
      password: 'new-password',
    })
    expect((wrapper.get('[data-testid="credential-library-input"]').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.text()).toContain('已保存 1 个，失败 0 个，未匹配 1 条，已完整保存跳过 1 个')
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })

  it('matches an imported account name while saving the actual OAuth email', async () => {
    const named = { ...account(20, 'oauth@example.test'), name: '沐4' }
    mocks.list.mockResolvedValue({ items: [named], total: 1, page: 1, page_size: 200, pages: 1 })
    mocks.save.mockResolvedValue({ ...named, credentials_status: completeStatus })
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="credential-library-input"]').setValue('沐4----password----JBSWY3DPEHPK3PXP')
    await wrapper.get('[data-testid="save-credential-library"]').trigger('click')
    await flushPromises()

    expect(mocks.save).toHaveBeenCalledWith(20, {
      email: 'oauth@example.test',
      password: 'password',
      totp_secret: 'JBSWY3DPEHPK3PXP',
    })
    wrapper.unmount()
  })

  it('deduplicates the same account when both its name and OAuth email are pasted', async () => {
    const named = { ...account(30, 'oauth@example.test'), name: '沐4' }
    mocks.list.mockResolvedValue({ items: [named], total: 1, page: 1, page_size: 200, pages: 1 })
    mocks.save.mockResolvedValue({ ...named, credentials_status: completeStatus })
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="credential-library-input"]').setValue([
      '沐4----first-password----JBSWY3DPEHPK3PXP',
      'oauth@example.test----second-password----JBSWY3DPEHPK3PXP',
    ].join('\n'))

    expect(wrapper.text()).toContain('已忽略 1 条重复内容')
    expect(wrapper.text()).toContain('同一账号已在上方，跳过')
    expect(wrapper.text()).toContain('可补充 1 个账号')

    await wrapper.get('[data-testid="save-credential-library"]').trigger('click')
    await flushPromises()

    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(mocks.save).toHaveBeenCalledWith(30, {
      email: 'oauth@example.test',
      password: 'first-password',
      totp_secret: 'JBSWY3DPEHPK3PXP',
    })
    wrapper.unmount()
  })
})
