import { timingSafeEqual } from 'node:crypto'

import { generateTOTP as sharedGenerateTOTP, isAuthenticatorChallenge } from './totp.mjs'

const CALLBACK = 'http://localhost:1455/auth/callback'
const SMS_TIMEOUT = 180_000
const PHONE_CONFIRMATION_TIMEOUT = 5 * 60_000
const TTL = 15 * 60_000
const STEP_DELAY = 1_000
export const BATCH_OAUTH_REASONS = Object.freeze([
  '', 'sms_timeout', 'sms_confirmation_timeout', 'email_code_required',
  'captcha_required', 'account_blocked', 'manual_challenge', 'task_expired',
  'invalid_credentials', 'authenticator_required', 'phone_rejected',
  'proxy_unavailable', 'navigation_timeout', 'browser_context_lost',
  'page_interaction_failed', 'invalid_totp', 'invalid_sms_code'
])
const reasons = new Set(BATCH_OAUTH_REASONS)

function fail(code, statusCode = 409) {
  return Object.assign(new Error(code), { code, statusCode })
}

function decodeSecret(secret) {
  if (typeof secret !== 'string' || secret.length > 256) throw fail('invalid_input', 400)
  const raw = secret.replace(/ /g, '').toUpperCase().replace(/=+$/, '')
  if (!/^[A-Z2-7]+$/.test(raw) || ![0, 2, 4, 5, 7].includes(raw.length % 8)) {
    throw fail('invalid_input', 400)
  }
  let bits = 0
  let value = 0
  const bytes = []
  for (const char of raw) {
    value = (value << 5) | 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(char)
    bits += 5
    if (bits >= 8) {
      bits -= 8
      bytes.push((value >>> bits) & 255)
      value &= (1 << bits) - 1
    }
  }
  if (value !== 0 || bytes.length < 10) throw fail('invalid_input', 400)
  return Buffer.from(bytes)
}

export function generateTOTP(secret, now = Date.now()) {
  const key = decodeSecret(secret)
  try {
    if (!Number.isSafeInteger(now) || now < 0) throw fail('invalid_input', 400)
    return sharedGenerateTOTP(secret, now)
  } finally {
    key.fill(0)
  }
}

function ownerKey(owner) {
  if (typeof owner === 'number' && Number.isSafeInteger(owner) && owner > 0) return String(owner)
  if (typeof owner === 'string' && /^[1-9]\d{0,18}$/.test(owner)) return owner
  throw fail('not_found', 404)
}

function automationFailureReason(error, stage) {
  const message = error instanceof Error ? error.message : String(error || '')
  if (/ERR_(?:PROXY|SOCKS|TUNNEL)_|ECONNREFUSED|proxy[^\n]*(?:failed|refused|unavailable|unreachable)/i.test(message)) {
    return 'proxy_unavailable'
  }
  if (stage === 'opening' && /timeout|ERR_TIMED_OUT/i.test(message)) return 'navigation_timeout'
  if (/target page[^\n]*closed|browser[^\n]*closed|context[^\n]*closed|new page/i.test(message)) {
    return 'browser_context_lost'
  }
  return 'page_interaction_failed'
}

function proxyOptions(value) {
  if (value == null) return undefined
  try {
    if (typeof value !== 'object' || Array.isArray(value)) throw new Error()
    const url = new URL(value.server)
    if (!['http:', 'https:', 'socks5:'].includes(url.protocol)
      || !url.hostname || url.username || url.password || url.search || url.hash
      || !['', '/'].includes(url.pathname)) throw new Error()
    const result = { server: value.server }
    for (const key of ['username', 'password']) {
      if (value[key] !== undefined) {
        if (typeof value[key] !== 'string' || value[key].length > 2048) throw new Error()
        result[key] = value[key]
      }
    }
    return result
  } catch {
    throw fail('invalid_input', 400)
  }
}

async function firstVisible(locator) {
  for (let i = 0, n = await locator.count(); i < n; i++) {
    const item = locator.nth(i)
    if (await item.isVisible()) return item
  }
  return null
}

