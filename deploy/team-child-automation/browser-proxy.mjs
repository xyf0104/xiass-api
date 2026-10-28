import { randomBytes } from 'node:crypto'
import { Server } from 'proxy-chain'

// Chromium cannot authenticate a SOCKS5 proxy itself. This listener shares the
// browser/automation network namespace and exists only for one private context.
export async function createProxyContext(browser, options = {}) {
  const proxy = options.proxy
  if (!proxy?.server) return browser.newContext(options)
  const upstream = new URL(proxy.server)
  if (!['socks5:', 'socks5h:'].includes(upstream.protocol)) return browser.newContext(options)
  if (!proxy.username && !proxy.password) {
    return browser.newContext({ ...options, proxy: { ...proxy, server: `socks5://${upstream.host}` } })
  }
  upstream.protocol = 'socks5h:'
  // URL setters leave literal '%' intact; proxy-chain decodes userinfo once.
  upstream.username = encodeURIComponent(proxy.username || '')
  upstream.password = encodeURIComponent(proxy.password || '')
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
  let closing
  const closeBridge = () => {
    closing ??= bridge.close(true).finally(() => { upstream.username = upstream.password = '' })
    return closing
  }
  try {
    const context = await browser.newContext({ ...options, proxy: { server: `http://127.0.0.1:${bridge.port}`, username, password } })
    const close = context.close.bind(context)
    context.once?.('close', () => { closeBridge().catch(() => {}) })
    context.close = async (...args) => {
      try {
        return await close(...args)
      } finally {
        await closeBridge()
      }
    }
    return context
  } catch (error) {
    await closeBridge()
    throw error
  }
}
