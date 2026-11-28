import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { nextTick } from 'vue'
import { auditAPI, type AuditLog, type AuditLogListResponse } from '@/api/admin/audit'
import Pagination from '@/components/common/Pagination.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import VisitorRecords from '../VisitorRecords.vue'

vi.mock('@/api/admin/audit', () => ({ auditAPI: { list: vi.fn() } }))

enableAutoUnmount(afterEach)
afterEach(() => vi.restoreAllMocks())

const listVisits = vi.mocked(auditAPI.list)
const signedInVisit: AuditLog = {
  id: 1,
  created_at: '2026-11-27T12:34:56Z',
  actor_user_id: 7,
  actor_email: 'visitor@example.com',
  actor_role: 'user',
  auth_method: 'jwt',
  credential_masked: '',
  action: 'site.visit',
  method: 'GET',
  path: '/dashboard',
  request_id: '',
  client_ip: '203.0.113.10',
  user_agent: 'Mozilla/5.0',
  status_code: 200,
  latency_ms: 0
}
const anonymousVisit: AuditLog = {
  ...signedInVisit,
  id: 2,
  actor_user_id: undefined,
  actor_email: '',
  actor_role: '',
  auth_method: '',
  path: '/login',
  client_ip: '2001:db8::1'
}

function response(items = [signedInVisit, anonymousVisit], total = items.length): AuditLogListResponse {
  return { items, total, page: 1, page_size: 20, pages: Math.ceil(total / 20) }
}

function mountRecords(locale = 'en') {
  return mount(VisitorRecords, {
    global: {
      plugins: [createI18n({
        legacy: false,
        locale,
        messages: { en, zh },
        messageCompiler: message => () => String(message)
      })]
    }
  })
}

function searchButton(wrapper: ReturnType<typeof mountRecords>) {
  return wrapper.findAll('button').find(button => button.text() === 'Search')!
}

