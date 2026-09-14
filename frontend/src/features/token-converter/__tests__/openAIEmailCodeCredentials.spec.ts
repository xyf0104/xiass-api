import { describe, expect, it } from 'vitest'
import { parseOpenAIEmailCodeCredentials } from '../openAIEmailCodeCredentials'

describe('parseOpenAIEmailCodeCredentials', () => {
  it('extracts only the email and 64-character token from a supplier row', () => {
    const token = 'a'.repeat(64)
    const result = parseOpenAIEmailCodeCredentials(`gpt-0    https://ic.g-c.cc    sample.alpha-3c\\@example.test    ${token}    Plus    美国洛杉矶-3    20260912    20261012    指纹浏览器\\`)

    expect(result.invalidRows).toEqual([])
    expect(result.rows).toEqual([expect.objectContaining({
      account: 'sample.alpha-3c@example.test',
      emailCodeToken: token,
    })])
  })

  it('accepts tab-separated rows and reports missing or ambiguous secrets', () => {
    const token = 'b'.repeat(64)
    const result = parseOpenAIEmailCodeCredentials([
      `gpt-0\thttps://ic.g-c.cc\tspeaker@example.com\t${token}\tPlus`,
      'gpt-0 https://ic.g-c.cc missing@example.com Plus',
      `first@example.com second@example.com ${token}`,
    ].join('\n'))

    expect(result.rows).toHaveLength(1)
    expect(result.rows[0]).toMatchObject({ account: 'speaker@example.com', emailCodeToken: token })
    expect(result.invalidRows).toEqual([
      expect.objectContaining({ lineNumber: 2, code: 'missing-email-or-token' }),
      expect.objectContaining({ lineNumber: 3, code: 'ambiguous-email-or-token' }),
    ])
  })

  it('does not accept the password and 2FA separator format as an email-token row', () => {
    const result = parseOpenAIEmailCodeCredentials('owner@example.com----password----JBSWY3DPEHPK3PXP')

    expect(result.rows).toEqual([])
    expect(result.invalidRows).toEqual([expect.objectContaining({ lineNumber: 1, code: 'missing-email-or-token' })])
  })
})
