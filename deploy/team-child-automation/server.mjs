import crypto from 'node:crypto'
import fs from 'node:fs'
import http from 'node:http'
import path from 'node:path'

import { chromium } from '@playwright/test'
import { generateTOTP, normalizeTOTPSecret, isAuthenticatorChallenge } from './totp.mjs'
import { BatchOAuthRunner } from './batch-oauth.mjs'
import { createProxyContext } from './browser-proxy.mjs'

const port = Number(process.env.PORT || 8090)
const cdpURL = process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222'
const membersURL = process.env.MEMBERS_URL || 'https://chatgpt.com/admin/members'
const operationTimeout = Number(process.env.OPERATION_TIMEOUT_MS || 30000)
const confirmationTimeout = Number(process.env.CONFIRMATION_TIMEOUT_MS || 12000)
const memberRenderTimeout = boundedDuration(process.env.MEMBER_RENDER_TIMEOUT_MS, 15000, 5000, 60000)
const pendingInviteRenderTimeout = boundedDuration(process.env.PENDING_INVITE_RENDER_TIMEOUT_MS, 15000, 3000, 60000)
const requestBodyLimit = Number(process.env.REQUEST_BODY_LIMIT_BYTES || 32768)
const serviceToken = String(process.env.TEAM_CHILD_AUTOMATION_TOKEN || '').trim()
const browserConnectTimeout = boundedDuration(process.env.BROWSER_CONNECT_TIMEOUT_MS, 30000, 1000, 120000)
const browserConnectRetryDelay = boundedDuration(process.env.BROWSER_CONNECT_RETRY_DELAY_MS, 750, 100, 5000)
const workflowTTL = boundedDuration(process.env.WORKFLOW_TTL_MS, 45 * 60 * 1000, 5 * 60 * 1000, 2 * 60 * 60 * 1000)
const workflowStateFile = process.env.NODE_ENV === 'test'
  ? ''
  : String(process.env.WORKFLOW_STATE_FILE || '/app/data/workflows.enc').trim()
const oauthPageTimeout = boundedDuration(process.env.OAUTH_PAGE_TIMEOUT_MS, 45000, 10000, 120000)
// The Members SPA exposes its shell before the native invitation controls are
// fully hydrated. Keep each transition visible long enough for the UI to
// settle, and never turn a slow render into a rapid refresh loop.
const memberPageMinimumDwellMs = 1500
const memberPageRefreshWindowMs = 5000
const memberPageMaxRefreshesPerWindow = 2
const memberRefreshAttempts = memberPageMaxRefreshesPerWindow
// Version 4 registers the mailbox identity in an isolated browser context
// before touching Team membership, then reuses that exact private session for
// OAuth. Older runners must not restore or execute the reordered workflow.
const workflowProtocolVersion = 4
const chatGPTHomeURL = 'https://chatgpt.com/'
const officialOpenAIClientID = 'app_EMoamEEZ73f0CkXaXp7hrann'
const officialOpenAIRedirectURI = 'http://localhost:1455/auth/callback'
const officialOpenAIScope = 'openid profile email offline_access'
const protectedMemberEmails = new Set(
  String(process.env.TEAM_CHILD_PROTECTED_MEMBER_EMAILS || '')
    .split(/[\s,;]+/)
    .map((value) => normalizeEmail(value))
    .filter(Boolean)
)

let browserPromise
let operation = Promise.resolve()
let activeWorkflowID = ''
const workflows = new Map()
// Member administration and OpenAI OAuth have separate, persistent tabs. The
// member tab keeps the ChatGPT administrator session and must never be
// navigated to auth.openai.com; the OAuth tab may move through login,
// verification, consent, and localhost callback pages independently.
let managedMembersPage
let managedOAuthPage
let managedPrivateContext
let managedPrivatePage
let managedPrivateWorkflowID = ''
let memberPageRefreshes = []
const memberPageRenderStartedAt = new WeakMap()

function workflowStateEncryptionKey() {
  if (!serviceToken) return undefined
  return crypto.createHash('sha256').update(`xiass-team-child-workflow:${serviceToken}`).digest()
}

function encryptWorkflowState(payload) {
  const key = workflowStateEncryptionKey()
  if (!key) throw new Error('workflow state encryption is unavailable')
  const iv = crypto.randomBytes(12)
  const cipher = crypto.createCipheriv('aes-256-gcm', key, iv)
  const ciphertext = Buffer.concat([cipher.update(payload, 'utf8'), cipher.final()])
  return JSON.stringify({
    version: 1,
    iv: iv.toString('base64url'),
    tag: cipher.getAuthTag().toString('base64url'),
    ciphertext: ciphertext.toString('base64url')
  })
}

function decryptWorkflowState(payload) {
  const key = workflowStateEncryptionKey()
  if (!key) throw new Error('workflow state encryption is unavailable')
  const envelope = JSON.parse(payload)
  if (envelope?.version !== 1) throw new Error('workflow state version is unsupported')
  const decipher = crypto.createDecipheriv('aes-256-gcm', key, Buffer.from(envelope.iv, 'base64url'))
  decipher.setAuthTag(Buffer.from(envelope.tag, 'base64url'))
  return Buffer.concat([
    decipher.update(Buffer.from(envelope.ciphertext, 'base64url')),
    decipher.final()
  ]).toString('utf8')
}

function persistWorkflowState() {
  if (!workflowStateFile || !serviceToken) return
  try {
    const directory = path.dirname(workflowStateFile)
    fs.mkdirSync(directory, { recursive: true, mode: 0o700 })
    const temporary = `${workflowStateFile}.${process.pid}.tmp`
    const encoded = encryptWorkflowState(JSON.stringify({
      schema_version: workflowProtocolVersion,
      active_workflow_id: activeWorkflowID,
      workflows: Array.from(workflows.values())
    }))
    fs.writeFileSync(temporary, encoded, { encoding: 'utf8', mode: 0o600 })
    fs.renameSync(temporary, workflowStateFile)
  } catch {
    console.error('team-child workflow state could not be persisted')
  }
}

function restoreWorkflowState() {
  if (!workflowStateFile || !serviceToken || !fs.existsSync(workflowStateFile)) return
  try {
    const decoded = JSON.parse(decryptWorkflowState(fs.readFileSync(workflowStateFile, 'utf8')))
    if (decoded?.schema_version !== workflowProtocolVersion || !Array.isArray(decoded.workflows)) return
    const now = Date.now()
    for (const candidate of decoded.workflows) {
      if (!candidate || typeof candidate.id !== 'string' || candidate.expiresAt <= now) continue
      if (
        !Array.isArray(candidate.nodes)
        || candidate.nodes.length !== workflowNodeDefinitions.length
        || candidate.nodes.some((node, index) => node?.key !== workflowNodeDefinitions[index][0])
      ) continue
      const workflow = candidate
      const inviteNode = workflow.nodes.find((node) => node.key === 'invite')
      const inviteConfirmationNode = workflow.nodes.find((node) => node.key === 'invite_confirm')
      if (typeof workflow.inviteSubmitted !== 'boolean') {
        workflow.inviteSubmitted = Boolean(
          workflow.inviteConfirmed
          || inviteNode?.status === 'completed'
          || (inviteConfirmationNode && inviteConfirmationNode.status !== 'pending')
          || /邀请操作已提交|待处理邀请中未出现|pending invites/i.test(String(inviteNode?.message || ''))
        )
      }
      workflow.inviteSubmittedAt = Number(workflow.inviteSubmittedAt || 0)
      workflow.emailCodeGeneration = Math.max(0, Number(workflow.emailCodeGeneration || 0))
      workflow.emailCodePurpose = ['registration', 'oauth_login', 'reauthorization'].includes(workflow.emailCodePurpose)
        ? workflow.emailCodePurpose
        : ''
      workflow.registrationCompleted = workflow.registrationCompleted === true
      workflow.oauthEmailSubmitted = workflow.oauthEmailSubmitted === true
      if (workflow.status === 'running') {
        workflow.status = 'failed'
        workflow.failedNodeKey = workflow.currentNodeKey || 'oauth'
        workflow.error = '自动化组件已恢复，请核对服务器浏览器当前页面后继续'
        const activeNode = workflow.nodes.find((node) => node.key === workflow.failedNodeKey)
        if (activeNode) {
          activeNode.status = 'failed'
          activeNode.message = workflow.error
        }
      }
      workflows.set(workflow.id, workflow)
    }
    const restoredActiveID = String(decoded.active_workflow_id || '')
    const restoredActive = workflows.get(restoredActiveID)
    if (restoredActive && ['running', 'manual_required', 'callback_ready', 'failed', 'paused'].includes(restoredActive.status)) {
      activeWorkflowID = restoredActiveID
    }
    persistWorkflowState()
  } catch {
    console.error('team-child workflow state could not be restored')
  }
}

function json(res, status, payload) {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'x-xiass-team-child-protocol': String(workflowProtocolVersion)
  })
  res.end(JSON.stringify(payload))
}

function authorized(req) {
  if (!serviceToken) return false
  const supplied = String(req.headers['x-xiass-team-child-token'] || '').trim()
  const expected = Buffer.from(serviceToken)
  const actual = Buffer.from(supplied)
  return expected.length === actual.length && crypto.timingSafeEqual(expected, actual)
}

function normalizedPath(req) {
  try {
    return new URL(req.url || '/', 'http://127.0.0.1').pathname
  } catch {
    return ''
  }
}

function boundedDuration(value, fallback, minimum, maximum) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= minimum && parsed <= maximum ? parsed : fallback
}

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

function markMemberPageRenderStarted(page) {
  if (page && typeof page === 'object') memberPageRenderStartedAt.set(page, Date.now())
}

async function waitForMemberPageDwell(page) {
  if (!page || typeof page !== 'object') return
  const startedAt = memberPageRenderStartedAt.get(page) || Date.now()
  memberPageRenderStartedAt.set(page, startedAt)
  const remaining = memberPageMinimumDwellMs - (Date.now() - startedAt)
  if (remaining > 0) await sleep(remaining)
}

async function reserveMemberPageHardRefresh() {
  const now = Date.now()
  memberPageRefreshes = memberPageRefreshes.filter((timestamp) => now - timestamp < memberPageRefreshWindowMs)
  if (memberPageRefreshes.length >= memberPageMaxRefreshesPerWindow) {
    const remaining = memberPageRefreshWindowMs - (now - memberPageRefreshes[0])
    if (remaining > 0) await sleep(remaining)
    return reserveMemberPageHardRefresh()
  }
  memberPageRefreshes.push(Date.now())
}

async function connectPersistentBrowser() {
  const deadline = Date.now() + browserConnectTimeout
  let lastError

  while (true) {
    try {
      return await chromium.connectOverCDP(cdpURL)
    } catch (error) {
      lastError = error
    }

    const remaining = deadline - Date.now()
    if (remaining <= 0) {
      const detail = lastError instanceof Error ? lastError.message : String(lastError || 'unknown error')
      throw new Error(`无法连接持久化 Chromium：${detail}`)
    }
    await sleep(Math.min(browserConnectRetryDelay, remaining))
  }
}

async function browser() {
  if (!browserPromise) {
    browserPromise = connectPersistentBrowser().catch((error) => {
      browserPromise = undefined
      throw error
    })
  }
  const connected = await browserPromise
  if (!connected.isConnected()) {
    browserPromise = undefined
    return browser()
  }
  return connected
}

async function createPrivateBrowserSession(connected) {
  const context = await connected.newContext()
  try {
    const page = await context.newPage()
    return { context, page }
  } catch (error) {
    await context.close().catch(() => undefined)
    throw error
  }
}

async function disposePrivateBrowserSession(workflowID = '') {
  if (workflowID && managedPrivateWorkflowID && managedPrivateWorkflowID !== workflowID) return
  const context = managedPrivateContext
  managedPrivateContext = undefined
  managedPrivatePage = undefined
  managedPrivateWorkflowID = ''
  await context?.close().catch(() => undefined)
}

async function ensurePrivateWorkflowPage(workflow, { create = false } = {}) {
  if (
    managedPrivateWorkflowID === workflow.id
    && managedPrivatePage
    && !managedPrivatePage.isClosed()
  ) return managedPrivatePage

  if (!create) throw new Error('本次隐私浏览器会话已失效，请取消后重新开始')
  await disposePrivateBrowserSession()
  const connected = await browser()
  const session = await createPrivateBrowserSession(connected)
  managedPrivateContext = session.context
  managedPrivatePage = session.page
  managedPrivateWorkflowID = workflow.id
  return session.page
}

async function navigatePrivateWorkflowPage(workflow, value, { create = false, validateOAuth = false } = {}) {
  const targetURL = validateOAuth ? validateOpenAIAuthURL(value) : String(value || '').trim()
  if (!targetURL) throw new Error('隐私浏览器地址无效')
  const active = await ensurePrivateWorkflowPage(workflow, { create })
  await activateBrowserPage(active)
  await active.goto(targetURL, {
    waitUntil: 'domcontentloaded',
    timeout: operationTimeout
  })
  await activateBrowserPage(active)
  return active
}

async function releaseCacheSession(cdpSession) {
  if (!cdpSession) return
  await cdpSession.send('Network.setCacheDisabled', { cacheDisabled: false }).catch(() => undefined)
  await cdpSession.detach().catch(() => undefined)
}

async function activateBrowserPage(active) {
  // The automation must run at the same speed whether or not an administrator
  // has opened the noVNC viewer. Activate the managed tab before waiting for a
  // client-rendered page so Chromium does not treat it as background content.
  await active.bringToFront().catch(() => undefined)
  let cdpSession
  try {
    cdpSession = await active.context().newCDPSession(active)
    await cdpSession.send('Page.bringToFront').catch(() => undefined)
    await cdpSession.send('Emulation.setFocusEmulationEnabled', { enabled: true }).catch(() => undefined)
  } catch {
    // Playwright's bringToFront above remains the compatibility fallback.
  } finally {
    await cdpSession?.detach().catch(() => undefined)
  }
}

