import { describe, expect, it } from 'vitest'
import { formatProxyDisplayName } from '../proxyDisplay'

describe('formatProxyDisplayName', () => {
  it.each([
    ['[Subscription] Source A / Tokyo 01', 'Tokyo 01'],
    ['  [Subscription] Source A / Tokyo 01  ', 'Tokyo 01'],
    ['[Subscription] Source A / Tokyo / Backup', 'Tokyo / Backup'],
    ['[Subscription] Source A / [Premium] Tokyo', '[Premium] Tokyo'],
    ['[Subscription] Source A / <b>Tokyo</b>', '<b>Tokyo</b>'],
  ])('shortens a subscription label without changing node text: %s', (name, expected) => {
    expect(formatProxyDisplayName(name)).toBe(expected)
  })

  it.each([
    'Manual proxy / Tokyo',
    '[Other] Source A / Tokyo',
    'Manual [Subscription] Source A / Tokyo',
    '[Subscription] Tokyo',
    '[Subscription] Source A /',
    '[Subscription] / Tokyo',
  ])('preserves names without a complete subscription prefix: %s', name => {
    expect(formatProxyDisplayName(name)).toBe(name)
  })

  it.each([undefined, null, '', '   '])('returns an empty label for %s', name => {
    expect(formatProxyDisplayName(name)).toBe('')
  })
})
