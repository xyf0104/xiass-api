import type { Account } from '@/types'

const normalizeUsageRefreshValue = (value: unknown): string => {
  if (value == null) return ''
  return String(value)
}

type OpenAICredentialGenerationAccount = Pick<
  Account,
  'id' | 'platform' | 'type' | 'credentials' | 'extra'
>

export const buildOpenAICredentialGenerationKey = (
  account: OpenAICredentialGenerationAccount
): string => {
  if (account.platform !== 'openai' || account.type !== 'oauth') {
    return ''
  }

  const extra = account.extra ?? {}
  const reauthorizationState = extra.xiass_openai_reauthorization_state
  const state = reauthorizationState && typeof reauthorizationState === 'object' && !Array.isArray(reauthorizationState)
    ? reauthorizationState as Record<string, unknown>
    : {}
  return [
    account.id,
    account.credentials?._token_version,
    account.credentials?.expires_at,
    state.last_succeeded_at,
    state.success_count
  ].map(normalizeUsageRefreshValue).join('|')
}

export const buildOpenAIUsageRefreshKey = (account: Pick<Account, 'id' | 'platform' | 'type' | 'updated_at' | 'last_used_at' | 'rate_limit_reset_at' | 'credentials' | 'extra'>): string => {
  if (account.platform !== 'openai' || account.type !== 'oauth') {
    return ''
  }

  const extra = account.extra ?? {}
  return [
    buildOpenAICredentialGenerationKey(account),
    account.updated_at,
    account.last_used_at,
    account.rate_limit_reset_at,
    extra.codex_usage_updated_at,
    extra.codex_5h_used_percent,
    extra.codex_5h_reset_at,
    extra.codex_5h_reset_after_seconds,
    extra.codex_5h_window_minutes,
    extra.codex_7d_used_percent,
    extra.codex_7d_reset_at,
    extra.codex_7d_reset_after_seconds,
    extra.codex_7d_window_minutes
  ].map(normalizeUsageRefreshValue).join('|')
}