async function reloadMemberPage(active, targetURL, forceRefresh) {
  // A normal read is deliberately DOM-only. The managed tab may already have
  // a fully authenticated Members SPA, and re-running goto here resets its
  // React state before member or invitation rows finish rendering.
  if (!forceRefresh && isTeamMembersPage(active)) return undefined

  let cdpSession
  if (forceRefresh) {
    await reserveMemberPageHardRefresh()
    try {
      cdpSession = await active.context().newCDPSession(active)
      await cdpSession.send('Network.enable')
      await cdpSession.send('Network.setCacheDisabled', { cacheDisabled: true })
    } catch {
      await releaseCacheSession(cdpSession)
      cdpSession = undefined
      // Navigation still invalidates the SPA route when a CDP cache toggle is
      // unavailable. Do not fail a member operation just because Chromium has
      // temporarily lost its DevTools session.
    }
  }
  try {
    await active.goto(targetURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
    markMemberPageRenderStarted(active)
    return cdpSession
  } catch (error) {
    await releaseCacheSession(cdpSession)
    throw error
  }
}

function isTeamMembersPage(page) {
  try {
    const parsed = new URL(page.url())
    return parsed.hostname.toLowerCase() === 'chatgpt.com' && parsed.pathname.toLowerCase() === '/admin/members'
  } catch {
    return false
  }
}

function reusableTeamPage(context) {
  if (managedMembersPage && !managedMembersPage.isClosed() && isTeamMembersPage(managedMembersPage)) return managedMembersPage
  return context.pages().find((page) => isTeamMembersPage(page) && !page.isClosed())
}

function isOpenAIWorkflowPage(page) {
  try {
    const parsed = new URL(page.url())
    const hostname = parsed.hostname.toLowerCase()
    return hostname === 'auth.openai.com' || hostname === 'openai.com' || hostname === 'localhost'
  } catch {
    return false
  }
}

function reusableOAuthPage(context) {
  if (managedOAuthPage && !managedOAuthPage.isClosed() && !isTeamMembersPage(managedOAuthPage)) return managedOAuthPage
  return context.pages().find((page) => !page.isClosed() && isOpenAIWorkflowPage(page))
}

async function membersPage({ forceRefresh = false, targetURL = membersURL, pending = false } = {}) {
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')
  // Never reuse an arbitrary visible tab. Only the dedicated Members page is
  // eligible; OAuth, CAPTCHA, callback, and operator tabs remain untouched.
  let active = reusableTeamPage(context)
  if (!active) active = await context.newPage()
  managedMembersPage = active
  let cdpSession
  try {
    await activateBrowserPage(active)
    cdpSession = await reloadMemberPage(active, targetURL, forceRefresh)
  } catch (error) {
    // A Chromium restart invalidates Playwright page objects without always
    // marking them closed. Recreate the dedicated tab once; never fall back to
    // another user-visible tab.
    if (!active.isClosed()) throw error
    active = await context.newPage()
    managedMembersPage = active
    await activateBrowserPage(active)
    cdpSession = await reloadMemberPage(active, targetURL, forceRefresh)
  }
  try {
    if (pending) await waitForPendingInvitesPageReady(active, { allowRecoveryNavigation: forceRefresh })
    else await waitForMemberPageReady(active)
    await waitForMemberPageDwell(active)
    await activateBrowserPage(active)
    return active
  } finally {
    // Keep cache disabled through the SPA's first render/API requests, then
    // restore the browser's normal caching for the operator's manual session.
    await releaseCacheSession(cdpSession)
  }
}

async function pendingInvitesPage({ forceRefresh = false } = {}) {
  // Start from the real Members route and let the page select its own Pending
  // invites tab. Some hosted builds keep `?tab=invites` in the URL while still
  // rendering the Members panel, which made a stale member row look like a
  // confirmed invitation.
  const current = await membersPage({ forceRefresh, targetURL: membersURL, pending: true })
  return current
}

// Existing-account reauthorization keeps using the persistent profile's
// dedicated OAuth tab. New Team identities use an isolated context instead.
async function navigatePersistentBrowser(value) {
  const targetURL = validateOpenAIAuthURL(value)
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')

  let active = reusableOAuthPage(context)
  if (!active) active = await context.newPage()
  managedOAuthPage = active

  await activateBrowserPage(active)
  await active.goto(targetURL, {
    waitUntil: 'domcontentloaded',
    timeout: operationTimeout
  })
  await activateBrowserPage(active)
  return { ok: true, url: active.url() }
}

async function workflowBrowserPage(workflow) {
  if (workflow.mode !== 'reauthorization') {
    return ensurePrivateWorkflowPage(workflow)
  }
  if (managedOAuthPage && !managedOAuthPage.isClosed() && !isTeamMembersPage(managedOAuthPage)) return managedOAuthPage
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')
  const active = reusableOAuthPage(context)
  if (!active) throw new Error('服务器浏览器没有可恢复的工作流标签页')
  managedOAuthPage = active
  return active
}

async function rowsFor(current) {
  const tableRows = current.locator('table tbody tr')
  if (await tableRows.count()) return tableRows
  return current.locator('[role="row"]')
}

function extractEmail(text) {
  return text.match(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i)?.[0] || ''
}

function normalizeEmail(email) {
  return String(email || '')
    .normalize('NFKC')
    .replace(/[\u200B-\u200D\uFEFF]/g, '')
    .trim()
    .toLowerCase()
}

function normalizeWorkflowEmail(value) {
  const normalized = normalizeEmail(value)
  const embedded = extractEmail(normalized)
  return embedded || normalized
}

function isValidWorkflowEmail(value) {
  return Boolean(value) && value.length <= 320 && /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(value)
}

function displayRole(value) {
  const normalized = String(value || '').trim().toLowerCase()
  if (/^(owner|所有者)$/.test(normalized)) return 'owner'
  if (/^(admin|administrator|管理员)$/.test(normalized)) return 'admin'
  if (/^(member|成员)$/.test(normalized)) return 'member'
  return ''
}

function seatTypeFromLines(lines) {
  const candidate = lines.find((line) => /^(standard|flexible|enterprise|business|标准|灵活|企业)$/i.test(line))
  return candidate || ''
}

function roleFromLines(lines) {
  const exact = lines.find((line) => /^(owner|admin|member|成员|管理员|所有者)$/i.test(line))
  if (exact) return displayRole(exact)
  const candidate = lines.find((line) => line.length <= 32 && /owner|admin|member|成员|管理员|所有者/i.test(line))
  return candidate ? displayRole(candidate) || 'unknown' : 'unknown'
}

function normalizedRole(role) {
  const normalized = String(role || '').trim().toLowerCase()
  if (['admin', 'administrator', '管理员'].includes(normalized)) return 'admin'
  if (['member', '成员'].includes(normalized)) return 'member'
  throw new Error('成员角色无效')
}

function isProtectedTeamMember(member) {
  const role = displayRole(member?.role)
  return role === 'owner' || role === 'admin' || protectedMemberEmails.has(normalizeEmail(member?.email))
}

function parsePendingInvites(text) {
  const normalized = String(text || '').replace(/\s+/g, ' ').trim()
  const patterns = [
    /(?:pending invitations?|pending invites?)\s*(?:[(:·-]\s*)?(\d{1,5})\b/i,
    /\b(\d{1,5})\s+(?:pending invitations?|pending invites?)\b/i,
    /(?:待处理邀请|待接受邀请)\s*(?:[（(：:]\s*)?(\d{1,5})\s*(?:个|条)?/i,
    /(\d{1,5})\s*(?:个|条)\s*(?:待处理邀请|待接受邀请)/i
  ]
  for (const pattern of patterns) {
    const match = normalized.match(pattern)
    if (match) return Number(match[1])
  }
  return undefined
}

async function readMembers(current) {
  await waitForMemberPageReady(current)
  const body = await current.locator('body').innerText()
  if (/log in|登录|sign in/i.test(body) && !/成员|members/i.test(body)) {
    throw new Error('服务器浏览器尚未登录 ChatGPT 管理员页面')
  }

  const rows = await rowsFor(current)
  const count = await rows.count()
  const members = []
  for (let index = 0; index < count; index += 1) {
    const text = (await rows.nth(index).innerText()).trim()
    const email = extractEmail(text)
    if (!email) continue
    const lines = text.split(/[\n\t]+/).map((line) => line.trim()).filter(Boolean)
    const role = roleFromLines(lines)
    const member = {
      // Email is the stable identity. A row index is unsafe after sorting or
      // pagination changes and must never be used for a destructive action.
      id: normalizeEmail(email),
      email,
      name: lines.find((line) => line !== email && line !== role) || '',
      role,
      seat_type: seatTypeFromLines(lines),
      status: 'active'
    }
    member.protected = isProtectedTeamMember(member)
    members.push(member)
  }

  const pageTitle = (await current.locator('h1,h2').allTextContents()).join(' ').trim()
  const pendingInvites = parsePendingInvites(body)
  // The Team owner is the current logged-in account in the upstream UI. For
  // this workflow the useful "seat" is the first non-owner account that can
  // actually be replaced, not the owner row marked "You".
  const replaceable = members.find((member) => member.role === 'member' && !member.protected)
  const seatEmail = replaceable?.email || ''
  return {
    ready: true,
    url: current.url(),
    members,
    pending_invites: pendingInvites ?? 0,
    seat_email: seatEmail,
    workspace_name: pageTitle
  }
}

async function listMembers({ forceRefresh = false, requireEmails = false } = {}) {
  let result
  const attempts = forceRefresh && requireEmails ? memberRefreshAttempts : 1
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const current = await membersPage({ forceRefresh })
    result = await readMembers(current)
    if (!requireEmails || result.members.some((member) => Boolean(normalizeEmail(member.email)))) return result
    if (attempt + 1 < attempts) await sleep(750)
  }
  if (requireEmails) throw new Error('未能刷新到成员邮箱列表，请在服务器浏览器确认成员页面后点击继续')
  return result
}

function pendingInvitesURL() {
  const target = new URL(membersURL)
  target.searchParams.set('tab', 'invites')
  return target.toString()
}

async function pendingInvitesRouteSelected(current) {
  try {
    const parsed = new URL(current.url())
    if (parsed.pathname.toLowerCase().includes('/invites')) return true
    // ChatGPT's current Members page keeps the selected tab in the query
    // string (`/admin/members?tab=invites`). The page body and exact target
    // email are still checked by the caller, so honoring this selected route
    // does not turn a stale Members shell into a pending invitation.
    if ((parsed.searchParams.get('tab') || '').toLowerCase() === 'invites') return true
  } catch {
    // The selected tab or active panel below is enough for SPA builds that use
    // a non-URL route.
  }
  const selectedControls = current.locator('[role="tab"][aria-selected="true"], [aria-current="page"], [aria-pressed="true"]')
  const selectedText = (await selectedControls.allTextContents().catch(() => [])).join(' ')
  if (/pending invitations?|pending invites?|待处理邀请|待接受邀请/i.test(selectedText)) return true

  // Do not scan the whole page here: the Members shell normally contains a
  // navigation label for Pending invites even when that panel is not selected.
  const headings = current.locator('h1:visible, h2:visible, h3:visible, [role="heading"]:visible, [role="tabpanel"]:visible')
  const headingText = (await headings.allTextContents().catch(() => [])).join(' ')
  return /pending invitations?|pending invites?|待处理邀请|待接受邀请/i.test(headingText)
}

// The current ChatGPT Members SPA does not expose aria-selected on its native
// tab buttons. It marks the active tab with the primary text/bottom-border
// classes instead. Keep this strict check separate from the broad route/body
// heuristic above so a visible "Pending invites" label in the shell cannot be
// mistaken for the selected panel.
async function pendingInvitesTabSelected(current) {
  const pattern = /pending invitations?|pending invites?|待处理邀请|待接受邀请/i
  const controls = current.locator('button:visible, [role="tab"]:visible, a:visible')
  const count = await controls.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const control = controls.nth(index)
    const text = (await control.innerText().catch(() => '')).replace(/\s+/g, ' ').trim()
    if (!pattern.test(text)) continue
    const ariaSelected = await control.getAttribute('aria-selected').catch(() => '')
    const ariaCurrent = await control.getAttribute('aria-current').catch(() => '')
    const dataState = await control.getAttribute('data-state').catch(() => '')
    const className = await control.getAttribute('class').catch(() => '')
    if (
      ariaSelected === 'true'
      || /^(page|true|active|selected)$/i.test(String(ariaCurrent || ''))
      || /^(open|active|selected)$/i.test(String(dataState || ''))
      || (className.includes('text-token-text-primary') && /border-token-(?:text|border)-secondary/.test(className))
    ) return true
  }
  return false
}

async function pendingInvitesControl(current) {
  const pattern = /pending invitations?|pending invites?|待处理邀请|待接受邀请/i
  for (const role of ['tab', 'button', 'link']) {
    const controls = current.getByRole(role, { name: pattern })
    const count = await controls.count().catch(() => 0)
    for (let index = 0; index < count; index += 1) {
      const control = controls.nth(index)
      if (await control.isVisible().catch(() => false)) return control
    }
  }

  // Some hosted builds render the tab as a plain button without an accessible
  // name. Match its own text only; never use a broad `invite` selector that can
  // activate the native Send invites action instead.
  const controls = current.locator('button:visible, [role="tab"]:visible, a:visible')
  const count = await controls.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const control = controls.nth(index)
    const text = (await control.innerText().catch(() => '')).replace(/\s+/g, ' ').trim()
    if (pattern.test(text)) return control
  }
  return null
}

async function selectPendingInvitesTab(current) {
  const deadline = Date.now() + Math.min(operationTimeout, Math.max(memberRenderTimeout, pendingInviteRenderTimeout))
  let lastClickAt = 0
  while (Date.now() < deadline) {
    if (await pendingInvitesTabSelected(current)) return true
    const control = await pendingInvitesControl(current)
    if (control) {
      await control.scrollIntoViewIfNeeded().catch(() => undefined)
      // Avoid repeatedly activating a tab while React is still committing the
      // first click. A second click during that window can return to Users.
      if (Date.now() - lastClickAt >= 350) {
        await control.click().catch(() => undefined)
        lastClickAt = Date.now()
      }
    }
    await sleep(200)
  }
  throw new Error('ChatGPT 成员页面无法切换到 Pending invites')
}

async function membersControl(current) {
  const pattern = /^members$|^成员$/i
  for (const role of ['tab', 'button', 'link']) {
    const controls = current.getByRole(role, { name: pattern })
    const count = await controls.count().catch(() => 0)
    for (let index = 0; index < count; index += 1) {
      const control = controls.nth(index)
      if (await control.isVisible().catch(() => false)) return control
    }
  }
  return null
}

async function pendingInviteSnapshot({ forceRefresh = false, expectedEmail = '', waitForExpectedEmail = false } = {}) {
  const current = await pendingInvitesPage({ forceRefresh })
  const wanted = normalizeEmail(expectedEmail)
  if (wanted && waitForExpectedEmail) {
    await waitForVisiblePendingInviteEmail(current, wanted)
  }

  // Prefer the visible invitation table. When it exists with no body rows, the
  // result is authoritatively empty; never fall back to broad page selectors
  // that can capture an administrator email or a stale Members row.
  const visibleTables = current.locator('table:visible')
  const hasVisibleTable = await visibleTables.count() > 0
  const visiblePanels = current.locator('[role="tabpanel"]:visible')
  const recordScope = await visiblePanels.count() > 0
    ? visiblePanels.last()
    : current.locator('main:visible').first()
  const pendingRows = hasVisibleTable
    ? visibleTables.locator('tbody tr, [role="row"]')
    : recordScope.locator('[role="row"], [role="listitem"], article, [data-testid*="invite" i], [data-testid*="pending" i]')
  try {
    await pendingRows.first().waitFor({ state: 'visible', timeout: 2500 })
  } catch {
    // An empty pending-invites page is a valid result.
  }

  const rows = pendingRows
  const count = await rows.count()
  const recordTexts = []
  for (let index = 0; index < count; index += 1) {
    recordTexts.push(await rows.nth(index).innerText())
  }
  const emails = pendingInviteEmailsFromTexts(recordTexts)
  if (wanted && await visiblePendingInviteEmail(current, wanted)) {
    // A few hosted builds render the invitation as an unstructured text card.
    // Only the exact requested email is accepted in that fallback; arbitrary
    // emails from the page shell are never treated as pending invitations.
    emails.add(wanted)
  }
  const body = await recordScope.innerText().catch(() => '')
  const explicitlyEmpty = /\bno results\b|\bno pending (?:invites?|invitations?)\b|暂无.*邀请|没有.*邀请|还没有.*邀请/i.test(body)
  return {
    emails: explicitlyEmpty && emails.size === 0 ? new Set() : emails,
    pendingInvites: explicitlyEmpty && emails.size === 0 ? 0 : (parsePendingInvites(body) ?? emails.size)
  }
}

function pendingInviteEmailsFromTexts(texts) {
  const emails = new Set()
  for (const text of texts) {
    const email = normalizeEmail(extractEmail(String(text || '')))
    if (email) emails.add(email)
  }
  return emails
}

async function pendingInviteRecord(current, expectedEmail, required = true) {
  await selectPendingInvitesTab(current)
  return pendingInviteRecordInCurrentView(current, expectedEmail, required)
}

async function pendingInviteRecordInCurrentView(current, expectedEmail, required = true) {
  const wanted = normalizeEmail(expectedEmail)
  const rows = current.locator('table:visible tbody tr, [role="row"]:visible, [role="listitem"]:visible, article:visible')
  const count = await rows.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const row = rows.nth(index)
    const email = normalizeEmail(extractEmail(await row.innerText().catch(() => '')))
    if (email === wanted) return { row, email }
  }
  if (required) throw new Error('Pending invites 中找不到目标邮箱')
  return null
}

async function waitForVisiblePendingInviteEmail(current, wanted) {
  const deadline = Date.now() + pendingInviteRenderTimeout
  while (Date.now() < deadline) {
    if (await visiblePendingInviteEmail(current, wanted)) return true
    // Keep the same SPA page alive while its pending-invite data arrives. A
    // navigation/reload on every poll can reset the tab before React commits
    // the invitation row, which previously caused successful invites to look
    // like failures.
    await sleep(300)
  }
  return false
}

async function visiblePendingInviteEmail(current, wanted) {
  if (!await pendingInvitesRouteSelected(current)) return false
  const escapedWanted = wanted.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const exactMatches = current.getByText(new RegExp(`^${escapedWanted}$`, 'i'))
  const count = await exactMatches.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const candidate = exactMatches.nth(index)
    if (!(await candidate.isVisible().catch(() => false))) continue
    const isRecord = await candidate.evaluate((element) => {
      let node = element
      for (let depth = 0; node && depth < 7; depth += 1) {
        const tag = node.tagName.toLowerCase()
        const role = node.getAttribute('role') || ''
        const testID = (node.getAttribute('data-testid') || '').toLowerCase()
        if (tag === 'tr' || tag === 'article' || role === 'row' || role === 'listitem' || /pending|invite/.test(testID)) return true
        node = node.parentElement
      }
      return false
    }).catch(() => false)
    if (isRecord) return true
  }

  // Last-resort support for a page that has no semantic row/card wrapper. The
  // exact text is still required, and the route has already been verified as
  // the selected Pending invites panel.
  return count > 0
}

async function waitForPendingInviteEmail(email) {
  const wanted = normalizeEmail(email)
  const latest = await pendingInviteSnapshot({
    forceRefresh: false,
    expectedEmail: wanted,
    waitForExpectedEmail: true
  })
  if (latest.emails.has(wanted)) return latest

  // Some hosted builds immediately move the accepted invite into Members.
  // Treat that as success too, but never infer success from a count alone.
  const members = await listMembers({ forceRefresh: false })
  if (members.members.some((member) => normalizeEmail(member.email) === wanted)) return latest
  throw new Error('邀请操作已提交但待处理邀请中未出现该邮箱，未在规定时间内确认页面状态')
}

// The ChatGPT members page is a client-rendered application. DOMContentLoaded
// fires before the member rows exist, which previously produced an apparently
// valid but empty result on slower servers. Wait for the stable toolbar first,
// then allow the table a short window to populate. An actually empty workspace
// still returns normally once the toolbar is visible.
async function waitForMemberPageReady(current) {
  const deadline = Date.now() + Math.min(operationTimeout, memberRenderTimeout)
  const inviteButton = current.getByRole('button', { name: /invite member|邀请成员/i }).first()
  const rows = current.locator('table tbody tr, [role="row"]')
  let toolbarVisibleAt = 0
  let emptyStateVisibleAt = 0
  while (Date.now() < deadline) {
    if (await pendingInvitesRouteSelected(current)) {
      const control = await membersControl(current)
      if (control) {
        await control.click().catch(() => undefined)
        markMemberPageRenderStarted(current)
        await sleep(500)
        continue
      }
      await sleep(400)
      continue
    }
    if (await rows.first().isVisible().catch(() => false)) {
      await waitForMemberPageDwell(current)
      return
    }
    if (await inviteButton.isVisible().catch(() => false)) {
      // The Members toolbar commits before the table data. Returning as soon
      // as Invite member appears makes an owner-only workspace look empty on
      // slower sessions. Give the row query time to observe the same render;
      // only accept an empty result after a short stable toolbar window.
      toolbarVisibleAt ||= Date.now()
      const body = await current.locator('body').innerText().catch(() => '')
      const explicitEmpty = /no members|no users|暂无成员|没有成员|还没有成员/i.test(body)
      if (explicitEmpty) {
        emptyStateVisibleAt ||= Date.now()
        if (Date.now() - emptyStateVisibleAt >= memberPageMinimumDwellMs) {
          await waitForMemberPageDwell(current)
          return
        }
      } else {
        emptyStateVisibleAt = 0
      }
      if (Date.now() - toolbarVisibleAt >= 5000) {
        await waitForMemberPageDwell(current)
        return
      }
    }
    await sleep(250)
  }
  if (await pendingInvitesRouteSelected(current)) {
    throw new Error('成员页面仍停留在待处理邀请页，请刷新成员页面后重试')
  }
  // Preserve the previous behavior for a genuinely empty workspace: readMembers
  // will produce the useful login/page error, while a stale Pending invites tab
  // is rejected explicitly above instead of being parsed as members.
}

