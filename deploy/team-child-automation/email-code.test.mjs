import assert from 'node:assert/strict'
import test from 'node:test'

import { createICGCEmailCodeSession, extractOpenAIVerificationCode } from './email-code.mjs'

const gateway = '/m/syntheticGateway1234567890'
const email = 'owner@example.test'
const token = 'a'.repeat(64)

function json(data, status = 200) {
  return new Response(JSON.stringify(status < 400
    ? { ok: true, data, error: null }
    : { ok: false, data: null, error: { code: status === 401 ? 'UNAUTHORIZED' : 'FAILED' } }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

test('extracts a single contextual OpenAI code', () => {
  assert.equal(extractOpenAIVerificationCode({ from: 'OpenAI', subject: 'Your temporary ChatGPT login code', text: 'Verification code: 543604' }), '543604')
  assert.equal(extractOpenAIVerificationCode({ from: 'Unrelated', text: 'Verification code: 543604' }), '')
  assert.equal(extractOpenAIVerificationCode({ from: 'OpenAI', text: 'Codes 543604 and 123456' }), '')
})

test('baselines old messages and returns only a new OpenAI code with pinned requests', async () => {
  let current = 10_000
  let listCount = 0
  const calls = []
  const fetch = async (url, options = {}) => {
    calls.push({ url: String(url), options })
    if (url === 'https://ic.g-c.cc/') return new Response(`<html data-public-mail-gateway="${gateway}"></html>`)
    if (String(url).endsWith('/authorize')) return json({ accessProof: 'proof', accessProofExpiresAt: 9999999999 })
    if (String(url).includes('/items?')) {
      listCount++
      return json({ messages: listCount < 3
        ? [{ id: 'old', from: 'OpenAI', subject: 'Old code', snippet: 'Verification code: 111111', date: new Date(1_000).toISOString() }]
        : [
            { id: 'new', from: 'OpenAI', subject: 'Your ChatGPT code', snippet: 'Open this message', date: new Date(12_000).toISOString() },
            { id: 'old', from: 'OpenAI', subject: 'Old code', snippet: 'Verification code: 111111', date: new Date(1_000).toISOString() },
          ] })
    }
    if (String(url).endsWith('/items/new')) return json({ message: { from: 'OpenAI', text: 'Your verification code is 543604.' } })
    throw new Error(`unexpected URL ${url}`)
  }
  const session = await createICGCEmailCodeSession({ email, token }, {
    fetch,
    now: () => current,
    setTimeout: (fn, ms) => { current += ms; queueMicrotask(fn); return 1 },
    clearTimeout: () => {},
  })

  assert.equal(await session.waitForCode({ notBefore: 10_000 }), '543604')
  assert.equal(calls[0].url, 'https://ic.g-c.cc/')
  const authorize = calls.find(call => call.url.endsWith('/authorize'))
  assert.deepEqual(JSON.parse(authorize.options.body), { email, token, turnstileToken: '' })
  const list = calls.find(call => call.url.includes('/items?'))
  assert.equal(list.options.headers['X-Public-Email'], email)
  assert.equal(list.options.headers['X-Public-Email-Token'], token)
  assert.equal(list.options.headers['X-Mail-Access-Proof'], 'proof')
  session.close()
})

test('distinguishes invalid binding tokens from a one-minute empty mailbox timeout', async () => {
  const deniedFetch = async (url) => {
    if (url === 'https://ic.g-c.cc/') return new Response(`<html data-public-mail-gateway="${gateway}"></html>`)
    return json(null, 401)
  }
  await assert.rejects(createICGCEmailCodeSession({ email, token }, { fetch: deniedFetch }), error => error?.code === 'email_code_access_denied')

  let current = 0
  const listeners = new Set()
  const signal = {
    aborted: false,
    addEventListener(_event, listener) { listeners.add(listener) },
    removeEventListener(_event, listener) { listeners.delete(listener) },
  }
  const emptyFetch = async (url) => {
    if (url === 'https://ic.g-c.cc/') return new Response(`<html data-public-mail-gateway="${gateway}"></html>`)
    if (String(url).endsWith('/authorize')) return json({ accessProof: 'proof', accessProofExpiresAt: 9999999999 })
    return json({ messages: [] })
  }
  const session = await createICGCEmailCodeSession({ email, token, signal }, {
    fetch: emptyFetch,
    now: () => current,
    setTimeout: (fn, ms) => { current += ms; queueMicrotask(fn); return 1 },
    clearTimeout: () => {},
  })
  await assert.rejects(session.waitForCode(), error => error?.code === 'email_code_timeout')
  assert.equal(current, 60_000)
  assert.equal(listeners.size, 0)
})

test('fails closed when the pre-login mailbox baseline is unavailable', async () => {
  const fetch = async (url) => {
    if (url === 'https://ic.g-c.cc/') return new Response(`<html data-public-mail-gateway="${gateway}"></html>`)
    if (String(url).endsWith('/authorize')) return json({ accessProof: 'proof', accessProofExpiresAt: 9999999999 })
    throw new Error('mailbox unavailable')
  }
  await assert.rejects(
    createICGCEmailCodeSession({ email, token }, { fetch }),
    error => error?.code === 'email_code_unavailable'
  )
})
