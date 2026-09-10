import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

const { getStatus } = vi.hoisted(() => ({ getStatus: vi.fn() }))

vi.mock('@/api/admin/executionNodes', () => ({
  executionNodesAPI: { getStatus }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

let wrapper: VueWrapper | undefined

async function mountNotice(mode: string, allowed: boolean, enabled = true) {
  getStatus.mockResolvedValue({
    admin_write_mode: mode,
    admin_write_allowed: allowed,
    runtime: { enabled: enabled, node_id: 'api2' }
  })
  const { default: Notice } = await import('../ExecutionNodeAdminAccessNotice.vue')
  wrapper = mount(Notice, {
    global: {
      stubs: { Icon: { props: ['name'], template: '<i :data-icon="name" />' } }
    }
  })
  await flushPromises()
  return wrapper
}

describe('ExecutionNodeAdminAccessNotice', () => {
  beforeEach(() => {
    vi.resetModules()
    getStatus.mockReset()
  })

  afterEach(() => {
    wrapper?.unmount()
  })

  it.each(['paired_full_access', 'primary', 'emergency_takeover'])(
    'does not show a takeover or read-only notice for writable mode %s', async mode => {
      const notice = await mountNotice(mode, true)
      expect(notice.find('[data-testid="execution-node-shared-access-notice"]').exists()).toBe(false)
      expect(getStatus).toHaveBeenCalledOnce()
    }
  )

  it.each(['secondary_read_only', 'pairing_unavailable', 'paired_full_access', 'emergency_takeover'])(
    'keeps denied writes read-only for mode %s without takeover styling', async mode => {
      const notice = await mountNotice(mode, false)
      const banner = notice.get('[data-testid="execution-node-shared-access-notice"]')
      expect(banner.text()).toContain('admin.executionNodes.sharedAccess.readOnlyTitle')
      expect(banner.text()).toContain('admin.executionNodes.sharedAccess.readOnlyDescription')
      expect(banner.text()).not.toContain('takeover')
      expect(banner.classes()).toContain('border-sky-200')
      expect(banner.classes()).not.toContain('border-amber-200')
      expect(banner.get('[data-icon="lock"]').exists()).toBe(true)
    }
  )

  it('does not show a multi-node notice when the local runtime is disabled', async () => {
    const notice = await mountNotice('single_node', false, false)
    expect(notice.find('[data-testid="execution-node-shared-access-notice"]').exists()).toBe(false)
  })
})
