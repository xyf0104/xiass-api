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
async function render(html: string, preview_html?: string) {
  api.getPelicanResult.mockResolvedValue(Object.freeze({ id: 'result', status: 'succeeded', html, preview_html }))
  const wrapper = mount(PelicanResultPreview, { props: { id: 'result', title: 'Preview' } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  document.querySelectorAll('script[data-test-nonce]').forEach(script => script.remove())
  vi.clearAllMocks()
  vi.useRealTimers()
})

describe('PelicanResultPreview', () => {
  it('renders complete HTML directly without shell requests, handshakes or timeout remounts', async () => {
    vi.useFakeTimers()
    const html = '<html><body><canvas></canvas><script>window.drawn=true</script></body></html>'
    const wrapper = await render(html)
    const frame = wrapper.get('iframe')
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('src')).toBeUndefined()
    expect(frame.attributes('srcdoc')).toContain('<canvas></canvas>')
    expect(frame.attributes('srcdoc')).toContain('window.drawn=true')
    expect(wrapper.find('script').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(31_000)
    expect(wrapper.get('iframe').element).toBe(frame.element)
    expect(api.getPelicanResult).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('uses repaired HTML without modifying the saved original or adding visible annotations', async () => {
    const html = '<html><script>const point = { x=1 };</script></html>'
    const wrapper = await render(html, '<html><script>const point = { x:1 };</script></html>')
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('const point = { x:1 };')
    expect((await api.getPelicanResult.mock.results.at(-1)!.value).html).toBe(html)
    expect(wrapper.get('[data-testid="result-thumbnail"]').element.children).toHaveLength(1)
  })

  it.each(['', ' \n\t'])('uses original HTML for an empty repaired preview (%j)', async previewHTML => {
    const wrapper = await render('<html><body>Original result</body></html>', previewHTML)
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('Original result')
  })

  it('keeps nonce CSP compatibility local to the sandbox and never authorizes external scripts', async () => {
    const bootstrap = document.createElement('script')
    bootstrap.dataset.testNonce = 'true'
    bootstrap.nonce = 'test'
    document.head.appendChild(bootstrap)
    const wrapper = await render('<body onload="draw()"><script>function draw(){}</script><script src="https://external.test/run.js" nonce="forged"></script><svg><script href="https://external.test/svg.js" nonce="forged"></script></svg></body>')
    const markup = wrapper.get('iframe').attributes('srcdoc')!
    const doc = new DOMParser().parseFromString(markup, 'text/html')
    expect(doc.querySelector('script:not([src])')?.getAttribute('nonce')).toBe('test')
    expect(doc.querySelector('script[src]')?.hasAttribute('nonce')).toBe(false)
    expect(doc.querySelector('script[href]')?.hasAttribute('nonce')).toBe(false)
    expect(doc.body.hasAttribute('onload')).toBe(false)
    expect(markup).toContain('["onload"]=(function(){')
    expect(markup).toContain("script-src 'unsafe-inline'")
    expect(markup).toContain("connect-src 'none'")
    expect(markup).toContain("frame-src 'none'")
    expect(wrapper.get('iframe').attributes('sandbox')).not.toContain('allow-same-origin')
  })

  it('rejects failed, mismatched and empty results', async () => {
    for (const result of [
      { id: 'result', status: 'failed', html: '<html>partial</html>' },
      { id: 'other', status: 'succeeded', html: '<html>wrong</html>' },
      { id: 'result', status: 'succeeded', html: ' ' }
    ]) {
      api.getPelicanResult.mockResolvedValue(result)
      const wrapper = mount(PelicanResultPreview, { props: { id: 'result', title: 'Preview' } })
      wrappers.push(wrapper)
      await flushPromises()
      expect(wrapper.find('iframe').exists()).toBe(false)
      expect(wrapper.get('[role="status"]').text()).toContain('resultFailed')
    }
  })

  it('aborts stale requests and ignores responses arriving after the ID changes or unmounts', async () => {
    let resolveFirst!: (value: object) => void
    api.getPelicanResult.mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve }))
    const wrapper = mount(PelicanResultPreview, { props: { id: 'first', title: 'Preview' } })
    wrappers.push(wrapper)
    const signal = api.getPelicanResult.mock.calls[0][1] as AbortSignal
    api.getPelicanResult.mockResolvedValue({ id: 'second', status: 'succeeded', html: '<html>second</html>' })
    await wrapper.setProps({ id: 'second' })
    await flushPromises()
    const frame = wrapper.get('iframe').element
    resolveFirst({ id: 'first', status: 'succeeded', html: '<html>first</html>' })
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect(wrapper.get('iframe').element).toBe(frame)
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('second')
    const active = api.getPelicanResult.mock.calls.at(-1)![1] as AbortSignal
    wrapper.unmount()
    expect(active.aborted).toBe(true)
  })
})