async function waitForPendingInvitesPageReady(current, { allowRecoveryNavigation = false } = {}) {
  const emptyPattern = /no pending|no invitations|暂无.*邀请|没有.*邀请|还没有.*邀请/i
  const deadline = Date.now() + Math.min(
    operationTimeout,
    Math.max(memberRenderTimeout, pendingInviteRenderTimeout)
  )
  let lastTabClickAt = 0
  let attemptedDirectRoute = false
  let selectedSince = 0

  while (Date.now() < deadline) {
    const body = await current.locator('body').innerText().catch(() => '')
    const routeSelected = await pendingInvitesRouteSelected(current)
    const selectedControl = await pendingInvitesTabSelected(current)
    const tableHeaders = (await current.locator('table:visible thead:visible, table:visible th:visible').allTextContents().catch(() => [])).join(' ')
    const searchMetadata = await current.locator('input:visible').evaluateAll((inputs) => inputs.map((input) => [
      input.getAttribute('placeholder') || '',
      input.getAttribute('aria-label') || '',
      input.getAttribute('name') || ''
    ].join(' '))).catch(() => [])
    const pendingContent = (
      (/email|邮箱/i.test(tableHeaders) && /date invited|invited|邀请日期|邀请时间/i.test(tableHeaders))
      || searchMetadata.some((value) => /search.*invites?|invites?.*search|搜索.*邀请/i.test(value))
      || emptyPattern.test(body)
    )
    if ((selectedControl || routeSelected) && pendingContent) {
      selectedSince ||= Date.now()
      if (Date.now() - selectedSince < memberPageMinimumDwellMs) {
        await sleep(250)
        continue
      }
      // Give the SPA one short render tick after the route/tab selection. This
      // prevents a previous Members table from being mistaken for Pending
      // invites when the page reuses its shell.
      await waitForMemberPageDwell(current)
      return
    }
    selectedSince = 0

    if (!selectedControl && Date.now() - lastTabClickAt >= 500) {
      const control = await pendingInvitesControl(current)
      if (control) {
        await control.scrollIntoViewIfNeeded().catch(() => undefined)
        await control.click().catch(() => undefined)
        lastTabClickAt = Date.now()
        markMemberPageRenderStarted(current)
        await sleep(300)
        continue
      }
    }

    // Query-string routing is only a fallback. The next loop still requires an
    // actual selected panel/heading before the page is accepted.
    if (allowRecoveryNavigation && !attemptedDirectRoute) {
      attemptedDirectRoute = true
      await reserveMemberPageHardRefresh()
      await current.goto(pendingInvitesURL(), { waitUntil: 'domcontentloaded', timeout: operationTimeout }).catch(() => undefined)
      markMemberPageRenderStarted(current)
      await sleep(300)
      continue
    }
    await sleep(250)
  }

  throw new Error('待处理邀请页面未完成加载，请刷新 ChatGPT 成员页面后重试')
}

async function clickText(current, pattern, options = {}) {
  const locator = current.getByRole('button', { name: pattern }).first()
  if (await locator.count()) {
    await locator.click(options)
    return
  }
  const textLocator = current.getByText(pattern).first()
  if (!(await textLocator.count())) throw new Error(`页面中找不到操作：${pattern}`)
  await textLocator.click(options)
}

async function firstVisibleAction(scope, patterns, roles = ['button']) {
  for (const pattern of patterns) {
    for (const role of roles) {
      const candidates = scope.getByRole(role, { name: pattern })
      const count = await candidates.count().catch(() => 0)
      for (let index = 0; index < count; index += 1) {
        const candidate = candidates.nth(index)
        if (!(await candidate.isVisible().catch(() => false))) continue
        if (await candidate.isDisabled().catch(() => false)) continue
        return candidate
      }
    }

    const texts = scope.getByText(pattern)
    const count = await texts.count().catch(() => 0)
    for (let index = 0; index < count; index += 1) {
      const candidate = texts.nth(index)
      if (await candidate.isVisible().catch(() => false)) return candidate
    }
  }
  return null
}

async function clickRemoveMemberMenuAction(current) {
  const menus = current.locator('[role="menu"]:visible')
  const menuCount = await menus.count().catch(() => 0)
  const menu = menuCount > 0 ? menus.nth(menuCount - 1) : null
  const patterns = [/^(?:remove member|移除成员)$/i]
  let action = menu ? await firstVisibleAction(menu, patterns, ['menuitem', 'button']) : null
  if (!action) action = await firstVisibleAction(current, patterns, ['menuitem', 'button'])
  if (!action) throw new Error('成员操作菜单中找不到移除成员操作')
  await action.click()
}

async function officialRemoveMemberConfirmationButton(current) {
  const dialogs = current.locator('[role="dialog"]:visible, [role="alertdialog"]:visible')
  const dialogCount = await dialogs.count().catch(() => 0)
  const patterns = [/^remove from workspace[.!]?$/i, /^(?:从工作空间移除|从工作区移除)[。！]?$/i]
  for (let index = dialogCount - 1; index >= 0; index -= 1) {
    const dialog = dialogs.nth(index)
    const action = await firstVisibleAction(dialog, patterns)
    if (action) return action
  }
  return null
}

async function confirmOfficialMemberRemoval(current) {
  let confirmationButton = null
  await waitUntil('ChatGPT 官方“Remove from workspace”确认弹窗未出现', async () => {
    confirmationButton = await officialRemoveMemberConfirmationButton(current)
    return Boolean(confirmationButton)
  })
  if (!confirmationButton) throw new Error('ChatGPT 官方移除成员确认按钮未出现')
  await confirmationButton.click()
}

async function visibleInviteDialog(current) {
  const dialog = current.locator('[data-testid="modal-invite-users-to-workspace"]').last()
  if ((await dialog.count()) > 0 && await dialog.isVisible().catch(() => false)) return dialog

  const roleDialog = current.getByRole('dialog').last()
  if ((await roleDialog.count()) > 0 && await roleDialog.isVisible().catch(() => false)) return roleDialog
  return null
}

async function openInviteDialog(current) {
  const existing = await visibleInviteDialog(current)
  if (existing) {
    await sleep(memberPageMinimumDwellMs)
    return existing
  }

  const inviteButton = await firstVisibleInviteButton(current)
  if (!inviteButton) {
    throw new Error('成员页面中找不到邀请成员按钮')
  }
  await inviteButton.click()

  await waitUntil('邀请成员弹窗未出现', async () => Boolean(await visibleInviteDialog(current)))
  const dialog = await visibleInviteDialog(current)
  if (!dialog) throw new Error('邀请成员弹窗未出现')
  // The dialog shell mounts before the Email input and Send invites action are
  // ready on slower hosted workspaces. Let it hydrate before touching either.
  await sleep(memberPageMinimumDwellMs)
  return dialog
}

async function firstVisibleInviteButton(current) {
  // Prefer the actual action label. A broad `/invite/` selector can pick the
  // Pending invites tab before it reaches the real Invite members button.
  for (const pattern of [/^invite members?$/i, /^invite$/i, /^邀请成员$/i, /^邀请$/i]) {
    const buttons = current.getByRole('button', { name: pattern })
    const count = await buttons.count().catch(() => 0)
    for (let index = 0; index < count; index += 1) {
      const button = buttons.nth(index)
      if (await button.isVisible().catch(() => false)) return button
    }
  }

  const buttons = current.locator('button')
  const count = await buttons.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const button = buttons.nth(index)
    if (!(await button.isVisible().catch(() => false))) continue
    const metadata = [
      await button.innerText().catch(() => ''),
      await button.getAttribute('aria-label'),
      await button.getAttribute('title'),
      await button.getAttribute('data-testid')
    ].filter(Boolean).join(' ').replace(/\s+/g, ' ').trim()
    if (!/invite|邀请/i.test(metadata) || /pending|待处理|待接受/i.test(metadata)) continue
    return button
  }
  return null
}

async function firstVisibleDialogButton(scope, pattern) {
  const exactPatterns = pattern.test('continue') || pattern.test('继续')
    ? [/^continue$/i, /^继续$/i]
    : [pattern]
  for (const exactPattern of exactPatterns) {
    const buttons = scope.getByRole('button', { name: exactPattern })
    const count = await buttons.count()
    for (let index = 0; index < count; index += 1) {
      const button = buttons.nth(index)
      if (await button.isVisible().catch(() => false)) return button
    }
  }

  const buttons = scope.getByRole('button', { name: pattern })
  const count = await buttons.count()
  for (let index = 0; index < count; index += 1) {
    const button = buttons.nth(index)
    if (!(await button.isVisible().catch(() => false))) continue
    const label = (await button.innerText().catch(() => '')).replace(/\s+/g, ' ').trim()
    if (/continue\s+(with|to)|继续使用|继续前往/i.test(label)) continue
    return button
  }
  return null
}

async function submitInviteDialog(scope, email) {
  const inputs = scope.locator('input')
  let input
  const count = await inputs.count()
  for (let index = 0; index < count; index += 1) {
    const candidate = inputs.nth(index)
    if (!(await candidate.isVisible().catch(() => false))) continue
    const type = (await candidate.getAttribute('type') || 'text').toLowerCase()
    const metadata = [
      type,
      await candidate.getAttribute('autocomplete'),
      await candidate.getAttribute('name'),
      await candidate.getAttribute('id'),
      await candidate.getAttribute('aria-label'),
      await candidate.getAttribute('placeholder')
    ].filter(Boolean).join(' ').toLowerCase()
    if (type === 'email' || /email|邮箱/.test(metadata)) {
      input = candidate
      break
    }
  }
  if (!input) throw new Error('邀请成员弹窗中找不到邮箱输入框')
  await input.fill(email)

  // This is the only supported invitation path. Fill the native Email field,
  // then click Send invites. Continue remains a compatibility fallback for an
  // older hosted build, but must never take priority over the real submit label.
  let submitButton
  await waitUntil('邀请成员弹窗中找不到可用的提交按钮', async () => {
    for (const pattern of [/^send invites?$/i, /^发送邀请$/i, /^continue$/i, /^继续$/i]) {
      const button = await firstVisibleDialogButton(scope, pattern)
      if (!button || await button.isDisabled().catch(() => true)) continue
      submitButton = button
      return true
    }
    return false
  })
  if (!submitButton) throw new Error('邀请成员弹窗中找不到可用的提交按钮')
  await submitButton.click()
}

async function memberRecord(current, email, required = true) {
  const wanted = normalizeEmail(email)
  const rows = await rowsFor(current)
  const count = await rows.count()
  for (let index = 0; index < count; index += 1) {
    const row = rows.nth(index)
    const actual = normalizeEmail(extractEmail(await row.innerText()))
    if (actual !== wanted) continue
    const lines = (await row.innerText()).split(/[\n\t]+/).map((line) => line.trim()).filter(Boolean)
    const member = {
      id: actual,
      email: extractEmail(await row.innerText()),
      role: roleFromLines(lines)
    }
    member.protected = isProtectedTeamMember(member)
    return { row, member }
  }
  if (required) throw new Error('成员列表中找不到该邮箱，请先刷新后重试')
  return null
}

async function memberRow(current, email, required = true) {
  const record = await memberRecord(current, email, required)
  return record?.row || null
}

function assertRemovableMember(member) {
  if (isProtectedTeamMember(member)) throw new Error('受保护的管理员账号不可移除或替换')
  if (displayRole(member.role) !== 'member') throw new Error('只能替换普通成员席位')
}

async function waitUntil(description, predicate) {
  const deadline = Date.now() + confirmationTimeout
  let lastError
  while (Date.now() < deadline) {
    try {
      if (await predicate()) return
    } catch (error) {
      lastError = error
    }
    await new Promise((resolve) => setTimeout(resolve, 300))
  }
  if (lastError instanceof Error) throw new Error(`${description}：${lastError.message}`)
  throw new Error(`${description}，未在规定时间内确认页面状态`)
}

async function clickMemberMenu(current, email) {
  const row = await memberRow(current, email)
  await row.scrollIntoViewIfNeeded()
  const buttons = row.locator('button')
  const buttonCount = await buttons.count()
  if (!buttonCount) throw new Error('成员行中找不到操作菜单')
  const trigger = buttons.nth(buttonCount - 1)

  // The current ChatGPT table renders the three-dot trigger as an icon-only
  // Radix menu. A default center click can land on the nested SVG <use>
  // element and leave the menu closed even though the button is interactive.
  // Click a stable button edge first, then use the keyboard activation path as
  // a fallback for builds that ignore pointer activation from the icon area.
  const openMenu = async () => {
    if (await trigger.getAttribute('aria-expanded').catch(() => '') === 'true') return true
    if (await current.locator('[role="menu"]:visible').count().catch(() => 0)) return true
    return false
  }

  await trigger.click({ position: { x: 4, y: 18 } }).catch(() => undefined)
  if (await openMenu()) return

  await trigger.press('Enter').catch(() => undefined)
  await waitUntil('成员操作菜单未展开', openMenu)
}

function markWorkflowInviteSubmitted(workflow) {
  if (!workflow || workflow.inviteSubmitted) return false
  workflow.inviteSubmitted = true
  workflow.inviteSubmittedAt = Date.now()
  persistWorkflowState()
  return true
}

async function inviteMember(email, { workflow = null, confirm = true } = {}) {
  const normalized = normalizeWorkflowEmail(email)
  if (!isValidWorkflowEmail(normalized)) {
    throw new Error('邀请邮箱格式无效')
  }
  const existing = await listMembers({ forceRefresh: false, requireEmails: true })
  if (existing.members.some((member) => normalizeEmail(member.email) === normalized)) {
    return {
      ...existing,
      pending_invites: existing.pending_invites,
      operation: { type: 'invite', email: normalized, submitted: false, confirmed: true }
    }
  }

  // A workflow records the native Send invites click immediately. Once that
  // bit is set, every continuation is confirmation-only and can never submit
  // the same invitation again. The direct member tool still checks Pending
  // once so a manual repeat click remains idempotent.
  if (!workflow) {
    const pendingBefore = await pendingInviteSnapshot({
      forceRefresh: false,
      expectedEmail: normalized,
      waitForExpectedEmail: false
    }).catch(() => null)
    if (pendingBefore?.emails.has(normalized)) {
      return {
        ...existing,
        pending_invites: pendingBefore.pendingInvites,
        operation: { type: 'invite', email: normalized, submitted: false, confirmed: true }
      }
    }
  }

  if (!workflow?.inviteSubmitted) {
    const current = await membersPage({ forceRefresh: false })
    const scope = await openInviteDialog(current)
    await submitInviteDialog(scope, normalized)
    markWorkflowInviteSubmitted(workflow)
  }

  if (!confirm) {
    return {
      ...existing,
      operation: { type: 'invite', email: normalized, submitted: true, confirmed: false }
    }
  }

  const confirmedPending = await waitForPendingInviteEmail(normalized)
  const latest = await listMembers({ forceRefresh: false, requireEmails: true })
  return {
    ...latest,
    pending_invites: confirmedPending?.pendingInvites || 1,
    operation: { type: 'invite', email: normalized, submitted: true, confirmed: true }
  }
}

async function removeMember(email) {
  const normalized = normalizeEmail(email)
  const current = await membersPage({ forceRefresh: false })
  const record = await memberRecord(current, normalized)
  assertRemovableMember(record.member)
  await clickMemberMenu(current, normalized)
  await clickRemoveMemberMenuAction(current)
  // ChatGPT renders a second destructive confirmation in a modal. Keep this
  // click dialog-scoped so a lingering menu action can never impersonate it.
  await confirmOfficialMemberRemoval(current)

  await waitUntil('移除操作已提交但成员仍存在', async () => !(await memberRow(current, normalized, false)))
  const result = await readMembers(current)
  return { ...result, operation: { type: 'remove', email: normalized, confirmed: true } }
}

async function updateMember(email, role) {
  const normalized = normalizeEmail(email)
  const normalizedTargetRole = normalizedRole(role)
  if (!['admin', 'member'].includes(normalizedTargetRole)) throw new Error('成员角色无效')
  const current = await membersPage({ forceRefresh: false })
  const record = await memberRecord(current, normalized)
  assertRemovableMember(record.member)
  await clickMemberMenu(current, normalized)
  await clickText(current, /edit|编辑/i)
  const dialog = current.getByRole('dialog').last()
  const scope = (await dialog.count()) ? dialog : current
  const select = scope.locator('select').first()
  if (await select.count()) {
    try {
      await select.selectOption({ value: normalizedTargetRole })
    } catch {
      const labels = normalizedTargetRole === 'admin' ? ['Admin', '管理员'] : ['Member', '成员']
      let selected = false
      let lastError
      for (const label of labels) {
        try {
          await select.selectOption({ label })
          selected = true
          break
        } catch (error) {
          lastError = error
        }
      }
      if (!selected) throw lastError || new Error('页面中找不到目标角色')
    }
  } else {
    await clickText(scope, normalizedTargetRole === 'admin' ? /admin|管理员/i : /member|成员/i)
  }
  await clickText(scope, /save|保存|update|更新/i)

  await waitUntil('角色更新已提交但页面未显示目标角色', async () => {
    const row = await memberRow(current, normalized, false)
    if (!row) return false
    const lines = (await row.innerText()).split(/[\n\t]+/).map((line) => line.trim()).filter(Boolean)
    try {
      return normalizedRole(roleFromLines(lines)) === normalizedTargetRole
    } catch {
      return false
    }
  })
  const result = await readMembers(current)
  return { ...result, operation: { type: 'update', email: normalized, role: normalizedTargetRole, confirmed: true } }
}

function validateWorkflowEmail(value, label) {
  const email = normalizeWorkflowEmail(value)
  if (!isValidWorkflowEmail(email)) {
    throw new Error(`${label}格式无效`)
  }
  return email
}

