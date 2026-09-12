import assert from 'node:assert/strict'
import { test } from 'node:test'
import { generateTOTP, isAuthenticatorChallenge } from './totp.mjs'

test('authenticator code matches RFC6238 SHA1 vectors truncated to six digits', () => {
  const secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'
  for (const [seconds, code] of [[59, '287082'], [1111111109, '081804'], [1111111111, '050471'], [1234567890, '005924'], [2000000000, '279037'], [20000000000, '353130']]) {
    assert.equal(generateTOTP(secret, seconds * 1000), code)
  }
})
test('ordinary email/SMS code screens are not authenticator challenges', () => {
  assert.equal(isAuthenticatorChallenge('Enter the verification code from your email', [{}]), false)
  assert.equal(isAuthenticatorChallenge('Text message verification code', [{}]), false)
  assert.equal(isAuthenticatorChallenge('Enter the code from your authenticator app', [{}]), true)
  assert.equal(isAuthenticatorChallenge('输入身份验证器中的验证码', [{}]), true)
  assert.equal(isAuthenticatorChallenge('authenticator app', []), false)
})
