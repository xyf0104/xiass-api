import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { test } from 'node:test'
import { chromium } from '@playwright/test'
import { Server } from 'proxy-chain'
import { createManualProxyContext } from './browser-proxy.mjs'

test('manual direct and selected egress override the browser default without sharing cookies', async () => {
  const fixture = http.createServer((_req, res) => {
    res.setHeader('Content-Type', 'text/html')
    res.end('<input id="text"><p>XIASS browser fixture</p>')
  })
  fixture.listen(0, '127.0.0.1')
  await once(fixture, 'listening')
  let selectedRequests = 0
  const selected = new Server({
    host: '127.0.0.1', port: 0,
    prepareRequestFunction: ({username, password}) => {
      if (username !== 'test' || password !== 'secret') return { requestAuthentication: true }
      selectedRequests += 1
      return {}
    }
  })
  await selected.listen()
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--proxy-bypass-list=<-loopback>'], proxy: { server: 'http://127.0.0.1:1' } })
  try {
    const direct = await createManualProxyContext(browser)
    const proxied = await createManualProxyContext(browser, { proxy: { server: `http://127.0.0.1:${selected.port}`, username: 'test', password: 'secret' } })
    const address = `http://127.0.0.1:${fixture.address().port}`
    const first = await direct.newPage()
    await first.goto(address)
    await first.locator('#text').fill('中文 Test ABC')
    assert.equal(await first.locator('#text').inputValue(), '中文 Test ABC')
    await direct.addCookies([{ name: 'fixture', value: 'direct-only', url: address }])
    const second = await proxied.newPage()
    await second.goto(address)
    assert.ok(selectedRequests > 0)
    assert.equal((await proxied.cookies(address)).length, 0)
    assert.ok((await second.screenshot()).length > 100)
    await direct.close()
    await proxied.close()
  } finally {
    await browser.close()
    await selected.close(true)
    await new Promise(resolve => fixture.close(resolve))
  }
})
