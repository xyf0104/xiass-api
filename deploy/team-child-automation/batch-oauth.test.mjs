import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import test from 'node:test'

import { BatchOAuthRunner, BATCH_OAUTH_REASONS, generateTOTP } from './batch-oauth.mjs'

const SECRET = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'
const callback = (state, base = 'http://localhost:1455/auth/callback') => `${base}?code=test-code&state=${state}`
const flush = async () => { for (let i = 0; i < 20; i++) await new Promise(setImmediate) }
function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

class Clock {
  time = 59_000
  timers = new Map()
  serial = 0
  now = () => this.time
  setTimeout = (fn, ms) => {
    const id = ++this.serial
    this.timers.set(id, { at: this.time + ms, fn })
    return id
  }
  clearTimeout = (id) => this.timers.delete(id)
  async advance(ms) {
    this.time += ms
    for (let round = 0; round < 30; round++) {
      const due = [...this.timers].filter(([, timer]) => timer.at <= this.time)
      if (!due.length) break
      for (const [id, timer] of due) {
        if (this.timers.delete(id)) timer.fn()
      }
      await flush()
    }
    await flush()
  }
}

class Page extends EventEmitter {
  constructor(plan) {
    super()
    this.plan = plan
    this.kind = plan.kind || 'email'
    this.location = 'about:blank'
    this.frame = {}
    this.calls = []
  }
  url() { return this.location }
  mainFrame() { return this.frame }
  setDefaultTimeout() {}
  async goto(url) {
    this.location = url
    this.state = new URL(url).searchParams.get('state')
    if (this.plan.gotoError) throw new Error('SECRET browser failure')
  }
  navigate(url, { main = true, navigation = true } = {}) {
    this.emit('request', { url: () => url, isNavigationRequest: () => navigation, frame: () => main ? this.frame : {} })
  }
  async finish() {
    this.navigate(this.plan.callback || callback(this.state))
    this.location = 'chrome-error://chromewebdata/'
    throw new Error('net::ERR_CONNECTION_REFUSED contains sensitive callback')
  }
}

function harness(plans = [], overrides = {}) {
  const clock = new Clock()
  const contexts = []
  let live = 0, maxLive = 0
  const browser = {
    async newContext(options) {
      const plan = plans[contexts.length] || {}
      const page = new Page(plan)
      const context = {
        options, page, closed: false, closes: 0,
        async newPage() {
          if (plan.newPageGate) await plan.newPageGate.promise
          if (plan.newPageError) throw new Error('SECRET new page error')
          return page
        },
        async close() {
          this.closes++
          if (plan.closeFailures > 0) { plan.closeFailures--; throw new Error('SECRET close error') }
          if (plan.closeGate) await plan.closeGate.promise
          if (!this.closed) { this.closed = true; live-- }
        }
      }
      contexts.push(context)
      live++
      maxLive = Math.max(maxLive, live)
      if (plan.contextGate) await plan.contextGate.promise
      return context
    }
  }
  const helpers = {
    now: clock.now, setTimeout: clock.setTimeout, clearTimeout: clock.clearTimeout,
    async inspectPage(page) { return { kind: page.kind, email: page.plan.identity } },
    async fillWorkflowEmail(page, value) { page.calls.push(['email', value]); page.kind = 'password' },
    async fillLoginPassword(page, value) {
      page.calls.push(['password', value])
      if (page.plan.passwordGate) await page.plan.passwordGate.promise
      page.kind = page.plan.afterPassword || 'phone'
    },
    async fillVerificationCode(page, value) {
      page.calls.push([page.kind, value])
      page.kind = page.kind === 'totp' ? page.plan.afterTOTP || 'workspace' : 'workspace'
    },
    async submitPhoneOnOpenAI(page, value) {
      page.calls.push(['phone', value])
      if (value === page.plan.rejectPhone) {
        page.kind = 'phone_rejected'
        throw new Error('provider SECRET number rejected')
      }
      page.kind = 'sms_code'
    },
    async chooseDefaultWorkspace(page) { page.calls.push(['workspace']); await page.finish() },
    ...overrides.helpers
  }
  const runner = new BatchOAuthRunner({ browser: overrides.browser || browser,
    validateAuthURL: overrides.validateAuthURL || ((url) => url), helpers })
  return { runner, clock, contexts, browser, maxLive: () => maxLive, live: () => live }
}

