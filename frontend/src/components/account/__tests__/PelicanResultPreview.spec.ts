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
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

describe('PelicanResultPreview', () => {
  it('keeps the opaque sandbox and restrictive CSP without accepting parent sizing messages', async () => {
    const wrapper = await render('<html><body><div style="height:6000px;width:3200px">Long result</div></body></html>')
    const iframe = wrapper.get('iframe')
    expect(iframe.attributes('sandbox')).toBe('allow-scripts')
    expect(iframe.attributes('referrerpolicy')).toBe('no-referrer')
    const srcdoc = iframe.attributes('srcdoc')
    expect(srcdoc).toContain("connect-src 'none'")
    expect(srcdoc).toContain("form-action 'none'")
    expect(srcdoc).toContain("img-src data:")
    expect(srcdoc).toContain('root.scrollWidth')
    expect(srcdoc).toContain('root.scrollHeight')
    expect(srcdoc).not.toContain('parent.postMessage')
    expect(srcdoc).not.toContain('setInterval')
    expect(srcdoc).not.toContain('attributes:true')
    window.dispatchEvent(new MessageEvent('message', { data: { type: 'pelican-preview-size', height: 20000 } }))
    await flushPromises()
    expect(iframe.attributes('style')).toBeUndefined()
  })
})

// Opt in with PELICAN_BROWSER_TEST=1; PLAYWRIGHT_MODULE may point to a shared runtime.
// These use the mounted component's real srcdoc and CSS, not a hand-written preview.
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
      const wrapper = await render(html.replace('id="first"', 'id="first" style="display:block;width:100px;height:100px;background:#16a34a"').replace('background:#e11d48', 'width:100px;height:100px;background:#e11d48'))
      const css = readFileSync(fileURLToPath(import.meta.url).replace(/__tests__\/[^/]+$/, 'PelicanResultPreview.vue'), 'utf8').split('<style scoped>')[1].split('</style>')[0]
      for (const width of [1440, 390]) {
        const page = await browser.newPage({ viewport: { width, height: 900 } })
        await page.setContent(`<style>${css}</style>${wrapper.html()}`)
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
