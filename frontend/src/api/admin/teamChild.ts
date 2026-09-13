import { apiClient } from '../client'
import type { Account } from '@/types'

export interface TeamChildMailboxStatus {
  configured: boolean
  browser_configured: boolean
}

export interface TeamChildBrowserSession {
  embed_url: string
  expires_at: string
  ticket_expires_at?: string
  controller_id: string
  control_expires_at: string
}

export interface TeamChildBrowserSessionRequest {
  controller_id?: string
  /** Explicitly replace the current graphical-browser controller. */
  take_over?: boolean
}

export interface TeamChildBrowserControl {
  controller_id: string
  control_expires_at?: string
  released?: boolean
}

export interface TeamChildMailbox {
  session_id: string
  email: string
  expires_at: string
}

export interface TeamChildMailboxList {
  emails: string[]
}

export interface TeamChildLoginSecret {
  email: string
  password: string
}

export interface OpenAIAccountReauthorizationCredentialsRequest {
  email: string
  /** Omitted preserves the saved password; empty explicitly clears it. */
  password?: string
  /** Omitted preserves the saved authenticator, empty explicitly clears it. */
  totp_secret?: string
}

export interface TeamChildMailboxCode {
  status: 'waiting' | 'received'
  code?: string
  expires_at: string
}

export interface TeamChildMailboxShareStatus {
  active: boolean
  email: string
  created_at?: string
  /** Returned only to an authenticated administrator, from an encrypted server-side copy. */
  token?: string
}

export interface TeamChildMailboxShareLink extends TeamChildMailboxShareStatus {
  /** Returned when a link is created, replaced, or safely restored for an administrator. */
  token: string
}

export interface TeamChildMailboxConfigImportResult {
  configured: boolean
  auth_mode: string
  domain: string
  restart_required: boolean
}

export interface TeamChildCreateAccountRequest {
  session_id: string
  code: string
  state: string
  redirect_uri?: string
  proxy_id?: number
  name: string
  concurrency: number
  priority: number
  group_ids: number[]
  /** Preserve the exact group selection, including an intentionally empty one. */
  skip_default_group_bind: true
  /** New workflow imports must be immediately eligible for scheduler selection. */
  schedulable: true
  /** Marks the imported account for the Team-child 401 reminder. */
  team_child?: boolean
  /** Binds the optional Team-child login secret to this exact protocol-4 import. */
  workflow_id: string
}

export interface TeamChildMember {
  /** Stable identity returned by the automation service: normalized email. */
  id: string
  email: string
  name?: string
  role: string
  seat_type?: string
  added_at?: string
  status?: string
  /** Owner/admin rows and instance-configured protected identities are read-only. */
  protected?: boolean
}

export interface TeamChildMembersResult {
  ready: boolean
  url?: string
  members: TeamChildMember[]
  pending_invites?: number
  workspace_name?: string
  message?: string
  seat_email?: string
  operation?: {
    type: 'inspect' | 'invite' | 'update' | 'remove'
    email?: string
    role?: string
    confirmed: boolean
  }
}

export type TeamChildWorkflowStatus = 'running' | 'manual_required' | 'callback_ready' | 'completed' | 'failed' | 'paused' | 'cancelled'

export type TeamChildWorkflowNodeStatus = 'pending' | 'running' | 'waiting' | 'completed' | 'failed' | 'cancelled'

export type TeamChildWorkflowNodeKey =
  | 'members' | 'remove' | 'invite' | 'invite_confirm' | 'oauth' | 'signup' | 'email' | 'password'
  | 'mail' | 'mailbox' | 'email_code' | 'phone' | 'sms_confirm' | 'phone_submit'
  | 'sms_poll' | 'sms_code' | 'profile_wait' | 'profile' | 'workspace_wait'
  | 'workspace' | 'callback' | 'import'

export interface TeamChildWorkflowNode {
  key: TeamChildWorkflowNodeKey
  number: number
  label: string
  status: TeamChildWorkflowNodeStatus
  message?: string
}

