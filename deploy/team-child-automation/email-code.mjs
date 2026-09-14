const PROVIDER_ORIGIN = 'https://ic.g-c.cc'
const DEFAULT_TIMEOUT_MS = 60_000
const DEFAULT_INTERVAL_MS = 5_000
const MAX_RESPONSE_BYTES = 256 * 1024

function emailCodeError(code) {
  return Object.assign(new Error(code), { code })
}

function validateLogin(email, token) {
  if (typeof email !== 'string' || email.length > 254 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    throw emailCodeError('email_code_access_denied')
  }
  if (typeof token !== 'string' || !/^[A-Fa-f0-9]{64}$/.test(token)) {
    throw emailCodeError('email_code_access_denied')
  }
}

async function boundedText(response) {
  const length = Number(response.headers?.get?.('content-length') || 0)
  if (Number.isFinite(length) && length > MAX_RESPONSE_BYTES) throw emailCodeError('email_code_unavailable')
  const body = await response.text()
  if (Buffer.byteLength(body) > MAX_RESPONSE_BYTES) throw emailCodeError('email_code_unavailable')
  return body
}

async function request(fetchImpl, url, options = {}) {
  let response
  try {
    response = await fetchImpl(url, { redirect: 'error', ...options })
  } catch (error) {
    if (options.signal?.aborted || error?.name === 'AbortError') throw emailCodeError('email_code_canceled')
    throw emailCodeError('email_code_unavailable')
  }
  let body
  try {
    body = await boundedText(response)
  } catch (error) {
    if (options.signal?.aborted || error?.name === 'AbortError') throw emailCodeError('email_code_canceled')
    if (error?.code) throw error
    throw emailCodeError('email_code_unavailable')
  }
  let payload
  try {
    payload = body ? JSON.parse(body) : null
  } catch {
    throw emailCodeError('email_code_unavailable')
  }
  if (!response.ok || payload?.ok === false) {
    if (response.status === 401 || String(payload?.error?.code || '').toUpperCase() === 'UNAUTHORIZED') {
      throw emailCodeError('email_code_access_denied')
    }
    throw emailCodeError('email_code_unavailable')
  }
  return payload?.data
}

async function discoverGateway(fetchImpl, signal) {
  let response
  try {
    response = await fetchImpl(`${PROVIDER_ORIGIN}/`, {
      redirect: 'error',
      signal,
      headers: { Accept: 'text/html' },
    })
  } catch (error) {
    if (signal?.aborted || error?.name === 'AbortError') throw emailCodeError('email_code_canceled')
    throw emailCodeError('email_code_unavailable')
  }
  if (!response.ok) throw emailCodeError('email_code_unavailable')
  const html = await boundedText(response)
  const gateway = html.match(/data-public-mail-gateway=["'](\/m\/[A-Za-z0-9_-]{16,128})["']/i)?.[1] || ''
  if (!gateway) throw emailCodeError('email_code_unavailable')
  return gateway
}

function normalizeMessages(data) {
  return Array.isArray(data?.messages) ? data.messages.filter(item => item && typeof item === 'object').slice(0, 20) : []
}

function messageID(message) {
  return String(message?.id || '').trim()
}

function messageTimestamp(message) {
  const parsed = Date.parse(String(message?.date || ''))
  return Number.isFinite(parsed) ? parsed : 0
}

function expiryEpochSeconds(value) {
  const numeric = Number(value)
  if (Number.isFinite(numeric) && numeric > 0) return numeric > 1_000_000_000_000 ? numeric / 1000 : numeric
  const parsed = Date.parse(String(value || ''))
  return Number.isFinite(parsed) ? parsed / 1000 : 0
}

function readableHTML(value) {
  return String(value || '').replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, ' ')
    .replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, ' ')
    .replace(/<[^>]+>/g, ' ')
}

function messageText(message) {
  return [message?.from, message?.subject, message?.snippet, message?.text, message?.htmlPreview, readableHTML(message?.html)]
    .map(value => String(value || ''))
    .join('\n')
    .replace(/\s+/g, ' ')
    .trim()
}

function isOpenAIMessage(message) {
  return /\bopenai\b|\bchatgpt\b/i.test(messageText(message))
}

export function extractOpenAIVerificationCode(message) {
  const source = messageText(message)
  if (!source || !isOpenAIMessage(message)) return ''
  const contextual = source.match(/(?:verification|verify|login|sign[- ]?in|security|temporary|one[- ]?time|验证码|登录代码|临时代码)[^\d]{0,80}(\d{6})/i)
  if (contextual) return contextual[1]
  const candidates = [...new Set(Array.from(source.matchAll(/\b\d{6}\b/g), match => match[0]))]
  return candidates.length === 1 ? candidates[0] : ''
}

