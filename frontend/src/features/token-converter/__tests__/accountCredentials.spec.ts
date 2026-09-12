import { describe, expect, it } from 'vitest'
import {
  formatAccountCredential,
  parseAccountCredentials,
} from '../accountCredentials'

describe('account credential parser', () => {
  it('preserves leading and trailing password spaces', () => {
    const result = parseAccountCredentials('account---- password with spaces ----JBSWY3DPEHPK3PXP')
    expect(result.rows[0].password).toBe(' password with spaces ')
  })
  it('parses CRLF and BOM separated account credentials', () => {
    const result = parseAccountCredentials('\uFEFFuser@example.com----p-a-s-s----JBSW Y3DP\r\nsecond----secret----ABC123\r\n')

    expect(result.invalidRows).toEqual([])
    expect(result.rows).toEqual([
      {
        index: 0,
        lineNumber: 1,
        account: 'user@example.com',
        password: 'p-a-s-s',
        twoFactor: 'JBSW Y3DP',
        twoFactorCleaned: false,
      },
      {
        index: 1,
        lineNumber: 2,
        account: 'second',
        password: 'secret',
        twoFactor: 'ABC123',
        twoFactorCleaned: false,
      },
    ])
  })

  it('removes trailing status marks from the copied 2FA value', () => {
    const result = parseAccountCredentials('account----password----JBSWY3DPEHPK3PXP  ✅')

    expect(result.rows[0].twoFactor).toBe('JBSWY3DPEHPK3PXP')
    expect(result.rows[0].twoFactorCleaned).toBe(true)
    expect(formatAccountCredential(result.rows[0])).toBe('account----password----JBSWY3DPEHPK3PXP')
  })

  it('uses the first and last separator so inner separator text is preserved', () => {
    const result = parseAccountCredentials('account----part-one----part-two----2fa')

    expect(result.rows[0]).toMatchObject({
      account: 'account',
      password: 'part-one----part-two',
      twoFactor: '2fa',
    })
    expect(formatAccountCredential(result.rows[0])).toBe('account----part-one----part-two----2fa')
  })

  it('keeps invalid non-empty rows with precise reasons', () => {
    const result = parseAccountCredentials([
      'no separators',
      '----password----2fa',
      'account--------2fa',
      'account----password----',
      '',
    ].join('\n'))

    expect(result.rows).toEqual([])
    expect(result.invalidRows.map((row) => [row.lineNumber, row.code])).toEqual([
      [1, 'missing-separators'],
      [2, 'missing-account'],
      [3, 'missing-password'],
      [4, 'missing-two-factor'],
    ])
  })
})
