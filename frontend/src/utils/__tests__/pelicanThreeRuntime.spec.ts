import { describe, expect, it, vi } from 'vitest'
import { preparePelicanHTML } from '../pelicanThreeRuntime'

const loaded = vi.hoisted(() => vi.fn())
vi.mock('../../../node_modules/three/build/three.cjs?raw', () => {
  loaded()
  return { default: 'exports.REVISION="test";' }
})

describe('optional isolated Three runtime', () => {
  it('keeps ordinary 2D HTML byte-identical without loading the runtime', async () => {
    for (const html of [
      '<!doctype html><html><body><svg><circle r="10"/></svg></body></html>',
      '<p>THREE dimensions</p><script type="application/json">{"library":"THREE"}</script>',
      '<script src="https://unknown.test/three.min.js"></script>',
      '<script type="module">import * as scene from "https://unknown.test/three.module.js"</script>'
    ]) expect(await preparePelicanHTML(html)).toBe(html)
    expect(loaded).not.toHaveBeenCalled()
  })

  it('loads the trusted source once, prepends it, and never evaluates generated code in the app', async () => {
    const source = '<script>window.THREE;document.body.dataset.executed="yes"</script><canvas></canvas>'
    const results = await Promise.all([preparePelicanHTML(source), preparePelicanHTML(source)])
    expect(loaded).toHaveBeenCalledTimes(1)
    expect(results[0]).toBe(results[1])
    const doc = new DOMParser().parseFromString(results[0], 'text/html')
    expect(doc.head.firstElementChild?.matches('script[data-xiass-three-runtime]')).toBe(true)
    expect(doc.head.firstElementChild?.textContent).toContain('window.THREE=exports')
    expect(document.body.dataset.executed).toBeUndefined()
    expect('THREE' in window).toBe(false)
    expect(await preparePelicanHTML(results[0])).toBe(results[0])
  })

  it.each([
    'https://cdn.jsdelivr.net/npm/three@0.160.0/build/three.min.js',
    'https://unpkg.com/three@0.159.0/build/three.js',
    '//unpkg.com/three/build/three.min.js',
    'https://cdnjs.cloudflare.com/ajax/libs/three.js/r128/three.min.js',
    'https://cdnjs.cloudflare.com/ajax/libs/three.js/0.160.0/three.js'
  ])('replaces a recognized classic library reference without fetching it (%s)', async url => {
    const result = await preparePelicanHTML(`<script src="${url}"></script><main>scene</main>`)
    const doc = new DOMParser().parseFromString(result, 'text/html')
    expect(doc.querySelector('script[src]')).toBeNull()
    expect(doc.querySelectorAll('script[data-xiass-three-runtime]')).toHaveLength(1)
  })

  it.each([
    'https://unpkg.com.evil.test/three/build/three.min.js',
    'https://unpkg.com/evil/three.min.js',
    'https://cdn.jsdelivr.net/npm/three@0.180.0/build/three.module.js',
    'https://cdnjs.cloudflare.com/ajax/libs/three.js/r128/controls/OrbitControls.js',
    'https://user:secret@unpkg.com/three/build/three.min.js',
    '/three.min.js'
  ])('leaves unrelated or unsupported scripts to the restrictive preview CSP (%s)', async url => {
    const html = `<script src="${url}"></script>`
    expect(await preparePelicanHTML(html)).toBe(html)
  })

  it('does not trust a spoofed runtime marker or authorize additional libraries', async () => {
    const result = await preparePelicanHTML('<script data-xiass-three-runtime="180">window.evil=true</script><script src="https://untrusted.test/script.js" nonce="fake"></script><script>new THREE.Scene()</script>')
    const doc = new DOMParser().parseFromString(result, 'text/html')
    expect(doc.head.firstElementChild?.textContent).toContain('exports.REVISION="test"')
    expect(doc.querySelector('script[src]')?.getAttribute('src')).toBe('https://untrusted.test/script.js')
    expect('evil' in window).toBe(false)
  })
})
