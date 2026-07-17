import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const indexHTML = readFileSync(resolve(process.cwd(), 'index.html'), 'utf8')

describe('pre-paint theme bootstrap', () => {
  it('applies the saved theme before the application module loads', () => {
    const themeBootstrap = indexHTML.indexOf("window.localStorage.getItem('theme')")
    const applicationModule = indexHTML.indexOf('src="/src/main.ts"')

    expect(indexHTML).toContain('<html lang="zh-CN" class="dark" data-theme="dark">')
    expect(indexHTML).toContain('nonce="__CSP_NONCE_VALUE__"')
    expect(indexHTML).toContain("root.classList.toggle('dark', theme === 'dark')")
    expect(indexHTML).toContain('root.dataset.theme = theme')
    expect(indexHTML).toContain('root.dataset.themeBackground =')
    expect(indexHTML).toContain("root.dataset.themeBooting = 'true'")
    expect(themeBootstrap).toBeGreaterThan(-1)
    expect(applicationModule).toBeGreaterThan(themeBootstrap)
  })

  it('keeps the application hidden until the bootstrapped theme has painted', () => {
    expect(indexHTML).toContain('background-color: #061720')
    expect(indexHTML).toContain("html[data-theme='light']")
    expect(indexHTML).toContain('background-color: #cdd8df')
    expect(indexHTML).toContain('html[data-theme-booting] #app')
    expect(indexHTML).toContain('transition: none !important')
  })

  it('paints the matching video poster behind the hidden application', () => {
    const bootBackground = indexHTML.indexOf('<div id="theme-boot-background"')
    const applicationRoot = indexHTML.indexOf('<div id="app"')

    expect(indexHTML).toContain("url('/media/xiass-dark-bokeh-poster.png')")
    expect(indexHTML).toContain("url('/media/xiass-light-water-poster.png')")
    expect(indexHTML).toContain("html[data-theme-background='blurred']")
    expect(indexHTML).toContain("html:not([data-theme-booting]) #theme-boot-background")
    expect(bootBackground).toBeGreaterThan(-1)
    expect(applicationRoot).toBeGreaterThan(bootBackground)
  })
})
