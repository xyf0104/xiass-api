import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/components/account/CreateAccountModal.vue'),
  'utf8'
)

describe('CreateAccountModal Grok account types', () => {
  it('offers API-key setup alongside OAuth with the official xAI default', () => {
    expect(source).toContain('data-testid="grok-account-type-api-key"')
    expect(source).toContain("@click=\"accountCategory = 'apikey'\"")
    expect(source).toContain("newPlatform === 'grok'")
    expect(source).toContain("? 'https://api.x.ai/v1'")
    expect(source).toContain("form.platform === 'grok'")
    expect(source).toContain("? 'xai-...'")
    const grokDefaults = source.match(
      /form\.platform === 'grok'\s*\? 'https:\/\/api\.x\.ai\/v1'/g
    )
    expect(grokDefaults?.length ?? 0).toBeGreaterThanOrEqual(2)
  })

  it('wires endpoint presets to API key and OAuth inputs without adding SSO UI', () => {
    expect(source).toContain('@select="apiKeyBaseUrl = $event"')
    expect(source).toContain('data-testid="grok-custom-base-url-toggle"')
    expect(source).toContain('data-testid="grok-custom-base-url-input"')
    expect(source).toContain('@select="grokOAuthBaseUrl = $event"')
    expect(source).not.toContain('grok-sso')
  })

  it('applies validated Grok OAuth upstream config to auth-code and refresh-token creation', () => {
    const validateCalls = source.match(/if \(!validateGrokOAuthUpstreamConfig\(\)\) return/g)
    const applyCalls = source.match(/applyGrokOAuthUpstreamConfig\(credentials\)/g)

    expect(validateCalls).toHaveLength(2)
    expect(applyCalls).toHaveLength(2)
  })

  it('validates the Grok API-key base URL before single or batch creation', () => {
    expect(source).toContain("if (form.platform === 'grok')")
    expect(source).toContain(
      "apiKeyBaseUrl.value.trim() || 'https://api.x.ai/v1'"
    )
    expect(source).toContain(
      'appStore.showError(t(`admin.accounts.grokCustomBaseUrl.${validationError}`))'
    )
  })
})