async function wait(ms, signal, setTimeoutImpl, clearTimeoutImpl) {
  if (signal?.aborted) throw emailCodeError('email_code_canceled')
  await new Promise((resolve, reject) => {
    let timer
    const cleanup = () => signal?.removeEventListener?.('abort', cancel)
    const cancel = () => {
      clearTimeoutImpl(timer)
      cleanup()
      reject(emailCodeError('email_code_canceled'))
    }
    const finish = () => {
      cleanup()
      resolve()
    }
    timer = setTimeoutImpl(finish, ms)
    signal?.addEventListener('abort', cancel, { once: true })
  })
}

export async function createICGCEmailCodeSession({ email, token, signal }, dependencies = {}) {
  validateLogin(email, token)
  const fetchImpl = dependencies.fetch || globalThis.fetch
  if (typeof fetchImpl !== 'function') throw emailCodeError('email_code_unavailable')
  const now = dependencies.now || Date.now
  const setTimeoutImpl = dependencies.setTimeout || setTimeout
  const clearTimeoutImpl = dependencies.clearTimeout || clearTimeout
  const normalizedEmail = email.trim().toLowerCase()
  let privateToken = token
  let gateway = await discoverGateway(fetchImpl, signal)
  let proof = ''
  let proofExpiresAt = 0
  let closed = false

  async function authorize() {
    const data = await request(fetchImpl, `${PROVIDER_ORIGIN}${gateway}/authorize`, {
      method: 'POST',
      signal,
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ email: normalizedEmail, token: privateToken, turnstileToken: '' }),
    })
    proof = String(data?.accessProof || '')
    proofExpiresAt = expiryEpochSeconds(data?.accessProofExpiresAt)
    if (!proof || proof.length > 4096) throw emailCodeError('email_code_unavailable')
  }

  function authHeaders() {
    return {
      Accept: 'application/json',
      'X-Public-Email': normalizedEmail,
      'X-Public-Email-Token': privateToken,
      'X-Mail-Access-Proof': proof,
    }
  }

  async function listMessages() {
    if (!proof || proofExpiresAt <= now() / 1000 + 5) await authorize()
    try {
      return normalizeMessages(await request(fetchImpl, `${PROVIDER_ORIGIN}${gateway}/items?limit=10`, {
        signal,
        headers: authHeaders(),
      }))
    } catch (error) {
      if (error?.code !== 'email_code_access_denied') throw error
      proof = ''
      await authorize()
      return normalizeMessages(await request(fetchImpl, `${PROVIDER_ORIGIN}${gateway}/items?limit=10`, {
        signal,
        headers: authHeaders(),
      }))
    }
  }

  async function messageDetail(id) {
    const data = await request(fetchImpl, `${PROVIDER_ORIGIN}${gateway}/items/${encodeURIComponent(id)}`, {
      signal,
      headers: authHeaders(),
    })
    return data?.message && typeof data.message === 'object' ? data.message : {}
  }

  await authorize()
  const baseline = new Set()
  for (const message of await listMessages()) {
    const id = messageID(message)
    if (id) baseline.add(id)
  }
  const processed = new Set(baseline)

  return {
    async waitForCode({ notBefore = now(), timeoutMs = DEFAULT_TIMEOUT_MS, intervalMs = DEFAULT_INTERVAL_MS } = {}) {
      const deadline = now() + timeoutMs
      let successfulQuery = false
      while (!closed && !signal?.aborted && now() < deadline) {
        try {
          const messages = await listMessages()
          successfulQuery = true
          messages.sort((left, right) => messageTimestamp(right) - messageTimestamp(left))
          for (const message of messages) {
            const id = messageID(message)
            if (!id || processed.has(id)) continue
            if (messageTimestamp(message) && messageTimestamp(message) < notBefore - 10_000) {
              processed.add(id)
              continue
            }
            if (!isOpenAIMessage(message)) {
              processed.add(id)
              continue
            }
            let code = extractOpenAIVerificationCode(message)
            if (!code) {
              try {
                code = extractOpenAIVerificationCode({ ...message, ...(await messageDetail(id)) })
              } catch (error) {
                if (error?.code === 'email_code_access_denied' || error?.code === 'email_code_canceled') throw error
                continue
              }
            }
            processed.add(id)
            if (code) return code
          }
        } catch (error) {
          if (error?.code === 'email_code_access_denied' || error?.code === 'email_code_canceled') throw error
        }
        const remaining = deadline - now()
        if (remaining <= 0) break
        await wait(Math.min(intervalMs, remaining), signal, setTimeoutImpl, clearTimeoutImpl)
      }
      if (closed || signal?.aborted) throw emailCodeError('email_code_canceled')
      throw emailCodeError(successfulQuery ? 'email_code_timeout' : 'email_code_unavailable')
    },
    close() {
      closed = true
      privateToken = ''
      proof = ''
      proofExpiresAt = 0
      baseline.clear()
      processed.clear()
      gateway = ''
    },
  }
}

export { PROVIDER_ORIGIN as ICGC_EMAIL_CODE_ORIGIN }