function body(id = 'task-1', owner = 1, extra = {}) {
  const auth = new URL('https://auth.openai.com/oauth/authorize')
  auth.searchParams.set('state', `state-${id}`)
  auth.searchParams.set('redirect_uri', 'http://localhost:1455/auth/callback')
  return { task_id: id, owner_id: owner, email: `${id}@example.com`, password: `password-${id}`,
    totp_secret: '', auth_url: auth.toString(), oauth_session_id: `session-${id}-1234567890`, ...extra }
}

async function stopAll(h, ids) {
  for (const [id, owner] of ids) await h.runner.cancel(id, owner)
  await flush()
}

test('RFC 6238 SHA-1 vectors reduced to six digits, padding and step boundaries', () => {
  const vectors = [[59, '287082'], [1111111109, '081804'], [1111111111, '050471'],
    [1234567890, '005924'], [2000000000, '279037'], [20000000000, '353130']]
  for (const [seconds, expected] of vectors) assert.equal(generateTOTP(SECRET, seconds * 1000), expected)
  assert.equal(generateTOTP(SECRET.toLowerCase(), 59_999), '287082')
  assert.notEqual(generateTOTP(SECRET, 60_000), '287082')
  assert.equal(generateTOTP('JBSW Y3DP EHPK 3PXP', 0), generateTOTP('JBSWY3DPEHPK3PXP', 0))
  for (const secret of ['', 'short', 'ABC!', 'otpauth://totp/test', 'A'.repeat(257)]) {
    assert.throws(() => generateTOTP(secret, 0), /invalid_input/)
  }
  assert.throws(() => generateTOTP(SECRET, -1), /invalid_input/)
})

test('three isolated contexts maximum, exact backend IDs, per-task proxy and credential identity', async () => {
  const h = harness()
  const ids = [['a', 1], ['b', 2], ['c', 3]]
  const requests = ids.map(([id, owner]) => body(id, owner, {
    proxy: { server: `http://proxy-${id}.example:8080`, username: id, password: `proxy-${id}`, bypass: '*' }
  }))
  const summaries = await Promise.all(requests.map((request) => h.runner.start(request)))
  await flush()
  assert.deepEqual(summaries.map((value) => value.id), ['a', 'b', 'c'])
  assert.equal(h.maxLive(), 3)
  await assert.rejects(h.runner.start(body('d', 4)), /capacity/)
  for (const [i, context] of h.contexts.entries()) {
    const id = ids[i][0]
    assert.deepEqual(context.options, { proxy: { server: `http://proxy-${id}.example:8080`, username: id, password: `proxy-${id}` } })
    assert.deepEqual(context.page.calls, [['email', `${id}@example.com`], ['password', `password-${id}`]])
    assert.equal(h.runner.get(id, ids[i][1]).stage, 'phone_required')
  }
  requests[0].proxy.password = 'mutated'
  assert.equal(h.contexts[0].options.proxy.password, 'proxy-a')
  await stopAll(h, ids)
  assert.equal(h.live(), 0)
  assert.ok(h.contexts.every((context) => context.closes === 1))
})

test('all methods enforce owner and snapshots contain no login, proxy or session secrets', async () => {
  const h = harness()
  const request = body()
  await h.runner.start(request)
  await flush()
  for (const action of [() => h.runner.get('task-1', 2), () => h.runner.phone('task-1', 2, '+15555550101'),
    () => h.runner.smsCode('task-1', 2, '123456')]) assert.throws(action, /not_found/)
  await assert.rejects(h.runner.cancel('task-1', 2), /not_found/)
  assert.throws(() => h.runner.get('task-1', '01'), /not_found/)
  const snapshot = h.runner.get('task-1', '1')
  assert.deepEqual(Object.keys(snapshot).sort(), ['id', 'owner_id', 'reason', 'stage', 'status'])
  snapshot.status = 'completed'
  assert.equal(h.runner.get('task-1', 1).status, 'running')
  assert.equal(JSON.stringify(h.runner), '{}')
  await assert.rejects(h.runner.start(body()), /task_exists/)
  await assert.rejects(h.runner.start(body('task-1', 2)), /task_exists/)
  await stopAll(h, [['task-1', 1]])
})

