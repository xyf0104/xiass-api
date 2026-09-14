import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import UserAccountAllowlistDialog from '../UserAccountAllowlistDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
    te: () => false,
  }),
}))

const BaseDialogStub = {
  props: ['show'],
  emits: ['close'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const group = { id: 10, name: 'OpenAI' }
const user = { id: 20, email: 'member@example.com', username: 'member', current_concurrency: 1 }
const candidates = [
  { id: 101, name: 'Account A', platform: 'openai', type: 'oauth', priority: 1, concurrency: 3, current_concurrency: 1, allowed: false, available: true },
  { id: 102, name: 'Account B', platform: 'openai', type: 'oauth', priority: 1, concurrency: 2, current_concurrency: 0, allowed: false, available: true },
]

function mountDialog(
  allowedAccountIds: number[],
  restricted: boolean,
  accountCandidates = candidates,
) {
  return mount(UserAccountAllowlistDialog, {
    props: {
      show: true,
      group,
      user,
      candidates: accountCandidates,
      activeAccountIds: [101],
      activeAccountConcurrency: { '101': 1 },
      restricted,
      allowedAccountIds,
      loading: false,
      saving: false,
    },
    global: {
      stubs: { BaseDialog: BaseDialogStub, Icon: true },
    },
  })
}

describe('UserAccountAllowlistDialog', () => {
  it('uses restricted=false, rather than an empty list, to represent original scheduling', () => {
    const wrapper = mountDialog([], false)

    expect((wrapper.get('[data-test="allowlist-account-101"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-test="allowlist-account-102"]').element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.get('[data-test="allowlist-restore-original"]').attributes('disabled')).toBeDefined()
  })

  it('allows a restricted empty selection to be saved as deny all', async () => {
    const wrapper = mountDialog([], true)

    expect((wrapper.get('[data-test="allowlist-account-101"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.get('[data-test="allowlist-save"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-test="allowlist-save"]').trigger('click')

    expect(wrapper.emitted('save')).toEqual([[[]]])
  })

  it('emits selected account IDs in candidate order', async () => {
    const wrapper = mountDialog([102], true)

    expect((wrapper.get('[data-test="allowlist-account-101"]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.get('[data-test="allowlist-account-102"]').element as HTMLInputElement).checked).toBe(true)

    await wrapper.get('[data-test="allowlist-account-101"]').trigger('change')
    await wrapper.get('[data-test="allowlist-account-102"]').trigger('change')
    await wrapper.get('[data-test="allowlist-save"]').trigger('click')

    expect(wrapper.emitted('save')).toEqual([[[101]]])
  })

  it('does not freeze future group accounts when all available accounts remain selected', async () => {
    const wrapper = mountDialog([101, 102], true)

    await wrapper.get('[data-test="allowlist-save"]').trigger('click')

    expect(wrapper.emitted('restore')).toHaveLength(1)
    expect(wrapper.emitted('save')).toBeUndefined()
  })

  it('preserves selected unavailable accounts while blocking new unavailable selections', async () => {
    const unavailableCandidates = [
      candidates[0],
      { ...candidates[1], allowed: true, available: false },
      { id: 103, name: 'Account C', platform: 'openai', type: 'oauth', priority: 1, concurrency: 2, allowed: false, available: false },
    ]
    const wrapper = mountDialog([102], true, unavailableCandidates)

    const selectedUnavailable = wrapper.get('[data-test="allowlist-account-102"]')
    const newUnavailable = wrapper.get('[data-test="allowlist-account-103"]')
    expect((selectedUnavailable.element as HTMLInputElement).checked).toBe(true)
    expect(selectedUnavailable.attributes('disabled')).toBeUndefined()
    expect(newUnavailable.attributes('disabled')).toBeDefined()

    await wrapper.get('[data-test="allowlist-save"]').trigger('click')
    expect(wrapper.emitted('save')).toEqual([[[102]]])

    await selectedUnavailable.trigger('change')
    await wrapper.get('[data-test="allowlist-save"]').trigger('click')
    expect(wrapper.emitted('save')).toEqual([[[102]], [[]]])
  })

  it('emits restore separately so callers can remove the restricted scope', async () => {
    const wrapper = mountDialog([101], true)

    await wrapper.get('[data-test="allowlist-restore-original"]').trigger('click')

    expect(wrapper.emitted('restore')).toHaveLength(1)
    expect(wrapper.emitted('save')).toBeUndefined()
  })

  it('shows occupied slots separately from requests switching accounts or finishing', async () => {
    const wrapper = mountDialog([], false)
    await wrapper.setProps({
      user: { ...user, current_concurrency: 3 },
      activeAccountConcurrency: { '101': 1 },
    })

    expect(wrapper.get('[data-test="allowlist-active-account-101"]').text()).toContain('×1')
    expect(wrapper.get('[data-test="allowlist-waiting-account"]').text()).toContain('换号或处理中')
    expect(wrapper.get('[data-test="allowlist-waiting-account"]').text()).toContain('×2')
  })
})
