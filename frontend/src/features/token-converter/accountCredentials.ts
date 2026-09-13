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
    const separatorRuns = [...source.matchAll(/-{4,}/g)]
    if (separatorRuns.length < 2) {
      invalidRows.push({ lineNumber, source, code: 'missing-separators' })
      return
    }

    const firstSeparator = separatorRuns[0]
    const lastSeparator = separatorRuns[separatorRuns.length - 1]
    const firstSeparatorStart = firstSeparator.index ?? -1
    const firstSeparatorEnd = firstSeparatorStart + firstSeparator[0].length
    const lastSeparatorStart = lastSeparator.index ?? -1
    const lastSeparatorEnd = lastSeparatorStart + lastSeparator[0].length

    const account = source.slice(0, firstSeparatorStart).trim()
    const password = source.slice(firstSeparatorEnd, lastSeparatorStart)
    const rawTwoFactor = source.slice(lastSeparatorEnd).trim()
    const twoFactor = cleanTwoFactorValue(rawTwoFactor)

    if (!account) {
      invalidRows.push({ lineNumber, source, code: 'missing-account' })
      return
    }
    if (!password.trim()) {
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