test('authenticator-only login and workspace capture callback request before navigation error', async () => {
  const h = harness([{ afterPassword: 'totp' }])
  await h.runner.start(body('totp', 1, { totp_secret: SECRET }))
  await flush()
  const result = h.runner.get('totp', 1)
  assert.equal(result.status, 'completed')
  assert.equal(result.stage, 'callback')
  assert.equal(result.callback_url, callback('state-totp'))
  assert.deepEqual(h.contexts[0].page.calls, [['email', 'totp@example.com'], ['password', 'password-totp'], ['totp', '287082'], ['workspace']])
  assert.equal(h.contexts[0].closed, true)
  assert.equal(h.contexts[0].page.listenerCount('request'), 0)
  assert.throws(() => h.runner.get('totp', 2), /not_found/)
  assert.equal((await h.runner.cancel('totp', 1)).status, 'completed')
  await h.clock.advance(15 * 60_000)
  assert.throws(() => h.runner.get('totp', 1), /not_found/)
  assert.equal(h.clock.timers.size, 0)
})

test('only exact callback authority/path, single code and matching single state can complete', async (t) => {
  const invalid = [
    callback('wrong'), callback('state-task-1', 'http://localhost:1456/auth/callback'),
    callback('state-task-1', 'https://localhost:1455/auth/callback'),
    callback('state-task-1', 'http://127.0.0.1:1455/auth/callback'),
    callback('state-task-1', 'http://localhost:1455/auth/callback/'),
    callback('state-task-1', 'http://user@localhost:1455/auth/callback'),
    `${callback('state-task-1')}&state=state-task-1`, `${callback('state-task-1')}&code=second`,
    `${callback('state-task-1')}#fragment`, `${callback('state-task-1')}&error=denied`,
    'http://localhost:1455/auth/callback?code=&state=state-task-1'
  ]
  for (const url of invalid) await t.test(`invalid callback ${invalid.indexOf(url)}`, async () => {
    const h = harness([{ afterPassword: 'workspace', callback: url }])
    await h.runner.start(body())
    await flush()
    const result = h.runner.get('task-1', 1)
    assert.equal(result.status, 'blocked')
    assert.equal(result.callback_url, undefined)
    assert.equal(h.live(), 0)
  })
})

test('cross-task state, subframe, fetch, foreign host and late callback cannot complete', async () => {
  const h = harness()
  await h.runner.start(body('a'))
  await h.runner.start(body('b'))
  await flush()
  const page = h.contexts[0].page
  page.navigate(callback('state-a'), { main: false })
  page.navigate(callback('state-a'), { navigation: false })
  page.navigate(callback('state-a', 'http://localhost.evil.example:1455/auth/callback'))
  assert.equal(h.runner.get('a', 1).status, 'running')
  page.navigate(callback('state-b'))
  await flush()
  assert.equal(h.runner.get('a', 1).status, 'blocked')
  assert.equal(h.runner.get('b', 1).status, 'running')
  await h.runner.cancel('b', 1)
  h.contexts[1].page.navigate(callback('state-b'))
  assert.equal(h.runner.get('b', 1).status, 'canceled')
  await flush()
})

test('phone and SMS are external inputs; reject duplicate actions and previously rejected number', async () => {
  const h = harness([{ rejectPhone: '+15555550101' }])
  await h.runner.start(body())
  await flush()
  const r = h.runner
  assert.throws(() => r.smsCode('task-1', 1, '123456'), /invalid_stage/)
  assert.throws(() => r.phone('task-1', 1, '555'), /invalid_input/)
  r.phone('task-1', 1, '+1 (555) 555-0101')
  assert.throws(() => r.phone('task-1', 1, '+15555550102'), /invalid_stage/)
  await flush()
  assert.equal(r.get('task-1', 1).reason, 'phone_rejected')
  assert.equal(r.get('task-1', 1).status, 'running')
  assert.throws(() => r.phone('task-1', 1, '+15555550101'), /phone_rejected/)
  r.phone('task-1', 1, '+15555550102')
  await flush()
  assert.equal(r.get('task-1', 1).stage, 'sms_waiting')
  assert.throws(() => r.phone('task-1', 1, '+15555550103'), /invalid_stage/)
  assert.throws(() => r.smsCode('task-1', 1, 'secret'), /invalid_input/)
  r.smsCode('task-1', 1, '123456')
  assert.throws(() => r.smsCode('task-1', 1, '123456'), /invalid_stage/)
  await flush()
  assert.equal(r.get('task-1', 1).status, 'completed')
  assert.equal(h.contexts[0].page.calls.filter(([kind]) => kind === 'phone').length, 2)
  assert.equal(h.live(), 0)
})

