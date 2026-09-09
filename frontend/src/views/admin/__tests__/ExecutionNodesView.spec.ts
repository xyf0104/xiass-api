import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import ExecutionNodesView from '../ExecutionNodesView.vue'
import type { ExecutionNodeAdminStatus, ExecutionNodePairingStatus } from '@/api/admin/executionNodes'

const { getStatus, getPairingStatus, initializeRuntime, updateOfflineTakeover, generatePairingInvite, pairExecutionNode, unpairExecutionNode, getAllWithCount, updateSettings, showError, showSuccess } = vi.hoisted(() => ({
  getStatus: vi.fn(),
  getPairingStatus: vi.fn(),
  initializeRuntime: vi.fn(),
  updateOfflineTakeover: vi.fn(),
  generatePairingInvite: vi.fn(),
  pairExecutionNode: vi.fn(),
  unpairExecutionNode: vi.fn(),
  getAllWithCount: vi.fn(),
  updateSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    executionNodes: { getStatus, getPairingStatus, initializeRuntime, updateOfflineTakeover, generatePairingInvite, pairExecutionNode, unpairExecutionNode },
    proxies: { getAllWithCount },
    settings: { updateSettings }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: (_error: unknown, fallback: string) => fallback,
  extractI18nErrorMessage: (_error: unknown, _t: unknown, _prefix: string, fallback: string) => fallback
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const ToggleStub = {
  inheritAttrs: false,
  props: {
    modelValue: { type: Boolean, required: true },
    disabled: { type: Boolean, default: false }
  },
  emits: ['update:modelValue'],
  template: '<button type="button" v-bind="$attrs" :disabled="disabled" @click="$emit(\'update:modelValue\', !modelValue)" />'
}

const ConfirmDialogStub = {
  props: { show: { type: Boolean, default: false } },
  emits: ['confirm', 'cancel'],
  template: '<button v-if="show" type="button" data-testid="execution-node-confirm" @click="$emit(\'confirm\')" />'
}

function statusFixture(overrides: Partial<ExecutionNodeAdminStatus> = {}): ExecutionNodeAdminStatus {
  return {
    balancing_enabled: false,
    can_enable: true,
    admin_write_allowed: true,
    admin_write_mode: 'primary',
    database_reachable: true,
    heartbeat_store_reachable: true,
    runtime: {
      enabled: true,
      node_id: 'api',
      default_proxy_id: 84,
      emergency_local_egress: true,
      control_plane: true,
      legacy_unassigned_node_id: 'api',
      legacy_unassigned_proxy_id: 84
    },
    nodes: [
      {
        node_id: 'api',
        weight: 3,
        proxy_id: 84,
        proxy_name: 'US egress',
        proxy_status: 'active',
        proxy_valid: true,
        online: true,
        is_local: true,
        account_stats: { total: 5, active: 4, schedulable: 3 }
      },
      {
        node_id: 'api2',
        weight: 1,
        proxy_id: 83,
        proxy_name: 'JP egress',
        proxy_status: 'active',
        proxy_valid: true,
        online: true,
        is_local: false,
        account_stats: { total: 2, active: 2, schedulable: 1 }
      }
    ],
    issues: [],
    ...overrides
  }
}

function pairingFixture(overrides: Partial<ExecutionNodePairingStatus> = {}): ExecutionNodePairingStatus {
  return {
    protocol_version: 1,
    local_node_id: 'api',
    paired: true,
    production_ready: true,
    protocol_compatible: true,
    database_shared: true,
    redis_shared: true,
    auth_compatible: true,
    invite_active: false,
    ...overrides
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve
    reject = onReject
  })
  return { promise, resolve, reject }
}

const mountedViews: VueWrapper[] = []

function mountView() {
  const wrapper = mount(ExecutionNodesView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        Toggle: ToggleStub,
        ConfirmDialog: ConfirmDialogStub,
        Icon: true
      }
    }
  })
  mountedViews.push(wrapper)
  return wrapper
}