function validateOpenAIAuthURL(value) {
  const raw = String(value || '').trim()
  if (!raw || raw.length > 8192) throw new Error('授权链接无效')
  let parsed
  try {
    parsed = new URL(raw)
  } catch {
    throw new Error('授权链接无效')
  }
  const query = parsed.searchParams
  const isOfficialPKCE = parsed.protocol === 'https:'
    && !parsed.username
    && !parsed.password
    && parsed.hostname.toLowerCase() === 'auth.openai.com'
    && parsed.pathname === '/oauth/authorize'
    && query.get('response_type') === 'code'
    && query.get('client_id') === officialOpenAIClientID
    && query.get('redirect_uri') === officialOpenAIRedirectURI
    && query.get('scope') === officialOpenAIScope
    && Boolean(query.get('state'))
    && Boolean(query.get('code_challenge'))
    && query.get('code_challenge_method') === 'S256'
    && query.get('codex_cli_simplified_flow') === 'true'
    && query.get('id_token_add_organizations') === 'true'
  if (!isOfficialPKCE) throw new Error('授权链接必须使用 XIASS 内置 OpenAI PKCE 登录流程')
  return parsed.toString()
}

function validateOAuthSessionID(value) {
  const sessionID = String(value || '').trim()
  if (!/^[A-Za-z0-9_-]{16,128}$/.test(sessionID)) throw new Error('XIASS OAuth 会话无效')
  return sessionID
}

async function waitForOAuthPage(description, predicate, timeout = oauthPageTimeout) {
  const deadline = Date.now() + timeout
  let lastError
  while (Date.now() < deadline) {
    try {
      const result = await predicate()
      if (result) return result
    } catch (error) {
      lastError = error
    }
    await sleep(250)
  }
  if (lastError instanceof Error) throw new Error(`${description}：${lastError.message}`)
  throw new Error(`${description}，未在规定时间内识别页面状态`)
}

async function firstVisible(locator) {
  const count = await locator.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const candidate = locator.nth(index)
    if (await candidate.isVisible().catch(() => false)) return candidate
  }
  return null
}

async function firstVisibleRole(current, role, patterns) {
  for (const pattern of patterns) {
    const match = await firstVisible(current.getByRole(role, { name: pattern }))
    if (match) return match
  }
  return null
}

async function firstVisibleInput(current, matcher) {
  const inputs = current.locator('input')
  const count = await inputs.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const input = inputs.nth(index)
    if (!(await input.isVisible().catch(() => false))) continue
    const metadata = [
      await input.getAttribute('type'),
      await input.getAttribute('name'),
      await input.getAttribute('id'),
      await input.getAttribute('autocomplete'),
      await input.getAttribute('aria-label'),
      await input.getAttribute('placeholder')
    ].filter(Boolean).join(' ').toLowerCase()
    if (matcher(metadata, input)) return input
  }
  return null
}

async function clickOAuthContinue(current) {
  const button = await firstVisibleRole(current, 'button', [/^continue$/i, /^next$/i, /^继续$/i, /^下一步$/i])
  if (!button) throw new Error('OpenAI 页面中找不到继续按钮')
  await button.click()
}

async function oauthBody(current) {
  return (await current.locator('body').innerText().catch(() => '')).replace(/\s+/g, ' ').trim()
}

async function refreshOpenAIErrorPage(current) {
  const body = await oauthBody(current)
  if (!/(?:error\s*400|400\s*(?:error|bad request)|请求错误|出了点问题)/i.test(body)) return false
  await current.reload({ waitUntil: 'domcontentloaded', timeout: operationTimeout })
  return true
}

function oauthPageHostname(current) {
  try {
    const parsed = new URL(current.url())
    if (!['http:', 'https:'].includes(parsed.protocol)) return ''
    return parsed.hostname.toLowerCase()
  } catch {
    return ''
  }
}

function isOpenAIIdentityHost(hostname) {
  return hostname === 'auth.openai.com'
    || hostname === 'chatgpt.com'
    || hostname === 'chat.openai.com'
    || hostname === 'openai.com'
    || hostname.endsWith('.openai.com')
}

function thirdPartyIdentityProviderName(hostname) {
  if (hostname === 'accounts.google.com' || hostname.endsWith('.google.com')) return 'Google'
  if (hostname === 'appleid.apple.com' || hostname.endsWith('.apple.com')) return 'Apple'
  if (hostname === 'login.microsoftonline.com' || hostname.endsWith('.microsoft.com')) return 'Microsoft'
  if (hostname.includes('okta')) return 'Okta'
  if (hostname.includes('auth0')) return 'Auth0'
  return '第三方身份提供商'
}

function thirdPartyIdentityProviderPage(current) {
  const hostname = oauthPageHostname(current)
  if (!hostname || hostname === 'localhost' || hostname === '127.0.0.1' || isOpenAIIdentityHost(hostname)) return ''
  return thirdPartyIdentityProviderName(hostname)
}

async function thirdPartyIdentityProviderOption(current) {
  const patterns = [
    /continue with google/i,
    /sign in with google/i,
    /continue with apple/i,
    /sign in with apple/i,
    /continue with microsoft/i,
    /sign in with microsoft/i,
    /single sign[ -]?on/i,
    /\bsso\b/i,
    /使用\s*(?:google|谷歌|apple|微软|microsoft)/i
  ]
  return await firstVisibleRole(current, 'button', patterns)
    || await firstVisibleRole(current, 'link', patterns)
}

function isReauthorizationWorkspacePage(body) {
  return /select\s+(?:a\s+)?(?:workspace|organization)|choose\s+(?:a\s+)?(?:workspace|organization)|continue\s+to\s+codex|authorize\s+codex|选择(?:工作空间|空间|组织)|(?:工作空间|空间|组织).*(?:继续|授权)/i.test(body)
}

function isReauthorizationAccountChooserPage(body) {
  return /choose\s+(?:an?\s+)?account|(?:log\s*in|sign\s*in|login)\s+(?:(?:to|with)\s+)?(?:another|a\s+different)\s+account|use\s+(?:another|a\s+different)\s+account|选择(?:一个)?(?:账号|账户)|(?:登录|使用)(?:其他|另一个|不同)(?:账号|账户)/i.test(body)
}

const loginForAnotherAccountPatterns = [
  /^(?:log\s*in|sign\s*in|login)\s+(?:(?:to|with)\s+)?(?:another|a\s+different)\s+account$/i,
  /^use\s+(?:another|a\s+different)\s+account$/i,
  /^登录(?:到)?(?:其他|另一个|不同)(?:账号|账户)$/i,
  /^使用(?:其他|另一个|不同)(?:账号|账户)登录$/i
]

const normalLoginPatterns = [/^log\s*in$/i, /^sign\s*in$/i, /^登录$/i]

function isReauthorizationEmailCodePage(body, inputs) {
  if (!inputs.length) return false
  if (isAuthenticatorChallenge(body, inputs)) return false
  if (/phone|text message|\bsms\b|mobile|手机号|短信/i.test(body)) return false
  return /email|inbox|mail|check your inbox|verify your email|verification code|邮箱|验证邮件|邮箱验证码/i.test(body)
}

async function reauthorizationNextState(current, workflow) {
  const callbackURL = await workflowCallbackURLFromPage(current, workflow)
  if (callbackURL) return { kind: 'callback', callbackURL }

  const provider = thirdPartyIdentityProviderPage(current)
  if (provider) return { kind: 'external_provider', provider }

  const passwordInput = await firstVisibleInput(current, (metadata) => /password/.test(metadata))
  if (passwordInput) return { kind: 'password', input: passwordInput }

  const verification = await verificationInputs(current)
  const body = await oauthBody(current)
  if (isAuthenticatorChallenge(body, verification)) return { kind: 'totp' }
  if (isReauthorizationEmailCodePage(body, verification)) return { kind: 'email_code' }

  if (isReauthorizationAccountChooserPage(body)) return { kind: 'account_chooser' }
  const emailInput = await firstVisibleInput(current, isEmailInputMetadata)
  if (emailInput) return { kind: 'email', input: emailInput }
  if (isReauthorizationWorkspacePage(body)) return { kind: 'workspace' }
  return { kind: 'unknown' }
}

async function registeredOAuthNextState(current, workflow) {
  const callbackURL = await workflowCallbackURLFromPage(current, workflow)
  if (callbackURL) return { kind: 'callback', callbackURL }

  const provider = thirdPartyIdentityProviderPage(current)
  if (provider) return { kind: 'external_provider', provider }

  const body = await oauthBody(current)
  const phoneInput = await firstVisibleInput(current, isPhoneInputMetadata)
  if (phoneInput && /phone number|required.*phone|手机号|电话号码/i.test(body)) return { kind: 'phone' }

  const verification = await verificationInputs(current)
  if (isReauthorizationEmailCodePage(body, verification)) return { kind: 'email_code' }
  if (verification.length > 0 && /phone|text message|sms|mobile|短信|手机/i.test(body)) return { kind: 'sms_code' }

  const passwordInput = await firstVisibleInput(current, (metadata) => /password/.test(metadata))
  if (passwordInput) return { kind: 'password', input: passwordInput }
  if (isReauthorizationAccountChooserPage(body)) return { kind: 'account_chooser' }

  const emailInput = await firstVisibleInput(current, isEmailInputMetadata)
  if (emailInput) return { kind: 'email', input: emailInput }
  if (isReauthorizationWorkspacePage(body)) return { kind: 'workspace' }
  return { kind: 'unknown' }
}

async function waitForRegisteredOAuthNextState(current, workflow) {
  return waitForOAuthPage('OpenAI OAuth 登录后未进入邮箱验证、手机号或工作空间页面', async () => {
    const state = await registeredOAuthNextState(current, workflow)
    return state.kind === 'unknown' ? null : state
  })
}

async function selectPrivateRegistrationEntry(current) {
  await refreshOpenAIErrorPage(current)
  let actionClicks = 0
  let lastActionKey = ''
  return waitForOAuthPage('ChatGPT 隐私页中找不到登录入口或邮箱输入框', async () => {
    const emailInput = await firstVisibleInput(current, isEmailInputMetadata)
    if (emailInput) return emailInput

    const provider = thirdPartyIdentityProviderPage(current)
    if (provider) throw new Error(`ChatGPT 注册页已转至${provider}登录，无法继续临时邮箱注册`)
    if (actionClicks >= 3) return null

    // The current ChatGPT landing page exposes both Log in and Sign up for
    // free. Prefer Log in as requested; unknown mailboxes are then offered the
    // normal email-code account creation path by OpenAI.
    const action = await firstVisibleRole(current, 'button', normalLoginPatterns)
      || await firstVisibleRole(current, 'link', normalLoginPatterns)
      || await firstVisibleRole(current, 'button', [/^sign up for free$/i, /^sign up$/i, /^注册(?:账号)?$/i])
      || await firstVisibleRole(current, 'link', [/^sign up for free$/i, /^sign up$/i, /^注册(?:账号)?$/i])
    if (!action) return null
    const actionKey = `${current.url()}|${await action.innerText().catch(() => '')}`
    if (actionKey === lastActionKey) return null
    lastActionKey = actionKey
    actionClicks += 1
    await action.click()
    return null
  })
}

async function selectLoginForAnotherAccount(current, workflow) {
  await refreshOpenAIErrorPage(current)
  let actionClicks = 0
  let lastActionKey = ''
  return waitForOAuthPage('OpenAI 登录页中找不到邮箱输入框或可识别的登录状态', async () => {
    const state = await reauthorizationNextState(current, workflow)
    if (state.kind === 'account_chooser') {
      if (actionClicks >= 3) return state
      const otherAccount = await firstVisibleRole(current, 'button', loginForAnotherAccountPatterns)
        || await firstVisibleRole(current, 'link', loginForAnotherAccountPatterns)
      if (!otherAccount) return state
      const actionKey = `${current.url()}|another-account|${await otherAccount.innerText().catch(() => '')}`
      if (actionKey === lastActionKey) return null
      lastActionKey = actionKey
      actionClicks += 1
      await otherAccount.click()
      return null
    }
    if (state.kind !== 'unknown') return state
    if (actionClicks >= 3) {
      const providerOption = await thirdPartyIdentityProviderOption(current)
      return providerOption ? { kind: 'external_provider_choice' } : null
    }
    const login = await firstVisibleRole(current, 'button', normalLoginPatterns)
      || await firstVisibleRole(current, 'link', normalLoginPatterns)
    if (!login) {
      const providerOption = await thirdPartyIdentityProviderOption(current)
      return providerOption ? { kind: 'external_provider_choice' } : null
    }
    const actionKey = `${current.url()}|${await login.innerText().catch(() => '')}`
    if (actionKey === lastActionKey) return null
    lastActionKey = actionKey
    actionClicks += 1
    await login.click()
    return null
  })
}

async function fillWorkflowEmail(current, email) {
  const input = await waitForOAuthPage('OpenAI 页面中找不到邮箱输入框', () => (
    firstVisibleInput(current, (metadata) => /email/.test(metadata))
  ))
  await input.fill(email)
  await clickOAuthContinue(current)
}

function isSignupAccountCreationRejectionText(value) {
  const text = String(value || '').replace(/\s+/g, ' ').trim()
  if (!text) return false
  return /(?:we\s+(?:could not|couldn't|were unable to|weren't able to)|unable to|could not|couldn't|cannot|can't)\s+(?:create|set up)\s+(?:your\s+)?account|(?:there was|we encountered|something went wrong)\s+(?:a\s+)?(?:problem|error)?.*?(?:creating|create).*?(?:account)|account\s+(?:could not be|was not)\s+created|无法(?:为您)?创建(?:您的|此|该|当前)?(?:账号|账户)|(?:账号|账户)创建失败|无法完成(?:账号|账户)创建/i.test(text)
}

async function signupAccountCreationRejection(current) {
  const candidates = current.locator('[role="alert"]:visible, [role="alertdialog"]:visible, h1:visible, h2:visible, p:visible')
  const texts = await candidates.allTextContents().catch(() => [])
  const matched = texts.find((text) => isSignupAccountCreationRejectionText(text))
  if (matched) return matched.replace(/\s+/g, ' ').trim()

  // A few OpenAI builds render the failure inside an unannotated form block.
  // The wording match remains deliberately narrow so a slow transition or a
  // generic "Create account" heading can never revoke a valid invitation.
  const body = await oauthBody(current)
  return isSignupAccountCreationRejectionText(body) ? body : ''
}

async function waitForEmailVerificationChallenge(current, description) {
  let codeOptionClicked = false
  const result = await waitForOAuthPage(description, async () => {
    const body = await oauthBody(current)
    const verification = await verificationInputs(current)
    if (isReauthorizationEmailCodePage(body, verification)) return { kind: 'email_code' }

    const rejection = await signupAccountCreationRejection(current)
    if (rejection) return { kind: 'rejected', message: rejection }

    const passwordInput = await firstVisibleInput(current, (metadata) => /password/.test(metadata))
    if (!passwordInput) return null
    if (!codeOptionClicked) {
      const codeOption = await firstVisibleRole(current, 'button', [
        /continue with (?:an? )?(?:email )?code/i,
        /use (?:an? )?(?:email )?(?:verification )?code/i,
        /email me (?:an? )?code/i,
        /send (?:an? )?(?:login )?code/i,
        /使用.*验证码|发送.*验证码/i
      ]) || await firstVisibleRole(current, 'link', [
        /continue with (?:an? )?(?:email )?code/i,
        /use (?:an? )?(?:email )?(?:verification )?code/i,
        /email me (?:an? )?code/i,
        /send (?:an? )?(?:login )?code/i,
        /使用.*验证码|发送.*验证码/i
      ])
      if (codeOption) {
        codeOptionClicked = true
        await codeOption.click()
        return null
      }
    }
    return { kind: 'password' }
  })
  if (result.kind === 'rejected') throw new Error(`OpenAI 无法创建当前临时邮箱账号：${result.message}`)
  if (result.kind === 'password') throw new Error('OpenAI 当前只显示密码登录，未提供邮箱验证码入口')
}

async function fillLoginPassword(current, password) {
  const input = await waitForOAuthPage('OpenAI 登录页中找不到密码输入框', () => (
    firstVisibleInput(current, (metadata) => /password/.test(metadata))
  ))
  await input.fill(password)
  await clickOAuthContinue(current)
}

async function verificationInputs(current) {
  const candidates = current.locator('input[autocomplete="one-time-code"], input[inputmode="numeric"], input[name*="code" i], input[id*="code" i]')
  const visible = []
  const count = await candidates.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const candidate = candidates.nth(index)
    if (await candidate.isVisible().catch(() => false)) visible.push(candidate)
  }
  return visible
}

async function fillVerificationCode(current, rawCode) {
  const code = String(rawCode || '').replace(/\s+/g, '')
  if (!/^\d{4,10}$/.test(code)) throw new Error('验证码格式无效')
  const inputs = await waitForOAuthPage('OpenAI 页面中找不到验证码输入框', () => verificationInputs(current))
  if (inputs.length === 1) {
    await inputs[0].fill(code)
  } else {
    if (inputs.length < code.length) throw new Error('OpenAI 验证码输入框数量与验证码不一致')
    for (let index = 0; index < code.length; index += 1) await inputs[index].fill(code[index])
  }
  // Some OpenAI verification pages submit as soon as the final digit is
  // entered. In that case the Continue button disappears before Playwright
  // can resolve it; accept the navigation once the code fields are gone.
  const continueButton = await firstVisibleRole(current, 'button', [/^continue$/i, /^next$/i, /^继续$/i, /^下一步$/i])
  if (continueButton) {
    await continueButton.click()
    return
  }
  await waitForOAuthPage('OpenAI 验证码已填写但页面未继续', async () => (
    (await verificationInputs(current)).length === 0
  ))
}

function isPhoneInputMetadata(metadata) {
  return /tel|phone|mobile|手机号/.test(metadata)
    && !/code|otp|one-time|验证码/.test(metadata)
}

function isEmailInputMetadata(metadata) {
  return /email|邮箱/.test(metadata)
    && !/code|otp|one-time|验证码/.test(metadata)
}

async function waitForPhonePage(current) {
  await waitForOAuthPage('OpenAI 未进入手机号验证页面', async () => {
    const body = await oauthBody(current)
    return /phone number|required.*phone|手机号|电话号码/i.test(body)
      && Boolean(await firstVisibleInput(current, isPhoneInputMetadata))
  })
}