test('manual challenges, explicit bans, identity mismatch, signup and missing TOTP close context', async (t) => {
  for (const [plan, reason] of [
    [{ afterPassword: 'captcha' }, 'captcha_required'], [{ afterPassword: 'email_code' }, 'email_code_required'],
    [{ afterPassword: 'account_blocked' }, 'account_blocked'], [{ afterPassword: 'totp' }, 'authenticator_required'],
    [{ afterPassword: 'signup' }, 'manual_challenge'], [{ identity: 'other@example.com' }, 'invalid_credentials'],
    [{ kind: 'workspace' }, 'invalid_credentials'], [{ afterPassword: 'external_provider' }, 'manual_challenge']
  ]) await t.test(reason, async () => {
    const h = harness([plan])
    await h.runner.start(body())
    await flush()
    const result = h.runner.get('task-1', 1)
    assert.equal(result.status, 'blocked')
    assert.equal(result.reason, reason)
    assert.ok(BATCH_OAUTH_REASONS.includes(result.reason))
    assert.equal(h.live(), 0)
    assert.equal(h.contexts[0].page.calls.some(([kind]) => ['phone', 'totp', 'workspace'].includes(kind)), false)
  })
})

test('SMS gets 180 seconds, confirmation gets five minutes, and records expire at 15 minutes', async (t) => {
  for (const sms of [false, true]) await t.test(sms ? 'SMS' : 'confirmation', async () => {
    const h = harness()
    await h.runner.start(body())
    await flush()
    if (sms) { h.runner.phone('task-1', 1, '+15555550101'); await flush() }
    const timeout = sms ? 180_000 : 300_000
    await h.clock.advance(timeout - 1)
    assert.equal(h.runner.get('task-1', 1).status, 'running')
    await h.clock.advance(1)
    assert.equal(h.runner.get('task-1', 1).reason, sms ? 'sms_timeout' : 'sms_confirmation_timeout')
    assert.equal(h.live(), 0)
    assert.throws(() => h.runner.phone('task-1', 1, '+15555550102'), /invalid_stage/)
    await h.clock.advance(900_000 - timeout)
    assert.throws(() => h.runner.get('task-1', 1), /not_found/)
    assert.equal(h.clock.timers.size, 0)
  })
})

test('login longer than 180 seconds and manual confirmation do not consume SMS reservation time', async () => {
  const gate = deferred()
  const h = harness([{ passwordGate: gate }])
  await h.runner.start(body())
  await flush()
  await h.clock.advance(240_000)
  assert.equal(h.runner.get('task-1', 1).status, 'running')
  assert.equal(h.runner.get('task-1', 1).stage, 'password')
  gate.resolve()
  await flush()
  await h.clock.advance(120_000)
  assert.equal(h.runner.get('task-1', 1).stage, 'phone_required')
  h.runner.phone('task-1', 1, '+15555550101')
  await flush()
  for (const elapsed of [60_000, 60_000, 59_999]) {
    await h.clock.advance(elapsed)
    assert.equal(h.runner.get('task-1', 1).status, 'running')
    assert.equal(h.runner.get('task-1', 1).stage, 'sms_waiting')
  }
  await h.clock.advance(1)
  assert.equal(h.runner.get('task-1', 1).reason, 'sms_timeout')
  assert.equal(h.live(), 0)
})