const defaults = {
  stepDelayMs: STEP_DELAY,
  onProgress() {},
  async oauthBody(page) { return (await page.locator('body').innerText()).replace(/\s+/g, ' ').trim() },
  async firstVisibleInput(page, matcher) {
    const inputs = page.locator('input')
    for (let i = 0, n = await inputs.count(); i < n; i++) {
      const input = inputs.nth(i)
      if (!(await input.isVisible())) continue
      const metadata = []
      for (const key of ['type', 'name', 'id', 'autocomplete', 'aria-label', 'placeholder']) {
        metadata.push(await input.getAttribute(key) || '')
      }
      if (matcher(metadata.join(' ').toLowerCase())) return input
    }
    return null
  },
  async firstVisibleRole(page, role, patterns) {
    for (const name of patterns) {
      const item = await firstVisible(page.getByRole(role, { name }))
      if (item) return item
    }
    return null
  },
  async verificationInputs(page) {
    const inputs = page.locator('input[autocomplete="one-time-code"], input[inputmode="numeric"], input[name*="code" i], input[id*="code" i]')
    const result = []
    for (let i = 0, n = await inputs.count(); i < n; i++) {
      if (await inputs.nth(i).isVisible()) result.push(inputs.nth(i))
    }
    return result
  }
}

// Parent may inject the same-named stateless server helpers. Never inject the
// existing shared-page/workflow helpers: each task owns only its new context.
export class BatchOAuthRunner {
  #browser
  #validate
  #h
  #tasks = new Map()
  #slots = 0

  constructor({ browser, validateAuthURL, helpers = {} }) {
    if (!browser || typeof validateAuthURL !== 'function') throw fail('invalid_dependencies', 500)
    this.#browser = browser
    this.#validate = validateAuthURL
    this.#h = { ...defaults, now: Date.now, setTimeout, clearTimeout, ...helpers }
  }

  async start(body) {
    if (!body || typeof body.task_id !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(body.task_id)
      || typeof body.email !== 'string' || body.email.length > 254
      || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(body.email)
      || typeof body.password !== 'string' || !body.password || body.password.length > 2048
      || typeof body.oauth_session_id !== 'string' || !/^[A-Za-z0-9_-]{16,128}$/.test(body.oauth_session_id)
      || typeof body.auth_url !== 'string' || body.auth_url.length > 8192) throw fail('invalid_input', 400)
    const owner = ownerKey(body.owner_id)
    const secret = body.totp_secret ?? ''
    if (secret !== '') decodeSecret(secret).fill(0)
    const proxy = proxyOptions(body.proxy)
    let auth
    try {
      const validated = await this.#validate(body.auth_url)
      if (validated === false) throw new Error()
      auth = new URL(typeof validated === 'string' ? validated : body.auth_url)
      if (auth.origin !== 'https://auth.openai.com' || auth.username || auth.password || auth.hash
        || auth.pathname !== '/oauth/authorize'
        || auth.searchParams.get('redirect_uri') !== CALLBACK
        || auth.searchParams.getAll('redirect_uri').length !== 1
        || auth.searchParams.getAll('state').length !== 1 || !auth.searchParams.get('state')) throw new Error()
    } catch {
      throw fail('invalid_auth_url', 400)
    }
    if (this.#tasks.has(body.task_id)) throw fail('task_exists')
    if (this.#slots >= 3) throw fail('capacity', 429)
    const task = {
      id: body.task_id, owner_id: body.owner_id, owner, status: 'running', stage: 'opening', reason: '',
      authURL: auth.toString(), state: auth.searchParams.get('state'),
      email: body.email.toLowerCase(), password: body.password, secret, proxy,
      rejected: new Set(), phone: '', pending: null, busy: false,
      emailSubmitted: false, expiresAt: this.#h.now() + TTL,
      stageDeadline: 0, smsDeadline: 0, context: null, page: null
    }
    this.#tasks.set(task.id, task)
    this.#slots++
    this.#report(task)
    task.ttlTimer = this.#timer(() => this.#expire(task), TTL)
    void this.#run(task)
    return this.#summary(task)
  }

  get(id, owner) { return this.#summary(this.#owned(id, owner)) }

  async cancel(id, owner) {
    const task = this.#owned(id, owner)
    this.#stop(task, 'canceled', '')
    if (task.context) await this.#close(task)
    return this.#summary(task)
  }

  // The authenticated backend owns confirmation and SMS-provider calls. These
  // two methods only deliver already-confirmed input to the current page.
  phone(id, owner, number) {
    const task = this.#owned(id, owner)
    const phone = typeof number === 'string' ? number.replace(/[\s()-]/g, '') : ''
    if (!/^\+[1-9]\d{6,14}$/.test(phone)) throw fail('invalid_input', 400)
    if (task.rejected.has(phone) || task.phone === phone) throw fail('phone_rejected')
    this.#input(task, 'phone_required', { kind: 'phone', value: phone })
    return this.#summary(task)
  }

  smsCode(id, owner, code) {
    const task = this.#owned(id, owner)
    if (typeof code !== 'string' || !/^\d{4,10}$/.test(code)) throw fail('invalid_input', 400)
    this.#input(task, 'sms_waiting', { kind: 'sms', value: code })
    return this.#summary(task)
  }

  #input(task, stage, input) {
    if (task.status !== 'running' || task.stage !== stage || task.busy || task.pending) throw fail('invalid_stage')
    task.pending = input
    task.wake?.()
  }

  #owned(id, owner) {
    const task = this.#tasks.get(id)
    if (!task || task.owner !== ownerKey(owner)) throw fail('not_found', 404)
    if (this.#h.now() >= task.expiresAt) {
      this.#expire(task)
      throw fail('not_found', 404)
    }
    this.#checkTimeout(task)
    return task
  }

  #summary(task) {
    const result = { id: task.id, owner_id: task.owner_id, status: task.status, stage: task.stage, reason: task.reason }
    // INTERNAL backend response only. Parent must never expose callback_url to
    // public task polling, access logs, persistence, or browser clients.
    if (task.status === 'completed' && task.callback) result.callback_url = task.callback
    return result
  }

