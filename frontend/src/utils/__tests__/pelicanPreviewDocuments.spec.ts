import { afterEach, describe, expect, it } from 'vitest'
import { createRequire } from 'node:module'
import { buildPelicanPreviewDocument, buildPelicanStandaloneDocument } from '../pelicanPreviewDocument'
import { preparePelicanHTML } from '../pelicanThreeRuntime'

function parse(html: string) { return new DOMParser().parseFromString(html, 'text/html') }

afterEach(() => document.querySelectorAll('script[data-document-test]').forEach(script => script.remove()))

describe('preview document modes', () => {
  it('keeps thumbnail defaults and allows full viewport documents without the fitting script', () => {
    const input = '<main>scene</main><script>window.scene=true</script>'
    expect(buildPelicanPreviewDocument(input)).toBe(buildPelicanPreviewDocument(input, { fit: true }))
    expect(buildPelicanPreviewDocument(input)).toContain('ResizeObserver')
    const enlarged = buildPelicanPreviewDocument(input, { fit: false })
    expect(enlarged).not.toContain('ResizeObserver')
    expect(parse(enlarged).querySelectorAll('script')).toHaveLength(1)
    expect(enlarged).toContain("connect-src 'none'")
  })

  it('honors nonce overrides including explicitly empty standalone nonces', () => {
    const bootstrap = document.createElement('script')
    bootstrap.nonce = 'panel'
    bootstrap.dataset.documentTest = 'true'
    document.head.appendChild(bootstrap)
    const html = '<script>window.scene=true</script><script src="https://evil.test/x.js" nonce="spoof"></script><svg><script href="https://evil.test/x.js" nonce="spoof"></script></svg>'
    expect(parse(buildPelicanPreviewDocument(html)).querySelector('script')?.getAttribute('nonce')).toBe('panel')
    const overridden = parse(buildPelicanPreviewDocument(html, { nonce: 'other' }))
    expect(overridden.querySelector('script')?.getAttribute('nonce')).toBe('other')
    expect(overridden.querySelector('script[src]')?.hasAttribute('nonce')).toBe(false)
    expect(overridden.querySelector('script[href]')?.hasAttribute('nonce')).toBe(false)
    expect(parse(buildPelicanPreviewDocument(html, { nonce: '' })).querySelector('[nonce]')).toBeNull()
  })

  it('escapes wrapper titles and attributes and keeps all generated code in one opaque frame', () => {
    const title = '</title><script>window.injected=true</script>"&'
    const html = '<main>"& scene</main><script>window.generated=true</script>'
    const outer = parse(buildPelicanStandaloneDocument(html, title))
    expect(outer.title).toBe(title)
    expect(outer.querySelectorAll('iframe')).toHaveLength(1)
    expect(outer.querySelectorAll('script')).toHaveLength(0)
    const frame = outer.querySelector('iframe')!
    expect(frame.getAttribute('sandbox')).toBe('allow-scripts')
    expect(frame.getAttribute('referrerpolicy')).toBe('no-referrer')
    expect(frame.getAttribute('title')).toBe(title)
    const inner = parse(frame.getAttribute('srcdoc')!)
    expect(inner.querySelector('main')?.textContent).toBe('"& scene')
    expect(inner.querySelector('script')?.textContent).toBe('window.generated=true')
    expect(inner.querySelector('[nonce]')).toBeNull()
    expect(frame.getAttribute('srcdoc')).not.toContain('ResizeObserver')
    expect(outer.querySelector('meta[http-equiv]')?.getAttribute('content')).toContain('frame-src about:')
  })

  it('retains the app nonce in inner scripts for Blob tabs without outer executable code', () => {
    const bootstrap = document.createElement('script')
    bootstrap.nonce = 'blob-parent'
    bootstrap.dataset.documentTest = 'true'
    document.head.appendChild(bootstrap)
    const outer = parse(buildPelicanStandaloneDocument('<script>window.scene=true</script>', 'Scene'))
    expect(outer.querySelector('script')).toBeNull()
    const inner = parse(outer.querySelector('iframe')!.getAttribute('srcdoc')!)
    expect(inner.querySelector('script')?.getAttribute('nonce')).toBe('blob-parent')
  })
})

