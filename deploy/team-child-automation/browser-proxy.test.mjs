import assert from 'node:assert/strict'
import http from 'node:http'
import net from 'node:net'
import { EventEmitter, once } from 'node:events'
import { test } from 'node:test'
import { createProxyContext } from './browser-proxy.mjs'

test('authenticated SOCKS5 egress is retained and the per-context bridge closes', { timeout: 15000 }, async () => {
  const sockets = new Set()
  const observed = []
  const credentials = { username: 'fixture%40user:@', password: 'fixture%password%20:/@' }
  const origin = http.createServer((req, res) => res.end('fixture-through-socks'))
  origin.listen(0, '127.0.0.1')
  await once(origin, 'listening')
  const socks = net.createServer(socket => {
    sockets.add(socket)
    socket.on('close', () => sockets.delete(socket))
    socket.on('error', () => {})
    let data = Buffer.alloc(0)
    let stage = 0
    const read = chunk => {
      data = Buffer.concat([data, chunk])
      if (stage === 0 && data.length >= 2 + data[1]) {
        assert.equal(data[0], 5)
        data = data.subarray(2 + data[1])
        socket.write(Buffer.from([5, 2]))
        stage = 1
      }
      if (stage === 1 && data.length >= 3 + data[1] && data.length >= 3 + data[1] + data[2 + data[1]]) {
        const username = data.subarray(2, 2 + data[1]).toString()
        const password = data.subarray(3 + data[1], 3 + data[1] + data[2 + data[1]]).toString()
        assert.equal(username, credentials.username)
        assert.equal(password, credentials.password)
        data = data.subarray(3 + data[1] + data[2 + data[1]])
        socket.write(Buffer.from([1, 0]))
        stage = 2
      }
      if (stage === 2 && data.length >= 7 && data[3] === 3 && data.length >= 7 + data[4]) {
        observed.push(data.subarray(5, 5 + data[4]).toString())
        assert.equal(observed.at(-1), 'fixture-target.test', 'DNS must be delegated to the chosen SOCKS server')
        const remaining = data.subarray(7 + data[4])
        socket.off('data', read)
        const remote = net.connect(origin.address().port, '127.0.0.1', () => {
          socket.write(Buffer.from([5, 0, 0, 1, 127, 0, 0, 1, 0, 80]))
          if (remaining.length) remote.write(remaining)
          socket.pipe(remote).pipe(socket)
        })
        remote.on('error', () => socket.destroy())
        socket.on('close', () => remote.destroy())
        stage = 3
      }
    }
    socket.on('data', read)
  })
  socks.listen(0, '127.0.0.1')
  await once(socks, 'listening')
  let received
  let closed = 0
  const browser = { async newContext(options) { received = options.proxy; return { close: async () => { closed++ } } } }
  let context
  try {
    context = await createProxyContext(browser, { proxy: { server: `socks5://127.0.0.1:${socks.address().port}`, ...credentials } })
    const proxy = new URL(received.server)
    assert.equal(proxy.hostname, '127.0.0.1')
    assert.notEqual(received.password, credentials.password)
    const denied = await new Promise((resolve, reject) => {
      const req = http.get({ hostname: proxy.hostname, port: proxy.port, path: 'http://fixture-target.test/echo' }, res => {
        res.resume()
        res.on('end', () => resolve(res.statusCode))
      })
      req.on('error', reject)
    })
    assert.equal(denied, 407)
    assert.equal(observed.length, 0, 'bridge authentication must precede upstream connection')
    const result = await new Promise((resolve, reject) => {
      const req = http.get({ hostname: proxy.hostname, port: proxy.port, path: 'http://fixture-target.test/echo', headers: { 'proxy-authorization': `Basic ${Buffer.from(`${received.username}:${received.password}`).toString('base64')}` } }, res => {
        let body = ''
        res.on('data', chunk => { body += chunk })
        res.on('end', () => resolve({ status: res.statusCode, body }))
      })
      req.on('error', reject)
    })
    assert.deepEqual(result, { status: 200, body: 'fixture-through-socks' })
    assert.equal(observed.length, 1)
    const tunnelResponse = await new Promise((resolve, reject) => {
      const req = http.request({ hostname: proxy.hostname, port: proxy.port, method: 'CONNECT', path: 'fixture-target.test:443', headers: { 'proxy-authorization': `Basic ${Buffer.from(`${received.username}:${received.password}`).toString('base64')}` } })
      req.on('connect', (res, socket, head) => {
        if (res.statusCode !== 200) { socket.destroy(); reject(new Error(`CONNECT status ${res.statusCode}`)); return }
        let body = head.toString()
        socket.on('data', chunk => { body += chunk })
        socket.on('end', () => resolve(body))
        socket.on('error', reject)
        socket.write('GET /echo HTTP/1.1\r\nHost: fixture-target.test\r\nConnection: close\r\n\r\n')
      })
      req.on('error', reject)
      req.end()
    })
    assert.match(tunnelResponse, /fixture-through-socks/)
    assert.equal(observed.length, 2, 'HTTP and HTTPS CONNECT must both delegate DNS to SOCKS')
    await context.close()
    assert.equal(closed, 1)
    await new Promise(resolve => {
      const socket = net.connect(Number(proxy.port), proxy.hostname)
      socket.on('connect', () => { socket.destroy(); assert.fail('proxy bridge survived context close') })
      socket.on('error', error => { assert.equal(error.code, 'ECONNREFUSED'); resolve() })
    })
  } finally {
    await context?.close()
    for (const socket of sockets) socket.destroy()
    await new Promise(resolve => socks.close(resolve))
    await new Promise(resolve => origin.close(resolve))
  }
})