test('rejection cancels old SMS timer; replacement gets one new nonrenewing 180-second timer', async () => {
  const h = harness()
  await h.runner.start(body())
  await flush()
  h.runner.phone('task-1', 1, '+15555550101')
  await flush()
  await h.clock.advance(120_000)
  h.contexts[0].page.kind = 'phone_rejected'
  await h.clock.advance(250)
  assert.equal(h.runner.get('task-1', 1).reason, 'phone_rejected')
  for (const elapsed of [60_000, 60_000]) {
    await h.clock.advance(elapsed)
    assert.equal(h.runner.get('task-1', 1).status, 'running')
    assert.equal(h.runner.get('task-1', 1).stage, 'phone_required')
  }
  assert.throws(() => h.runner.phone('task-1', 1, '+15555550101'), /phone_rejected/)
  h.runner.phone('task-1', 1, '+15555550102')
  await flush()
  await h.clock.advance(179_999)
  assert.equal(h.runner.get('task-1', 1).status, 'running')
  await h.clock.advance(1)
  assert.equal(h.runner.get('task-1', 1).reason, 'sms_timeout')
  assert.equal(h.live(), 0)
})

test('repeated rejection observations do not extend five-minute confirmation deadline', async () => {
  const h = harness([{ rejectPhone: '+15555550101' }])
  await h.runner.start(body())
  await flush()
  h.runner.phone('task-1', 1, '+15555550101')
  await flush()
  for (const elapsed of [100_000, 100_000, 99_999]) {
    await h.clock.advance(elapsed)
    assert.equal(h.runner.get('task-1', 1).reason, 'phone_rejected')
  }
  await h.clock.advance(1)
  assert.equal(h.runner.get('task-1', 1).reason, 'sms_confirmation_timeout')
  assert.equal(h.live(), 0)
})

test('leaving SMS for workspace disarms SMS timeout while total attempt remains bounded', async () => {
  const gate = deferred()
  const h = harness([], { helpers: { chooseDefaultWorkspace: async () => gate.promise } })
  await h.runner.start(body())
  await flush()
  h.runner.phone('task-1', 1, '+15555550101')
  await flush()
  await h.clock.advance(120_000)
  h.runner.smsCode('task-1', 1, '123456')
  await flush()
  await h.clock.advance(180_000)
  assert.equal(h.runner.get('task-1', 1).status, 'running')
  assert.equal(h.runner.get('task-1', 1).stage, 'workspace')
  await h.runner.cancel('task-1', 1)
  gate.resolve()
  await flush()
  assert.equal(h.live(), 0)
})

test('15-minute attempt limit overrides a late SMS reservation without extending record TTL', async () => {
  const gate = deferred()
  const h = harness([{ passwordGate: gate }])
  await h.runner.start(body())
  await flush()
  await h.clock.advance(840_000)
  gate.resolve()
  await flush()
  h.runner.phone('task-1', 1, '+15555550101')
  await flush()
  await h.clock.advance(59_999)
  assert.equal(h.runner.get('task-1', 1).status, 'running')
  await h.clock.advance(1)
  assert.throws(() => h.runner.get('task-1', 1), /not_found/)
  assert.equal(h.live(), 0)
  assert.equal(h.clock.timers.size, 0)
})

test('cancellation during delayed context creation keeps slot until the late context closes', async () => {
  const contextGate = deferred(), closeGate = deferred()
  const h = harness([{ contextGate, closeGate }, {}, {}])
  await h.runner.start(body('a'))
  await h.runner.start(body('b'))
  await h.runner.start(body('c'))
  await flush()
  assert.equal((await h.runner.cancel('a', 1)).status, 'canceled')
  await assert.rejects(h.runner.start(body('d')), /capacity/)
  contextGate.resolve()
  await flush()
  assert.equal(h.contexts[0].closes, 1)
  assert.equal(h.contexts[0].page.calls.length, 0)
  await assert.rejects(h.runner.start(body('d')), /capacity/)
  closeGate.resolve()
  await flush()
  await h.runner.start(body('d'))
  await flush()
  assert.equal(h.maxLive(), 3)
  await stopAll(h, [['b', 1], ['c', 1], ['d', 1]])
})