describe.skipIf(process.env.PELICAN_BROWSER_TEST !== '1')('offline Three in real Chrome', () => {
  it('renders animated pixels on desktop/mobile with sandbox isolation and no remote requests', async () => {
    const require = createRequire(import.meta.url)
    const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
    const browser = await chromium.launch({
      headless: true,
      args: ['--use-angle=swiftshader', '--enable-unsafe-swiftshader'],
      ...(process.env.PELICAN_CHROME ? { executablePath: process.env.PELICAN_CHROME } : {})
    })
    const bootstrap = document.createElement('script')
    bootstrap.nonce = 'test'
    bootstrap.dataset.documentTest = 'true'
    document.head.appendChild(bootstrap)
    const html = await preparePelicanHTML(`<!doctype html><html><body style="margin:0"><script src="https://cdn.jsdelivr.net/npm/three@0.160.0/build/three.min.js"></script><script>
      const scene = new THREE.Scene(); scene.background = new THREE.Color(0x123456);
      const camera = new THREE.PerspectiveCamera(50, innerWidth/innerHeight, 0.1, 100); camera.position.z=4;
      const renderer = new THREE.WebGLRenderer({preserveDrawingBuffer:true}); renderer.setSize(innerWidth,innerHeight); document.body.appendChild(renderer.domElement);
      const cube = new THREE.Mesh(new THREE.BoxGeometry(), new THREE.MeshNormalMaterial()); scene.add(cube);
      function animate(t){cube.rotation.x=t/700;cube.rotation.y=t/900;renderer.render(scene,camera);requestAnimationFrame(animate)} requestAnimationFrame(animate);
      document.body.dataset.revision=THREE.REVISION;
      try { parent.document.body.dataset.escaped='yes' } catch { document.body.dataset.isolated='yes' }
      try { localStorage.setItem('escaped','yes') } catch { document.body.dataset.storage='blocked' }
      fetch('https://untrusted.test/data').catch(()=>{});
      </script><script src="https://untrusted.test/evil.js"></script></body></html>`)
    try {
      for (const width of [1440, 390]) {
        for (const mode of ['thumbnail', 'standalone', 'blob']) {
          const standalone = mode !== 'thumbnail'
          const page = await browser.newPage({ viewport: { width, height: 900 } })
          const requests: string[] = []
          const errors: string[] = []
          page.on('pageerror', (error: Error) => errors.push(error.message))
          const body = standalone ? buildPelicanStandaloneDocument(html, '3D scene')
            : `<iframe sandbox="allow-scripts" style="border:0;width:1100px;height:690px" srcdoc="${buildPelicanPreviewDocument(html, { nonce: 'test' }).replace(/&/g, '&amp;').replace(/"/g, '&quot;')}"></iframe>`
          await page.context().route('**/*', (route: { request(): { url(): string }; fulfill(options: object): Promise<void>; abort(): Promise<void> }) => {
            requests.push(route.request().url())
            return route.request().url() === 'https://preview.test/' ? route.fulfill({
              status: 200, contentType: 'text/html',
              headers: mode === 'standalone' ? {} : { 'Content-Security-Policy': "default-src 'self'; script-src 'self' 'nonce-test'; style-src 'unsafe-inline'; frame-src 'self'; connect-src 'self'" },
              body: mode === 'blob' ? '<main>App page</main>' : body
            }) : route.abort()
          })
          await page.goto('https://preview.test/')
          let previewPage = page
          if (mode === 'blob') {
            const opened = page.context().waitForEvent('page')
            await page.evaluate((source: string) => {
              const url = URL.createObjectURL(new Blob([source], { type: 'text/html' }))
              window.open(url, '_blank', 'noopener,noreferrer')
            }, body)
            previewPage = await opened
            previewPage.on('pageerror', (error: Error) => errors.push(error.message))
            await previewPage.waitForLoadState('load')
            expect(await previewPage.evaluate(() => window.opener)).toBeNull()
          }
          const frame = previewPage.frames()[1]
          await frame.waitForFunction(() => document.body.dataset.revision === '180')
          await frame.waitForTimeout(150)
          expect(await frame.evaluate(() => ({ ...document.body.dataset }))).toMatchObject({ isolated: 'yes', storage: 'blocked' })
          const pixels = await frame.evaluate(() => {
            const canvas = document.querySelector('canvas')!
            const gl = canvas.getContext('webgl2')!
            const pixel = new Uint8Array(4)
            gl.readPixels(canvas.width/2,canvas.height/2,1,1,gl.RGBA,gl.UNSIGNED_BYTE,pixel)
            return { color: [...pixel], image: canvas.toDataURL(), width: canvas.width, height: canvas.height }
          })
          expect(pixels.color[3]).toBe(255)
          expect(pixels.color.slice(0, 3)).not.toEqual([18, 52, 86])
          expect(pixels.width).toBe(standalone ? width : 1100)
          expect(pixels.height).toBe(standalone ? 900 : 690)
          await frame.waitForTimeout(160)
          expect(await frame.evaluate(() => document.querySelector('canvas')!.toDataURL())).not.toBe(pixels.image)
          expect(await previewPage.evaluate(() => ({ three: 'THREE' in window, escaped: document.body.dataset.escaped }))).toEqual({ three: false, escaped: undefined })
          expect(requests.filter(url => !url.startsWith('blob:'))).toEqual(['https://preview.test/'])
          expect(errors).toEqual([])
          expect((await previewPage.screenshot()).byteLength).toBeGreaterThan(1000)
          if (previewPage !== page) await previewPage.close()
          await page.close()
        }
      }
    } finally { await browser.close() }
  }, 60_000)
})
