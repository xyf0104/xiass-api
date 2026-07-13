export type AppTheme = 'light' | 'dark'

const THEME_STORAGE_KEY = 'theme'
const TRANSITION_CLASS = 'theme-transitioning'
const TRANSITION_DURATION_MS = 520

let transitionTimer: number | undefined

export function getCurrentTheme(): AppTheme {
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}

export function getInitialTheme(): AppTheme {
  return localStorage.getItem(THEME_STORAGE_KEY) === 'light' ? 'light' : 'dark'
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
  root.style.colorScheme = theme

  if (persist) {
    localStorage.setItem(THEME_STORAGE_KEY, theme)
  }
}

export function toggleTheme(): AppTheme {
  const nextTheme: AppTheme = getCurrentTheme() === 'dark' ? 'light' : 'dark'
  applyTheme(nextTheme)
  return nextTheme
}
