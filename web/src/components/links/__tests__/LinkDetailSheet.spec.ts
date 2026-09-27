import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { mountView } from '@/test/mount'
import LinksView from '@/views/links/LinksView.vue'

const link = {
  id: 'l1', run_id: 'run1', report_id: 'rp1', report_title: 'Daily orders', delivery_id: null, file_name: 'daily.csv', format: 'csv',
  status: 'active', expires_at: '2026-10-01T00:00:00Z', require_login: false, revoked_at: null, revoked_by_name: null,
  download_count: 2, last_download_at: '2026-09-26T10:00:00Z', created_at: '2026-09-25T10:00:00Z',
}
const $ = (sel: string) => document.body.querySelector<HTMLElement>(sel)
const $$ = (sel: string) => [...document.body.querySelectorAll<HTMLElement>(sel)]

describe('link detail', () => {
  it('opens from the list with who downloaded, when and with what', async () => {
    mockApi({
      'GET /api/v1/links': () => json(200, { items: [link], next_cursor: null }),
      'GET /api/v1/links/l1': () => json(200, {
        ...link,
        downloads: [
          { at: '2026-09-26T10:00:00Z', ip: '203.0.113.9', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/140.0 Safari/537.36', user_id: 'u1', user_name: 'Ana' },
          { at: '2026-09-26T09:00:00Z', ip: '198.51.100.4', user_agent: 'curl/8.7.1', user_id: null, user_name: null },
        ],
      }),
    })
    const { wrapper } = await mountView(LinksView, { path: '/links' })
    useSessionStore().me = { ...sampleMe, role: 'viewer' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="link-open-daily.csv"]').exists()).toBe(true))
    await wrapper.find('[data-testid="link-open-daily.csv"]').trigger('click')
    await vi.waitFor(() => expect($$('[data-testid="link-download"]')).toHaveLength(2))
    const rows = $$('[data-testid="link-download"]').map((r) => r.textContent)
    expect(rows[0]).toContain('Ana')
    expect(rows[0]).toContain('Chrome on macOS')
    expect(rows[1]).toContain('Anonymous')
    expect($('[data-testid="link-detail"]')!.textContent).toContain('2 downloads')
    // Viewers cannot revoke.
    expect($('[data-testid="link-detail-revoke"]')).toBeNull()
    wrapper.unmount()
  })
})
