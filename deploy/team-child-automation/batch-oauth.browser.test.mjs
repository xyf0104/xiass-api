import assert from 'node:assert/strict'
import fs from 'node:fs'
import { test } from 'node:test'
import { chromium } from '@playwright/test'
import { BatchOAuthRunner } from './batch-oauth.mjs'

const executablePath = process.env.CHROMIUM_EXECUTABLE_PATH || chromium.executablePath()
test('three real isolated Chromium contexts complete the fixture login and callback', { skip: !fs.existsSync(executablePath), timeout: 45000 }, async () => {
  const browser = await chromium.launch({ executablePath, headless: true })
  const contexts = []
  const runner = new BatchOAuthRunner({ validateAuthURL: value => value, browser: {
    async newContext(options) {
      const context = await browser.newContext(options)
      contexts.push(context)
      let state = ''
      let email = ''
      await context.route('**/*', async route => {
        const url = new URL(route.request().url())
        if (url.hostname === 'localhost') { await route.fulfill({ status: 200, body: 'callback' }); return }
        assert.equal(url.origin, 'https://auth.openai.com', 'fixture must not contact the network')
        let content
        const form = (action, label, input) => `<html><body><h1>${label}</h1><p>${email}</p><form method="post" action="${action}">${input}<button type="submit">Continue</button></form></body></html>`
        if (url.pathname === '/oauth/authorize') {
          state = url.searchParams.get('state')
          content = form('/fixture/password', 'Log in', '<input type="email" name="email" aria-label="Email" />')
        } else if (url.pathname === '/fixture/password') {
          email = new URLSearchParams(route.request().postData()).get('email')
          content = form('/fixture/totp', 'Enter your password', '<input type="password" name="password" />')
        } else if (url.pathname === '/fixture/totp') {
          content = form('/fixture/phone', 'Enter the code from your authenticator app', '<input name="code" autocomplete="one-time-code" />')
        } else if (url.pathname === '/fixture/phone') {
          assert.match(new URLSearchParams(route.request().postData()).get('code'), /^\d{6}$/)
          content = form('/fixture/sms', 'Phone number required', '<input type="tel" name="phone" /><label><input type="radio" name="delivery" />Text message</label>')
        } else if (url.pathname === '/fixture/sms') {
          content = form('/fixture/workspace', 'Enter the SMS code', '<input name="code" autocomplete="one-time-code" />')
        } else if (url.pathname === '/fixture/workspace') {
          content = form(`http://localhost:1455/auth/callback?code=fixture-code&state=${state}`, 'Select a workspace', '<label><input type="radio" name="workspace" checked />Default</label>')
        } else throw new Error('Unexpected fixture navigation')
        await route.fulfill({ contentType: 'text/html', body: content })
      })
      return context
    }
  } })
  const ids = ['fixture-task-00001', 'fixture-task-00002', 'fixture-task-00003']
  try {
    for (const id of ids) await runner.start({ task_id: id, owner_id: 1, email: `${id}@example.test`, password: 'Synthetic-only-test', totp_secret: 'JBSWY3DPEHPK3PXP', oauth_session_id: `session-${id}`, auth_url: `https://auth.openai.com/oauth/authorize?redirect_uri=${encodeURIComponent('http://localhost:1455/auth/callback')}&state=${id}` })
    const phones = new Set()
    const codes = new Set()
    const deadline = Date.now() + 30000
    while (Date.now() < deadline) {
      for (const [index, id] of ids.entries()) {
        const task = runner.get(id, 1)
        assert.ok(!['failed', 'blocked'].includes(task.status), `${id}: ${task.status} ${task.reason}`)
        if (task.stage === 'phone_required' && !phones.has(id)) { runner.phone(id, 1, `+1202555012${index}`); phones.add(id) }
        if (task.stage === 'sms_waiting' && !codes.has(id)) { runner.smsCode(id, 1, '123456'); codes.add(id) }
      }
      if (ids.every(id => runner.get(id, 1).status === 'completed')) break
      await new Promise(resolve => setTimeout(resolve, 100))
    }
    for (const id of ids) {
      const task = runner.get(id, 1)
      assert.equal(task.status, 'completed', `${id}: ${task.stage} ${task.reason}`)
      assert.equal(new URL(task.callback_url).searchParams.get('state'), id)
    }
    assert.equal(contexts.length, 3)
    assert.equal(new Set(contexts).size, 3)
  } finally {
    for (const id of ids) await runner.cancel(id, 1).catch(() => {})
    await browser.close()
  }
})
