import { createHmac } from 'node:crypto'

export function normalizeTOTPSecret(value) {
  const normalized = String(value || '').replace(/[\s=]/g, '').toUpperCase()
  if (!/^[A-Z2-7]{16,256}$/.test(normalized)) throw new Error('invalid authenticator secret')
  return normalized
}

export function generateTOTP(value, timestamp = Date.now()) {
  const secret = normalizeTOTPSecret(value)
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let buffer = 0
  let bits = 0
  const bytes = []
  for (const character of secret) {
    buffer = (buffer << 5) | alphabet.indexOf(character)
    bits += 5
    if (bits >= 8) {
      bits -= 8
      bytes.push((buffer >>> bits) & 255)
      buffer &= (1 << bits) - 1
    }
  }
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(timestamp / 30000)))
  const key = Buffer.from(bytes)
  const signature = createHmac('sha1', key).update(counter).digest()
  key.fill(0)
  const offset = signature[signature.length - 1] & 15
  return String((signature.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, '0')
}

export function isAuthenticatorChallenge(body, inputs) {
  return inputs.length > 0 && /authenticator\s*(?:app|code)?|authentication\s+app|身份验证器|验证器应用|身份验证应用|动态口令/i.test(body)
}