describe('VisitorRecords', () => {
  beforeEach(() => {
    localStorage.clear()
    listVisits.mockReset()
    listVisits.mockResolvedValue(response())
  })

  it('lists visit time, account with client IP, anonymous IP, and page using the visit action filter', async () => {
    const wrapper = mountRecords()
    await flushPromises()

    expect(listVisits).toHaveBeenCalledWith({ action: 'site.visit', page: 1, page_size: 20 })
    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('visitor@example.com')
    expect(rows[0].text()).toContain('Client IP 203.0.113.10')
    expect(rows[0].text()).toContain('/dashboard')
    expect(rows[0].get('time').attributes('datetime')).toBe(signedInVisit.created_at)
    expect(rows[0].get('time').text()).toMatch(/2026/)
    expect(rows[1].text()).toContain('2001:db8::1')
    expect(rows[1].text()).toContain('/login')
    expect(rows[1].text()).not.toContain('Client IP')
    expect(wrapper.find('form').exists()).toBe(false)
  })

  it('shows loading feedback and disables refresh until the request finishes', async () => {
    let finish!: (value: AuditLogListResponse) => void
    listVisits.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const wrapper = mountRecords()
    await nextTick()

    expect(wrapper.attributes('aria-busy')).toBe('true')
    expect(wrapper.get('[role="status"]').text()).toBe('Loading...')
    expect(wrapper.get('button[aria-label="Refresh"]').attributes('disabled')).toBeDefined()

    finish(response())
    await flushPromises()
    expect(wrapper.attributes('aria-busy')).toBe('false')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it.each(['visitor@example.com', '2001:db8::1', '/login'])('searches account, IP, or path with q (%s)', async query => {
    const wrapper = mountRecords()
    await flushPromises()
    await wrapper.get('#visitor-search').setValue(`  ${query}  `)
    await wrapper.get('#visitor-search').trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(listVisits).toHaveBeenLastCalledWith(expect.objectContaining({ action: 'site.visit', q: query, page: 1 }))
  })

  it('applies local time filters as ISO times and resets pagination for searches and page size changes', async () => {
    listVisits.mockResolvedValue(response([signedInVisit], 60))
    const wrapper = mountRecords()
    await flushPromises()
    await wrapper.getComponent(Pagination).findAll('nav button').find(button => button.text() === '2')!.trigger('click')
    await flushPromises()
    expect(listVisits).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }))

    await wrapper.get('#visitor-search').setValue('/dashboard')
    await wrapper.get('#visitor-start-time').setValue('2026-11-26T10:00')
    await wrapper.get('#visitor-end-time').setValue('2026-11-27T11:00')
    await searchButton(wrapper).trigger('click')
    await flushPromises()
    const expectedFilters = {
      action: 'site.visit',
      q: '/dashboard',
      start_time: new Date(2026, 10, 26, 10).toISOString(),
      end_time: new Date(2026, 10, 27, 11).toISOString()
    }
    expect(listVisits).toHaveBeenLastCalledWith({ ...expectedFilters, page: 1, page_size: 20 })

    await wrapper.getComponent(Pagination).findAll('nav button').find(button => button.text() === '2')!.trigger('click')
    await flushPromises()
    expect(listVisits).toHaveBeenLastCalledWith({ ...expectedFilters, page: 2, page_size: 20 })
    await wrapper.get('.page-size-select button').trigger('click')
    const sizeOption = Array.from(document.querySelectorAll('[role="option"]')).find(option => option.textContent?.trim() === '50')!
    sizeOption.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(listVisits).toHaveBeenLastCalledWith({ ...expectedFilters, page: 1, page_size: 50 })

    await wrapper.get('#visitor-search').setValue('/unsent')
    await wrapper.get('button[aria-label="Refresh"]').trigger('click')
    await flushPromises()
    expect(listVisits).toHaveBeenLastCalledWith({ ...expectedFilters, page: 1, page_size: 50 })

    await wrapper.get('#visitor-search').setValue('')
    await wrapper.get('#visitor-start-time').setValue('')
    await wrapper.get('#visitor-end-time').setValue('')
    await searchButton(wrapper).trigger('click')
    await flushPromises()
    expect(listVisits).toHaveBeenLastCalledWith({ action: 'site.visit', page: 1, page_size: 50 })
  })

  it('blocks an inverted time range with an accessible error', async () => {
    const wrapper = mountRecords()
    await flushPromises()
    await wrapper.get('#visitor-start-time').setValue('2026-11-27T12:00')
    await wrapper.get('#visitor-end-time').setValue('2026-11-26T12:00')
    expect(wrapper.get('#visitor-time-error').attributes('role')).toBe('alert')
    expect(wrapper.get('#visitor-start-time').attributes('aria-invalid')).toBe('true')
    expect(searchButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.get('#visitor-search').trigger('keydown', { key: 'Enter' })
    expect(listVisits).toHaveBeenCalledTimes(1)
  })

  it('shows an empty state in Chinese', async () => {
    listVisits.mockResolvedValue(response([], 0))
    const wrapper = mountRecords('zh')
    await flushPromises()

    expect(wrapper.text()).toContain('暂无访客记录')
    expect(wrapper.findComponent(Pagination).exists()).toBe(false)
  })

  it('renders visitor identity and page in the shared mobile table layout', async () => {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ ...matchMedia(query), matches: false }))
    const wrapper = mountRecords()
    await flushPromises()

    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.findAll('[data-field="visitor"]').map(cell => cell.text())).toEqual([
      'Visitorvisitor@example.comClient IP 203.0.113.10',
      'Visitor2001:db8::1'
    ])
    expect(wrapper.findAll('[data-field="path"]').map(cell => cell.text())).toEqual(['Page/dashboard', 'Page/login'])
  })

  it('keeps Enter on the refresh button separate from applying draft filters', async () => {
    const wrapper = mountRecords()
    await flushPromises()
    await wrapper.get('#visitor-search').setValue('/unsent')
    await wrapper.get('button[aria-label="Refresh"]').trigger('keydown', { key: 'Enter' })
    expect(listVisits).toHaveBeenCalledTimes(1)
    await wrapper.get('button[aria-label="Refresh"]').trigger('click')
    await flushPromises()
    expect(listVisits).toHaveBeenLastCalledWith({ action: 'site.visit', page: 1, page_size: 20 })
  })

  it('retries a failed request with the applied search and recovers', async () => {
    const wrapper = mountRecords()
    await flushPromises()
    listVisits.mockRejectedValueOnce(new Error('offline'))
    await wrapper.get('#visitor-search').setValue('/dashboard')
    await searchButton(wrapper).trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('Failed to load visitor records')
    expect(wrapper.findComponent(Pagination).exists()).toBe(false)
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(listVisits).toHaveBeenLastCalledWith(expect.objectContaining({ q: '/dashboard', page: 1 }))
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('visitor@example.com')
  })

  it.each(['resolve', 'reject'])('ignores a superseded request when it later %ss', async outcome => {
    let finish!: (value: AuditLogListResponse) => void
    let fail!: (error: Error) => void
    listVisits.mockReturnValueOnce(new Promise((resolve, reject) => {
      finish = resolve
      fail = reject
    }))
    const wrapper = mountRecords()
    await wrapper.get('#visitor-search').setValue('/login')
    await wrapper.get('#visitor-search').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(wrapper.text()).toContain('/login')

    if (outcome === 'resolve') finish(response([{ ...signedInVisit, path: '/outdated' }]))
    else fail(new Error('outdated failure'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('/outdated')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.attributes('aria-busy')).toBe('false')
  })

  it('moves to the last available page when refreshing after records disappear', async () => {
    listVisits.mockResolvedValue(response([signedInVisit], 60))
    const wrapper = mountRecords()
    await flushPromises()
    await wrapper.getComponent(Pagination).findAll('nav button').find(button => button.text() === '3')!.trigger('click')
    await flushPromises()
    listVisits.mockResolvedValue(response([signedInVisit], 1))
    await wrapper.get('button[aria-label="Refresh"]').trigger('click')
    await flushPromises()

    expect(listVisits).toHaveBeenLastCalledWith({ action: 'site.visit', page: 1, page_size: 20 })
    expect(wrapper.getComponent(Pagination).props('page')).toBe(1)
  })
})
