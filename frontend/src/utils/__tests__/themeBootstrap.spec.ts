import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const indexHTML = readFileSync(resolve(process.cwd(), 'index.html'), 'utf8')

describe('pre-paint theme bootstrap', () => {
  it('applies the saved theme before the application module loads', () => {
    const themeBootstrap = indexHTML.indexOf("window.localStorage.getItem('theme')")
    const applicationModule = indexHTML.indexOf('src="/src/main.ts"')

    expect(indexHTML).toContain('<html lang="zh-CN" class="dark">')
    expect(indexHTML).toContain('nonce="__CSP_NONCE_VALUE__"')
    expect(indexHTML).toContain("root.classList.toggle('dark', theme === 'dark')")
    expect(themeBootstrap).toBeGreaterThan(-1)
    expect(applicationModule).toBeGreaterThan(themeBootstrap)
  })

  it('provides matching dark and light canvas colors before CSS loads', () => {
    expect(indexHTML).toContain('background-color: #020617')
    expect(indexHTML).toContain('html:not(.dark)')
    expect(indexHTML).toContain('background-color: #f9fafb')
  })
})
