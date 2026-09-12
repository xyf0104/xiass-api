import { randomBytes } from 'node:crypto'
import { Server } from 'proxy-chain'

// Chromium cannot authenticate a SOCKS5 proxy itself. This listener shares the
// browser container's network namespace and exists only for one private context.
export async function createProxyContext(browser, options = {}) {
  const proxy = options.proxy
  if (!proxy?.server?.startsWith('socks5:') || (!proxy.username && !proxy.password)) return browser.newContext(options)
  const upstream = new URL(proxy.server)
  upstream.protocol = 'socks5h:'
  upstream.username = proxy.username || ''
  upstream.password = proxy.password || ''
  const username = randomBytes(16).toString('hex')
  const password = randomBytes(24).toString('hex')
  const bridge = new Server({
    host: '127.0.0.1', port: 0, verbose: false,
    prepareRequestFunction: request => ({
      requestAuthentication: request.username !== username || request.password !== password,
      upstreamProxyUrl: upstream.toString()
    })
  })
  // Only sanitized workflow failures reach the admin UI, never proxy credentials.
  bridge.on('requestFailed', () => {})
  await bridge.listen()
  try {
    const context = await browser.newContext({ ...options, proxy: { server: `http://127.0.0.1:${bridge.port}`, username, password } })
    const close = context.close.bind(context)
    let closed = false
    context.close = async (...args) => {
      await close(...args)
      if (!closed) {
        await bridge.close(true)
        upstream.username = upstream.password = ''
        closed = true
      }
    }
    return context
  } catch (error) {
    await bridge.close(true)
    upstream.username = upstream.password = ''
    throw error
  }
}
