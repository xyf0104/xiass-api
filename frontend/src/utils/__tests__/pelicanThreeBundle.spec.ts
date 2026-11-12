// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

describe.skipIf(process.env.PELICAN_BROWSER_TEST !== '1')('production Three packaging', () => {
  it('keeps the runtime chunk unrequested for 2D and loads it once for 3D', async () => {
    const { build, loadConfigFromFile } = await import('vite')
    const root = fileURLToPath(new URL('../../..', import.meta.url))
    const loaded = await loadConfigFromFile({ command: 'build', mode: 'test' }, `${root}/vite.config.ts`)
    const configuredOutput = loaded?.config.build?.rollupOptions?.output
    if (!configuredOutput || Array.isArray(configuredOutput)) throw new Error('Missing application chunk configuration')
    const runtimePlugin = loaded?.config.plugins?.find(plugin => (plugin as { name?: string })?.name === 'pelican-three-runtime')
    expect(runtimePlugin).toBeDefined()
    const bundle = await build({
      root, configFile: false, logLevel: 'silent',
      plugins: [runtimePlugin, {
        name: 'preview-document-fixture',
        resolveId(id) { if (id === 'preview-fixture') return '\0preview-fixture' },
        load(id) {
          if (id !== '\0preview-fixture') return
          return `import {preparePelicanHTML} from ${JSON.stringify(`${root}/src/utils/pelicanThreeRuntime.ts`)};
            import {buildPelicanPreviewDocument} from ${JSON.stringify(`${root}/src/utils/pelicanPreviewDocument.ts`)};
            window.renderTest=async html=>{document.querySelector('iframe').srcdoc=buildPelicanPreviewDocument(await preparePelicanHTML(html));};`
        }
      }],
      build: { write: false, rollupOptions: { input: 'preview-fixture', output: { manualChunks: configuredOutput.manualChunks } } }
    })
    if (Array.isArray(bundle) || !('output' in bundle)) throw new Error('Expected one production output')
    const chunks = bundle.output.filter(item => item.type === 'chunk')
    const entry = chunks.find(chunk => chunk.isEntry)!
    const runtime = chunks.find(chunk => chunk.name === 'pelican-three-runtime')!
    expect(runtime).toBeDefined()
    expect(entry.imports).not.toContain(runtime.fileName)
    expect(entry.dynamicImports).toContain(runtime.fileName)
    expect(runtime.code.length).toBeLessThan(1_000_000)
    const require = createRequire(import.meta.url)
    const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')
    const browser = await chromium.launch({ headless: true, ...(process.env.PELICAN_CHROME ? { executablePath: process.env.PELICAN_CHROME } : {}) })
    try {
      const page = await browser.newPage()
      page.setDefaultTimeout(5000)
      const requests: string[] = []
      const errors: string[] = []
      page.on('request', (request: { url(): string }) => requests.push(request.url()))
      page.on('pageerror', (error: Error) => errors.push(error.message))
      await page.route('**/*', (route: { request(): { url(): string }; fulfill(options: object): Promise<void>; abort(): Promise<void> }) => {
        const path = new URL(route.request().url()).pathname.slice(1)
        if (!path) return route.fulfill({
          contentType: 'text/html',
          headers: { 'Content-Security-Policy': "default-src 'self'; script-src 'self' 'nonce-test'; style-src 'unsafe-inline'; frame-src 'self'" },
          body: `<iframe sandbox="allow-scripts"></iframe><script type="module" nonce="test" src="/${entry.fileName}"></script>`
        })
        const chunk = chunks.find(item => item.fileName === path)
        return chunk ? route.fulfill({ contentType: 'text/javascript', body: chunk.code }) : route.abort()
      })
      await page.goto('https://compiled-preview.test/')
      await page.evaluate(async () => {
        await (window as unknown as { renderTest(html: string): Promise<void> }).renderTest('<main>Instant 2D</main>')
      })
      await page.frames()[1].waitForSelector('main')
      expect(requests.some(url => url.includes(runtime.fileName))).toBe(false)
      for (let attempt = 0; attempt < 2; attempt++) {
        await page.evaluate(async () => {
          await (window as unknown as { renderTest(html: string): Promise<void> }).renderTest('<body><script>document.body.dataset.revision=THREE.REVISION;new THREE.Scene()</script></body>')
        })
        await page.frames()[1].waitForFunction(() => document.body.dataset.revision === '180')
      }
      expect(requests.filter(url => url.includes(runtime.fileName))).toHaveLength(1)
      expect(await page.evaluate(() => 'THREE' in window)).toBe(false)
      expect(errors).toEqual([])
    } finally { await browser.close() }
  }, 30_000)
})
