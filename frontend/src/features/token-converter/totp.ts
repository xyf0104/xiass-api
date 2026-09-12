const BASE32_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
const TRAILING_STATUS_MARKERS = /(?:\s*(?:✅|✔️?|☑️?))+\s*$/u

export function cleanTwoFactorValue(value: string): string {
  return value.trim().replace(TRAILING_STATUS_MARKERS, '').trim()
}

export function normalizeBase32Secret(value: string): string {
  const cleaned = cleanTwoFactorValue(value)
  if (/^otpauth:\/\//i.test(cleaned)) {
    try {
      const secret = new URL(cleaned).searchParams.get('secret')
      if (!secret) throw new Error('missing secret')
      return secret.replace(/[\s=]+/g, '').toUpperCase()
    } catch {
      throw new Error('invalid otpauth URI')
    }
  }
  return cleaned.replace(/[\s=]+/g, '').toUpperCase()
}

function decodeBase32(value: string): Uint8Array {
  const normalized = normalizeBase32Secret(value)
  if (!normalized || !/^[A-Z2-7]+$/.test(normalized)) {
    throw new Error('invalid Base32 secret')
  }

  let buffer = 0
  let bits = 0
  const bytes: number[] = []
  for (const character of normalized) {
    const digit = BASE32_ALPHABET.indexOf(character)
    buffer = (buffer << 5) | digit
    bits += 5
    while (bits >= 8) {
      bits -= 8
      bytes.push((buffer >>> bits) & 0xff)
      buffer &= bits === 0 ? 0 : (1 << bits) - 1
    }
  }
  return new Uint8Array(bytes)
}

function counterBytes(counter: number): Uint8Array {
  const output = new Uint8Array(8)
  const view = new DataView(output.buffer)
  view.setUint32(0, Math.floor(counter / 0x100000000), false)
  view.setUint32(4, counter >>> 0, false)
  return output
}

export async function generateTotp(
  secret: string,
  timestamp = Date.now(),
  options: { digits?: number; period?: number } = {},
): Promise<string> {
  const digits = options.digits ?? 6
  const period = options.period ?? 30
  if (!Number.isInteger(digits) || digits < 6 || digits > 8 || !Number.isInteger(period) || period <= 0) {
    throw new Error('invalid TOTP options')
  }
  if (!globalThis.crypto?.subtle) throw new Error('Web Crypto is unavailable')

  const key = await globalThis.crypto.subtle.importKey(
    'raw',
    decodeBase32(secret),
    { name: 'HMAC', hash: 'SHA-1' },
    false,
    ['sign'],
  )
  const counter = Math.floor(timestamp / 1000 / period)
  const signature = new Uint8Array(await globalThis.crypto.subtle.sign('HMAC', key, counterBytes(counter)))
  const offset = signature[signature.length - 1] & 0x0f
  const binary = (
    ((signature[offset] & 0x7f) << 24)
    | ((signature[offset + 1] & 0xff) << 16)
    | ((signature[offset + 2] & 0xff) << 8)
    | (signature[offset + 3] & 0xff)
  ) >>> 0
  return String(binary % (10 ** digits)).padStart(digits, '0')
}

export function totpSecondsRemaining(timestamp = Date.now(), period = 30): number {
  const elapsed = Math.floor(timestamp / 1000) % period
  return period - elapsed
}
