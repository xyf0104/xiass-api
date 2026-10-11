const OPENAI_QUOTA_REAUTH_REASONS = new Set([
  'OPENAI_QUOTA_UPSTREAM_ERROR',
  'OPENAI_QUOTA_RESET_REJECTED'
])

const asRecord = (value: unknown): Record<string, unknown> | null => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

const isTrue = (value: unknown): boolean => value === true || value === 'true'

/**
 * Detect only explicit OpenAI quota-upstream authentication failures.
 * A generic HTTP 401 may belong to the administrator session and must not mark
 * the managed account as revoked.
 */
export const isOpenAIQuotaReauthError = (value: unknown): boolean => {
  const error = asRecord(value)
  if (!error || Number(error.status) !== 401) return false

  const metadata = asRecord(error.metadata)
  if (isTrue(error.needs_reauth) || isTrue(metadata?.needs_reauth)) return true
  if (error.error_code === 'unauthenticated' || metadata?.error_code === 'unauthenticated') return true

  const reason = typeof error.reason === 'string'
    ? error.reason
    : typeof error.code === 'string'
      ? error.code
      : ''
  return OPENAI_QUOTA_REAUTH_REASONS.has(reason)
}

export const isOpenAIQuotaAdminRequest = (url: string): boolean =>
  /\/admin\/openai\/accounts\/[^/]+\/(?:quota(?:\/refresh)?|reset-quota(?:\/reconcile)?)(?:[/?#]|$)/.test(url)
