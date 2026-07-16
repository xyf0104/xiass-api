export type AppTheme = 'light' | 'dark'

const THEME_STORAGE_KEY = 'theme'
const TRANSITION_CLASS = 'theme-transitioning'
const TRANSITION_DURATION_MS = 520
const THEME_BACKGROUND_READY_EVENT = 'xiass-theme-background-ready'
const THEME_BACKGROUND_READY_TIMEOUT_MS = 2400

let transitionTimer: number | undefined

function readBootstrapTheme(): AppTheme | null {
  if (!document.documentElement.hasAttribute('data-theme-booting')) {
    return null
  }

  const theme = document.documentElement.dataset.theme
  return theme === 'light' || theme === 'dark' ? theme : null
}

function readPersistedTheme(): AppTheme | null {
  try {
    return localStorage.getItem(THEME_STORAGE_KEY) === 'light' ? 'light' : 'dark'
  } catch {
    return null
  }
}

export function getCurrentTheme(): AppTheme {
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}

export function getInitialTheme(): AppTheme {
  return readBootstrapTheme() ?? readPersistedTheme() ?? 'dark'
}

export function applyTheme(
  theme: AppTheme,
  options: { persist?: boolean; animate?: boolean } = {}
): void {
  const { persist = true, animate = true } = options
  const root = document.documentElement

  if (animate) {
    root.classList.add(TRANSITION_CLASS)
    window.clearTimeout(transitionTimer)
    transitionTimer = window.setTimeout(() => {
      root.classList.remove(TRANSITION_CLASS)
    }, TRANSITION_DURATION_MS)
  }

  root.classList.toggle('dark', theme === 'dark')
  root.dataset.theme = theme
  root.style.colorScheme = theme

  if (persist) {
    localStorage.setItem(THEME_STORAGE_KEY, theme)
  }
}

export function notifyThemeBackgroundReady(theme: AppTheme): void {
  const root = document.documentElement
  root.dataset.themeBackgroundReady = theme
  window.dispatchEvent(new CustomEvent(THEME_BACKGROUND_READY_EVENT, {
    detail: { theme }
  }))
}

export function releaseThemeBootstrapGuard(): void {
  const root = document.documentElement
  const expectedTheme = getCurrentTheme()
  const hasThemeBackground = document.querySelector('.theme-video-background') !== null
  let settled = false

  const revealAfterPaint = () => {
    const reveal = () => {
      delete root.dataset.themeBooting
    }

    if (typeof window.requestAnimationFrame === 'function') {
      window.requestAnimationFrame(() => window.requestAnimationFrame(reveal))
      return
    }

    window.setTimeout(reveal, 0)
  }

  const finish = () => {
    if (settled) return
    settled = true
    window.removeEventListener(THEME_BACKGROUND_READY_EVENT, onBackgroundReady)
    window.clearTimeout(timeoutId)
    revealAfterPaint()
  }

  const onBackgroundReady = (event: Event) => {
    const detail = (event as CustomEvent<{ theme?: AppTheme }>).detail
    if (detail?.theme === expectedTheme) finish()
  }

  if (!hasThemeBackground || root.dataset.themeBackgroundReady === expectedTheme) {
    revealAfterPaint()
    return
  }

  const timeoutId = window.setTimeout(finish, THEME_BACKGROUND_READY_TIMEOUT_MS)
  window.addEventListener(THEME_BACKGROUND_READY_EVENT, onBackgroundReady)
}

export function toggleTheme(): AppTheme {
  const nextTheme: AppTheme = getCurrentTheme() === 'dark' ? 'light' : 'dark'
  applyTheme(nextTheme)
  return nextTheme
}
