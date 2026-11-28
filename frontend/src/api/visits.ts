import { buildApiUrl } from './url'

// Visit reporting must never block navigation or invoke global authentication UI.
export async function recordPageVisit(path: string): Promise<void> {
  try {
    const token = localStorage.getItem('auth_token')
    const send = (accessToken: string | null) => fetch(buildApiUrl('/visits'), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(accessToken ? { Authorization: `Bearer ${accessToken}` } : {}),
      },
      credentials: 'include',
      keepalive: true,
      body: JSON.stringify({ path: path.split(/[?#]/, 1)[0] }),
    })
    const response = await send(token)
    if (response.status === 401 && token) {
      await send(null)
    }
  } catch {
    // Navigation remains usable when reporting is unavailable.
  }
}
