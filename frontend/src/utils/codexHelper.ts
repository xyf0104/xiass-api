import type { ApiKey } from '@/types'

export interface CodexHelperConnection {
  callback: URL
  state: string
}

export interface CodexHelperPayload {
  base_url: string
  api_key: string
  key_name: string
}

export function parseCodexHelperConnection(callbackValue: unknown, stateValue: unknown): CodexHelperConnection {
  if (typeof callbackValue !== 'string' || typeof stateValue !== 'string') {
    throw new Error('缺少配置助手连接参数')
  }
  if (!/^[A-Za-z0-9_-]{32,128}$/.test(stateValue)) {
    throw new Error('配置助手会话无效')
  }

  let callback: URL
  try {
    callback = new URL(callbackValue)
  } catch {
    throw new Error('配置助手回调地址无效')
  }
  const isLoopback = callback.hostname === '127.0.0.1' || callback.hostname === '[::1]' || callback.hostname === '::1'
  const port = Number(callback.port)
  if (
    callback.protocol !== 'http:' ||
    !isLoopback ||
    !Number.isInteger(port) ||
    port < 1024 ||
    port > 65535 ||
    callback.pathname !== '/callback' ||
    callback.username !== '' ||
    callback.password !== ''
  ) {
    throw new Error('配置助手只能使用本机回环地址')
  }
  return { callback, state: stateValue }
}

export function buildCodexHelperCallback(
  connection: CodexHelperConnection,
  baseUrl: string,
  apiKey: Pick<ApiKey, 'key' | 'name'>
): string {
  const payload: CodexHelperPayload = {
    base_url: normalizeBaseUrl(baseUrl),
    api_key: apiKey.key,
    key_name: apiKey.name
  }
  const encodedPayload = encodeBase64Url(JSON.stringify(payload))
  const callback = new URL(connection.callback)
  callback.search = ''
  callback.hash = new URLSearchParams({
    state: connection.state,
    payload: encodedPayload
  }).toString()
  return callback.toString()
}

export function isCodexCompatibleKey(key: ApiKey): boolean {
  return key.status === 'active' && key.group?.platform === 'openai'
}

function normalizeBaseUrl(value: string): string {
  const parsed = new URL(value)
  if (parsed.protocol !== 'https:' || !parsed.hostname || parsed.username || parsed.password) {
    throw new Error('XIASS API 地址无效')
  }
  parsed.search = ''
  parsed.hash = ''
  parsed.pathname = parsed.pathname.replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  return parsed.toString().replace(/\/$/, '')
}

function encodeBase64Url(value: string): string {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}
