import { describe, expect, it } from 'vitest'
import { buildCodexHelperCallback, parseCodexHelperConnection } from '@/utils/codexHelper'

const state = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1'

describe('codexHelper', () => {
  it('accepts only a loopback callback', () => {
    const connection = parseCodexHelperConnection('http://127.0.0.1:43123/callback', state)
    expect(connection.callback.hostname).toBe('127.0.0.1')
    expect(() => parseCodexHelperConnection('https://evil.example/callback', state)).toThrow()
    expect(() => parseCodexHelperConnection('http://127.0.0.1:80/callback', state)).toThrow()
    expect(() => parseCodexHelperConnection('http://127.0.0.1:43123/other', state)).toThrow()
  })

  it('keeps the API key in the URL fragment only', () => {
    const connection = parseCodexHelperConnection('http://127.0.0.1:43123/callback', state)
    const target = buildCodexHelperCallback(connection, 'https://api.xiass.com/v1/', {
      key: 'sk-secret-value',
      name: '我的 Codex 密钥'
    })
    const parsed = new URL(target)
    expect(parsed.search).toBe('')
    expect(parsed.hash).not.toBe('')
    expect(target.split('#')[0]).not.toContain('sk-secret-value')

    const params = new URLSearchParams(parsed.hash.slice(1))
    const encoded = params.get('payload')!
    const normalized = encoded.replace(/-/g, '+').replace(/_/g, '/')
    const padded = normalized + '='.repeat((4 - normalized.length % 4) % 4)
    const decoded = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(padded), char => char.charCodeAt(0))))
    expect(decoded).toEqual({
      base_url: 'https://api.xiass.com',
      api_key: 'sk-secret-value',
      key_name: '我的 Codex 密钥'
    })
  })
})