test('cancel while browser acquisition is delayed never creates a context', async () => {
  const gate = deferred()
  const h = harness([], { browser: () => gate.promise })
  await h.runner.start(body())
  await h.runner.cancel('task-1', 1)
  gate.resolve(h.browser)
  await flush()
  assert.equal(h.contexts.length, 0)
  assert.equal(h.runner.get('task-1', 1).status, 'canceled')
})

test('15-minute attempt expiry closes context even when login action is still pending', async () => {
  const gate = deferred()
  const h = harness([{ passwordGate: gate }])
  await h.runner.start(body())
  await flush()
  await h.clock.advance(899_999)
  assert.equal(h.runner.get('task-1', 1).status, 'running')
  await h.clock.advance(1)
  assert.equal(h.live(), 0)
  assert.throws(() => h.runner.get('task-1', 1), /not_found/)
  gate.resolve()
  await flush()
  assert.equal(h.contexts[0].closes, 1)
})

test('page acquisition failure is sanitized and cleanup failure does not free a live slot', async () => {
  const h = harness([{ newPageError: true, closeFailures: 1 }, {}, {}])
  await h.runner.start(body('a'))
  await h.runner.start(body('b'))
  await h.runner.start(body('c'))
  await flush()
  assert.equal(h.runner.get('a', 1).reason, 'manual_challenge')
  assert.doesNotMatch(JSON.stringify(h.runner.get('a', 1)), /SECRET/)
  // Cleanup may retry once immediately when stop() and finally share a close.
  await h.clock.advance(1000)
  assert.equal(h.contexts[0].closed, true)
  assert.ok(h.contexts[0].closes >= 2)
  await h.runner.start(body('d'))
  await flush()
  assert.equal(h.maxLive(), 3)
  await stopAll(h, [['b', 1], ['c', 1], ['d', 1]])
})

test('validation rejects unsafe URL, malformed inputs and proxy credentials without browser work', async () => {
  const h = harness()
  for (const extra of [{ task_id: '' }, { owner_id: 0 }, { email: 'bad' }, { password: '' },
    { oauth_session_id: 'bad' }, { totp_secret: 'invalid!' }, { proxy: { server: 'file:///tmp/test' } },
    { proxy: { server: 'http://user:secret@proxy.example' } }, { auth_url: 'https://evil.example' }]) {
    await assert.rejects(h.runner.start(body('bad', 1, extra)))
  }
  const duplicate = body()
  duplicate.auth_url += '&state=another'
  await assert.rejects(h.runner.start(duplicate), /invalid_auth_url/)
  assert.equal(h.contexts.length, 0)
  const rejected = harness([], { validateAuthURL: () => { throw new Error('SECRET validator error') } })
  await assert.rejects(rejected.runner.start(body()), /invalid_auth_url/)
  assert.equal(rejected.contexts.length, 0)
})

test('default inspector recognizes authenticator vs email OTP and explicit ban, not generic rate errors', async (t) => {
  const cases = [
    ['Enter code from your authenticator app', true, 'totp'],
    ['Check your inbox for a verification code', true, 'email_code'],
    ['Your account has been deactivated', false, 'account_blocked'],
    ['Verify you are human CAPTCHA', false, 'captcha'],
    ['Too many requests. Try later.', false, 'unknown'],
    ['Log in. Do not have an account? Create an account.', false, 'unknown']
  ]
  for (const [text, code, expected] of cases) await t.test(expected, async () => {
    const empty = { count: async () => 0 }
    const h = harness([], { helpers: {
      inspectPage: undefined,
      oauthBody: async () => text,
      verificationInputs: async () => code ? [{}] : [],
      firstVisibleInput: async () => null,
      firstVisibleRole: async () => null
    } })
    await h.runner.start(body())
    h.contexts[0].page.locator = () => empty
    await flush()
    const result = h.runner.get('task-1', 1)
    const reasons = { totp: 'authenticator_required', email_code: 'email_code_required', account_blocked: 'account_blocked', captcha: 'captcha_required' }
    assert.equal(result.reason, reasons[expected] || '')
    assert.equal(result.status, expected === 'unknown' ? 'running' : 'blocked')
    await h.runner.cancel('task-1', 1)
    await flush()
  })
})