// Run against a real browser with PELICAN_BROWSER_TEST=1 and PLAYWRIGHT_MODULE.
describe.skipIf(process.env.PELICAN_BROWSER_TEST !== '1')('Direct HTML under production CSP', () => {
  const fixtures = {
    long: '<main style="height:6000px;width:1100px">scene</main>',
    wide: '<main style="height:900px;width:3600px">scene</main>',
    viewport: '<main style="height:200vh;width:150vw">scene</main>',
    dynamic: '<main>scene</main><script>setTimeout(()=>{document.querySelector("main").style.height="5000px"},100)</script>',
    animated: '<main><svg width="200" height="200"><circle cx="100" cy="100" r="50" fill="blue"/></svg></main><script>function animate(t){document.querySelector("circle").setAttribute("cx",String(100+Math.sin(t/100)*40));requestAnimationFrame(animate)}requestAnimationFrame(animate)</script>',
    handlers: '<main><form><input name="choice" value="form"><button type="button" value="drawn" onclick="document.body.dataset.clicked=value+choice.value;return false">Run</button><button id="local" type="button" value="original" onclick="var value=\'local\';document.body.dataset.local=value">Local</button><textarea oninput="document.body.dataset.input=value">initial text</textarea></form></main><script onerror="document.body.dataset.scriptError=\'yes\'">window.onload=()=>{document.body.dataset.loaded="script"}</script>'
  }
  it.each(Object.entries(fixtures))('paints and fits %s on first load, moves and reopen without a network shell', async (name, html) => {
    const require = createRequire(import.meta.url)
    const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
    const browser = await chromium.launch({ headless: true, ...(process.env.PELICAN_CHROME ? { executablePath: process.env.PELICAN_CHROME } : {}) })
    const bootstrap = document.createElement('script')
    bootstrap.dataset.testNonce = 'true'
    bootstrap.nonce = 'test'
    document.head.appendChild(bootstrap)
    try {
      const wrapper = await render(`<html><body style="margin:0" onload="document.body.dataset.loaded='yes'">${html}<script>document.body.dataset.inline='yes'</script></body></html>`)
      const css = readFileSync(fileURLToPath(import.meta.url).replace(/__tests__\/[^/]+$/, 'PelicanResultPreview.vue'), 'utf8').split('<style scoped>')[1].split('</style>')[0]
      for (const width of [1440, 390]) {
        const page = await browser.newPage({ viewport: { width, height: 900 } })
        const errors: string[] = []
        const requests: string[] = []
        page.on('pageerror', (error: Error) => errors.push(error.message))
        page.on('request', (request: { url(): string }) => requests.push(request.url()))
        await page.route('**/*', (route: { fulfill(options: object): Promise<void> }) => route.fulfill({
          status: 200, contentType: 'text/html',
          headers: { 'Content-Security-Policy': "default-src 'self'; script-src 'self' 'nonce-test'; style-src 'unsafe-inline'; frame-src 'self'; connect-src 'self'" },
          body: `<style>${css}</style>${wrapper.html()}`
        }))
        await page.goto('http://pelican.test/', { waitUntil: 'load' })
        for (const phase of ['initial', 'move', 'reopen']) {
          if (phase === 'move') await page.evaluate(() => document.body.appendChild(document.querySelector('.preview-shell')!))
          if (phase === 'reopen') await page.evaluate(() => {
            const old = document.querySelector('.preview-shell')!
            old.replaceWith(old.cloneNode(true))
          })
          await page.waitForTimeout(350)
          const frame = page.frames()[1]
          expect(await frame.evaluate(() => document.body.dataset)).toMatchObject({ inline: 'yes', loaded: name === 'handlers' ? 'script' : 'yes' })
          if (name === 'handlers') {
            await frame.locator('button').first().evaluate((button: HTMLButtonElement) => button.click())
            expect(errors).toEqual([])
            expect(await frame.evaluate(() => document.body.dataset.clicked)).toBe('drawnform')
            await frame.locator('#local').evaluate((button: HTMLButtonElement) => button.click())
            expect(await frame.evaluate(() => document.body.dataset.local)).toBe('local')
            expect(await frame.locator('#local').getAttribute('value')).toBe('original')
            expect(await frame.locator('textarea').inputValue()).toBe('initial text')
            await frame.locator('textarea').fill('updated')
            expect(await frame.evaluate(() => document.body.dataset.input)).toBe('updated')
          }
          const geometry = await frame.evaluate(() => {
            const rect = document.querySelector('main')!.getBoundingClientRect()
            return { x: rect.x, y: rect.y, right: rect.right, bottom: rect.bottom, width: innerWidth, height: innerHeight }
          })
          expect(geometry.width).toBe(1100)
          expect(geometry.height).toBe(690)
          expect(geometry.x).toBeGreaterThanOrEqual(-0.1)
          expect(geometry.y).toBeGreaterThanOrEqual(-0.1)
          expect(geometry.right).toBeLessThanOrEqual(1100.1)
          expect(geometry.bottom).toBeLessThanOrEqual(690.1)
          if (name === 'animated') {
            const before = await frame.locator('circle').getAttribute('cx')
            await page.waitForTimeout(180)
            expect(await frame.locator('circle').getAttribute('cx')).not.toBe(before)
          }
          expect(await frame.evaluate(() => {
            try { void parent.document; return false } catch { return true }
          })).toBe(true)
          expect(await frame.evaluate(() => {
            try { void localStorage.length; return false } catch { return true }
          })).toBe(true)
          expect(await frame.evaluate(async () => {
            try { await fetch('/unexpected-network'); return false } catch { return true }
          })).toBe(true)
        }
        expect(errors).toEqual([])
        expect(requests.filter(url => url.includes('pelican-preview'))).toEqual([])
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        await page.close()
      }
    } finally { await browser.close() }
  }, 20000)
})
