import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const videoSource = readFileSync(resolve(dir, '../DarkVideoBackground.vue'), 'utf8')
const styleSource = readFileSync(resolve(dir, '../../../style.css'), 'utf8')

describe('console floating surfaces', () => {
  it('uses the profile-menu opacity for generic dropdowns and popovers', () => {
    expect(styleSource).toContain('--xiass-console-light-floating-surface: rgb(255 255 255 / 0.92)')
    expect(styleSource).toContain('--xiass-console-floating-surface: rgb(8 21 38 / 0.88)')
    expect(styleSource.match(/\.date-picker-dropdown,/g)).toHaveLength(2)
    expect(styleSource).toContain("[role='listbox']")
    expect(styleSource).toContain("[class*='absolute'][class*='z-'][class*='rounded'][class*='shadow']")
    expect(styleSource).toContain("[class*='fixed'][class*='z-'][class*='rounded'][class*='shadow']")
  })

  it('keeps modal surfaces opaque enough to hide content underneath', () => {
    expect(styleSource).toContain('--xiass-console-light-surface-raised: rgb(255 255 255 / 0.92)')
    expect(styleSource).toContain('--xiass-console-surface-raised: rgb(8 21 38 / 0.88)')
    expect(styleSource).toContain("[class*='fixed'][class*='inset-0'][class*='z-'] > div[class*='rounded'][class*='shadow']")
    expect(styleSource).toContain("div[class*='flex'] > div[class*='relative'][class*='rounded'][class*='shadow']")
  })
})

describe('theme video loop transition', () => {
  it('fades through a theme poster instead of using a hard native loop', () => {
    expect(videoSource.match(/<video/g)).toHaveLength(2)
    expect(videoSource.match(/theme-video-background__poster/g)?.length).toBeGreaterThanOrEqual(2)
    expect(videoSource).not.toMatch(/\n\s+loop\n/)
    expect(videoSource).toContain('setLoopFading(theme, true)')
    expect(videoSource).toContain('video.currentTime = 0')
    expect(videoSource).toContain('await waitForFirstFrame(video)')
  })
})
