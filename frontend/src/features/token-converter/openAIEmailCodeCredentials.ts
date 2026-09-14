export const OPENAI_EMAIL_CODE_PROVIDER = 'https://ic.g-c.cc'

export type OpenAIEmailCodeCredentialErrorCode = 'missing-email-or-token' | 'ambiguous-email-or-token'

export interface OpenAIEmailCodeCredentialRow {
  index: number
  lineNumber: number
  account: string
  emailCodeToken: string
}

export interface InvalidOpenAIEmailCodeCredentialRow {
  lineNumber: number
  source: string
  code: OpenAIEmailCodeCredentialErrorCode
}

export interface OpenAIEmailCodeCredentialParseResult {
  rows: OpenAIEmailCodeCredentialRow[]
  invalidRows: InvalidOpenAIEmailCodeCredentialRow[]
}

const emailPattern = /[A-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi
const tokenPattern = /[A-F0-9]{64}/gi

function uniqueMatches(source: string, pattern: RegExp): string[] {
  return [...new Set(source.match(pattern) || [])]
}

export function parseOpenAIEmailCodeCredentials(input: string): OpenAIEmailCodeCredentialParseResult {
  const rows: OpenAIEmailCodeCredentialRow[] = []
  const invalidRows: InvalidOpenAIEmailCodeCredentialRow[] = []

  input.replace(/^\uFEFF/, '').split(/\r?\n/).forEach((rawLine, lineIndex) => {
    const source = rawLine.trim()
    if (!source) return
    const lineNumber = lineIndex + 1
    const normalized = source.replace(/\\@/g, '@')
    const emails = uniqueMatches(normalized, emailPattern).map(value => value.toLowerCase())
    const tokens = uniqueMatches(normalized, tokenPattern)
    if (emails.length === 0 || tokens.length === 0) {
      invalidRows.push({ lineNumber, source, code: 'missing-email-or-token' })
      return
    }
    if (emails.length !== 1 || tokens.length !== 1) {
      invalidRows.push({ lineNumber, source, code: 'ambiguous-email-or-token' })
      return
    }
    rows.push({
      index: rows.length,
      lineNumber,
      account: emails[0],
      emailCodeToken: tokens[0],
    })
  })

  return { rows, invalidRows }
}

