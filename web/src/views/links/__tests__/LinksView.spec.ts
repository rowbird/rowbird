import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { mountView } from '@/test/mount'

import LinksView from '../LinksView.vue'

const link = {
  id: 'l1', run_id: 'run-1', report_id: 'r1', report_title: 'Daily sales', delivery_id: 'd1', file_name: 'daily-sales-2026-09-25-0800.xlsx', format: 'xlsx',
  status: 'active', expires_at: '2026-10-02T08:00:00Z', require_login: true, revoked_at: null, revoked_by_name: null, download_count: 3,
  last_download_at: '2026-09-25T09:00:00Z', created_at: '2026-09-25T08:00:00Z',
}

describe('LinksView', () => {
  it('lists active links and revokes one after confirmation', async () => {
    let revoked = false
    const calls = mockApi({
      'GET /api/v1/links': () => json(200, { items: revoked ? [] : [link], next_cursor: null }),
      'POST /api/v1/links/l1/revoke': () => {
        revoked = true
        return json(200, { ...link, status: 'revoked', downloads: [] })
      },
    })
    const { wrapper } = await mountView(LinksView, { path: '/links', locale: 'pt-BR' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find(`[data-testid="link-row-${link.file_name}"]`).exists()).toBe(true))
    const row = wrapper.find(`[data-testid="link-row-${link.file_name}"]`).text()
    expect(row).toContain('Daily sales')
    expect(row).toContain('exige login')
    expect(row).toContain('Ativo')
    expect(new URL(calls[0]!.url).searchParams.get('active')).toBe('true')

    await wrapper.find('[data-testid="link-revoke"]').trigger('click')
    const confirm = await vi.waitFor(() => {
      const b = [...document.body.querySelectorAll('button')].find((x) => x.textContent?.trim() === 'Revogar link' && x.closest('[role="alertdialog"]'))
      expect(b).toBeTruthy()
      return b!
    })
    confirm.click()
    await vi.waitFor(() => expect(wrapper.text()).toContain('Nenhum link compartilhado'))
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/links/l1/revoke'))).toBe(true)
    wrapper.unmount()
  })
})