export interface TeamChildWorkflow {
  schema_version: 4
  id: string
  status: TeamChildWorkflowStatus
  mode?: 'registration' | 'reauthorization'
  target_account_id?: number
  expires_at: string
  manual_required: boolean
  pause_requested?: boolean
  oauth_session_id?: string
  oauth_state?: string
  current_node?: TeamChildWorkflowNodeKey | ''
  callback_url?: string
  error?: string
  email_code_generation?: number
  email_code_purpose?: 'registration' | 'oauth_login' | 'reauthorization' | ''
  password_available?: boolean
  nodes: TeamChildWorkflowNode[]
}

export interface ActiveTeamChildWorkflowResult {
  schema_version: 4
  active: boolean
  workflow?: TeamChildWorkflow
}

export interface StartTeamChildWorkflowRequest {
  /** Empty only when the ordinary member seat was already removed manually. */
  seat_email?: string
  invite_email: string
  auth_url: string
  oauth_session_id: string
  seat_already_removed: boolean
  /** Must be set only after the XIASS in-page destructive-action dialog. */
  confirmed: true
}

const currentWorkflowNodeOrder: TeamChildWorkflowNodeKey[] = [
  'signup', 'email', 'mail', 'mailbox', 'email_code', 'members', 'remove', 'invite', 'invite_confirm',
  'oauth', 'password', 'phone', 'sms_confirm', 'phone_submit',
  'sms_poll', 'sms_code', 'profile_wait', 'profile', 'workspace_wait',
  'workspace', 'callback', 'import'
]

function requireCurrentTeamChildWorkflow(workflow: TeamChildWorkflow): TeamChildWorkflow {
  const nodes = Array.isArray(workflow?.nodes) ? workflow.nodes : []
  const current = workflow?.schema_version === 4
    && nodes.length === currentWorkflowNodeOrder.length
    && nodes.every((node, index) => node?.key === currentWorkflowNodeOrder[index])
  if (!current) {
    throw new Error('Team 自动化运行组件版本不匹配，请完成运行组件更新后重试')
  }
  return workflow
}

export async function getMailboxStatus(): Promise<TeamChildMailboxStatus> {
  const { data } = await apiClient.get<TeamChildMailboxStatus>('/admin/openai/team-child/mailbox-status')
  return data
}

export async function createMailbox(): Promise<TeamChildMailbox> {
  const { data } = await apiClient.post<TeamChildMailbox>('/admin/openai/team-child/mailboxes')
  return data
}

export async function listMailboxes(): Promise<string[]> {
  const { data } = await apiClient.get<TeamChildMailboxList>('/admin/openai/team-child/mailboxes')
  return Array.isArray(data.emails) ? data.emails : []
}

export async function selectMailbox(email: string): Promise<TeamChildMailbox> {
  const { data } = await apiClient.post<TeamChildMailbox>('/admin/openai/team-child/mailboxes/select', { email })
  return data
}

export async function getActiveMailbox(): Promise<TeamChildMailbox | null> {
  const { data } = await apiClient.get<{ active: boolean; mailbox?: TeamChildMailbox }>('/admin/openai/team-child/mailboxes/active')
  return data.active && data.mailbox ? data.mailbox : null
}

export async function importMailboxConfig(file: File): Promise<TeamChildMailboxConfigImportResult> {
  const formData = new FormData()
  formData.append('file', file, file.name)
  const { data } = await apiClient.post<TeamChildMailboxConfigImportResult>(
    '/admin/openai/team-child/mailbox-config',
    formData,
    { headers: { 'Content-Type': 'multipart/form-data' } }
  )
  return data
}

export async function createBrowserSession(payload: TeamChildBrowserSessionRequest = {}): Promise<TeamChildBrowserSession> {
  const { data } = await apiClient.post<TeamChildBrowserSession>(
    '/admin/openai/team-child/browser-sessions',
    payload
  )
  return data
}

export async function heartbeatTeamChildBrowserControl(controllerID: string): Promise<TeamChildBrowserControl> {
  const { data } = await apiClient.post<TeamChildBrowserControl>('/admin/openai/team-child/browser-control/heartbeat', {
    controller_id: controllerID
  })
  return data
}

export async function releaseTeamChildBrowserControl(controllerID: string): Promise<TeamChildBrowserControl> {
  const { data } = await apiClient.delete<TeamChildBrowserControl>('/admin/openai/team-child/browser-control', {
    data: { controller_id: controllerID }
  })
  return data
}

