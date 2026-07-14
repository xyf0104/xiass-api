export const SERVICE_RECOVERY_TIMEOUT_MS = 3 * 60 * 1000
export const SERVICE_RECOVERY_RETRY_INTERVAL_MS = 1500

export interface ServiceRecoveryOptions {
  targetVersion: string
  getVersion: () => Promise<string | null | undefined>
  timeoutMs?: number
  retryIntervalMs?: number
  now?: () => number
  sleep?: (milliseconds: number) => Promise<void>
}

export function normalizeReleaseVersion(version: string | null | undefined): string {
  return (version ?? '').trim().replace(/^v/i, '')
}

export function isExpectedServiceRestartError(error: unknown): boolean {
  if (error == null || typeof error !== 'object') return false

  const candidate = error as {
    code?: unknown
    message?: unknown
    status?: unknown
    response?: { status?: unknown }
  }
  const status = Number(candidate.response?.status ?? candidate.status)

  return candidate.code === 'ECONNABORTED'
    || candidate.message === 'Network Error'
    || !candidate.response
    || [502, 503, 504].includes(status)
}

export async function waitForServiceVersion({
  targetVersion,
  getVersion,
  timeoutMs = SERVICE_RECOVERY_TIMEOUT_MS,
  retryIntervalMs = SERVICE_RECOVERY_RETRY_INTERVAL_MS,
  now = Date.now,
  sleep = (milliseconds) => new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds))
}: ServiceRecoveryOptions): Promise<boolean> {
  const expectedVersion = normalizeReleaseVersion(targetVersion)
  if (!expectedVersion) return false

  const deadline = now() + Math.max(0, timeoutMs)
  const retryDelay = Math.max(250, retryIntervalMs)

  while (true) {
    try {
      if (normalizeReleaseVersion(await getVersion()) === expectedVersion) {
        return true
      }
    } catch {
      // A restart commonly returns a transport error or a temporary 502. Keep
      // the current page in place and retry until the new container is ready.
    }

    if (now() >= deadline) return false
    await sleep(retryDelay)
  }
}
