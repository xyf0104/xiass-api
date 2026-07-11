import { describe, expect, it } from 'vitest'
import { sanitizeUrl } from '@/utils/url'

describe('sanitizeUrl', () => {
  it('accepts safe absolute, site-relative, and image data URLs', () => {
    expect(sanitizeUrl('https://example.com/logo.png')).toBe('https://example.com/logo.png')
    expect(sanitizeUrl('/branding/logo.png', { allowRelative: true })).toBe('/branding/logo.png')
    expect(sanitizeUrl('DATA:image/png;base64,AAAA', { allowDataUrl: true })).toBe(
      'DATA:image/png;base64,AAAA'
    )
  })

  it('rejects unsafe or malformed URLs', () => {
    const options = { allowRelative: true, allowDataUrl: true }
    expect(sanitizeUrl('javascript:alert(1)', options)).toBe('')
    expect(sanitizeUrl('//evil.example/logo.png', options)).toBe('')
    expect(sanitizeUrl('/\\evil.example/logo.png', options)).toBe('')
    expect(sanitizeUrl('data:image/png;base64', options)).toBe('')
    expect(sanitizeUrl('https://example.com/logo\n.png', options)).toBe('')
  })
})
