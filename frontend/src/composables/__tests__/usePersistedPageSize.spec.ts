import { afterEach, describe, expect, it } from 'vitest'

import { getPersistedPageSize, setPersistedPageSize } from '@/composables/usePersistedPageSize'

describe('usePersistedPageSize', () => {
  afterEach(() => {
    localStorage.clear()
    delete window.__APP_CONFIG__
  })

  it('keeps an explicit user selection when the system default is injected', () => {
    window.__APP_CONFIG__ = {
      table_default_page_size: 1000,
      table_page_size_options: [20, 50, 1000]
    } as any
    localStorage.setItem('table-page-size', '50')
    localStorage.setItem('table-page-size-source', 'user')

    expect(getPersistedPageSize()).toBe(50)
  })

  it('uses the configured system default when no user selection exists', () => {
    window.__APP_CONFIG__ = {
      table_default_page_size: 50,
      table_page_size_options: [10, 20, 50, 100]
    } as any

    expect(getPersistedPageSize()).toBe(50)
  })

  it('normalizes and persists a user selection', () => {
    window.__APP_CONFIG__ = {
      table_default_page_size: 20,
      table_page_size_options: [10, 20, 50, 100]
    } as any

    setPersistedPageSize(35)

    expect(localStorage.getItem('table-page-size')).toBe('50')
    expect(getPersistedPageSize()).toBe(50)
  })
})
