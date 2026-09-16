import assert from 'node:assert/strict'
import fs from 'node:fs'
import { test } from 'node:test'
import { chromium } from '@playwright/test'
import { submitPhoneOnOpenAI } from './server.mjs'

const executablePath = process.env.CHROMIUM_EXECUTABLE_PATH || chromium.executablePath()
const source = fs.readFileSync(new URL('../../tools/xiass-adspower-helper/openai_automation.go', import.meta.url), 'utf8')
const helperScript = source.match(/const oauthSelectTextMessageJS = `([\s\S]*?)`/)[1]

test('SMS channel is confirmed before submitting a phone', { skip: !fs.existsSync(executablePath) }, async () => {
  const browser = await chromium.launch({ executablePath, headless: true })
  try {
    for (const engine of ['helper', 'team']) {
      for (const mode of ['radio', 'aria', 'missing', 'inert', 'unbound-label']) {
        const page = await browser.newPage()
        const sms = mode === 'radio' ? '<label><input type="radio" name="delivery" value="sms">Text message</label>'
          : mode === 'aria' ? '<button type="button" role="radio" aria-checked="false" onclick="this.setAttribute(\'aria-checked\',\'true\')">Text message</button>'
            : mode === 'inert' ? '<button type="button" role="radio" aria-checked="false">Text message</button>'
              : mode === 'unbound-label' ? '<label>Text message</label>' : ''
        await page.setContent(`<form><input type="tel"><label><input type="radio" name="delivery" checked>WhatsApp</label>${sms}<button type="submit">Continue</button></form><script>window.sent=false;document.querySelector('form').onsubmit=e=>{e.preventDefault();window.sent=true;document.body.innerHTML='SMS code <input autocomplete="one-time-code">'}</script>`)
        const run = () => engine === 'helper' ? page.evaluate(helperScript) : submitPhoneOnOpenAI(page, '+12025550123')
        if (['radio', 'aria'].includes(mode)) {
          await run()
          if (engine === 'team') assert.equal(await page.evaluate(() => window.sent), true)
        } else {
          await assert.rejects(run)
          assert.equal(await page.evaluate(() => window.sent), false, `${engine}: ${mode} must not send`)
        }
        await page.close()
      }
    }
  } finally {
    await browser.close()
  }
})
