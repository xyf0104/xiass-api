import { apiClient } from '../client'

export interface ExecutionNodeAccountStats {
  total: number
  active: number
  schedulable: number
}

export interface ExecutionNodeAdminIssue {
  code: string
  severity: 'error' | 'warning' | string
  message: string
}

export interface ExecutionNodeRuntimeStatus {
  enabled: boolean
  node_id: string
  default_proxy_id: number
  emergency_local_egress: boolean
  control_plane: boolean
  legacy_unassigned_node_id: string
  legacy_unassigned_proxy_id: number
}

export interface ExecutionNodeAdminNode {
  node_id: string
  weight: number
  proxy_id: number
  proxy_name?: string
  proxy_status?: string
  proxy_valid: boolean
  online: boolean
  is_local: boolean
  account_stats: ExecutionNodeAccountStats
}

export interface ExecutionNodeFailoverStatus {
  enabled: boolean
  ready: boolean
  witness_reachable: boolean
  database_fence_ready: boolean
  local_authority: boolean
  holder_node_id?: string
  generation: number
  expires_at?: string
  last_error?: string
}

export interface ExecutionNodeAdminStatus {
  balancing_enabled: boolean
  can_enable: boolean
  admin_write_allowed: boolean
  admin_write_mode: string
  database_reachable: boolean
  heartbeat_store_reachable: boolean
  failover: ExecutionNodeFailoverStatus
  runtime: ExecutionNodeRuntimeStatus
  nodes: ExecutionNodeAdminNode[]
  issues: ExecutionNodeAdminIssue[]
}

export interface ExecutionNodePairingPeer {
  node_id: string
  version?: string
  protocol_version: number
  database_fingerprint: string
  redis_fingerprint: string
  auth_fingerprint: string
  state_fingerprint: string
  paired_at: string
  peer_url?: string
  ready: boolean
}

export interface ExecutionNodePairingStatus {
  protocol_version: number
  local_node_id: string
  paired: boolean
  production_ready: boolean
  protocol_compatible: boolean
  database_shared: boolean
  redis_shared: boolean
  auth_compatible: boolean
  invite_active: boolean
  invite_expires_at?: string
  state_fingerprint?: string
  state_error?: string
  peer?: ExecutionNodePairingPeer
}

export interface ExecutionNodePairingInvite {
  token: string
  expires_at: string
}

export interface ExecutionNodeRuntimeConfig {
  node_id: string
  default_proxy_id: number
  legacy_unassigned_node_id: string
  legacy_unassigned_proxy_id: number
}

export async function getStatus(): Promise<ExecutionNodeAdminStatus> {
  const { data } = await apiClient.get<ExecutionNodeAdminStatus>('/admin/settings/execution-nodes/status')
  const runtime = data.runtime
  const localNodeID = runtime?.node_id?.trim() ?? ''
  const primaryNodeID = runtime?.legacy_unassigned_node_id?.trim() ?? ''
  const legacyResponseAllowed = !runtime?.enabled || (localNodeID !== '' && localNodeID === primaryNodeID)
  return {
    ...data,
    // During a rolling update an older backend does not return the permission
    // fields yet. Only a confirmed single/primary node may stay writable; an
    // unknown secondary fails closed until its backend has been upgraded.
    admin_write_allowed: typeof data.admin_write_allowed === 'boolean' ? data.admin_write_allowed : legacyResponseAllowed,
    admin_write_mode: data.admin_write_mode ?? (legacyResponseAllowed ? (runtime?.enabled ? 'primary' : 'single_node') : 'secondary_read_only'),
    failover: data.failover ?? {
      enabled: false,
      ready: false,
      witness_reachable: false,
      database_fence_ready: false,
      local_authority: false,
      generation: 0
    },
    nodes: data.nodes ?? [],
    issues: data.issues ?? []
  }
}

export async function getPairingStatus(): Promise<ExecutionNodePairingStatus> {
  const { data } = await apiClient.get<ExecutionNodePairingStatus>('/admin/settings/execution-nodes/pairing')
  return data
}

export async function initializeRuntime(nodeID: string): Promise<ExecutionNodeRuntimeConfig> {
  const { data } = await apiClient.post<ExecutionNodeRuntimeConfig>('/admin/settings/execution-nodes/runtime/initialize', { node_id: nodeID })
  return data
}

export async function updateOfflineTakeover(enabled: boolean): Promise<void> {
  await apiClient.post('/admin/settings/execution-nodes/runtime/offline-takeover', { enabled })
}

export async function generatePairingInvite(): Promise<ExecutionNodePairingInvite> {
  const { data } = await apiClient.post<ExecutionNodePairingInvite>('/admin/settings/execution-nodes/pairing/invite')
  return data
}

export async function pairExecutionNode(peerURL: string, token: string, targetNodeID: string, targetURL: string): Promise<ExecutionNodePairingStatus> {
  const { data } = await apiClient.post<ExecutionNodePairingStatus>('/admin/settings/execution-nodes/pairing/join', {
    peer_url: peerURL,
    token,
    target_node_id: targetNodeID,
    target_url: targetURL
  })
  return data
}

export async function unpairExecutionNode(): Promise<void> {
  await apiClient.post('/admin/settings/execution-nodes/pairing/unpair')
}

export const executionNodesAPI = { getStatus, getPairingStatus, initializeRuntime, updateOfflineTakeover, generatePairingInvite, pairExecutionNode, unpairExecutionNode }

export default executionNodesAPI