describe('ExecutionNodesView', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.useFakeTimers()
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    getStatus.mockResolvedValue(statusFixture())
    getPairingStatus.mockResolvedValue({
      protocol_version: 1,
      local_node_id: 'api',
      paired: false,
      production_ready: false,
      protocol_compatible: false,
      database_shared: false,
      redis_shared: false,
      auth_compatible: false,
      invite_active: false
    })
    generatePairingInvite.mockResolvedValue({ token: 'a'.repeat(64), expires_at: '2026-09-04T12:10:00Z' })
    initializeRuntime.mockResolvedValue({ node_id: 'api', tunnel_token: 'b'.repeat(64), default_proxy_id: 84, legacy_unassigned_node_id: 'api', legacy_unassigned_proxy_id: 84 })
    updateOfflineTakeover.mockResolvedValue(undefined)
    pairExecutionNode.mockResolvedValue({
      protocol_version: 1,
      local_node_id: 'api',
      paired: true,
      production_ready: true,
      protocol_compatible: true,
      database_shared: true,
      redis_shared: true,
      auth_compatible: true,
      invite_active: false,
      peer: { node_id: 'api2', protocol_version: 1, database_fingerprint: 'db', redis_fingerprint: 'redis', auth_fingerprint: 'auth', state_fingerprint: 'state', paired_at: '2026-09-04T12:00:00Z' }
    })
    unpairExecutionNode.mockResolvedValue(undefined)
    getAllWithCount.mockResolvedValue([
      { id: 84, name: 'US egress' },
      { id: 83, name: 'JP egress' }
    ])
    updateSettings.mockResolvedValue({})
  })

  afterEach(() => {
    mountedViews.splice(0).forEach((wrapper) => wrapper.unmount())
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('loads node health, account counts, and current weights', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="execution-node-label-0"]').text()).toContain('admin.executionNodes.localNode')
    expect(wrapper.get('[data-testid="execution-node-label-1"]').text()).toContain('admin.executionNodes.otherNode')
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').element).toHaveProperty('value', '3')
    expect(wrapper.text()).toContain('5')
    expect(wrapper.find('[data-testid="execution-node-proxy-1"]').exists()).toBe(false)
  })

  it('renders disabled multi-node runtime as a neutral single-node state', async () => {
    getStatus.mockResolvedValue(statusFixture({
      can_enable: false,
      runtime: {
        ...statusFixture().runtime,
        enabled: false,
        node_id: '',
        default_proxy_id: 0,
        legacy_unassigned_proxy_id: 0
      },
      issues: [{ code: 'MULTI_NODE_DISABLED', severity: 'info', message: 'single node is healthy' }]
    }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('single node is healthy')
    expect(wrapper.text()).not.toContain('MULTI_NODE_DISABLED')
    expect(wrapper.text()).not.toContain('LOCAL_NODE_ID_INVALID')
    expect(wrapper.text()).not.toContain('NODE_PROXY_UNAVAILABLE')
  })

  it('renders older server responses with null node collections', async () => {
    getStatus.mockResolvedValue({
      ...statusFixture({
        can_enable: false,
        runtime: { ...statusFixture().runtime, enabled: false, node_id: '', default_proxy_id: 0, legacy_unassigned_proxy_id: 0 }
      }),
      nodes: null,
      issues: null
    } as unknown as ExecutionNodeAdminStatus)
    const wrapper = mountView()
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="execution-node-label-0"]').text()).toContain('admin.executionNodes.localNode')
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').element).toHaveProperty('value', '9')
    expect(wrapper.get('[data-testid="execution-node-generate-invite"]').attributes('disabled')).toBeDefined()
  })

  it('requires confirmation before enabling and saves the full policy', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-balancing-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="execution-node-confirm"]').exists()).toBe(true)
    expect(updateSettings).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="execution-node-confirm"]').trigger('click')
    await wrapper.get('[data-testid="execution-node-save"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith({
      execution_node_balancing_enabled: true,
      execution_node_weights: { api: 3, api2: 1 },
      execution_node_proxy_ids: { api: 84, api2: 83 }
    })
  })

  it('offers local runtime initialization before balancing is enabled', async () => {
    getStatus.mockResolvedValue(statusFixture({
      can_enable: false,
      runtime: { ...statusFixture().runtime, enabled: false, node_id: '', default_proxy_id: 0, legacy_unassigned_proxy_id: 0 },
      nodes: [],
      issues: [{ code: 'MULTI_NODE_DISABLED', severity: 'info', message: 'single node is healthy' }]
    }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-initialize"]').trigger('click')
    await flushPromises()

    expect(initializeRuntime).toHaveBeenCalledWith('node1')
    expect(showSuccess).toHaveBeenCalled()
    expect(wrapper.get('[data-testid="execution-node-generate-invite"]').attributes('disabled')).toBeDefined()
  })

  it('blocks enabling when the server preflight has errors', async () => {
    getStatus.mockResolvedValue(statusFixture({
      can_enable: false,
      issues: [{ code: 'LOCAL_RUNTIME_DISABLED', severity: 'error', message: 'runtime disabled' }]
    }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-balancing-toggle"]').trigger('click')

    expect(wrapper.find('[data-testid="execution-node-confirm"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="execution-node-balancing-toggle"]').attributes('disabled')).toBeDefined()
    expect(showError).not.toHaveBeenCalled()
  })

  it('generates a one-time invite and pairs through the peer URL', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-generate-invite"]').trigger('click')
    await flushPromises()
    expect(generatePairingInvite).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="execution-node-invite-token"]').element).toHaveProperty('value', 'a'.repeat(64))

    await wrapper.get('[data-testid="execution-node-peer-url"]').setValue('https://api2.example.com')
    await wrapper.get('[data-testid="execution-node-peer-token"]').setValue('b'.repeat(64))
    getPairingStatus.mockResolvedValue(pairingFixture())
    await wrapper.get('[data-testid="execution-node-pair"]').trigger('click')
    await flushPromises()

    expect(pairExecutionNode).toHaveBeenCalledWith('https://api2.example.com', 'b'.repeat(64), 'api', window.location.origin)
    expect(wrapper.text()).toContain('admin.executionNodes.pairingReady')
  })

  it('auto-detects local pairing details and changes offline takeover without a restart', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="execution-node-target-id"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="execution-node-target-url"]').exists()).toBe(false)

    await wrapper.get('[data-testid="execution-node-takeover-toggle"]').trigger('click')
    await flushPromises()

    expect(updateOfflineTakeover).toHaveBeenCalledWith(false)
    expect(showSuccess).toHaveBeenCalled()
  })

  it('hides internal identity and egress while still permitting weight changes', async () => {
    getStatus.mockResolvedValue(statusFixture({ balancing_enabled: true }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="execution-node-id-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="execution-node-proxy-0"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="execution-node-label-0"]').text()).toContain('admin.executionNodes.localNode')
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-testid="execution-node-weight-0"]').setValue('4')
    await wrapper.get('[data-testid="execution-node-save"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith(expect.objectContaining({
      execution_node_balancing_enabled: true,
      execution_node_weights: { api: 4, api2: 1 }
    }))
  })

  it.each([true, false])('shows paired full access independently of peer online=%s', async (online) => {
    getStatus.mockResolvedValue(statusFixture({
      admin_write_allowed: true,
      admin_write_mode: 'paired_full_access',
      runtime: { ...statusFixture().runtime, node_id: 'api2', emergency_local_egress: false },
      nodes: statusFixture().nodes.map((node) => ({ ...node, online }))
    }))
    const wrapper = mountView()
    await flushPromises()

    const notice = wrapper.get('[data-testid="execution-node-admin-access"]').text()
    expect(notice).toContain('admin.executionNodes.adminWritePaired')
    expect(notice).not.toContain('admin.executionNodes.adminWriteTakeover')
    expect(notice).not.toContain('admin.executionNodes.adminWriteSecondary')
  })

  it('shows pairing verification failure instead of legacy secondary or takeover permission', async () => {
    getStatus.mockResolvedValue(statusFixture({
      admin_write_allowed: false,
      admin_write_mode: 'pairing_unavailable',
      runtime: { ...statusFixture().runtime, emergency_local_egress: true }
    }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-node-admin-access"]').text()).toContain('admin.executionNodes.adminWritePairingUnavailable')
  })

  it('explains that the secondary machine is read-only while keeping shared weights editable', async () => {
    getStatus.mockResolvedValue(statusFixture({
      balancing_enabled: true,
      admin_write_allowed: false,
      admin_write_mode: 'secondary_read_only',
      runtime: { ...statusFixture().runtime, node_id: 'api2', legacy_unassigned_node_id: 'api' },
      nodes: statusFixture().nodes.map((node) => ({ ...node, is_local: node.node_id === 'api2' }))
    }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="execution-node-admin-access"]').text()).toContain('admin.executionNodes.adminWriteSecondary')
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').attributes('disabled')).toBeUndefined()
  })

  it('refreshes both healthy snapshots every ten seconds without overwriting a dirty draft', async () => {
    getPairingStatus.mockResolvedValue(pairingFixture())
    getStatus
      .mockResolvedValueOnce(statusFixture({
        balancing_enabled: true,
        nodes: statusFixture().nodes.map((node) => (
          node.node_id === 'api' ? { ...node, weight: 3 } : node
        ))
      }))
      .mockResolvedValue(statusFixture({
        balancing_enabled: true,
        nodes: statusFixture().nodes.map((node) => (
          node.node_id === 'api' ? { ...node, weight: 8 } : node
        ))
      }))

    const wrapper = mountView()
    await flushPromises()
    const weightInput = wrapper.get('[data-testid="execution-node-weight-0"]')
    expect(weightInput.element).toHaveProperty('value', '3')

    await vi.advanceTimersByTimeAsync(9_999)
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(getPairingStatus).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').element).toHaveProperty('value', '8')

    await wrapper.get('[data-testid="execution-node-weight-0"]').setValue('7')
    getStatus.mockResolvedValue(statusFixture({
      balancing_enabled: true,
      nodes: statusFixture().nodes.map((node) => (
        node.node_id === 'api' ? { ...node, weight: 6 } : node
      ))
    }))
    await vi.advanceTimersByTimeAsync(10_000)
    await flushPromises()

    expect(getStatus).toHaveBeenCalledTimes(3)
    expect(getPairingStatus).toHaveBeenCalledTimes(3)
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').element).toHaveProperty('value', '7')
    wrapper.unmount()
  })

  it.each([
    ['database unavailable', { database_reachable: false }],
    ['heartbeat unavailable', { heartbeat_store_reachable: false }],
    ['status error', { issues: [{ code: 'SHARED_WEIGHTS_INVALID', severity: 'error', message: 'invalid weights' }] }],
    ['status warning', { issues: [{ code: 'ACCOUNT_STATS_UNAVAILABLE', severity: 'warning', message: 'stats unavailable' }] }],
    ['offline active node', { nodes: statusFixture().nodes.map((node) => ({ ...node, online: false })) }],
    ['invalid active egress', { nodes: statusFixture().nodes.map((node) => ({ ...node, proxy_valid: false })) }],
    ['routing enabled without a runtime', { balancing_enabled: true, runtime: { ...statusFixture().runtime, enabled: false } }]
  ] satisfies [string, Partial<ExecutionNodeAdminStatus>][])('polls every three seconds for %s despite successful HTTP responses', async (_name, overrides) => {
    getStatus.mockResolvedValue(statusFixture(overrides))
    getPairingStatus.mockResolvedValue(pairingFixture())
    mountView()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(2_999)
    expect(getStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
  })

  it.each([
    ['pair not ready', { production_ready: false }],
    ['protocol mismatch', { protocol_compatible: false }],
    ['database mismatch', { database_shared: false }],
    ['Redis mismatch', { redis_shared: false }],
    ['auth mismatch', { auth_compatible: false }],
    ['pair state error', { state_error: 'shared state unavailable' }]
  ] satisfies [string, Partial<ExecutionNodePairingStatus>][])('polls every three seconds for %s even when node status is healthy', async (_name, overrides) => {
    getPairingStatus.mockResolvedValue(pairingFixture(overrides))
    mountView()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(3_000)
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
  })

  it.each([false, true])('keeps an expected unpaired setup on ten seconds with runtime enabled=%s', async (enabled) => {
    getStatus.mockResolvedValue(statusFixture({
      can_enable: false,
      runtime: { ...statusFixture().runtime, enabled },
      heartbeat_store_reachable: enabled,
      nodes: [],
      issues: enabled
        ? [{ code: 'PAIRING_NOT_READY', severity: 'error', message: 'awaiting setup' }]
        : [{ code: 'MULTI_NODE_DISABLED', severity: 'info', message: 'single node' }]
    }))
    mountView()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(9_999)
    expect(getStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
  })

  it('treats missing pairing as a fault when routing is enabled', async () => {
    getStatus.mockResolvedValue(statusFixture({ balancing_enabled: true }))
    mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(3_000)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
  })

  it.each(['status', 'pairing'])('retries a failed %s request after three seconds and returns to ten seconds after recovery', async (endpoint) => {
    getPairingStatus.mockResolvedValue(pairingFixture())
    const request = endpoint === 'status' ? getStatus : getPairingStatus
    request.mockRejectedValueOnce(new Error('offline'))
    mountView()
    await flushPromises()
    expect(showError).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(3_000)
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(9_999)
    expect(getStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(getStatus).toHaveBeenCalledTimes(3)
    expect(getPairingStatus).toHaveBeenCalledTimes(3)
  })

  it('refreshes pairing readiness automatically and uses the latest health to schedule the next poll', async () => {
    getPairingStatus.mockResolvedValueOnce(pairingFixture({ production_ready: false })).mockResolvedValue(pairingFixture())
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-node-pairing"]').text()).toContain('admin.executionNodes.pairingIncomplete')

    await vi.advanceTimersByTimeAsync(3_000)
    expect(wrapper.get('[data-testid="execution-node-pairing"]').text()).toContain('admin.executionNodes.pairingReady')
    await vi.advanceTimersByTimeAsync(9_999)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(getPairingStatus).toHaveBeenCalledTimes(3)
  })

  it('keeps the last pairing snapshot without repeated toasts when background reads fail', async () => {
    getPairingStatus.mockResolvedValueOnce(pairingFixture()).mockRejectedValue(new Error('offline'))
    const wrapper = mountView()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(13_000)
    expect(getStatus).toHaveBeenCalledTimes(3)
    expect(getPairingStatus).toHaveBeenCalledTimes(3)
    expect(showError).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="execution-node-pairing"]').text()).toContain('admin.executionNodes.pairingReady')

    await wrapper.get('[data-testid="execution-node-refresh"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledTimes(1)
  })

  it.each(['status', 'pairing'])('waits for the slow %s request before unlocking manual refresh or scheduling another poll', async (endpoint) => {
    const pending = deferred<ExecutionNodeAdminStatus | ExecutionNodePairingStatus>()
    const request = endpoint === 'status' ? getStatus : getPairingStatus
    request.mockReturnValueOnce(pending.promise)
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(getPairingStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="execution-node-refresh"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="execution-node-refresh"]').trigger('click')
    expect(getPairingStatus).toHaveBeenCalledTimes(1)
    pending.resolve(endpoint === 'status' ? statusFixture() : pairingFixture())
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-node-refresh"]').attributes('disabled')).toBeUndefined()
    await vi.advanceTimersByTimeAsync(9_999)
    expect(getStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
  })

  it('resets the polling deadline after manual refresh without retaining the old timeout', async () => {
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(9_000)
    await wrapper.get('[data-testid="execution-node-refresh"]').trigger('click')
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(9_999)
    expect(getStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(getStatus).toHaveBeenCalledTimes(3)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('pauses while hidden and immediately refreshes both snapshots when visible', async () => {
    mountView()
    await flushPromises()
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(getStatus).toHaveBeenCalledTimes(1)

    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('does not start the initial requests when mounted in a hidden page', async () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    mountView()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(getStatus).not.toHaveBeenCalled()
    expect(getPairingStatus).not.toHaveBeenCalled()

    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(getPairingStatus).toHaveBeenCalledTimes(1)
  })

  it('discards a hidden in-flight response and coalesces repeated resume events without overlap', async () => {
    const pending = deferred<ExecutionNodeAdminStatus>()
    getStatus.mockReturnValueOnce(pending.promise)
    const wrapper = mountView()
    await flushPromises()
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(1)

    pending.resolve(statusFixture({ nodes: statusFixture().nodes.map((node) => ({ ...node, weight: 99 })) }))
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').element).toHaveProperty('value', '3')
    expect(vi.getTimerCount()).toBe(1)
  })

  it('discards a pre-save poll, waits for it, and reads the saved policy without overlapping requests', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="execution-node-weight-0"]').setValue('7')
    const pending = deferred<ExecutionNodeAdminStatus>()
    getStatus.mockReturnValueOnce(pending.promise)
    await vi.advanceTimersByTimeAsync(10_000)
    await wrapper.get('[data-testid="execution-node-save"]').trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledTimes(1)
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)

    getStatus.mockResolvedValue(statusFixture({ nodes: statusFixture().nodes.map((node) => ({ ...node, weight: 7 })) }))
    pending.resolve(statusFixture({ nodes: statusFixture().nodes.map((node) => ({ ...node, weight: 99 })) }))
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(3)
    expect(getPairingStatus).toHaveBeenCalledTimes(3)
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').element).toHaveProperty('value', '7')
    expect(vi.getTimerCount()).toBe(1)
  })

  it('pauses polling during an offline takeover update and resumes afterward', async () => {
    const pending = deferred<void>()
    updateOfflineTakeover.mockReturnValueOnce(pending.promise)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="execution-node-takeover-toggle"]').trigger('click')
    await vi.advanceTimersByTimeAsync(30_000)
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="execution-node-refresh"]').attributes('disabled')).toBeDefined()

    pending.resolve()
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(getPairingStatus).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('cleans up its timer and visibility listener on unmount', async () => {
    const removeListener = vi.spyOn(document, 'removeEventListener')
    const wrapper = mountView()
    await flushPromises()
    expect(vi.getTimerCount()).toBe(1)
    wrapper.unmount()

    expect(vi.getTimerCount()).toBe(0)
    expect(removeListener).toHaveBeenCalledWith('visibilitychange', expect.any(Function))
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(getStatus).toHaveBeenCalledTimes(1)
  })

  it('ignores late failures after unmount without scheduling more work or showing errors', async () => {
    const pending = deferred<ExecutionNodePairingStatus>()
    getPairingStatus.mockReturnValueOnce(pending.promise)
    const wrapper = mountView()
    await flushPromises()
    wrapper.unmount()
    pending.reject(new Error('late failure'))
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    expect(getStatus).toHaveBeenCalledTimes(1)
  })
})