  #timer(fn, ms) {
    const timer = this.#h.setTimeout(fn, ms)
    timer?.unref?.()
    return timer
  }

  #report(task) {
    this.#h.onProgress({ id: task.id, status: task.status, stage: task.stage, reason: task.reason })
  }

  #checkTimeout(task) {
    if (task.status !== 'running') return true
    if (this.#h.now() >= task.expiresAt) {
      this.#expire(task)
      return true
    }
    if (task.stageDeadline && this.#h.now() >= task.stageDeadline) {
      this.#stop(task, 'failed', task.stage === 'sms_waiting' ? 'sms_timeout' : 'sms_confirmation_timeout')
      return true
    }
    return false
  }

  #stage(task, stage) {
    if (task.status !== 'running' || task.stage === stage) return
    this.#h.clearTimeout(task.stageTimer)
    task.stage = stage
    task.stageDeadline = 0
    if (stage === 'phone_required') task.stageDeadline = this.#h.now() + PHONE_CONFIRMATION_TIMEOUT
    if (stage === 'sms_waiting') {
      // One deadline per submitted reservation, never renewed by polling or
      // re-observing its SMS screen. Only a different submitted phone resets it.
      task.smsDeadline ||= this.#h.now() + SMS_TIMEOUT
      task.stageDeadline = task.smsDeadline
    }
    if (task.stageDeadline) {
      task.stageTimer = this.#timer(() => this.#checkTimeout(task), Math.max(0, task.stageDeadline - this.#h.now()))
    }
    this.#report(task)
  }

  #expire(task) {
    this.#stop(task, 'failed', 'task_expired')
    task.callback = ''
    if (this.#tasks.get(task.id) === task) this.#tasks.delete(task.id)
  }

  #stop(task, status, reason) {
    if (task.status !== 'running') return
    task.status = status
    task.reason = reasons.has(reason) ? reason : 'manual_challenge'
    task.password = task.secret = task.authURL = ''
    task.proxy = undefined
    task.pending = null
    this.#h.clearTimeout(task.stageTimer)
    task.stageDeadline = 0
    this.#report(task)
    task.wake?.()
    if (task.context) void this.#close(task)
  }

  async #close(task) {
    if (!task.context) return false
    if (!task.closing) {
      task.closing = Promise.resolve().then(() => task.context.close()).then(() => true, () => false)
    }
    const closed = await task.closing
    if (!closed) task.closing = null
    return closed
  }

  #pause(task, ms = 250) {
    if (task.status !== 'running') return Promise.resolve()
    return new Promise((resolve) => {
      const timer = this.#timer(() => wake(), ms)
      const wake = () => {
        this.#h.clearTimeout(timer)
        if (task.wake === wake) task.wake = null
        resolve()
      }
      task.wake = wake
    })
  }

  async #stepPause(task) {
    const delay = Number(this.#h.stepDelayMs)
    if (task.status === 'running' && Number.isFinite(delay) && delay > 0) await this.#pause(task, delay)
  }

  #callback(task, raw) {
    if (task.status !== 'running') return false
    if (this.#checkTimeout(task)) return true
    let url
    try { url = new URL(raw) } catch { return false }
    if (url.hostname !== 'localhost' && !['127.0.0.1', '[::1]'].includes(url.hostname)) return false
    const state = Buffer.from(url.searchParams.get('state') || '')
    const expected = Buffer.from(task.state)
    if (url.origin !== 'http://localhost:1455' || url.pathname !== '/auth/callback'
      || url.username || url.password || url.hash || url.searchParams.has('error')
      || url.searchParams.getAll('state').length !== 1 || url.searchParams.getAll('code').length !== 1
      || !url.searchParams.get('code')?.trim() || state.length !== expected.length
      || !timingSafeEqual(state, expected) || !task.emailSubmitted) {
      this.#stop(task, 'blocked', 'manual_challenge')
      return true
    }
    task.callback = url.toString()
    this.#stage(task, 'callback_received')
    this.#stop(task, 'completed', '')
    return true
  }

  #trustedPage(task) {
    try {
      const url = new URL(task.page.url())
      return url.origin === 'https://auth.openai.com' && !url.username && !url.password
    } catch { return false }
  }

  async #inspect(page) {
    if (this.#h.inspectPage) return this.#h.inspectPage(page)
    const body = await this.#h.oauthBody(page)
    if (/captcha|verify (?:that )?you are human|checking your browser|验证您是人类|人机验证/i.test(body)) return { kind: 'captcha' }
    if (await firstVisible(page.locator('iframe[src*="captcha"], iframe[src*="challenges.cloudflare.com"]'))) return { kind: 'captcha' }
    if (/(?:your |this )?account (?:has been |is )?(?:deactivated|disabled|suspended|banned)|account_deactivated|账号.*(?:封禁|停用)/i.test(body)) return { kind: 'account_blocked' }
    if (/incorrect password|invalid (?:email or password|credentials)|wrong password/i.test(body)) return { kind: 'invalid_credentials' }
    if (/\/create-account|\/signup|\/about-you/.test(new URL(page.url()).pathname)
      || /tell us about yourself|create (?:a |your )password/i.test(body)) return { kind: 'signup' }
    const inputs = await this.#h.verificationInputs(page)
    if (/check your (?:email|inbox)|code (?:we |was )?sent to (?:your )?email|verify your email|邮箱验证码/i.test(body)) return { kind: 'email_code' }
    if (inputs.length) {
      const invalidCode = /incorrect (?:verification )?code|invalid (?:verification )?code|wrong code|code (?:is|was) invalid|验证码.*(?:错误|无效)/i.test(body)
      if (isAuthenticatorChallenge(body, inputs)) return { kind: invalidCode ? 'invalid_totp' : 'totp' }
      if (/text message|\bsms\b|phone|mobile|短信/i.test(body)) return { kind: invalidCode ? 'invalid_sms_code' : 'sms_code' }
      return { kind: 'email_code' }
    }
    if (/phone.*(?:invalid|unavailable|used too many)|too many.*phone|手机号.*(?:不可用|次数过多)/i.test(body)) return { kind: 'phone_rejected' }
    if (await this.#h.firstVisibleInput(page, (s) => /tel|phone|mobile/.test(s) && !/code|otp/.test(s))) return { kind: 'phone' }
    const emailMatch = body.match(/[A-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i)
    const email = emailMatch?.[0]?.toLowerCase()
    if (await this.#h.firstVisibleInput(page, (s) => /password/.test(s))) return { kind: 'password', email }
    if (await this.#h.firstVisibleInput(page, (s) => /email/.test(s) && !/code|otp/.test(s))) return { kind: 'email' }
    if (/select\s+(?:a\s+)?(?:workspace|organization)|choose\s+(?:a\s+)?(?:workspace|organization)|continue\s+to\s+codex|authorize\s+codex|选择.*(?:工作空间|组织)/i.test(body)) return { kind: 'workspace', email }
    const login = await this.#h.firstVisibleRole(page, 'button', [/^(?:log in|sign in|use another account)$/i])
      || await this.#h.firstVisibleRole(page, 'link', [/^(?:log in|sign in|use another account)$/i])
    return login ? { kind: 'login', action: login } : { kind: 'unknown' }
  }

  async #continue(page) {
    if (this.#h.clickOAuthContinue) return this.#h.clickOAuthContinue(page)
    const button = await this.#h.firstVisibleRole(page, 'button', [/^(?:continue|next|verify|allow|authorize|继续|下一步)$/i])
    if (!button) throw fail('manual_challenge')
    await button.click()
  }

  async #fill(page, kind, value) {
    const helper = kind === 'email' ? 'fillWorkflowEmail' : 'fillLoginPassword'
    if (this.#h[helper]) return this.#h[helper](page, value)
    const input = await this.#h.firstVisibleInput(page, (s) => kind === 'email' ? /email/.test(s) && !/code|otp/.test(s) : /password/.test(s))
    if (!input) throw fail('manual_challenge')
    await input.fill(value)
    await this.#continue(page)
  }

  async #code(page, value) {
    if (this.#h.fillVerificationCode) return this.#h.fillVerificationCode(page, value)
    const inputs = await this.#h.verificationInputs(page)
    if (inputs.length === 1) await inputs[0].fill(value)
    else {
      if (inputs.length !== value.length) throw fail('manual_challenge')
      for (let i = 0; i < value.length; i++) await inputs[i].fill(value[i])
    }
    const button = await this.#h.firstVisibleRole(page, 'button', [/^(?:continue|next|verify|继续|下一步)$/i])
    if (button) await button.click()
  }

  async #submitPhone(page, value) {
    if (this.#h.submitPhoneOnOpenAI) return this.#h.submitPhoneOnOpenAI(page, value)
    const input = await this.#h.firstVisibleInput(page, (s) => /tel|phone|mobile/.test(s) && !/code|otp/.test(s))
    if (!input) throw fail('manual_challenge')
    await input.fill(value)
    const sms = await this.#h.firstVisibleRole(page, 'radio', [/text message|短信/i])
    if (sms) await sms.click()
    const button = await this.#h.firstVisibleRole(page, 'button', [/^(?:send (?:a )?code|send sms|text me|continue|next|继续|下一步)$/i])
    if (!button) throw fail('manual_challenge')
    await button.click()
  }

  async #workspace(page) {
    if (this.#h.chooseDefaultWorkspace) return this.#h.chooseDefaultWorkspace(page)
    const option = await this.#h.firstVisibleRole(page, 'radio', [/default|personal|默认|个人/i])
      || await this.#h.firstVisibleRole(page, 'button', [/^default workspace$/i])
    if (option) await option.click()
    await this.#continue(page)
  }

  async #run(task) {
    let requestListener
    const attempted = new Set()
    try {
      const browser = await (typeof this.#browser === 'function' ? this.#browser() : this.#browser)
      if (task.status !== 'running') return
      task.context = await browser.newContext({ proxy: task.proxy })
      if (task.status !== 'running') return
      task.page = await task.context.newPage()
      if (task.status !== 'running') return
      const page = task.page
      page.setDefaultTimeout?.(10_000)
      requestListener = (request) => {
        try {
          if (request.isNavigationRequest() && request.frame() === page.mainFrame()) this.#callback(task, request.url())
        } catch { /* Detached frames cannot complete this task. */ }
      }
      page.on('request', requestListener)
      await page.goto(task.authURL, { waitUntil: 'domcontentloaded', timeout: 30_000 })
      await this.#stepPause(task)
      while (task.status === 'running') {
        if (this.#checkTimeout(task)) break
        if (this.#callback(task, page.url())) break
        if (!this.#trustedPage(task)) { this.#stop(task, 'blocked', 'manual_challenge'); break }
        const state = await this.#inspect(page)
        if (this.#checkTimeout(task)) break
        if (!this.#trustedPage(task)) { this.#stop(task, 'blocked', 'manual_challenge'); break }
        if (state.email && state.email.toLowerCase() !== task.email) { this.#stop(task, 'blocked', 'invalid_credentials'); break }
        const manual = { captcha: 'captcha_required', email_code: 'email_code_required', account_blocked: 'account_blocked',
          invalid_credentials: 'invalid_credentials', invalid_totp: 'invalid_totp', invalid_sms_code: 'invalid_sms_code',
          signup: 'manual_challenge', external_provider: 'manual_challenge' }[state.kind]
        if (manual) { this.#stop(task, 'blocked', manual); break }
        if (state.kind === 'phone_rejected') {
          if (task.phone) task.rejected.add(task.phone)
          this.#stage(task, 'phone_required')
          task.reason = 'phone_rejected'
          this.#report(task)
        } else if (state.kind === 'phone' && !task.phone) this.#stage(task, 'phone_required')
        else if (state.kind === 'sms_code') {
          if (!task.phone) { this.#stop(task, 'blocked', 'manual_challenge'); break }
          this.#stage(task, 'sms_waiting')
        }
        if (task.pending) {
          const input = task.pending
          task.pending = null
          task.busy = true
          try {
            if (input.kind === 'phone' && ['phone', 'phone_rejected'].includes(state.kind)) {
              this.#stage(task, 'phone_submitting')
              task.phone = input.value
              task.smsDeadline = 0
              task.reason = ''
              await this.#submitPhone(page, input.value)
              await this.#stepPause(task)
              if (!this.#checkTimeout(task)) this.#stage(task, 'sms_waiting')
            } else if (input.kind === 'sms' && state.kind === 'sms_code') {
              this.#stage(task, 'sms_submitting')
              await this.#code(page, input.value)
              await this.#stepPause(task)
            } else throw fail('manual_challenge')
          } catch {
            if (task.status === 'running' && this.#trustedPage(task)
              && (await this.#inspect(page)).kind === 'phone_rejected') {
              task.rejected.add(task.phone)
              this.#stage(task, 'phone_required')
              task.reason = 'phone_rejected'
              this.#report(task)
            } else throw fail('manual_challenge')
          } finally {
            input.value = ''
            task.busy = false
          }
          continue
        }
        if (['login', 'email', 'password', 'totp', 'workspace'].includes(state.kind) && !attempted.has(state.kind)) {
          attempted.add(state.kind)
          this.#stage(task, state.kind)
          if (state.kind === 'login') await state.action.click()
          if (state.kind === 'email') {
            task.emailSubmitted = true
            await this.#fill(page, 'email', task.email)
          }
          if (state.kind === 'password') {
            if (!task.emailSubmitted) { this.#stop(task, 'blocked', 'invalid_credentials'); break }
            await this.#fill(page, 'password', task.password)
            task.password = ''
          }
          if (state.kind === 'totp') {
            if (!task.secret) { this.#stop(task, 'blocked', 'authenticator_required'); break }
            const code = generateTOTP(task.secret, this.#h.now())
            task.secret = ''
            await this.#code(page, code)
          }
          if (state.kind === 'workspace') {
            if (!task.emailSubmitted) { this.#stop(task, 'blocked', 'invalid_credentials'); break }
            await this.#workspace(page)
            if (task.status === 'running') this.#stage(task, 'callback_waiting')
          }
          await this.#stepPause(task)
          continue
        }
        await this.#pause(task)
      }
    } catch (error) {
      this.#stop(task, 'failed', automationFailureReason(error, task.stage))
    } finally {
      if (requestListener) task.page?.off('request', requestListener)
      // Acquisition is deliberately not raced against cancellation. A late
      // context must be closed before its reservation can ever be reused.
      while (task.context && !(await this.#close(task))) {
        await new Promise((resolve) => this.#timer(resolve, 1000))
      }
      task.context = task.page = null
      task.password = task.secret = task.authURL = task.phone = task.state = task.email = ''
      task.proxy = task.pending = null
      task.rejected.clear()
      this.#slots--
    }
  }
}