async function openAIPhoneRecoveryState(current) {
  const phoneInput = await firstVisibleInput(current, isPhoneInputMetadata)
  if (phoneInput) return 'phone'

  const passwordInput = await firstVisibleInput(current, (metadata) => /password/.test(metadata))
  if (passwordInput) return 'password'

  const emailInput = await firstVisibleInput(current, isEmailInputMetadata)
  if (emailInput) return 'email'

  const body = await oauthBody(current)
  if (/invalid authorization step|invalid_auth_step|授权步骤无效/i.test(body)) return 'invalid_auth_step'
  const codeInputs = await verificationInputs(current)
  if (codeInputs.length > 0 && /phone|text message|sms|mobile|短信|手机/i.test(body)) return 'sms_code'
  if (codeInputs.length > 0 && /email|inbox|邮箱|验证邮件/i.test(body)) return 'email_code'
  if (/localhost:1455\/auth\/callback/.test(current.url())) return 'callback'
  if (/workspace|工作空间|空间/i.test(body)) return 'workspace'
  return 'unknown'
}

async function recoverOpenAIPhoneEntry(current, workflow) {
  const password = String(workflow.loginPassword || '')
  const email = normalizeWorkflowEmail(workflow.inviteEmail)
  let backAttempts = 0
  let restartedOAuth = false

  for (let attempt = 0; attempt < 8; attempt += 1) {
    const state = await openAIPhoneRecoveryState(current)
    if (state === 'phone') return 'phone'
    if (state === 'email_code') return 'email_code'
    if (state === 'callback' || state === 'workspace') {
      throw new Error('OpenAI 重新授权已跳过手机号页面，请在内嵌浏览器核对后继续')
    }
    if (state === 'invalid_auth_step') {
      if (restartedOAuth) {
        throw new Error('OpenAI 授权步骤已失效，请重新生成 XIASS 官方 OAuth 链接后继续')
      }
      restartedOAuth = true
      backAttempts = 0
      await current.goto(workflow.authURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
      await sleep(500)
      continue
    }
    if (state === 'password') {
      if (password) {
        await fillLoginPassword(current, password)
        await sleep(500)
        continue
      }
      await waitForEmailVerificationChallenge(current, '重新进入 OAuth 后未提供邮箱验证码登录')
      return 'email_code'
    }
    if (state === 'email') {
      if (!email) throw new Error('重新进入手机号步骤时找不到本次注册邮箱')
      await fillWorkflowEmail(current, email)
      await sleep(500)
      continue
    }

    if (backAttempts < 2) {
      backAttempts += 1
      await current.goBack({ waitUntil: 'domcontentloaded', timeout: operationTimeout }).catch(() => null)
      await sleep(500)
      continue
    }
    if (!restartedOAuth) {
      restartedOAuth = true
      backAttempts = 0
      await current.goto(workflow.authURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
      await sleep(500)
      continue
    }
    throw new Error('OpenAI 无法返回手机号输入页，请在内嵌浏览器退回手机号步骤后继续')
  }
  throw new Error('OpenAI 重新进入手机号步骤超时')
}

async function submitPhoneOnOpenAI(current, rawPhone) {
  const phone = String(rawPhone || '').replace(/[\s()-]/g, '')
  if (!/^\+[1-9]\d{6,14}$/.test(phone)) throw new Error('手机号必须是完整国际格式')
  const input = await waitForOAuthPage('OpenAI 手机号页面中找不到输入框', () => (
    firstVisibleInput(current, isPhoneInputMetadata)
  ))
  await input.fill(phone)

  const textMessage = await firstVisibleRole(current, 'radio', [/text message|短信/i])
    || await firstVisibleRole(current, 'button', [/text message|短信/i])
    || await firstVisibleRole(current, 'option', [/text message|短信/i])
  if (textMessage) await textMessage.click().catch(() => undefined)

  const submit = await firstVisibleRole(current, 'button', [/send (?:a )?code|send sms|text me|continue|next|发送.*验证码|发送短信|继续|下一步/i])
  if (!submit) throw new Error('OpenAI 手机号页面中找不到发送短信按钮')
  await submit.click()

  const verificationState = await waitForOAuthPage('OpenAI 未进入短信验证码页面', async () => {
    const body = await oauthBody(current)
    if (/invalid authorization step|invalid_auth_step|授权步骤无效/i.test(body)) {
      return 'invalid_auth_step'
    }
    if (/phone.*(?:invalid|unavailable|used too many)|too many.*phone|无法使用.*号码|手机号.*(?:不可用|次数过多)/i.test(body)) {
      return 'phone_rejected'
    }
    return (await verificationInputs(current)).length > 0
      && /phone|text message|sms|mobile|短信|手机/i.test(body)
  })
  if (verificationState === 'phone_rejected') {
    throw new Error('当前手机号不可用或使用次数过多，请确认换号后继续')
  }
  if (verificationState === 'invalid_auth_step') {
    throw new Error('OpenAI 授权步骤已失效，将从 XIASS 官方 OAuth 链接重新进入手机号步骤')
  }
}

async function submitPhoneWithOAuthRecovery(current, workflow, phone) {
  try {
    await submitPhoneOnOpenAI(current, phone)
    return 'submitted'
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error || '')
    if (!/授权步骤已失效|invalid_auth_step|invalid authorization step/i.test(message)) throw error
    const recoveryState = await recoverOpenAIPhoneEntry(current, workflow)
    if (recoveryState === 'email_code') return 'email_code'
    await submitPhoneOnOpenAI(current, phone)
    return 'submitted'
  }
}

async function fillProfile(current) {
  await sleep(5000)
  await waitForOAuthPage('OpenAI 未进入姓名和年龄页面', async () => {
    const body = await oauthBody(current)
    return /name|yourself|about you|姓名|年龄|介绍.*自己/i.test(body)
      && (await current.locator('input:visible').count().catch(() => 0)) > 0
  })

  const nameInput = await firstVisibleInput(current, (metadata) => /name|姓名/.test(metadata))
    || await firstVisible(current.locator('input:visible'))
  if (!nameInput) throw new Error('OpenAI 资料页中找不到姓名输入框')
  await nameInput.fill('black')

  const ageInput = await firstVisibleInput(current, (metadata) => /age|birth|年龄|出生/.test(metadata))
  if (ageInput) {
    await ageInput.fill('26')
  } else {
    const visibleInputs = current.locator('input:visible')
    if (await visibleInputs.count() > 1) {
      await visibleInputs.nth(1).fill('26')
    } else {
      throw new Error('OpenAI 资料页中找不到年龄输入框')
    }
  }
  await clickOAuthContinue(current)
}

async function chooseDefaultWorkspace(current) {
  await sleep(10000)
  await waitForOAuthPage('OpenAI 未进入默认工作空间页面', async () => {
    const body = await oauthBody(current)
    return /workspace|工作空间|空间/i.test(body)
      || /localhost:1455\/auth\/callback/.test(current.url())
  })
  if (/localhost:1455\/auth\/callback/.test(current.url())) return

  const defaultChoice = await firstVisibleRole(current, 'radio', [/default|workspace|默认|工作空间/i])
    || await firstVisibleRole(current, 'button', [/default workspace|默认工作空间/i])
  if (defaultChoice) await defaultChoice.click().catch(() => undefined)
  await clickOAuthContinue(current)
}

function callbackURLFromNavigationEntries(entries, expectedState) {
  for (const entry of [...(Array.isArray(entries) ? entries : [])].reverse()) {
    try {
      const parsed = new URL(String(entry?.url || ''))
      if (
        ['http:', 'https:'].includes(parsed.protocol)
        && parsed.hostname === 'localhost'
        && parsed.pathname === '/auth/callback'
        && Boolean(parsed.searchParams.get('code'))
        && parsed.searchParams.get('state') === expectedState
      ) return parsed.toString()
    } catch {
      // Ignore unrelated or browser-internal history entries.
    }
  }
  return ''
}

async function workflowCallbackURLFromPage(current, workflow) {
  const expectedState = new URL(workflow.authURL).searchParams.get('state') || ''
  const direct = callbackURLFromNavigationEntries([{ url: current.url() }], expectedState)
  if (direct) return direct

  let cdpSession
  try {
    // Chromium replaces page.url() with chrome-error://chromewebdata when the
    // localhost receiver is absent, while the address bar keeps code/state.
    cdpSession = await current.context().newCDPSession(current)
    const history = await cdpSession.send('Page.getNavigationHistory')
    return callbackURLFromNavigationEntries(history?.entries, expectedState)
  } catch {
    return ''
  } finally {
    await cdpSession?.detach().catch(() => undefined)
  }
}

async function captureWorkflowCallback(current, workflow) {
  const raw = await waitForOAuthPage('浏览器未出现 OAuth 回调 URL', () => (
    workflowCallbackURLFromPage(current, workflow)
  ))
  return validateWorkflowCallbackURL(raw, workflow)
}

function beginWorkflowEmailChallenge(workflow, purpose, nodeKey, message) {
  workflow.emailCodePurpose = purpose
  workflow.emailCodeGeneration = Math.max(0, Number(workflow.emailCodeGeneration || 0)) + 1
  setWorkflowNode(workflow, nodeKey, 'waiting', message)
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
  persistWorkflowState()
}

async function waitForPrivateRegistrationComplete(current) {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const result = await waitForOAuthPage('ChatGPT 邮箱验证码已提交，但账号注册尚未完成', async () => {
      const body = await oauthBody(current)
      const rejection = await signupAccountCreationRejection(current)
      if (rejection) return { kind: 'rejected', message: rejection }
      const provider = thirdPartyIdentityProviderPage(current)
      if (provider) return { kind: 'external_provider', provider }

      const verification = await verificationInputs(current)
      const emailInput = await firstVisibleInput(current, isEmailInputMetadata)
      const visibleInputs = await current.locator('input:visible').count().catch(() => 0)
      if (/name|yourself|about you|姓名|年龄|介绍.*自己/i.test(body) && visibleInputs > 0) {
        return { kind: 'profile' }
      }
      const hostname = oauthPageHostname(current)
      if (
        hostname === 'chatgpt.com'
        && verification.length === 0
        && !emailInput
        && !/welcome back|log in|sign in|登录/i.test(body)
      ) return { kind: 'registered' }
      return null
    })
    if (result.kind === 'registered') return
    if (result.kind === 'rejected') throw new Error(`OpenAI 无法创建当前临时邮箱账号：${result.message}`)
    if (result.kind === 'external_provider') throw new Error(`ChatGPT 注册已转至${result.provider}登录，无法继续临时邮箱流程`)
    if (result.kind === 'profile') {
      await fillProfile(current)
      continue
    }
  }
  throw new Error('ChatGPT 注册资料已提交，但没有返回可用的登录会话')
}

async function runPrivateRegistrationUntilMailbox(workflow) {
  setWorkflowNode(workflow, 'signup', 'running', '正在新建隐私浏览器并打开 ChatGPT')
  const current = await navigatePrivateWorkflowPage(workflow, chatGPTHomeURL, { create: true })
  const emailInput = await selectPrivateRegistrationEntry(current)
  completeWorkflowNode(workflow, 'signup', '已在独立隐私会话进入 ChatGPT 邮箱登录/注册')

  setWorkflowNode(workflow, 'email', 'running', '正在填入本次新邮箱')
  await emailInput.fill(workflow.inviteEmail)
  await clickOAuthContinue(current)
  completeWorkflowNode(workflow, 'email', '新邮箱已提交给 ChatGPT')

  setWorkflowNode(workflow, 'mail', 'running', '正在等待 ChatGPT 发送注册验证码')
  await waitForEmailVerificationChallenge(current, 'ChatGPT 未进入邮箱验证码页面')
  completeWorkflowNode(workflow, 'mail', 'ChatGPT 已发送注册邮箱验证码')
  beginWorkflowEmailChallenge(workflow, 'registration', 'mailbox', '正在轮询新邮箱中的注册验证码')
}

async function executeMemberInvitation(workflow) {
  if (!workflow.registrationCompleted) throw new Error('新邮箱账号尚未注册成功，禁止提前修改 Team 成员')

  setWorkflowNode(workflow, 'members', 'running', '正在刷新并读取实时成员页面')
  const initial = await listMembers({ forceRefresh: true, requireEmails: true })
  const selected = initial.members.find((member) => normalizeEmail(member.email) === workflow.seatEmail)

  if (workflow.seatAlreadyRemoved) {
    const replaceable = initial.members.find((member) => displayRole(member.role) === 'member' && !isProtectedTeamMember(member))
    if (replaceable) throw new Error('实时成员列表中仍有可替换的普通成员，请选择该成员后按常规流程操作')
    const unverified = initial.members.find((member) => !isProtectedTeamMember(member))
    if (unverified) throw new Error('成员角色无法确认受保护状态，请在浏览器中核对后重新刷新成员列表')
    completeWorkflowNode(workflow, 'members', '已确认当前成员席位状态')
    completeWorkflowNode(workflow, 'remove', '普通成员席位已由人工腾出，未执行移除')
  } else if (selected) {
    assertRemovableMember(selected)
    completeWorkflowNode(workflow, 'members', '已读取并确认可替换的普通成员席位')
    setWorkflowNode(workflow, 'remove', 'running', '正在通过成员行菜单移除已选普通成员')
    await removeMember(workflow.seatEmail)
    completeWorkflowNode(workflow, 'remove', '已从实时成员页面确认成员移除')
  } else {
    const unexpectedReplaceable = initial.members.find((member) => displayRole(member.role) === 'member' && !isProtectedTeamMember(member))
    if (unexpectedReplaceable) throw new Error('已选成员不在实时成员列表中，但仍存在其他普通成员，请刷新后重新选择')
    completeWorkflowNode(workflow, 'members', '已确认原成员不再位于实时成员列表')
    completeWorkflowNode(workflow, 'remove', '已确认所选成员此前已成功移除')
  }

  setWorkflowNode(workflow, 'invite', 'running', '正在原生 Invite member 弹窗提交已注册邮箱')
  const currentMembers = await listMembers({ forceRefresh: false })
  const invitationAccepted = currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)
  if (invitationAccepted) {
    completeInviteStep(workflow)
    completeInviteNodes(workflow, '无需重复提交邀请', '新邮箱已出现在成员列表中')
  } else if (workflow.inviteSubmitted) {
    await confirmInviteNode(workflow)
  } else {
    await inviteMember(workflow.inviteEmail, { workflow, confirm: false })
    completeWorkflowNode(workflow, 'invite', '已在原生页面提交邀请')
    await confirmInviteNode(workflow)
  }
}

function completeRegisteredOAuthLoginNode(workflow, message) {
  if (workflowNodeState(workflow, 'password')?.status !== 'completed') {
    completeWorkflowNode(workflow, 'password', message)
  }
}

function completeRegistrationOnlyNodes(workflow, message) {
  for (const key of ['phone', 'sms_confirm', 'phone_submit', 'sms_poll', 'sms_code', 'profile_wait', 'profile']) {
    if (workflowNodeState(workflow, key)?.status !== 'completed') completeWorkflowNode(workflow, key, message)
  }
}

async function finishRegisteredOAuth(workflow, current, state) {
  completeRegistrationOnlyNodes(workflow, '本次 OAuth 未要求手机号或资料验证')
  if (state.kind === 'callback') {
    completeWorkflowNode(workflow, 'workspace_wait', 'OpenAI 已直接返回 OAuth 回调')
    completeWorkflowNode(workflow, 'workspace', '本次授权无需选择工作空间')
  } else {
    setWorkflowNode(workflow, 'workspace_wait', 'running', '正在等待默认工作空间页面')
    await chooseDefaultWorkspace(current)
    completeWorkflowNode(workflow, 'workspace_wait', '默认工作空间页面已出现')
    completeWorkflowNode(workflow, 'workspace', '已选择默认工作空间并继续')
  }
  setWorkflowNode(workflow, 'callback', 'running', '正在读取浏览器地址栏中的 OAuth 回调')
  workflow.callbackURL = state.callbackURL || await captureWorkflowCallback(current, workflow)
  completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已捕获并校验')
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
  workflow.currentNodeKey = 'import'
  workflow.status = 'callback_ready'
  persistWorkflowState()
}

async function advanceRegisteredOAuth(workflow, current, state) {
  if (state.kind === 'external_provider') {
    throw new Error(`OAuth 已转至${state.provider}登录，临时邮箱流程不会点击第三方登录`)
  }
  if (state.kind === 'sms_code') throw new Error('OAuth 页面仍停留在旧短信验证码步骤，请重新开始本次授权')

  if (state.kind === 'account_chooser') {
    const otherAccount = await firstVisibleRole(current, 'button', loginForAnotherAccountPatterns)
      || await firstVisibleRole(current, 'link', loginForAnotherAccountPatterns)
    if (!otherAccount) throw new Error('OAuth 账号选择页中找不到“使用其他账号登录”')
    await otherAccount.click()
    return advanceRegisteredOAuth(workflow, current, await waitForRegisteredOAuthNextState(current, workflow))
  }

  if (state.kind === 'email') {
    setWorkflowNode(workflow, 'password', 'running', 'OAuth 登录页已出现，正在填入新邮箱')
    await state.input.fill(workflow.inviteEmail)
    await clickOAuthContinue(current)
    workflow.oauthEmailSubmitted = true
    persistWorkflowState()
    return advanceRegisteredOAuth(workflow, current, await waitForRegisteredOAuthNextState(current, workflow))
  }

  if (state.kind === 'password') {
    setWorkflowNode(workflow, 'password', 'running', 'OAuth 正在切换为邮箱验证码登录')
    await waitForEmailVerificationChallenge(current, 'OAuth 登录页未提供邮箱验证码入口')
    beginWorkflowEmailChallenge(workflow, 'oauth_login', 'password', '正在轮询 OAuth 登录邮箱验证码')
    return
  }

  if (state.kind === 'email_code') {
    beginWorkflowEmailChallenge(workflow, 'oauth_login', 'password', '正在轮询 OAuth 登录邮箱验证码')
    return
  }

  if (state.kind === 'phone') {
    completeRegisteredOAuthLoginNode(workflow, workflow.oauthEmailSubmitted
      ? '新邮箱与登录验证码已通过，正在进入手机号验证'
      : '已复用隐私页中的新邮箱登录状态')
    workflow.emailCodePurpose = ''
    completeWorkflowNode(workflow, 'phone', '已进入 OpenAI 手机号验证页面')
    setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待 XIASS Team 自动化领取手机号')
    workflow.status = 'manual_required'
    activeWorkflowID = workflow.id
    persistWorkflowState()
    return
  }

  if (state.kind === 'workspace' || state.kind === 'callback') {
    completeRegisteredOAuthLoginNode(workflow, workflow.oauthEmailSubmitted
      ? '新邮箱登录验证已完成'
      : '已复用隐私页中的新邮箱登录状态')
    workflow.emailCodePurpose = ''
    await finishRegisteredOAuth(workflow, current, state)
    return
  }

  const login = await firstVisibleRole(current, 'button', normalLoginPatterns)
    || await firstVisibleRole(current, 'link', normalLoginPatterns)
  if (!login) throw new Error('OAuth 页面中找不到登录入口、邮箱输入框或已登录状态')
  await login.click()
  await advanceRegisteredOAuth(workflow, current, await waitForRegisteredOAuthNextState(current, workflow))
}

