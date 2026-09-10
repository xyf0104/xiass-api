import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/api/client', () => ({
  apiClient: { get }
}))

import { executionNodesAPI, getStatus } from '@/api/admin/executionNodes'

const legacyStatus = (nodeID: string, primaryNodeID: string, enabled = true) => ({
  balancing_enabled: enabled,
  can_enable: true,
  database_reachable: true,
  heartbeat_store_reachable: true,
  runtime: {
    enabled,
    node_id: nodeID,
    default_proxy_id: 84,
    control_plane: nodeID === primaryNodeID,
    legacy_unassigned_node_id: primaryNodeID,
    legacy_unassigned_proxy_id: 84
  },
  nodes: [],
  issues: []
})

describe('execution nodes API rolling-update compatibility', () => {
  beforeEach(() => {
    get.mockReset()
  })

  it('keeps a confirmed primary node writable when an older backend omits admin-write fields', async () => {
    get.mockResolvedValue({ data: legacyStatus('api', 'api') })

    await expect(getStatus()).resolves.toMatchObject({
      admin_write_allowed: true,
      admin_write_mode: 'primary'
    })
  })

  it('fails closed on a secondary node when an older backend omits admin-write fields', async () => {
    get.mockResolvedValue({ data: legacyStatus('api2', 'api') })

    await expect(getStatus()).resolves.toMatchObject({
      admin_write_allowed: false,
      admin_write_mode: 'secondary_read_only'
    })
  })

  it('keeps an explicitly reported backend permission instead of inferring a legacy fallback', async () => {
    get.mockResolvedValue({
      data: {
        ...legacyStatus('api2', 'api'),
        admin_write_allowed: true,
        admin_write_mode: 'paired_full_access'
      }
    })

    await expect(getStatus()).resolves.toMatchObject({
      admin_write_allowed: true,
      admin_write_mode: 'paired_full_access'
    })
  })

  it('does not synthesize disaster recovery status or expose its mutation endpoint', async () => {
    get.mockResolvedValue({ data: legacyStatus('api', 'api') })

    const status = await getStatus()
    expect(status).not.toHaveProperty('failover')
    expect(status.runtime).not.toHaveProperty('emergency_local_egress')
    expect(executionNodesAPI).not.toHaveProperty('updateOfflineTakeover')
    expect(get).toHaveBeenCalledWith('/admin/settings/execution-nodes/status')
  })
})
