export interface OutputTokenSpeedRow {
  output_tokens?: number | null
  duration_ms?: number | null
  first_token_ms?: number | null
  stream?: boolean | null
}

export interface OutputTokenSpeed {
  value: number
  usesNonStreamFallback: boolean
}

const finiteNumber = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value)

/**
 * Calculates generated tokens per second using only an observable output interval.
 * Non-stream rows may use total duration, but callers must show that fallback label.
 */
export const getOutputTokenSpeed = (row: OutputTokenSpeedRow): OutputTokenSpeed | null => {
  const outputTokens = row.output_tokens
  const durationMs = row.duration_ms
  if (!finiteNumber(outputTokens) || outputTokens < 0 || !finiteNumber(durationMs) || durationMs <= 0) {
    return null
  }

  const firstTokenMs = row.first_token_ms
  const firstTokenMissing = firstTokenMs == null
  if (!firstTokenMissing && (!finiteNumber(firstTokenMs) || firstTokenMs < 0 || durationMs <= firstTokenMs)) {
    return null
  }

  if (!firstTokenMissing) {
    const outputSeconds = (durationMs - firstTokenMs) / 1000
    return { value: outputTokens / outputSeconds, usesNonStreamFallback: false }
  }

  if (firstTokenMissing && row.stream === false) {
    return { value: outputTokens / (durationMs / 1000), usesNonStreamFallback: true }
  }

  return null
}

export const formatOutputTokenSpeed = (row: OutputTokenSpeedRow): string => {
  const speed = getOutputTokenSpeed(row)
  return speed && Number.isFinite(speed.value) ? `${speed.value.toFixed(2)} t/s` : '-'
}