test('ordinary HTTP proxies keep native Chromium authentication unchanged', async () => {
  const options = { proxy: { server: 'http://127.0.0.1:8080', username: 'user', password: 'password' } }
  await createProxyContext({ async newContext(actual) { assert.deepEqual(actual, options); return {} } }, options)
})

test('authenticated SOCKS5H upstream is normalized through the per-context bridge', async () => {
  let received
  const browser = { async newContext(options) { received = options.proxy; return { close: async () => {} } } }
  const context = await createProxyContext(browser, {
    proxy: { server: 'socks5h://127.0.0.1:1080', username: 'fixture-user', password: 'fixture-password' }
  })
  assert.ok(received.server.startsWith('http://127.0.0.1:'))
  await context.close()
})

test('unauthenticated SOCKS aliases use native Chromium with remote DNS', async () => {
  for (const protocol of ['socks5', 'socks5h', 'SOCKS5H']) {
    let received
    await createProxyContext({ async newContext(options) { received = options } }, {
      locale: 'en-US', proxy: { server: `${protocol}://127.0.0.1:1080` }
    })
    assert.deepEqual(received, { locale: 'en-US', proxy: { server: 'socks5://127.0.0.1:1080' } })
  }
})

async function assertBridgeClosed(server) {
  const url = new URL(server)
  await new Promise((resolve, reject) => {
    const socket = net.connect(Number(url.port), url.hostname)
    socket.on('connect', () => { socket.destroy(); reject(new Error('proxy bridge survived context cleanup')) })
    socket.on('error', error => { error.code === 'ECONNREFUSED' ? resolve() : reject(error) })
  })
}

test('bridge closes when context creation or context close fails', async () => {
  const options = { proxy: { server: 'socks5://127.0.0.1:1080', username: 'fixture', password: 'secret' } }
  for (const failure of ['create', 'close']) {
    let received
    const browser = { async newContext(actual) {
      received = actual.proxy
      if (failure === 'create') throw new Error('create failed')
      return { async close() { throw new Error('close failed') } }
    } }
    if (failure === 'create') {
      await assert.rejects(createProxyContext(browser, options), /create failed/)
    } else {
      const context = await createProxyContext(browser, options)
      await assert.rejects(context.close(), /close failed/)
      await assert.rejects(context.close(), /close failed/)
    }
    await assertBridgeClosed(received.server)
  }
})

test('browser-originated context close releases the bridge', async () => {
  let received
  const context = new EventEmitter()
  context.close = async () => {}
  await createProxyContext({ async newContext(options) { received = options.proxy; return context } }, {
    proxy: { server: 'SOCKS5H://127.0.0.1:1080', username: 'fixture', password: 'secret' }
  })
  context.emit('close')
  await new Promise(setImmediate)
  await assertBridgeClosed(received.server)
  await context.close()
})
