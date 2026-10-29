import assert from 'node:assert/strict'
import fs from 'node:fs'
import { test } from 'node:test'
import { chromium } from '@playwright/test'
import { BatchOAuthRunner, profileFormInputs } from './batch-oauth.mjs'

process.env.NODE_ENV = 'test'
const { fillProfile, fillVerificationCode, submitPhoneOnOpenAI, postSMSNextState } = await import('./server.mjs')
const executablePath = process.env.CHROMIUM_EXECUTABLE_PATH || chromium.executablePath()
const browserOptions = { skip: !fs.existsSync(executablePath), timeout: 45000 }
const phoneFields = '<input type="text" aria-label="Search countries"><select><option>United States</option><option>Suriname</option></select><input type="tel" name="__reservedForPhoneNumberInput_tel" inputmode="numeric" aria-label="National number"><label><input type="radio" name="delivery" value="whatsapp" checked>WhatsApp</label><label><input type="radio" name="delivery" value="sms">Text Message</label>'
const profileFields = '<input name="name" autocomplete="name"><input name="age" inputmode="numeric" aria-label="Age">'
const form = (title, action, fields) => `<html><body><h1>${title}</h1><form method="post" action="${action}">${fields}<button type="submit">Continue</button></form></body></html>`

test('country dropdowns never become profile forms or receive profile defaults', browserOptions, async () => {
  const browser = await chromium.launch({ executablePath, headless: true })
  try {
    const page = await browser.newPage()
    await page.route('**/*', route => route.fulfill({ contentType: 'text/html', body: form('Phone number required', '/phone-verification', phoneFields) }))
    await page.goto('https://auth.openai.com/add-phone')
    assert.match(await page.locator('body').innerText(), /name/i, 'reproduces the old Suriname match')
    assert.equal(await profileFormInputs(page), null)
    await assert.rejects(fillProfile(page), /不是可填写/)
    assert.equal(await page.locator('input[type=tel]').inputValue(), '')
    assert.equal(await page.locator('input[type=text]').inputValue(), '')
    assert.equal((await postSMSNextState(page, { authURL: 'https://auth.openai.com/oauth/authorize?state=fixture' })).kind, 'unknown')

    await page.goto('https://auth.openai.com/about-you')
    await page.setContent(form('Tell us about you', '/workspace', profileFields))
    assert.ok(await profileFormInputs(page))
    assert.equal((await postSMSNextState(page, { authURL: 'https://auth.openai.com/oauth/authorize?state=fixture' })).kind, 'profile')
    await page.setContent('<label for="opaque-a">Full name</label><input id="opaque-a"><label for="opaque-b">Age</label><input id="opaque-b" type="number">')
    assert.ok(await profileFormInputs(page), 'associated labels identify fields with opaque IDs')
    await page.locator('#opaque-b').evaluate(element => { element.disabled = true })
    assert.ok(await profileFormInputs(page), 'a hydrating profile remains recognizable while fill waits for editable fields')
  } finally {
    await browser.close()
  }
})

test('production helpers complete direct, profile-only, phone-only and phone-then-profile batch paths', browserOptions, async () => {
  const browser = await chromium.launch({ executablePath, headless: true })
  try {
    for (const mode of ['direct', 'no-phone', 'phone-only', 'phone-profile']) {
      const id = `profile-fixture-${mode}`
      const stages = []
      const submissions = []
      let page
      const runner = new BatchOAuthRunner({
        validateAuthURL: value => value,
        helpers: { stepDelayMs: 0, fillProfile, fillVerificationCode, submitPhoneOnOpenAI, onProgress: event => stages.push(event.stage) },
        browser: {
          async newContext() {
            const context = await browser.newContext()
            await context.route('**/*', async route => {
              const request = route.request()
              const url = new URL(request.url())
              if (url.origin === 'http://localhost:1455') {
                await route.fulfill({ body: 'callback' })
                return
              }
              assert.equal(url.origin, 'https://auth.openai.com', 'fixture cannot access external services')
              const data = new URLSearchParams(request.postData() || '')
              let content
              if (url.pathname === '/oauth/authorize') {
                content = form('Log in', '/password', '<input type="email" name="email">')
              } else if (url.pathname === '/password') {
                content = form('Password', mode === 'direct' ? '/workspace' : mode === 'no-phone' ? '/about-you' : '/add-phone', '<input type="password" name="password">')
              } else if (url.pathname === '/add-phone') {
                content = form('Phone number required', '/phone-verification', phoneFields)
              } else if (url.pathname === '/phone-verification') {
                assert.equal(data.get('delivery'), 'sms')
                assert.equal(data.get('__reservedForPhoneNumberInput_tel'), '+12025550123')
                submissions.push('phone')
                content = form('Check your phone SMS code', mode === 'phone-profile' ? '/about-you' : '/workspace', mode === 'phone-only'
                  ? '<input type="tel" name="pin" inputmode="numeric">'
                  : '<input name="code" autocomplete="one-time-code" inputmode="numeric">')
              } else if (url.pathname === '/about-you') {
                if (mode === 'phone-profile') assert.equal(data.get('code'), '123456')
                content = form('Tell us about you', '/workspace', profileFields)
              } else if (url.pathname === '/workspace') {
                if (mode === 'phone-only') assert.equal(data.get('pin'), '123456')
                else if (mode !== 'direct') {
                  assert.equal(data.get('name'), 'black')
                  assert.equal(data.get('age'), '26')
                  submissions.push('profile')
                }
                content = form('Select a workspace', `http://localhost:1455/auth/callback?code=fixture-code&state=${id}`, '')
              } else throw new Error(`unexpected fixture path ${url.pathname}`)
              await route.fulfill({ contentType: 'text/html', body: content })
            })
            context.on('page', opened => { page = opened })
            return context
          }
        }
      })
      try {
        await runner.start({ task_id: id, owner_id: 1, email: `${mode}@example.test`, password: 'Synthetic-password', oauth_session_id: `session-${id}`, auth_url: `https://auth.openai.com/oauth/authorize?redirect_uri=${encodeURIComponent('http://localhost:1455/auth/callback')}&state=${id}` })
        let phoneSent = false
        let codeSent = false
        const deadline = Date.now() + 15000
        while (Date.now() < deadline) {
          const task = runner.get(id, 1)
          assert.ok(!['failed', 'blocked'].includes(task.status), `${mode}: ${task.stage} ${task.reason}`)
          if (task.stage === 'phone_required' && !phoneSent) {
            assert.equal(await page.locator('input[type=tel]').inputValue(), '', 'no auto-acquisition or profile defaults before operator input')
            assert.equal(await page.locator('input[type=text]').inputValue(), '')
            assert.deepEqual(submissions, [])
            runner.phone(id, 1, '+12025550123')
            phoneSent = true
          }
          if (task.stage === 'sms_waiting' && !codeSent) { runner.smsCode(id, 1, '123456'); codeSent = true }
          if (task.status === 'completed') break
          await new Promise(resolve => setTimeout(resolve, 50))
        }
        assert.equal(runner.get(id, 1).status, 'completed', `${mode}: ${runner.get(id, 1).stage}`)
        assert.equal(stages.includes('phone_required'), mode.startsWith('phone-'))
        assert.equal(stages.includes('profile'), mode === 'no-phone' || mode === 'phone-profile')
      } finally {
        await runner.cancel(id, 1).catch(() => {})
      }
    }
  } finally {
    await browser.close()
  }
})