async function runRegisteredAccountOAuth(workflow) {
  setWorkflowNode(workflow, 'oauth', 'running', '正在同一隐私会话打开 XIASS 官方 OAuth')
  const current = await navigatePrivateWorkflowPage(workflow, workflow.authURL, { validateOAuth: true })
  completeWorkflowNode(workflow, 'oauth', 'XIASS 官方 OAuth 已在注册账号的隐私会话中打开')
  await advanceRegisteredOAuth(workflow, current, await waitForRegisteredOAuthNextState(current, workflow))
}

function completeReauthorizationOnlyNodes(workflow) {
  for (const key of ['phone', 'sms_confirm', 'phone_submit', 'sms_poll', 'sms_code', 'profile_wait', 'profile']) {
    completeWorkflowNode(workflow, key, '已有账号重新授权无需执行此步骤')
  }
}

async function finishOAuthReauthorization(workflow, current, state) {
  const active = current || await workflowBrowserPage(workflow)
  completeReauthorizationOnlyNodes(workflow)
  if (state?.kind === 'callback') {
    completeWorkflowNode(workflow, 'workspace_wait', 'OpenAI 已直接返回 OAuth 回调，无需选择工作空间')
    completeWorkflowNode(workflow, 'workspace', '本次授权未出现工作空间选择，已直接继续')
  } else {
    setWorkflowNode(workflow, 'workspace_wait', 'running', '正在等待默认工作空间页面')
    await chooseDefaultWorkspace(active)
    completeWorkflowNode(workflow, 'workspace_wait', '默认工作空间页面已出现')
    completeWorkflowNode(workflow, 'workspace', '已选择默认工作空间并继续')
  }
  setWorkflowNode(workflow, 'callback', 'running', '正在读取浏览器地址栏中的 OAuth 回调')
  workflow.callbackURL = state?.callbackURL || await captureWorkflowCallback(active, workflow)
  completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已捕获并校验')
  setWorkflowNode(workflow, 'import', 'waiting', '等待将新 OAuth 凭据覆盖导入原 Team 账号')
  workflow.currentNodeKey = 'import'
  workflow.reauthorizationManualReason = ''
  workflow.status = 'callback_ready'
  persistWorkflowState()
}

async function waitForReauthorizationNextState(current, workflow) {
  return waitForOAuthPage('OpenAI 登录后未进入可识别的授权页面', async () => {
    const state = await reauthorizationNextState(current, workflow)
    return state.kind === 'unknown' ? null : state
  })
}

function reauthorizationNodeCompleted(workflow, key) {
  return workflowNodeState(workflow, key)?.status === 'completed'
}

function markReauthorizationManualRequirement(workflow, key, reason, message) {
  workflow.reauthorizationManualReason = reason
  setWorkflowNode(workflow, key, 'waiting', message)
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
  persistWorkflowState()
}

function completeReauthorizationSignInNode(workflow, message) {
  if (!reauthorizationNodeCompleted(workflow, 'signup')) completeWorkflowNode(workflow, 'signup', message)
}

function completeReauthorizationEmailNode(workflow, message) {
  if (!reauthorizationNodeCompleted(workflow, 'email')) completeWorkflowNode(workflow, 'email', message)
  workflow.reauthorizationEmailSubmitted = true
  persistWorkflowState()
}

function completeReauthorizationPasswordNode(workflow, message) {
  if (!reauthorizationNodeCompleted(workflow, 'password')) completeWorkflowNode(workflow, 'password', message)
}

function skipReauthorizationEmailVerification(workflow) {
  if (!reauthorizationNodeCompleted(workflow, 'mail')) completeWorkflowNode(workflow, 'mail', '本次重新授权不需要邮箱验证码')
  if (!reauthorizationNodeCompleted(workflow, 'mailbox')) completeWorkflowNode(workflow, 'mailbox', '已跳过邮箱轮询')
  if (!reauthorizationNodeCompleted(workflow, 'email_code')) completeWorkflowNode(workflow, 'email_code', '已跳过邮箱验证码填入')
}

function requireManualReauthorizationLogin(workflow, state) {
  if (state.kind === 'external_provider' || state.kind === 'external_provider_choice') {
    const provider = state.provider ? `${state.provider} ` : ''
    markReauthorizationManualRequirement(
      workflow,
      workflow.reauthorizationEmailSubmitted ? 'password' : 'signup',
      'external_provider',
      `OpenAI 已转至${provider}身份登录；XIASS 不会点击 Google、Apple、Microsoft 或 SSO，请在内嵌浏览器完成目标账号登录后点击继续自动化`
    )
    return
  }
  if (state.kind === 'workspace' || state.kind === 'callback' || state.kind === 'password' || state.kind === 'email_code') {
    markReauthorizationManualRequirement(
      workflow,
      'signup',
      'existing_session',
      `OAuth 标签页当前已有登录状态。为避免导入非目标账号，请在内嵌浏览器切换至 ${workflow.inviteEmail} 后点击继续自动化`
    )
    return
  }
  markReauthorizationManualRequirement(
    workflow,
    'signup',
    'unknown_login_state',
    'OpenAI 当前页面需要人工处理；XIASS 不会猜测或点击第三方登录，请在内嵌浏览器完成目标账号登录后点击继续自动化'
  )
}

async function advanceOAuthReauthorization(workflow, current, state, { operatorConfirmed = false } = {}) {
  if (state.kind === 'external_provider' || state.kind === 'external_provider_choice') {
    requireManualReauthorizationLogin(workflow, state)
    return
  }

  if (!workflow.reauthorizationEmailSubmitted) {
    if (state.kind === 'email') {
      setWorkflowNode(workflow, 'signup', 'running', '正在进入已有账号登录路径')
      completeReauthorizationSignInNode(workflow, '已进入 OpenAI 已有账号登录路径')
      setWorkflowNode(workflow, 'email', 'running', '正在填入目标登录邮箱')
      await state.input.fill(workflow.inviteEmail)
      await clickOAuthContinue(current)
      completeReauthorizationEmailNode(workflow, '目标登录邮箱已提交')
      return advanceOAuthReauthorization(workflow, current, await waitForReauthorizationNextState(current, workflow))
    }
    if (!operatorConfirmed) {
      requireManualReauthorizationLogin(workflow, state)
      return
    }
    if (state.kind === 'unknown') {
      requireManualReauthorizationLogin(workflow, state)
      return
    }
    completeReauthorizationSignInNode(workflow, '管理员已在内嵌浏览器切换至目标账号')
    completeReauthorizationEmailNode(workflow, '管理员已在内嵌浏览器提交目标登录邮箱')
  }

  if (state.kind === 'email') {
    setWorkflowNode(workflow, 'email', 'running', '正在重新填入目标登录邮箱')
    await state.input.fill(workflow.inviteEmail)
    await clickOAuthContinue(current)
    completeReauthorizationEmailNode(workflow, '目标登录邮箱已提交')
    return advanceOAuthReauthorization(workflow, current, await waitForReauthorizationNextState(current, workflow), { operatorConfirmed })
  }

  if (state.kind === 'password') {
    const password = String(workflow.loginPassword || '')
    if (!password) {
      markReauthorizationManualRequirement(
        workflow,
        'password',
        'password',
        'OpenAI 当前要求登录密码；该账号未保存密码，请在内嵌浏览器输入后点击继续自动化'
      )
      return
    }
    setWorkflowNode(workflow, 'password', 'running', 'OpenAI 已要求密码，正在填入服务器保存的登录密码')
    await fillLoginPassword(current, password)
    completeReauthorizationPasswordNode(workflow, '登录密码已自动填入并提交')
    return advanceOAuthReauthorization(workflow, current, await waitForReauthorizationNextState(current, workflow), { operatorConfirmed })
  }

  if (state.kind === 'totp') {
    if (!workflow.loginTOTPSecret || workflow.totpSubmitted) {
      markReauthorizationManualRequirement(workflow, 'password', 'totp', '请在内嵌浏览器完成 2FA 验证后继续')
      return
    }
    workflow.totpSubmitted = true
    setWorkflowNode(workflow, 'password', 'running', '正在填写该账号的实时 2FA 验证码')
    // Avoid submitting a code at the very end of its validity window.
    const remaining = 30000 - Date.now() % 30000
    if (remaining < 3000) await sleep(remaining + 100)
    if (workflow.cancelRequested || workflow.pauseRequested) return
    await fillVerificationCode(current, generateTOTP(workflow.loginTOTPSecret))
    return advanceOAuthReauthorization(workflow, current, await waitForReauthorizationNextState(current, workflow), { operatorConfirmed })
  }

  if (state.kind === 'email_code') {
    const passwordWasManual = workflow.reauthorizationManualReason === 'password'
    completeReauthorizationPasswordNode(workflow, passwordWasManual
      ? '管理员已在内嵌浏览器提交登录密码'
      : 'OpenAI 本次未要求登录密码')
    completeWorkflowNode(workflow, 'mail', 'OpenAI 已发送重新登录邮箱验证码')
    workflow.reauthorizationManualReason = 'email_code'
    beginWorkflowEmailChallenge(workflow, 'reauthorization', 'mailbox', '正在轮询该历史邮箱中的 OpenAI 验证码')
    return
  }

  if (state.kind === 'workspace' || state.kind === 'callback') {
    const passwordWasManual = workflow.reauthorizationManualReason === 'password'
    completeReauthorizationPasswordNode(workflow, passwordWasManual
      ? '管理员已在内嵌浏览器提交登录密码'
      : 'OpenAI 本次未要求登录密码')
    skipReauthorizationEmailVerification(workflow)
    await finishOAuthReauthorization(workflow, current, state)
    return
  }

  requireManualReauthorizationLogin(workflow, state)
}

async function runOAuthReauthorization(workflow, { resumeCurrentPage = false, operatorConfirmed = false } = {}) {
  let current
  if (resumeCurrentPage) {
    current = await workflowBrowserPage(workflow)
  } else {
    setWorkflowNode(workflow, 'oauth', 'running', '正在独立 OAuth 标签页打开官方 PKCE URL')
    await navigatePersistentBrowser(workflow.authURL)
    current = managedOAuthPage
    if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
    completeWorkflowNode(workflow, 'oauth', 'XIASS 官方 OAuth 页面已打开')
  }
  if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
  const state = await selectLoginForAnotherAccount(current, workflow)
  await advanceOAuthReauthorization(workflow, current, state, { operatorConfirmed })
}

async function continueWorkflowWithEmailCode(workflow, code) {
  const current = await workflowBrowserPage(workflow)
  const purpose = workflow.emailCodePurpose || (workflow.mode === 'reauthorization' ? 'reauthorization' : 'registration')

  if (purpose === 'oauth_login') {
    setWorkflowNode(workflow, 'password', 'running', '正在将 OAuth 登录邮箱验证码填入 OpenAI')
    await fillVerificationCode(current, code)
    completeRegisteredOAuthLoginNode(workflow, 'OAuth 登录邮箱验证码已自动填入并提交')
    workflow.emailCodePurpose = ''
    await advanceRegisteredOAuth(workflow, current, await waitForRegisteredOAuthNextState(current, workflow))
    return
  }

  completeWorkflowNode(workflow, 'mailbox', 'Cloudflare 已读取 OpenAI 验证邮件')
  setWorkflowNode(workflow, 'email_code', 'running', '正在将邮箱验证码填入 OpenAI')
  await fillVerificationCode(current, code)
  completeWorkflowNode(workflow, 'email_code', '邮箱验证码已自动填入并提交')
  workflow.emailCodePurpose = ''

  if (workflow.mode === 'reauthorization') {
    await advanceOAuthReauthorization(
      workflow,
      current,
      await waitForReauthorizationNextState(current, workflow),
      { operatorConfirmed: true }
    )
    return
  }

  await waitForPrivateRegistrationComplete(current)
  workflow.registrationCompleted = true
  persistWorkflowState()
  await executeMemberInvitation(workflow)
  await runRegisteredAccountOAuth(workflow)
}

function resetPhoneReplacementNodes(workflow) {
  for (const key of ['phone_submit', 'sms_poll', 'sms_code']) {
    const node = workflowNodeState(workflow, key)
    if (!node) continue
    node.status = 'pending'
    node.message = ''
  }
  workflow.failedNodeKey = ''
  workflow.error = ''
  workflow.currentNodeKey = 'phone_submit'
  persistWorkflowState()
}

async function continueWorkflowWithPhone(workflow, phone, replacing = false) {
  const current = await workflowBrowserPage(workflow)
  if (replacing) {
    setWorkflowNode(workflow, 'phone_submit', 'running', '旧号码已释放，正在返回 OpenAI 手机号步骤')
    const recoveryState = await recoverOpenAIPhoneEntry(current, workflow)
    completeWorkflowNode(workflow, 'sms_confirm', 'XIASS Team 自动化已更换手机号')
    if (recoveryState === 'email_code') {
      beginWorkflowEmailChallenge(workflow, 'oauth_login', 'password', '重新登录已发送邮箱验证码，正在轮询')
      return
    }
    setWorkflowNode(workflow, 'phone_submit', 'running', '已返回手机号页，正在填入新号码并选择 Text message')
  } else {
    completeWorkflowNode(workflow, 'sms_confirm', 'XIASS Team 自动化已领取手机号')
    setWorkflowNode(workflow, 'phone_submit', 'running', '正在填入完整号码并选择 Text message')
  }
  const submissionState = await submitPhoneWithOAuthRecovery(current, workflow, phone)
  if (submissionState === 'email_code') {
    beginWorkflowEmailChallenge(workflow, 'oauth_login', 'password', '重新进入 OAuth 后已发送邮箱验证码，正在轮询')
    return
  }
  workflow.lastSubmittedPhone = phone
  completeWorkflowNode(workflow, 'phone_submit', replacing
    ? '新号码已提交并选择 Text message'
    : '号码已提交并选择 Text message')
  setWorkflowNode(workflow, 'sms_poll', 'waiting', '正在通过 XIASS SMS 服务轮询验证码')
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
  persistWorkflowState()
}

async function postSMSNextState(current, workflow) {
  const callbackURL = await workflowCallbackURLFromPage(current, workflow)
  if (callbackURL) return { kind: 'callback', callbackURL }
  const body = await oauthBody(current)
  const visibleInputs = await current.locator('input:visible').count().catch(() => 0)
  if (/name|yourself|about you|姓名|年龄|介绍.*自己/i.test(body) && visibleInputs > 0) return { kind: 'profile' }
  if (isReauthorizationWorkspacePage(body)) return { kind: 'workspace' }
  return { kind: 'unknown' }
}

async function continueWorkflowWithSMSCode(workflow, code) {
  const current = await workflowBrowserPage(workflow)
  completeWorkflowNode(workflow, 'sms_poll', 'XIASS SMS 服务已读取验证码')
  setWorkflowNode(workflow, 'sms_code', 'running', '正在填入短信验证码并继续')
  await fillVerificationCode(current, code)
  completeWorkflowNode(workflow, 'sms_code', '短信验证码已自动填入并提交')

  let state = await waitForOAuthPage('短信验证码已提交，但 OpenAI 未进入资料、工作空间或回调页面', async () => {
    const next = await postSMSNextState(current, workflow)
    return next.kind === 'unknown' ? null : next
  })
  if (state.kind === 'profile') {
    setWorkflowNode(workflow, 'profile_wait', 'running', '正在等待并填写资料页面')
    await fillProfile(current)
    completeWorkflowNode(workflow, 'profile_wait', '资料页面已出现')
    completeWorkflowNode(workflow, 'profile', '已填写姓名 black 和年龄 26 并继续')
    state = await waitForOAuthPage('资料已提交，但 OpenAI 未进入工作空间或回调页面', async () => {
      const next = await postSMSNextState(current, workflow)
      return ['workspace', 'callback'].includes(next.kind) ? next : null
    })
  } else {
    completeWorkflowNode(workflow, 'profile_wait', '本次流程未出现资料页面')
    completeWorkflowNode(workflow, 'profile', '无需填写姓名和年龄')
  }

  if (state.kind === 'callback') {
    completeWorkflowNode(workflow, 'workspace_wait', 'OpenAI 已直接返回 OAuth 回调')
    completeWorkflowNode(workflow, 'workspace', '本次授权无需选择工作空间')
  } else {
    setWorkflowNode(workflow, 'workspace_wait', 'running', '正在等待默认工作空间页面')
    await chooseDefaultWorkspace(current)
    completeWorkflowNode(workflow, 'workspace_wait', '默认工作空间页面已出现')
    completeWorkflowNode(workflow, 'workspace', '已选择默认工作空间并继续')
  }

  setWorkflowNode(workflow, 'callback', 'running', '正在读取浏览器地址栏中的 OAuth 回调')
  workflow.callbackURL = state.callbackURL || await captureWorkflowCallback(current, workflow)
  completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已捕获并校验')
  workflow.status = 'callback_ready'
  workflow.currentNodeKey = 'import'
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
}

function workflowNode(key, number, label) {
  return { key, number, label, status: 'pending', message: '' }
}

