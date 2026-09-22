import { describe, expect, it } from 'vitest'
import { formatOutputTokenSpeed, getOutputTokenSpeed } from '../outputTokenSpeed'

describe('output token speed', () => {
  it('uses the time after the first token for streamed output', () => {
    expect(getOutputTokenSpeed({ output_tokens: 900, first_token_ms: 500, duration_ms: 3500, stream: true })).toEqual({
      value: 300,
      usesNonStreamFallback: false,
    })
    expect(formatOutputTokenSpeed({ output_tokens: 900, first_token_ms: 500, duration_ms: 3500, stream: true })).toBe('300.00 t/s')
  })

  it('labels the explicit non-stream fallback', () => {
    expect(getOutputTokenSpeed({ output_tokens: 120, duration_ms: 2000, first_token_ms: null, stream: false })).toEqual({
      value: 60,
      usesNonStreamFallback: true,
    })
  })

  it.each([
    { output_tokens: 100, duration_ms: null, first_token_ms: 10, stream: true },
    { output_tokens: 100, duration_ms: 1000, first_token_ms: 1000, stream: true },
    { output_tokens: 100, duration_ms: 1000, first_token_ms: 1200, stream: false },
    { output_tokens: 100, duration_ms: 1000, first_token_ms: -1, stream: false },
    { output_tokens: 100, duration_ms: 1000, first_token_ms: Number.NaN, stream: false },
    { output_tokens: 100, duration_ms: 1000, first_token_ms: null, stream: true },
    { output_tokens: Number.NaN, duration_ms: 1000, first_token_ms: 10, stream: true },
  ])('returns no speed for missing or invalid timing data', (row) => {
    expect(getOutputTokenSpeed(row)).toBeNull()
    expect(formatOutputTokenSpeed(row)).toBe('-')
  })
})
