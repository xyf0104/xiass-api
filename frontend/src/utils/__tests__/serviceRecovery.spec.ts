import { describe, expect, it } from 'vitest'

import {
  isExpectedServiceRestartError,
  normalizeReleaseVersion,
  waitForServiceVersion
} from '../serviceRecovery'

describe('service recovery polling', () => {
  it('normalizes optional v prefixes before comparing versions', () => {
    expect(normalizeReleaseVersion(' v1.0.77 ')).toBe('1.0.77')
    expect(normalizeReleaseVersion('1.0.77')).toBe('1.0.77')
  })

  it('recognizes transport failures caused by the restart window', () => {
    expect(isExpectedServiceRestartError({ message: 'Network Error' })).toBe(true)
    expect(isExpectedServiceRestartError({ response: { status: 502 } })).toBe(true)
    expect(isExpectedServiceRestartError({ response: { status: 503 } })).toBe(true)
    expect(isExpectedServiceRestartError({ response: { status: 500 } })).toBe(false)
  })

  it('waits through an unavailable and stale service until the target version is ready', async () => {
    const responses: Array<string | Error> = [
      new Error('temporary 502'),
      '1.0.76',
      'v1.0.77'
    ]
    let elapsed = 0

    const ready = await waitForServiceVersion({
      targetVersion: '1.0.77',
      getVersion: async () => {
        const response = responses.shift()
        if (response instanceof Error) throw response
        return response
      },
      timeoutMs: 10_000,
      retryIntervalMs: 500,
      now: () => elapsed,
      sleep: async (milliseconds) => {
        elapsed += milliseconds
      }
    })

    expect(ready).toBe(true)
    expect(elapsed).toBe(1000)
  })

  it('returns false when the expected version never becomes available', async () => {
    let elapsed = 0

    const ready = await waitForServiceVersion({
      targetVersion: '1.0.77',
      getVersion: async () => '1.0.76',
      timeoutMs: 1000,
      retryIntervalMs: 500,
      now: () => elapsed,
      sleep: async (milliseconds) => {
        elapsed += milliseconds
      }
    })

    expect(ready).toBe(false)
  })
})