const workflowNodeDefinitions = [
  ['signup', '隐私页注册新邮箱'],
  ['email', '填入注册邮箱'],
  ['mail', '发送注册邮箱验证码'],
  ['mailbox', '读取注册邮箱验证码'],
  ['email_code', '提交注册邮箱验证码'],
  ['members', '读取成员席位'],
  ['remove', '移除已选成员'],
  ['invite', '提交成员邀请'],
  ['invite_confirm', '确认 Pending invites'],
  ['oauth', '同一隐私页打开 OAuth'],
  ['password', '完成 OAuth 邮箱登录验证'],
  ['phone', '进入手机号页面'],
  ['sms_confirm', '自动领取手机号'],
  ['phone_submit', '填入号码并选择 Text message'],
  ['sms_poll', '轮询短信验证码'],
  ['sms_code', '自动填入短信验证码'],
  ['profile_wait', '等待资料页 5 秒'],
  ['profile', '填写 black / 26'],
  ['workspace_wait', '等待工作空间 10 秒'],
  ['workspace', '默认工作空间继续'],
  ['callback', '捕获 OAuth 回调'],
  ['import', '按勾选配置导入 XIASS']
]

function createWorkflow(seatEmail, inviteEmail, authURL, oauthSessionID, seatAlreadyRemoved) {
  const id = crypto.randomBytes(24).toString('base64url')
  const now = Date.now()
  return {
    id,
    seatEmail,
    seatAlreadyRemoved,
    inviteEmail,
    authURL,
    oauthSessionID,
    createdAt: now,
    expiresAt: now + workflowTTL,
    status: 'running',
    error: '',
    failedNodeKey: '',
    inviteSubmitted: false,
    inviteSubmittedAt: 0,
    inviteConfirmed: false,
    inviteConfirmedAt: 0,
    registrationCompleted: false,
    oauthEmailSubmitted: false,
    emailCodePurpose: '',
    emailCodeGeneration: 0,
    callbackURL: '',
    lastSubmittedPhone: '',
    pauseRequested: false,
    pausedFromStatus: '',
    pausedNodeKey: '',
    currentNodeKey: '',
    nodes: workflowNodeDefinitions.map(([key, label], index) => workflowNode(key, index + 1, label))
  }
}

function createReauthorizationWorkflow(accountID, email, password, authURL, oauthSessionID, totpSecret = '') {
  const workflow = createWorkflow('', email, authURL, oauthSessionID, true)
  workflow.mode = 'reauthorization'
  workflow.targetAccountID = accountID
  workflow.loginPassword = password
  // Reauthorization material is encrypted in the account, never in workflow snapshots.
  Object.defineProperty(workflow, 'loginTOTPSecret', { value: totpSecret ? normalizeTOTPSecret(totpSecret) : '', writable: true, enumerable: false })
  workflow.totpSubmitted = false
  workflow.reauthorizationEmailSubmitted = false
  workflow.reauthorizationManualReason = ''
  workflow.inviteSubmitted = true
  workflow.inviteConfirmed = true
  completeWorkflowNode(workflow, 'members', '已有 Team 账号重新授权，无需读取成员席位')
  completeWorkflowNode(workflow, 'remove', '已有 Team 账号重新授权，不移除成员')
  completeWorkflowNode(workflow, 'invite', '已有 Team 账号重新授权，不重复发送邀请')
  completeWorkflowNode(workflow, 'invite_confirm', '历史 Team 邮箱已绑定原账号')
  return workflow
}

function workflowSummary(workflow) {
  const oauthState = new URL(workflow.authURL).searchParams.get('state') || ''
  const summary = {
    schema_version: workflowProtocolVersion,
    id: workflow.id,
    status: workflow.status,
    expires_at: new Date(workflow.expiresAt).toISOString(),
    manual_required: workflow.status === 'manual_required',
    pause_requested: Boolean(workflow.pauseRequested),
    seat_already_removed: Boolean(workflow.seatAlreadyRemoved),
    oauth_session_id: workflow.oauthSessionID,
    oauth_state: oauthState,
    current_node: workflow.currentNodeKey || '',
    email_code_generation: Math.max(0, Number(workflow.emailCodeGeneration || 0)),
    email_code_purpose: workflow.emailCodePurpose || '',
    password_available: false,
    mode: workflow.mode === 'reauthorization' ? 'reauthorization' : 'registration',
    nodes: workflow.nodes.map(({ key, number, label, status, message }) => ({ key, number, label, status, ...(message ? { message } : {}) }))
  }
  if (workflow.mode === 'reauthorization' && Number.isSafeInteger(workflow.targetAccountID)) {
    summary.target_account_id = workflow.targetAccountID
  }
  if (workflow.error) summary.error = workflow.error
  // The callback is deliberately held only in process memory. It is returned
  // to the authenticated XIASS admin caller after the operator pastes it,
  // so the existing state-validated import endpoint can consume it.
  if (['callback_ready', 'completed'].includes(workflow.status) && workflow.callbackURL) summary.callback_url = workflow.callbackURL
  return summary
}

function setWorkflowNode(workflow, key, status, message = '') {
  if (workflow.cancelRequested && status !== 'cancelled') throw new Error('工作流已由管理员停止')
  if (workflow.pauseRequested && ['running', 'waiting'].includes(status)) {
    workflow.status = 'paused'
    workflow.pausedNodeKey = key
    workflow.currentNodeKey = key
    persistWorkflowState()
    throw new Error('工作流已暂停')
  }
  const node = workflow.nodes.find((item) => item.key === key)
  if (!node) return
  node.status = status
  node.message = message
  if (['running', 'waiting', 'failed'].includes(status)) workflow.currentNodeKey = key
  persistWorkflowState()
}

function completeWorkflowNode(workflow, key, message) {
  setWorkflowNode(workflow, key, 'completed', message)
}

function workflowNodeState(workflow, key) {
  return workflow.nodes.find((node) => node.key === key)
}

function workflowFailureNodeKey(workflow, fallbackKey) {
  return workflow.nodes.find((node) => node.status === 'running')?.key
    || workflow.currentNodeKey
    || fallbackKey
}

function completeInviteStep(workflow) {
  markWorkflowInviteSubmitted(workflow)
  workflow.inviteConfirmed = true
  workflow.inviteConfirmedAt = Date.now()
}

function completeInviteNodes(workflow, submissionMessage, confirmationMessage) {
  completeWorkflowNode(workflow, 'invite', submissionMessage)
  completeWorkflowNode(workflow, 'invite_confirm', confirmationMessage)
}

async function confirmInviteNode(workflow) {
  setWorkflowNode(workflow, 'invite_confirm', 'running', '正在核对 Members 和 Pending invites')
  const latestMembers = await listMembers({ forceRefresh: false })
  if (latestMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)) {
    completeInviteStep(workflow)
    completeWorkflowNode(workflow, 'invite_confirm', '临时邮箱已在实时成员列表中确认')
    return
  }
  const pending = await pendingInviteSnapshot({ forceRefresh: false, expectedEmail: workflow.inviteEmail, waitForExpectedEmail: true })
  if (!pending.emails.has(normalizeEmail(workflow.inviteEmail))) {
    throw new Error('Pending invites 中未找到目标临时邮箱，请在内嵌浏览器完成邀请后继续')
  }
  completeInviteStep(workflow)
  completeWorkflowNode(workflow, 'invite_confirm', '已在 Pending invites 精确匹配临时邮箱')
}

async function resumeFineWorkflowFromNextNode(workflow, requestedNextKey = '') {
  const nextKey = requestedNextKey || workflow.failedNodeKey
  const requestedNode = workflowNodeState(workflow, nextKey)
  if (!requestedNode) throw new Error('当前工作流没有可继续的节点')
  requestedNode.status = 'pending'
  requestedNode.message = ''
  workflow.failedNodeKey = ''
  workflow.error = ''
  workflow.status = 'running'

  try {
    if (['signup', 'email', 'mail', 'mailbox', 'email_code'].includes(nextKey) && !workflow.registrationCompleted) {
      for (const key of ['signup', 'email', 'mail', 'mailbox', 'email_code']) {
        const node = workflowNodeState(workflow, key)
        if (node) {
          node.status = 'pending'
          node.message = ''
        }
      }
      workflow.emailCodePurpose = ''
      await disposePrivateBrowserSession(workflow.id)
      await runPrivateRegistrationUntilMailbox(workflow)
      return
    }

    if (['members', 'remove', 'invite', 'invite_confirm'].includes(nextKey)) {
      await executeMemberInvitation(workflow)
      await runRegisteredAccountOAuth(workflow)
      return
    }

    if (['oauth', 'password'].includes(nextKey)) {
      resetOAuthWorkflowSteps(workflow)
      await runRegisteredAccountOAuth(workflow)
      return
    }

    const current = await workflowBrowserPage(workflow)
    if (nextKey === 'phone') {
      await waitForPhonePage(current)
      completeWorkflowNode(workflow, 'phone', '已进入 OpenAI 手机号验证页面')
      setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待 XIASS Team 自动化领取手机号')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (nextKey === 'sms_confirm') {
      setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待 XIASS Team 自动化领取手机号')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (nextKey === 'phone_submit') {
      setWorkflowNode(workflow, 'phone_submit', 'waiting', '等待 XIASS 将已确认的完整国际号码填入 OpenAI')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (['sms_poll', 'sms_code'].includes(nextKey)) {
      await waitForOAuthPage('OpenAI 未停留在短信验证码页面', async () => (await verificationInputs(current)).length > 0)
      setWorkflowNode(workflow, 'sms_poll', 'waiting', '正在通过 XIASS SMS 服务轮询验证码')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (await recoverWorkflowCallback(workflow)) return
    throw new Error('当前页面状态无法自动恢复，请取消后重新开始')
  } catch (error) {
    const active = workflow.nodes.find((node) => node.status === 'running')
    markWorkflowNodeFailed(workflow, active?.key || nextKey, error)
  }
}

function redactWorkflowError(error) {
  const raw = error instanceof Error ? error.message : String(error || 'unknown error')
  return raw
    .replace(/https?:\/\/[^\s]+/gi, '外部页面')
    .replace(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi, '邮箱')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 240) || '自动化步骤未完成'
}

function pruneWorkflows() {
  const now = Date.now()
  let changed = false
  for (const [id, workflow] of workflows.entries()) {
    if (workflow.expiresAt > now) continue
    if (activeWorkflowID === id) activeWorkflowID = ''
    workflows.delete(id)
    void disposePrivateBrowserSession(id)
    changed = true
  }
  if (changed) persistWorkflowState()
}

function activeWorkflow() {
  pruneWorkflows()
  if (!activeWorkflowID) return undefined
  const workflow = workflows.get(activeWorkflowID)
  if (!workflow || !['running', 'manual_required', 'callback_ready', 'failed', 'paused'].includes(workflow.status)) {
    activeWorkflowID = ''
    persistWorkflowState()
    return undefined
  }
  return workflow
}

async function activeWorkflowStatus() {
  const workflow = activeWorkflow()
  if (!workflow) return { schema_version: workflowProtocolVersion, active: false }
  return { schema_version: workflowProtocolVersion, active: true, workflow: await workflowStatus(workflow.id) }
}

function validateWorkflowCallbackURL(value, workflow) {
  const raw = String(value || '').trim()
  if (!raw || raw.length > 8192) throw new Error('回调 URL 无效')
  let parsed
  try {
    parsed = new URL(raw)
  } catch {
    throw new Error('回调 URL 无效')
  }
  const code = parsed.searchParams.get('code')?.trim() || ''
  const state = parsed.searchParams.get('state')?.trim() || ''
  const expectedState = new URL(workflow.authURL).searchParams.get('state')?.trim() || ''
  if (!code || !state || !expectedState || state !== expectedState) {
    throw new Error('回调 state 与当前 XIASS OAuth 会话不匹配')
  }
  if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error('回调 URL 协议无效')
  return parsed.toString()
}

async function submitWorkflowCallback(id, value) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'cancelled') throw new Error('当前工作流已停止，请重新开始')
  if (!workflow.inviteConfirmed) throw new Error('临时邮箱邀请尚未完成，暂不能提交 OAuth 回调')

  workflow.callbackURL = validateWorkflowCallbackURL(value, workflow)
  workflow.error = ''
  workflow.status = 'callback_ready'
  workflow.currentNodeKey = 'import'
  setWorkflowNode(workflow, 'callback', 'completed', 'OAuth 回调 code/state 已捕获并校验')
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
  return workflowSummary(workflow)
}

function restartableOAuthWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (!['manual_required', 'failed'].includes(workflow.status)) {
    throw new Error('当前工作流尚未进入可重新授权的状态')
  }
  if (!workflow.inviteConfirmed) {
    throw new Error('临时邮箱邀请尚未完成，暂不能只重启 OAuth')
  }
  return workflow
}

function resetOAuthWorkflowSteps(workflow) {
  workflow.totpSubmitted = false
  const startIndex = workflow.nodes.findIndex((node) => node.key === 'oauth')
  const reauthorizationLoginNodes = new Set(['signup', 'email', 'mail', 'mailbox', 'email_code'])
  for (let index = 0; index < workflow.nodes.length; index += 1) {
    if (index < Math.max(0, startIndex) && !(workflow.mode === 'reauthorization' && reauthorizationLoginNodes.has(workflow.nodes[index].key))) {
      continue
    }
    workflow.nodes[index].status = 'pending'
    workflow.nodes[index].message = ''
  }
  workflow.callbackURL = ''
  workflow.reauthorizationEmailSubmitted = false
  workflow.reauthorizationManualReason = ''
  workflow.oauthEmailSubmitted = false
  workflow.emailCodePurpose = ''
  workflow.currentNodeKey = 'oauth'
  workflow.error = ''
  persistWorkflowState()
}

async function restartOAuthWorkflow(id, value, oauthSessionIDValue) {
  const workflow = restartableOAuthWorkflow(id)
  const authURL = validateOpenAIAuthURL(value)
  const oauthSessionID = validateOAuthSessionID(oauthSessionIDValue)
  resetOAuthWorkflowSteps(workflow)
  workflow.authURL = authURL
  workflow.oauthSessionID = oauthSessionID
  const action = workflow.mode === 'reauthorization'
    ? () => runOAuthReauthorization(workflow)
    : workflow.registrationCompleted
      ? () => runRegisteredAccountOAuth(workflow)
      : () => runPrivateRegistrationUntilMailbox(workflow)
  return scheduleWorkflowNodeAction(workflow, 'oauth', action)
}

async function executeWorkflow(workflow) {
  try {
    await runPrivateRegistrationUntilMailbox(workflow)
  } catch (error) {
    const activeNode = workflow.nodes.find((node) => node.status === 'running')
    markWorkflowNodeFailed(workflow, activeNode?.key || workflow.currentNodeKey || 'signup', error)
  }
}

function markWorkflowNodeFailed(workflow, nodeKey, error) {
  if (workflow.cancelRequested || workflow.status === 'cancelled') return '工作流已由管理员停止'
  if (workflow.pauseRequested || workflow.status === 'paused') {
    workflow.status = 'paused'
    workflow.pausedNodeKey = workflow.pausedNodeKey || nodeKey
    workflow.currentNodeKey = workflow.pausedNodeKey
    persistWorkflowState()
    return '工作流已暂停'
  }
  const message = redactWorkflowError(error)
  workflow.status = 'failed'
  workflow.error = message
  workflow.failedNodeKey = nodeKey
  workflow.currentNodeKey = nodeKey
  setWorkflowNode(workflow, nodeKey, 'failed', message)
  activeWorkflowID = workflow.id
  return message
}

function validateWorkflowCode(value) {
  const code = String(value || '').replace(/\s+/g, '')
  if (!/^\d{4,10}$/.test(code)) throw new Error('验证码格式无效')
  return code
}

function workflowForAutomationInput(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'cancelled') throw new Error('当前工作流已停止，请重新开始')
  if (workflow.status === 'paused' || workflow.pauseRequested) throw new Error('当前工作流已暂停，请先继续自动化')
  if (workflow.status === 'running') throw new Error('当前自动化节点仍在执行')
  return workflow
}

function scheduleWorkflowNodeAction(workflow, nodeKey, action) {
  workflow.status = 'running'
  workflow.error = ''
  workflow.failedNodeKey = ''
  setWorkflowNode(workflow, nodeKey, 'running', workflowNodeState(workflow, nodeKey)?.message || '正在执行')
  activeWorkflowID = workflow.id
  void runExclusive(async () => {
    try {
      await action()
    } catch (error) {
      markWorkflowNodeFailed(workflow, workflowFailureNodeKey(workflow, nodeKey), error)
    }
  })
  return workflowSummary(workflow)
}

function submitWorkflowEmailCode(id, value) {
  const workflow = workflowForAutomationInput(id)
  if (!['mailbox', 'email_code', 'password'].includes(workflow.currentNodeKey)) throw new Error('当前页面尚未等待邮箱验证码')
  const code = validateWorkflowCode(value)
  const nodeKey = workflow.emailCodePurpose === 'oauth_login' ? 'password' : 'email_code'
  return scheduleWorkflowNodeAction(workflow, nodeKey, () => continueWorkflowWithEmailCode(workflow, code))
}

function submitWorkflowPhone(id, value) {
  const workflow = workflowForAutomationInput(id)
  const previousNode = workflow.currentNodeKey
  const previousStatus = workflow.status
  if (!['sms_confirm', 'phone_submit', 'sms_poll', 'sms_code'].includes(previousNode)) {
    throw new Error('当前页面尚未等待手机号')
  }
  const phone = String(value || '').replace(/[\s()-]/g, '')
  if (!/^\+[1-9]\d{6,14}$/.test(phone)) throw new Error('手机号必须是完整国际格式')
  const replacing = ['sms_poll', 'sms_code'].includes(previousNode)
    || (previousStatus === 'failed' && previousNode === 'phone_submit')
    || (Boolean(workflow.lastSubmittedPhone) && workflow.lastSubmittedPhone !== phone)
  if (replacing) resetPhoneReplacementNodes(workflow)
  return scheduleWorkflowNodeAction(workflow, 'phone_submit', () => continueWorkflowWithPhone(workflow, phone, replacing))
}

