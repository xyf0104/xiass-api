import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIOAuthCredentialLibraryPanel from '../OpenAIOAuthCredentialLibraryPanel.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  save: vi.fn(),
  getExecutionNodeStatus: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  accountsAPI: { list: mocks.list },
  executionNodesAPI: { getStatus: mocks.getExecutionNodeStatus },
}))
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
const emailCodeCompleteStatus = {
  has_xiass_openai_oauth_reauth_email: true,
  has_xiass_openai_oauth_reauth_email_code_token_encrypted: true,
}

async function mountDialog() {
  const wrapper = mount(OpenAIOAuthCredentialLibraryPanel, {
    props: { active: true },
    global: {
      stubs: {
        Icon: { template: '<span />' },
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('OpenAIOAuthCredentialLibraryPanel', () => {
  beforeEach(() => {
    mocks.list.mockReset().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 200, pages: 0 })
    mocks.save.mockReset()
    mocks.getExecutionNodeStatus.mockReset().mockResolvedValue({
      admin_write_allowed: true,
      admin_write_mode: 'single_node',
      runtime: {
        enabled: false,
        node_id: 'api',
        legacy_unassigned_node_id: 'api',
      },
    })
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
    expect(wrapper.text()).toContain('当前服务器可管理3')
    expect(wrapper.text()).toContain('密码 + 2FA已保存1')
    expect(wrapper.text()).toContain('密码 + 2FA待补充2')
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

    await wrapper.get('[data-testid="credential-library-password-input"]').setValue([
      'manual@example.test----new-password----JBSWY3DPEHPK3PXP',
      'MANUAL@example.test----ignored-password----JBSWY3DPEHPK3PXP',
      'saved@example.test----saved-password----JBSWY3DPEHPK3PXP',
      'missing@example.test----missing-password----JBSWY3DPEHPK3PXP',
    ].join('\n'))

    expect(wrapper.text()).toContain('已忽略 1 条重复内容')
    expect(wrapper.text()).toContain('可补充或切换 1 个账号')
    expect(wrapper.text()).toContain('密码 + 2FA已保存，跳过')
    expect(wrapper.text()).toContain('未找到现有 OpenAI OAuth 账号')

    await wrapper.get('[data-testid="save-credential-library"]').trigger('click')
    await flushPromises()

    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(mocks.save).toHaveBeenCalledWith(10, {
      email: 'manual@example.test',
      password: 'new-password',
    })
    expect((wrapper.get('[data-testid="credential-library-password-input"]').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.text()).toContain('已保存 1 个，失败 0 个，未匹配 1 条，当前方式已保存跳过 1 个')
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })

  it('matches an imported account name while saving the actual OAuth email', async () => {
    const named = { ...account(20, 'oauth@example.test'), name: '沐4' }
    mocks.list.mockResolvedValue({ items: [named], total: 1, page: 1, page_size: 200, pages: 1 })
    mocks.save.mockResolvedValue({ ...named, credentials_status: completeStatus })
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="credential-library-password-input"]').setValue('沐4----password----JBSWY3DPEHPK3PXP')
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

    await wrapper.get('[data-testid="credential-library-password-input"]').setValue([
      '沐4----first-password----JBSWY3DPEHPK3PXP',
      'oauth@example.test----second-password----JBSWY3DPEHPK3PXP',
    ].join('\n'))

    expect(wrapper.text()).toContain('已忽略 1 条重复内容')
    expect(wrapper.text()).toContain('同一账号已在上方，跳过')
    expect(wrapper.text()).toContain('可补充或切换 1 个账号')

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

  it('only edits accounts owned by the current browser node without paired write access', async () => {
    mocks.getExecutionNodeStatus.mockResolvedValue({
      admin_write_allowed: false,
      admin_write_mode: 'secondary_read_only',
      runtime: {
        enabled: true,
        node_id: 'api2',
        legacy_unassigned_node_id: 'api',
      },
    })
    const primary = { ...account(40, 'primary@example.test'), execution_node_id: 'api' }
    const secondary = { ...account(41, 'secondary@example.test'), execution_node_id: 'api2' }
    mocks.list.mockResolvedValue({ items: [primary, secondary], total: 2, page: 1, page_size: 200, pages: 1 })
    mocks.save.mockResolvedValue({ ...secondary, credentials_status: completeStatus })
    const wrapper = await mountDialog()

    expect(wrapper.text()).toContain('当前服务器可管理1')
    await wrapper.get('[data-testid="credential-library-password-input"]').setValue([
      'primary@example.test----primary-password----JBSWY3DPEHPK3PXP',
      'secondary@example.test----secondary-password----JBSWY3DPEHPK3PXP',
    ].join('\n'))
    await wrapper.get('[data-testid="save-credential-library"]').trigger('click')
    await flushPromises()

    expect(mocks.save).toHaveBeenCalledTimes(1)
    expect(mocks.save).toHaveBeenCalledWith(41, {
      email: 'secondary@example.test',
      password: 'secondary-password',
      totp_secret: 'JBSWY3DPEHPK3PXP',
    })
    wrapper.unmount()
  })

  it('stores email-code accounts in a separate mode and switches away from password credentials', async () => {
    const passwordSaved = account(50, 'mail@example.test', completeStatus)
    mocks.list.mockResolvedValue({ items: [passwordSaved], total: 1, page: 1, page_size: 200, pages: 1 })
    mocks.save.mockResolvedValue({ ...passwordSaved, credentials_status: emailCodeCompleteStatus })
    const wrapper = await mountDialog()
    const token = 'b'.repeat(64)

    await wrapper.get('[data-testid="credential-mode-email-code"]').trigger('click')
    expect(wrapper.find('[data-testid="credential-library-password-input"]').exists()).toBe(false)
    await wrapper.get('[data-testid="credential-library-email-code-input"]').setValue(
      `gpt-0 https://ic.g-c.cc mail@example.test ${token} Plus 美国洛杉矶-3 20260912 20261012 指纹浏览器`
    )

    expect(wrapper.text()).toContain('当前保存：密码 + 2FA；保存后切换为 邮箱验证码')
    await wrapper.get('[data-testid="save-credential-library"]').trigger('click')
    await flushPromises()

    expect(mocks.save).toHaveBeenCalledWith(50, {
      email: 'mail@example.test',
      email_code_token: token,
    })
    expect(wrapper.text()).toContain('邮箱验证码：已保存 1 个')
    wrapper.unmount()
  })

  it('skips an email-code account already saved in the selected mode', async () => {
    const saved = account(51, 'saved-mail@example.test', emailCodeCompleteStatus)
    mocks.list.mockResolvedValue({ items: [saved], total: 1, page: 1, page_size: 200, pages: 1 })
    const wrapper = await mountDialog()

    await wrapper.get('[data-testid="credential-mode-email-code"]').trigger('click')
    await wrapper.get('[data-testid="credential-library-email-code-input"]').setValue(
      `saved-mail@example.test ${'c'.repeat(64)}`
    )

    expect(wrapper.text()).toContain('邮箱验证码已保存，跳过')
    expect(wrapper.get('[data-testid="save-credential-library"]').attributes('disabled')).toBeDefined()
    expect(mocks.save).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
