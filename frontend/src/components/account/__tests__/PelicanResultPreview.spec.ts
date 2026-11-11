import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createRequire } from 'node:module'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import PelicanResultPreview from '../PelicanResultPreview.vue'

const api = vi.hoisted(() => ({ getPelicanResult: vi.fn() }))
vi.mock('@/api/admin/pelicanBenchmark', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const wrappers: VueWrapper[] = []
async function render(html: string) {
  api.getPelicanResult.mockResolvedValue({ id: 'result', status: 'succeeded', html })
  const wrapper = mount(PelicanResultPreview, { props: { id: 'result', title: 'Preview' } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks(); vi.useRealTimers() })

describe('PelicanResultPreview', () => {
  it('sends the complete HTML once to the isolated shell without inheriting the parent CSP', async () => {
    const html = '<html><body onload="draw()"><canvas></canvas><script>function draw(){}</script></body></html>'
    const wrapper = await render(html)
    const iframe = wrapper.get('iframe')
    expect(iframe.attributes('sandbox')).toBe('allow-scripts')
    expect(iframe.attributes('referrerpolicy')).toBe('no-referrer')
    expect(iframe.attributes('src')).toBe('/api/v1/pelican-preview')
    expect(iframe.attributes('srcdoc')).toBeUndefined()
    const postMessage = vi.fn()
    Object.defineProperty(iframe.element, 'contentWindow', { value: { postMessage }, configurable: true })
    await iframe.trigger('load')
    expect(postMessage).toHaveBeenCalledExactlyOnceWith({ type: 'xiass-pelican-render', html }, '*')
    await iframe.trigger('load')
    expect(postMessage).toHaveBeenCalledTimes(1)
    window.dispatchEvent(new MessageEvent('message', { data: { type: 'pelican-preview-size', height: 20000 } }))
    await flushPromises()
    expect(iframe.attributes('style')).toBeUndefined()
  })

  it('never renders failed or mismatched results', async () => {
    for (const result of [
      { id: 'result', status: 'failed', html: '<html>partial</html>' },
      { id: 'other', status: 'succeeded', html: '<html>wrong result</html>' }
    ]) {
      api.getPelicanResult.mockResolvedValue(result)
      const wrapper = mount(PelicanResultPreview, { props: { id: 'result', title: 'Preview' } })
      wrappers.push(wrapper)
      await flushPromises()
      expect(wrapper.find('iframe').exists()).toBe(false)
      expect(wrapper.get('[role="status"]').text()).toContain('resultFailed')
    }
  })

  it('aborts stale requests and gives changed results a fresh isolated document', async () => {
    const wrapper = await render('<html>first</html>')
    const firstFrame = wrapper.get('iframe').element
    const firstSignal = api.getPelicanResult.mock.calls[0][1] as AbortSignal
    api.getPelicanResult.mockResolvedValue({ id: 'second', status: 'succeeded', html: '<html>second</html>' })
    await wrapper.setProps({ id: 'second' })
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    expect(wrapper.get('iframe').element).not.toBe(firstFrame)
    const postMessage = vi.fn()
    Object.defineProperty(wrapper.get('iframe').element, 'contentWindow', { value: { postMessage }, configurable: true })
    await wrapper.get('iframe').trigger('load')
    expect(postMessage).toHaveBeenCalledExactlyOnceWith({ type: 'xiass-pelican-render', html: '<html>second</html>' }, '*')
    const activeSignal = api.getPelicanResult.mock.calls.at(-1)![1] as AbortSignal
    wrapper.unmount()
    expect(activeSignal.aborted).toBe(true)
  })

  it.each([false, true])('shows a failure if the shell never acknowledges the HTML (load=%s)', async loaded => {
    vi.useFakeTimers()
    const wrapper = await render('<html>result</html>')
    const iframe = wrapper.get('iframe')
    const frameWindow = { postMessage: vi.fn() }
    Object.defineProperty(iframe.element, 'contentWindow', { value: frameWindow, configurable: true })
    if (loaded) await iframe.trigger('load')
    for (const [origin, source] of [['https://untrusted.example', frameWindow], ['null', {}]] as const) {
      const event = new MessageEvent('message', { data: { type: 'xiass-pelican-render-accepted' }, origin })
      Object.defineProperty(event, 'source', { value: source })
      window.dispatchEvent(event)
    }
    await vi.advanceTimersByTimeAsync(15_000)
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.get('[role="status"]').text()).toContain('resultFailed')
  })

  it('keeps an acknowledged preview running beyond the shell timeout', async () => {
    vi.useFakeTimers()
    const wrapper = await render('<html>result</html>')
    const iframe = wrapper.get('iframe')
    const frameWindow = { postMessage: vi.fn() }
    Object.defineProperty(iframe.element, 'contentWindow', { value: frameWindow, configurable: true })
    await iframe.trigger('load')
    const event = new MessageEvent('message', { data: { type: 'xiass-pelican-render-accepted' }, origin: 'null' })
    Object.defineProperty(event, 'source', { value: frameWindow })
    window.dispatchEvent(event)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(wrapper.find('iframe').exists()).toBe(true)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(frameWindow.postMessage).toHaveBeenCalledTimes(1)
  })
})

// Opt in with PELICAN_BROWSER_TEST=1; PLAYWRIGHT_MODULE may point to a shared runtime.
// These use the component's markup/CSS and the exact embedded server shell.
// Unit tests above cover the Vue load-event handoff; browser cases cover rendering under CSP.
describe.skipIf(process.env.PELICAN_BROWSER_TEST !== '1')('Pelican full-document browser regressions', () => {
  const fixtures = {
    long: '<html><body style="margin:0"><main style="height:6000px;background:#edf6f0;position:relative"><b id="first">FIRST</b><b id="last" style="position:absolute;bottom:0;right:0;background:#e11d48">LAST</b></main></body></html>',
    wide: '<html><body style="margin:0"><main style="width:3600px;height:900px;background:#eef2ff;position:relative"><b id="first">FIRST</b><b id="last" style="position:absolute;bottom:0;right:0;background:#e11d48">LAST</b></main></body></html>',
    viewport: '<html><body style="margin:0"><main style="height:200vh;width:150vw;background:#eff6ff;position:relative"><b id="first">FIRST</b><b id="last" style="position:absolute;bottom:0;right:0;background:#e11d48">LAST</b></main></body></html>',
    dynamic: '<html><body style="margin:0"><main style="height:100vh;background:#f0fdf4;position:relative"><b id="first">FIRST</b><b id="last" style="position:absolute;bottom:0;right:0;background:#e11d48">LAST</b></main><script>setTimeout(()=>{document.querySelector("main").style.height="5000px";document.querySelector("main").style.width="2800px"},200)</script></body></html>',
    animated: '<html><body style="margin:0"><main style="height:1200px;background:#f0fdf4;position:relative"><b id="first">FIRST</b><svg width="200" height="200"><circle cx="100" cy="100" r="50" fill="blue"/></svg><b id="last" style="position:absolute;bottom:0;right:0;background:#e11d48">LAST</b></main><script>function animate(t){document.querySelector("circle").setAttribute("cx",String(100+Math.sin(t/100)*40));requestAnimationFrame(animate)}requestAnimationFrame(animate)</script></body></html>'
  }
  it.each(Object.entries(fixtures))('fits the entire %s result without scroll or viewport feedback on desktop and mobile', async (name, html) => {
    const require = createRequire(import.meta.url)
    const browserRequire = createRequire(require.resolve(process.env.PLAYWRIGHT_MODULE || 'playwright'))
    const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
    const { PNG } = browserRequire('pngjs')
    const browser = await chromium.launch({ headless: true, ...(process.env.PELICAN_CHROME ? { executablePath: process.env.PELICAN_CHROME } : {}) })
    try {
      const resultHTML = html.replace('id="first"', 'id="first" style="display:block;width:100px;height:100px;background:#16a34a"').replace('background:#e11d48', 'width:100px;height:100px;background:#e11d48')
      const wrapper = await render(resultHTML)
      const css = readFileSync(fileURLToPath(import.meta.url).replace(/__tests__\/[^/]+$/, 'PelicanResultPreview.vue'), 'utf8').split('<style scoped>')[1].split('</style>')[0]
      const routes = new URL('../../../../../backend/internal/server/routes/', import.meta.url)
      const shell = readFileSync(new URL('pelican_preview.html', routes), 'utf8').replaceAll('__MAX_HTML_BYTES__', '1048576')
      const policy = readFileSync(new URL('pelican_preview.go', routes), 'utf8').match(/const pelicanPreviewCSP = "([^"]+)"/)![1]
      const payload = JSON.stringify({ type: 'xiass-pelican-render', html: resultHTML }).replace(/</g, '\\u003c')
      for (const width of [1440, 390]) {
        const page = await browser.newPage({ viewport: { width, height: 900 } })
        await page.route('http://pelican.test/**', async (route: { request: () => { url: () => string }; fulfill: (options: object) => Promise<void> }) => {
          if (new URL(route.request().url()).pathname === '/api/v1/pelican-preview') {
            await route.fulfill({ status: 200, contentType: 'text/html', headers: { 'Content-Security-Policy': policy, 'X-Frame-Options': 'SAMEORIGIN' }, body: shell })
          } else {
            await route.fulfill({ status: 200, contentType: 'text/html', headers: { 'Content-Security-Policy': "default-src 'self'; script-src 'self' 'nonce-test'; style-src 'unsafe-inline'; frame-src 'self'" }, body: `<style>${css}</style>${wrapper.html()}<script nonce="test">const frame=document.querySelector('iframe');frame.addEventListener('load',()=>frame.contentWindow.postMessage(${payload},'*'),{once:true});</script>` })
          }
        })
        await page.goto('http://pelican.test/')
        await page.waitForTimeout(1200)
        const frame = page.frames()[1]
        const inspect = () => frame.evaluate(() => ({
          viewport: [innerWidth, innerHeight], transform: document.documentElement.style.transform,
          markers: ['first', 'last'].map(id => {
            const rect = document.getElementById(id)!.getBoundingClientRect()
            return { x: rect.x, y: rect.y, right: rect.right, bottom: rect.bottom }
          })
        }))
        const first = await inspect()
        expect(first.viewport).toEqual([1100, 690])
        for (const rect of first.markers) {
          expect(rect.x).toBeGreaterThanOrEqual(-0.1)
          expect(rect.y).toBeGreaterThanOrEqual(-0.1)
          expect(rect.right).toBeLessThanOrEqual(1100.1)
          expect(rect.bottom).toBeLessThanOrEqual(690.1)
        }
        await frame.evaluate(() => {
          const metrics = { writes: 0 }
          new MutationObserver(records => { metrics.writes += records.length }).observe(document.documentElement, { attributes: true, attributeFilter: ['style'] })
          Object.assign(window, { pelicanMetrics: metrics })
        })
        await page.waitForTimeout(2200)
        expect((await inspect()).transform).toBe(first.transform)
        expect(await frame.evaluate(() => (window as unknown as { pelicanMetrics: { writes: number } }).pelicanMetrics.writes), 'idle and SVG attribute animation must not rescale the document').toBe(0)
        const box = await page.locator('.preview-shell').boundingBox()
        expect(box.width).toBe(220)
        expect(box.height).toBe(138)
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        const png = PNG.sync.read(await page.locator('.preview-shell').screenshot())
        let red = 0
        let green = 0
        for (let i = 0; i < png.data.length; i += 4) {
          const [r, g, b] = png.data.subarray(i, i + 3)
          if (r > 170 && g < 100 && b < 140) red++
          if (g > 100 && r < 100 && b < 130) green++
        }
        expect(red, 'last marker must actually paint, not merely have in-bounds geometry').toBeGreaterThan(0)
        expect(green, 'first marker must actually paint').toBeGreaterThan(0)
        if (process.env.PELICAN_SCREENSHOTS) await page.screenshot({ path: `${process.env.PELICAN_SCREENSHOTS}/full-preview-${name}-${width}.png` })
        await page.close()
      }
    } finally { await browser.close() }
  }, 20000)
})