function submitWorkflowSMSCode(id, value) {
  const workflow = workflowForAutomationInput(id)
  if (!['sms_poll', 'sms_code'].includes(workflow.currentNodeKey)) throw new Error('当前页面尚未等待短信验证码')
  const code = validateWorkflowCode(value)
  return scheduleWorkflowNodeAction(workflow, 'sms_code', () => continueWorkflowWithSMSCode(workflow, code))
}

function completeWorkflowImport(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status !== 'callback_ready' || !workflow.callbackURL) throw new Error('OAuth 回调尚未就绪')
  completeWorkflowNode(workflow, 'import', workflow.mode === 'reauthorization'
    ? '新 OAuth 凭据已覆盖导入原 Team 账号'
    : '已按勾选的分组、优先级和并发导入 XIASS')
  workflow.status = 'completed'
  workflow.currentNodeKey = 'import'
  workflow.loginPassword = ''
  workflow.loginTOTPSecret = ''
  workflow.reauthorizationEmailSubmitted = false
  workflow.reauthorizationManualReason = ''
  workflow.emailCodePurpose = ''
  if (activeWorkflowID === workflow.id) activeWorkflowID = ''
  void disposePrivateBrowserSession(workflow.id)
  persistWorkflowState()
  return workflowSummary(workflow)
}

async function startWorkflow(payload) {
  const seatAlreadyRemoved = payload?.seat_already_removed === true
  const rawSeatEmail = normalizeWorkflowEmail(payload?.seat_email)
  if (seatAlreadyRemoved && rawSeatEmail) throw new Error('人工腾位工作流不能携带待移除成员')
  const seatEmail = seatAlreadyRemoved ? '' : validateWorkflowEmail(rawSeatEmail, '成员邮箱')
  const inviteEmail = validateWorkflowEmail(payload?.invite_email, '临时邮箱')
  if (seatEmail && seatEmail === inviteEmail) throw new Error('临时邮箱不能与待移除成员相同')
  if (payload?.confirmed !== true) throw new Error('需要确认移除成员和发送邀请后才能开始')
  const authURL = validateOpenAIAuthURL(payload?.auth_url)
  const oauthSessionID = validateOAuthSessionID(payload?.oauth_session_id)
  if (activeWorkflow()) throw new Error('已有 Team 子号工作流正在进行，请先完成或取消当前工作流')

  const workflow = createWorkflow(seatEmail, inviteEmail, authURL, oauthSessionID, seatAlreadyRemoved)
  workflows.set(workflow.id, workflow)
  activeWorkflowID = workflow.id
  persistWorkflowState()
  // Return immediately so the UI can show the operation timeline while the
  // shared Chromium service serially performs the destructive actions.
  void runExclusive(() => executeWorkflow(workflow))
  return workflowSummary(workflow)
}

async function startReauthorizationWorkflow(payload) {
  const accountID = Number(payload?.account_id)
  if (!Number.isSafeInteger(accountID) || accountID <= 0) throw new Error('Team 子号账号 ID 无效')
  const email = validateWorkflowEmail(payload?.email, 'Team 子号邮箱')
  const password = String(payload?.password || '')
  if (password && (password.length > 2048 || password.trim() === '')) {
    throw new Error('Team 子号登录密码无效')
  }
  const authURL = validateOpenAIAuthURL(payload?.auth_url)
  const oauthSessionID = validateOAuthSessionID(payload?.oauth_session_id)
  if (activeWorkflow()) throw new Error('已有 Team 子号工作流正在进行，请先完成或取消当前工作流')

  const workflow = createReauthorizationWorkflow(accountID, email, password, authURL, oauthSessionID, payload?.totp_secret || '')
  workflows.set(workflow.id, workflow)
  activeWorkflowID = workflow.id
  persistWorkflowState()
  void runExclusive(() => runOAuthReauthorization(workflow).catch((error) => {
    const activeNode = workflow.nodes.find((node) => node.status === 'running')
    markWorkflowNodeFailed(workflow, activeNode?.key || workflow.currentNodeKey || 'oauth', error)
  }))
  return workflowSummary(workflow)
}

async function continueWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'paused' || workflow.pauseRequested) return resumePausedWorkflow(workflow)
  if (workflow.status === 'manual_required' && workflow.mode === 'reauthorization') {
    const current = activeWorkflow()
    if (current && current.id !== workflow.id) throw new Error('已有其他 Team 子号工作流正在进行')
    workflow.status = 'running'
    workflow.error = ''
    workflow.failedNodeKey = ''
    activeWorkflowID = workflow.id
    void runExclusive(async () => {
      try {
        await runOAuthReauthorization(workflow, { resumeCurrentPage: true, operatorConfirmed: true })
      } catch (error) {
        markWorkflowNodeFailed(workflow, workflowFailureNodeKey(workflow, workflow.currentNodeKey || 'oauth'), error)
      }
    })
    persistWorkflowState()
    return workflowSummary(workflow)
  }
  if (workflow.status !== 'failed') throw new Error('当前工作流无需继续或仍在执行中')
  const current = activeWorkflow()
  if (current && current.id !== workflow.id) throw new Error('已有其他 Team 子号工作流正在进行')

  if (workflow.failedNodeKey) {
    workflow.status = 'running'
    workflow.error = ''
    activeWorkflowID = workflow.id
    void runExclusive(async () => {
      if (await recoverWorkflowCallback(workflow)) return
      if (workflow.mode === 'reauthorization') {
        resetOAuthWorkflowSteps(workflow)
        await runOAuthReauthorization(workflow)
        return
      }
      await resumeFineWorkflowFromNextNode(workflow)
    })
    return workflowSummary(workflow)
  }
  throw new Error('当前工作流没有可继续的新版自动化节点，请重新开始')
}

async function recoverWorkflowCallback(workflow) {
  let current
  try {
    current = await workflowBrowserPage(workflow)
  } catch {
    // A member-page failure can occur before an OAuth tab exists. That is a
    // normal fine-node recovery case, not a callback-recovery failure.
    return false
  }
  const callbackURL = await workflowCallbackURLFromPage(current, workflow)
  if (!callbackURL) return false

  const failedNode = workflowNodeState(workflow, workflow.failedNodeKey)
  if (failedNode?.status === 'failed') {
    completeWorkflowNode(workflow, failedNode.key, '该节点已完成，已从浏览器地址栏恢复 OAuth 回调')
  }
  workflow.callbackURL = validateWorkflowCallbackURL(callbackURL, workflow)
  workflow.error = ''
  workflow.failedNodeKey = ''
  completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已从浏览器地址栏捕获并校验')
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
  workflow.currentNodeKey = 'import'
  workflow.status = 'callback_ready'
  persistWorkflowState()
  return true
}

function workflowStatus(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  return workflowSummary(workflow)
}

function workflowSecret(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.mode === 'reauthorization' || !workflow.registrationCompleted || !workflow.inviteConfirmed) {
    throw new Error('当前 Team 子号工作流尚未完成注册和邀请确认')
  }
  if (!['callback_ready', 'completed'].includes(workflow.status) || !workflow.callbackURL) {
    throw new Error('当前 Team 子号 OAuth 回调尚未就绪')
  }
  return { email: workflow.inviteEmail, password: '' }
}

function cancelWorkflowState(workflow) {
  if (!workflow || !['running', 'manual_required', 'callback_ready', 'failed', 'paused'].includes(workflow.status)) return workflow
  const activeNode = workflow.nodes.find((node) => ['running', 'waiting', 'failed'].includes(node.status))
    || workflow.nodes.find((node) => node.status === 'pending')
  if (activeNode) {
    activeNode.status = 'cancelled'
    activeNode.message = '工作流已由管理员停止，可从第一步重新开始'
  }
  workflow.cancelRequested = true
  workflow.status = 'cancelled'
  workflow.error = ''
  workflow.failedNodeKey = ''
  workflow.loginPassword = ''
  workflow.loginTOTPSecret = ''
  workflow.reauthorizationEmailSubmitted = false
  workflow.reauthorizationManualReason = ''
  workflow.emailCodePurpose = ''
  workflow.pauseRequested = false
  workflow.pausedFromStatus = ''
  workflow.pausedNodeKey = ''
  return workflow
}

function pauseWorkflowState(workflow) {
  if (!workflow || !['running', 'manual_required'].includes(workflow.status)) return workflow
  if (workflow.pauseRequested) return workflow
  workflow.pauseRequested = true
  workflow.pausedFromStatus = workflow.status
  workflow.pausedNodeKey = workflow.currentNodeKey || workflow.nodes.find((node) => ['running', 'waiting'].includes(node.status))?.key || ''
  workflow.status = 'paused'
  persistWorkflowState()
  return workflow
}

function pauseWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  pauseWorkflowState(workflow)
  return workflowSummary(workflow)
}

function resumePausedWorkflow(workflow) {
  if (!workflow.pauseRequested && workflow.status !== 'paused') throw new Error('当前工作流未暂停')
  const previousStatus = workflow.pausedFromStatus
  const resumeNodeKey = workflow.pausedNodeKey || workflow.currentNodeKey
  workflow.pauseRequested = false
  workflow.pausedFromStatus = ''
  workflow.pausedNodeKey = ''

  const currentNode = workflowNodeState(workflow, resumeNodeKey)
  if (previousStatus === 'manual_required' || currentNode?.status === 'waiting') {
    workflow.status = 'manual_required'
    workflow.currentNodeKey = resumeNodeKey
    persistWorkflowState()
    return workflowSummary(workflow)
  }

  workflow.status = 'running'
  activeWorkflowID = workflow.id
  void runExclusive(async () => {
    try {
      if (workflow.mode === 'reauthorization') {
        resetOAuthWorkflowSteps(workflow)
        await runOAuthReauthorization(workflow)
      } else if (!resumeNodeKey || resumeNodeKey === 'members') {
        await executeWorkflow(workflow)
      } else {
        await resumeFineWorkflowFromNextNode(workflow, resumeNodeKey)
      }
    } catch (error) {
      markWorkflowNodeFailed(workflow, workflowFailureNodeKey(workflow, resumeNodeKey || 'members'), error)
    }
  })
  persistWorkflowState()
  return workflowSummary(workflow)
}

function cancelWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  cancelWorkflowState(workflow)
  if (activeWorkflowID === workflow.id) activeWorkflowID = ''
  void disposePrivateBrowserSession(workflow.id)
  persistWorkflowState()
  return workflowSummary(workflow)
}

async function runExclusive(task) {
  const next = operation.then(task, task)
  operation = next.catch(() => undefined)
  return next
}

async function readBody(req) {
  let body = ''
  for await (const chunk of req) {
    body += chunk
    if (Buffer.byteLength(body) > requestBodyLimit) throw new Error('请求体过大')
  }
  try {
    return body ? JSON.parse(body) : {}
  } catch {
    throw new Error('请求体不是有效 JSON')
  }
}

async function handle(req, res) {
  const path = normalizedPath(req)
  if (req.method === 'GET' && path === '/healthz') {
    return json(res, 200, { ok: true, workflow_schema_version: workflowProtocolVersion })
  }
  if (req.method === 'GET' && path === '/readyz') {
    try {
      const connected = await browser()
      return json(res, connected.isConnected() ? 200 : 503, {
        ok: connected.isConnected(),
        workflow_schema_version: workflowProtocolVersion
      })
    } catch {
      return json(res, 503, { ok: false, workflow_schema_version: workflowProtocolVersion })
    }
  }
  if (!authorized(req)) return json(res, 401, { error: 'automation service authentication required' })

  if (path === '/batch-oauth/tasks' || path.startsWith('/batch-oauth/tasks/')) {
    try {
      if (req.method === 'POST' && path === '/batch-oauth/tasks') {
        return json(res, 200, await batchOAuth.start(await readBody(req)))
      }
      const match = path.match(/^\/batch-oauth\/tasks\/([A-Za-z0-9_-]{16,128})(?:\/(cancel|phone|sms-code))?$/)
      if (!match) return json(res, 404, { error: 'not found' })
      const [, id, action] = match
      if (req.method === 'GET' && !action) {
        const owner = Number(new URL(req.url, 'http://127.0.0.1').searchParams.get('owner_id'))
        return json(res, 200, batchOAuth.get(id, owner))
      }
      if (req.method !== 'POST' || !action) return json(res, 405, { error: 'method not allowed' })
      const body = await readBody(req)
      const owner = body.owner_id
      if (action === 'cancel') return json(res, 200, await batchOAuth.cancel(id, owner))
      if (action === 'phone') return json(res, 200, batchOAuth.phone(id, owner, body.phone))
      return json(res, 200, batchOAuth.smsCode(id, owner, body.code))
    } catch (error) {
      // Never include external page text, callback URLs or request payloads.
      return json(res, [400, 404, 409, 429].includes(error?.statusCode) ? error.statusCode : 503, { error: 'batch OAuth action failed' })
    }
  }

  if (req.method === 'GET' && path === '/workflows/active') {
    try {
      return json(res, 200, await activeWorkflowStatus())
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  const workflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})$/)
  const pauseWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/pause$/)
  const workflowSecretMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/secret$/)
  // Progress inspection is intentionally outside the serialized browser queue.
  // A remove/invite workflow can take several seconds; queuing this read behind
  // it would leave the XIASS page stuck at "running" until all browser actions
  // finished, defeating the step-by-step workspace. This path only reads the
  // in-memory workflow state and visible OAuth controls; it never navigates or
  // submits an external page.
  if (workflowMatch && req.method === 'GET') {
    try {
      return json(res, 200, await workflowStatus(workflowMatch[1]))
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  // Cancellation must bypass the serialized browser queue so it can stop a
  // long-running page wait. The active action observes cancelRequested at the
  // next node boundary and cannot advance to a later external operation.
  if (workflowMatch && req.method === 'DELETE') {
    try {
      return json(res, 200, cancelWorkflow(workflowMatch[1]))
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  // Pausing is also out-of-band. A running page action may finish, but the
  // workflow cannot enter its next browser node until the operator resumes it.
  if (pauseWorkflowMatch && req.method === 'POST') {
    try {
      return json(res, 200, pauseWorkflow(pauseWorkflowMatch[1]))
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  try {
    const result = await runExclusive(async () => {
      if (req.method === 'GET' && path === '/members') return listMembers()
      if (req.method === 'POST' && path === '/members/refresh') return listMembers({ forceRefresh: true })
      if (req.method === 'POST' && path === '/members/inspect') {
        const result = await listMembers({ forceRefresh: true })
        return { ...result, operation: { type: 'inspect', confirmed: true } }
      }
      if (req.method === 'POST' && path === '/members/invite') {
        const body = await readBody(req)
        return inviteMember(String(body.email || '').trim())
      }
      if (req.method === 'DELETE' && path === '/members') {
        const body = await readBody(req)
        return removeMember(String(body.email || '').trim())
      }
      if (req.method === 'PATCH' && path === '/members') {
        const body = await readBody(req)
        return updateMember(String(body.email || '').trim(), String(body.role || 'member'))
      }
      if (req.method === 'POST' && path === '/workflows') {
        const body = await readBody(req)
        return startWorkflow(body)
      }
      if (req.method === 'POST' && path === '/workflows/reauthorize') {
        const body = await readBody(req)
        return startReauthorizationWorkflow(body)
      }
      if (workflowSecretMatch && req.method === 'GET') return workflowSecret(workflowSecretMatch[1])
      const continueWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/continue$/)
      if (continueWorkflowMatch && req.method === 'POST') return continueWorkflow(continueWorkflowMatch[1])
      const emailCodeWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/email-code$/)
      if (emailCodeWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowEmailCode(emailCodeWorkflowMatch[1], body?.code)
      }
      const phoneWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/phone$/)
      if (phoneWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowPhone(phoneWorkflowMatch[1], body?.phone)
      }
      const smsCodeWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/sms-code$/)
      if (smsCodeWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowSMSCode(smsCodeWorkflowMatch[1], body?.code)
      }
      const completeWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/complete$/)
      if (completeWorkflowMatch && req.method === 'POST') return completeWorkflowImport(completeWorkflowMatch[1])
      const callbackWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/callback$/)
      if (callbackWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowCallback(callbackWorkflowMatch[1], body?.callback_url)
      }
      const restartOAuthWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/restart-oauth$/)
      if (restartOAuthWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return restartOAuthWorkflow(restartOAuthWorkflowMatch[1], body?.auth_url, body?.oauth_session_id)
      }
      return null
    })
    if (result === null) return json(res, 404, { error: 'not found' })
    return json(res, 200, result)
  } catch (error) {
    return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
  }
}

const batchOAuth = new BatchOAuthRunner({
  browser: async () => {
    const connected = await browser()
    return { newContext: options => createProxyContext(connected, options) }
  },
  validateAuthURL: validateOpenAIAuthURL
})

if (process.env.NODE_ENV !== 'test') {
  restoreWorkflowState()
  http.createServer(handle).listen(port, '0.0.0.0', () => {
    console.log(`team-child-automation listening on ${port}`)
  })
}

export {
  handle,
  activateBrowserPage,
  callbackURLFromNavigationEntries,
  cancelWorkflowState,
  confirmOfficialMemberRemoval,
  completeReauthorizationOnlyNodes,
  completeWorkflowNode,
  createPrivateBrowserSession,
  createReauthorizationWorkflow,
  createWorkflow,
  decryptWorkflowState,
  encryptWorkflowState,
  fillVerificationCode,
  advanceRegisteredOAuth,
  beginWorkflowEmailChallenge,
  markWorkflowInviteSubmitted,
  pauseWorkflowState,
  pendingInviteEmailsFromTexts,
  pendingInviteRecord,
  pendingInvitesTabSelected,
  isSignupAccountCreationRejectionText,
  registeredOAuthNextState,
  reauthorizationNextState,
  advanceOAuthReauthorization,
  recoverOpenAIPhoneEntry,
  resetOAuthWorkflowSteps,
  reusableOAuthPage,
  resumePausedWorkflow,
  selectLoginForAnotherAccount,
  setWorkflowNode,
  submitInviteDialog,
  validateOAuthSessionID,
  validateWorkflowCode,
  workflowProtocolVersion,
  workflowNodeDefinitions,
  workflowFailureNodeKey,
  workflowSummary
}
