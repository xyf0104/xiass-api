import assert from 'node:assert/strict'
import http from 'node:http'
import net from 'node:net'
import { once } from 'node:events'
import { test } from 'node:test'
import { createProxyContext } from './browser-proxy.mjs'

test('authenticated SOCKS5 egress is retained and the per-context bridge closes', { timeout: 15000 }, async () => {
  const sockets = new Set()
  const observed = []
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
        assert.equal(username, 'fixture-user')
        assert.equal(password, 'fixture-password')
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
    context = await createProxyContext(browser, { proxy: { server: `socks5://127.0.0.1:${socks.address().port}`, username: 'fixture-user', password: 'fixture-password' } })
    const proxy = new URL(received.server)
    assert.equal(proxy.hostname, '127.0.0.1')
    assert.notEqual(received.password, 'fixture-password')
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