export async function listTeamChildMembers(): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.get<TeamChildMembersResult>('/admin/openai/team-child/members')
  return data
}

export async function refreshTeamChildMembers(): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.post<TeamChildMembersResult>('/admin/openai/team-child/members/refresh')
  return data
}

export async function inspectTeamChildSeat(): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.post<TeamChildMembersResult>('/admin/openai/team-child/members/inspect')
  return data
}

export async function inviteTeamChildMember(email: string): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.post<TeamChildMembersResult>('/admin/openai/team-child/members/invite', { email })
  return data
}

export async function updateTeamChildMember(email: string, role: string): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.patch<TeamChildMembersResult>('/admin/openai/team-child/members', { email, role })
  return data
}

export async function removeTeamChildMember(email: string): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.delete<TeamChildMembersResult>('/admin/openai/team-child/members', { data: { email } })
  return data
}

export async function startTeamChildWorkflow(payload: StartTeamChildWorkflowRequest): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>('/admin/openai/team-child/workflows', payload)
  return requireCurrentTeamChildWorkflow(data)
}

export async function getTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.get<TeamChildWorkflow>(`/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}`)
  return requireCurrentTeamChildWorkflow(data)
}

export async function getActiveTeamChildWorkflow(): Promise<TeamChildWorkflow | null> {
  const { data } = await apiClient.get<ActiveTeamChildWorkflowResult>('/admin/openai/team-child/workflows/active')
  if (data.schema_version !== 4) {
    throw new Error('Team 自动化运行组件版本不匹配，请完成运行组件更新后重试')
  }
  return data.active && data.workflow ? requireCurrentTeamChildWorkflow(data.workflow) : null
}

export async function continueTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(`/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/continue`)
  return requireCurrentTeamChildWorkflow(data)
}

export async function pauseTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(`/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/pause`)
  return requireCurrentTeamChildWorkflow(data)
}

export async function submitTeamChildWorkflowCallback(workflowID: string, callbackURL: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/callback`,
    { callback_url: callbackURL }
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function restartTeamChildWorkflowOAuth(workflowID: string, authURL: string, oauthSessionID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/restart-oauth`,
    { auth_url: authURL, oauth_session_id: oauthSessionID }
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function reauthorizeTeamChildAccount(accountID: number, authURL: string, oauthSessionID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/accounts/${encodeURIComponent(String(accountID))}/reauthorize`,
    { auth_url: authURL, oauth_session_id: oauthSessionID }
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function saveOpenAIAccountReauthorizationCredentials(
  accountID: number,
  payload: OpenAIAccountReauthorizationCredentialsRequest
): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/openai/accounts/${encodeURIComponent(String(accountID))}/reauthorization-credentials`,
    payload
  )
  return data
}

export async function reauthorizeOpenAIAccount(accountID: number, authURL: string, oauthSessionID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/accounts/${encodeURIComponent(String(accountID))}/reauthorize`,
    { auth_url: authURL, oauth_session_id: oauthSessionID }
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function submitTeamChildWorkflowEmailCode(workflowID: string, code: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/email-code`,
    { code }
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function submitTeamChildWorkflowPhone(workflowID: string, phone: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/phone`,
    { phone }
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function submitTeamChildWorkflowSMSCode(workflowID: string, code: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/sms-code`,
    { code }
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function completeTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/complete`
  )
  return requireCurrentTeamChildWorkflow(data)
}

export async function revealTeamChildWorkflowPassword(workflowID: string): Promise<TeamChildLoginSecret> {
  const { data } = await apiClient.get<TeamChildLoginSecret>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/password`
  )
  return data
}

export async function revealTeamChildAccountPassword(accountID: number): Promise<TeamChildLoginSecret> {
  const { data } = await apiClient.get<TeamChildLoginSecret>(
    `/admin/openai/team-child/accounts/${encodeURIComponent(String(accountID))}/password`
  )
  return data
}

export async function cancelTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.delete<TeamChildWorkflow>(`/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}`)
  return requireCurrentTeamChildWorkflow(data)
}

