import { cleanTwoFactorValue } from './totp'

export const ACCOUNT_CREDENTIAL_SEPARATOR = '----'

export type CredentialParseErrorCode =
  | 'missing-separators'
  | 'missing-account'
  | 'missing-password'
  | 'missing-two-factor'

export interface AccountCredentialRow {
  index: number
  lineNumber: number
  account: string
  password: string
  twoFactor: string
  twoFactorCleaned: boolean
}

export interface InvalidCredentialRow {
  lineNumber: number
  source: string
  code: CredentialParseErrorCode
}

export interface AccountCredentialParseResult {
  rows: AccountCredentialRow[]
  invalidRows: InvalidCredentialRow[]
}

export function parseAccountCredentials(input: string): AccountCredentialParseResult {
  const rows: AccountCredentialRow[] = []
  const invalidRows: InvalidCredentialRow[] = []

  input.replace(/^\uFEFF/, '').split(/\r?\n/).forEach((rawLine, lineIndex) => {
    const source = rawLine.trim()
    if (!source) return

    const lineNumber = lineIndex + 1
    const firstSeparator = source.indexOf(ACCOUNT_CREDENTIAL_SEPARATOR)
    const lastSeparator = source.lastIndexOf(ACCOUNT_CREDENTIAL_SEPARATOR)
    if (firstSeparator < 0 || lastSeparator === firstSeparator) {
      invalidRows.push({ lineNumber, source, code: 'missing-separators' })
      return
    }

    const account = source.slice(0, firstSeparator).trim()
    const password = source.slice(firstSeparator + ACCOUNT_CREDENTIAL_SEPARATOR.length, lastSeparator)
    const rawTwoFactor = source.slice(lastSeparator + ACCOUNT_CREDENTIAL_SEPARATOR.length).trim()
    const twoFactor = cleanTwoFactorValue(rawTwoFactor)

    if (!account) {
      invalidRows.push({ lineNumber, source, code: 'missing-account' })
      return
    }
    if (!password) {
      invalidRows.push({ lineNumber, source, code: 'missing-password' })
      return
    }
    if (!twoFactor) {
      invalidRows.push({ lineNumber, source, code: 'missing-two-factor' })
      return
    }

    rows.push({
      index: rows.length,
      lineNumber,
      account,
      password,
      twoFactor,
      twoFactorCleaned: twoFactor !== rawTwoFactor,
    })
  })

  return { rows, invalidRows }
}

export function formatAccountCredential(row: AccountCredentialRow): string {
  return [row.account, row.password, row.twoFactor].join(ACCOUNT_CREDENTIAL_SEPARATOR)
}
