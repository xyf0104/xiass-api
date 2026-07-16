import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  applyTheme,
  getInitialTheme,
  notifyThemeBackgroundReady,
  releaseThemeBootstrapGuard
} from '../theme'

describe('theme bootstrap guard', () => {
  afterEach(() => {
    document.documentElement.className = ''
    delete document.documentElement.dataset.theme
    delete document.documentElement.dataset.themeBackgroundReady
    delete document.documentElement.dataset.themeBooting
    document.documentElement.style.colorScheme = ''
    localStorage.clear()
    vi.unstubAllGlobals()
  })

  it('uses the inline bootstrap theme before consulting storage', () => {
    document.documentElement.dataset.theme = 'dark'
    document.documentElement.dataset.themeBooting = 'true'
    localStorage.setItem('theme', 'light')

    expect(getInitialTheme()).toBe('dark')
  })

  it('keeps the HTML data theme aligned with an explicit theme change', () => {
    applyTheme('light', { persist: false, animate: false })

    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.style.colorScheme).toBe('light')
  })

  it('reveals a route without a themed background after two animation frames', () => {
    const frames: FrameRequestCallback[] = []
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      frames.push(callback)
      return frames.length
    })
    document.documentElement.classList.add('dark')
    document.documentElement.dataset.theme = 'dark'
    document.documentElement.dataset.themeBooting = 'true'
    releaseThemeBootstrapGuard()
    expect(document.documentElement.dataset.themeBooting).toBe('true')

    frames.shift()?.(0)
    expect(document.documentElement.dataset.themeBooting).toBe('true')

    frames.shift()?.(0)
    expect(document.documentElement.dataset.themeBooting).toBeUndefined()
  })

  it('keeps the application hidden until the active theme background is ready', () => {
    const frames: FrameRequestCallback[] = []
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      frames.push(callback)
      return frames.length
    })
    document.documentElement.classList.add('dark')
    document.documentElement.dataset.theme = 'dark'
    document.documentElement.dataset.themeBooting = 'true'
    const background = document.createElement('div')
    background.className = 'theme-video-background'
    document.body.appendChild(background)

    releaseThemeBootstrapGuard()
    notifyThemeBackgroundReady('light')

    expect(frames).toHaveLength(0)
    expect(document.documentElement.dataset.themeBooting).toBe('true')

    notifyThemeBackgroundReady('dark')

    expect(frames).toHaveLength(1)
    frames.shift()?.(0)
    frames.shift()?.(0)
    expect(document.documentElement.dataset.themeBooting).toBeUndefined()
    background.remove()
  })
})