export async function pollMailboxCode(sessionId: string): Promise<TeamChildMailboxCode> {
  const { data } = await apiClient.get<TeamChildMailboxCode>(
    `/admin/openai/team-child/mailboxes/${encodeURIComponent(sessionId)}/code`
  )
  return data
}

export async function deleteMailboxSession(sessionId: string): Promise<void> {
  await apiClient.delete(`/admin/openai/team-child/mailboxes/${encodeURIComponent(sessionId)}`)
}

export async function getTeamChildMailboxShare(accountID: number): Promise<TeamChildMailboxShareStatus> {
  const { data } = await apiClient.get<TeamChildMailboxShareStatus>(
    `/admin/openai/team-child/accounts/${encodeURIComponent(String(accountID))}/mailbox-share`
  )
  return data
}

export async function createTeamChildMailboxShare(accountID: number, replace = false): Promise<TeamChildMailboxShareLink> {
  const { data } = await apiClient.post<TeamChildMailboxShareLink>(
    `/admin/openai/team-child/accounts/${encodeURIComponent(String(accountID))}/mailbox-share`,
    { replace }
  )
  return data
}

export async function revokeTeamChildMailboxShare(accountID: number): Promise<void> {
  await apiClient.delete(`/admin/openai/team-child/accounts/${encodeURIComponent(String(accountID))}/mailbox-share`)
}

export async function getPendingTeamChildMailboxShare(email: string, accountID?: number): Promise<TeamChildMailboxShareStatus> {
  const { data } = await apiClient.get<TeamChildMailboxShareStatus>('/admin/openai/team-child/mailbox-share', {
    params: {
      email,
      ...(accountID ? { account_id: accountID } : {})
    }
  })
  return data
}

export async function createPendingTeamChildMailboxShare(email: string, replace = false, accountID?: number): Promise<TeamChildMailboxShareLink> {
  const { data } = await apiClient.post<TeamChildMailboxShareLink>('/admin/openai/team-child/mailbox-share', {
    email,
    replace,
    ...(accountID ? { account_id: accountID } : {})
  })
  return data
}

export async function revokePendingTeamChildMailboxShare(email: string, accountID?: number): Promise<void> {
  await apiClient.delete('/admin/openai/team-child/mailbox-share', {
    data: {
      email,
      ...(accountID ? { account_id: accountID } : {})
    }
  })
}

export async function createOpenAIAccountFromOAuth(payload: TeamChildCreateAccountRequest): Promise<Account> {
  const { data } = await apiClient.post<Account>('/admin/openai/create-from-oauth', payload)
  return data
}

export const teamChildAPI = {
  getMailboxStatus,
  createBrowserSession,
  heartbeatTeamChildBrowserControl,
  releaseTeamChildBrowserControl,
  getTeamChildMailboxShare,
  createTeamChildMailboxShare,
  revokeTeamChildMailboxShare,
  getPendingTeamChildMailboxShare,
  createPendingTeamChildMailboxShare,
  revokePendingTeamChildMailboxShare,
  listTeamChildMembers,
  refreshTeamChildMembers,
  inspectTeamChildSeat,
  inviteTeamChildMember,
  updateTeamChildMember,
  removeTeamChildMember,
  startTeamChildWorkflow,
  getTeamChildWorkflow,
  getActiveTeamChildWorkflow,
  continueTeamChildWorkflow,
  pauseTeamChildWorkflow,
  submitTeamChildWorkflowCallback,
  restartTeamChildWorkflowOAuth,
  reauthorizeTeamChildAccount,
  saveOpenAIAccountReauthorizationCredentials,
  reauthorizeOpenAIAccount,
  submitTeamChildWorkflowEmailCode,
  submitTeamChildWorkflowPhone,
  submitTeamChildWorkflowSMSCode,
  completeTeamChildWorkflow,
  cancelTeamChildWorkflow,
  createMailbox,
  listMailboxes,
  selectMailbox,
  getActiveMailbox,
  importMailboxConfig,
  pollMailboxCode,
  deleteMailboxSession,
  revealTeamChildWorkflowPassword,
  revealTeamChildAccountPassword,
  createOpenAIAccountFromOAuth
}

export default teamChildAPI
