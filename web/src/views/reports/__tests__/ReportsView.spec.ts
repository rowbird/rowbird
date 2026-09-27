import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { mountView } from '@/test/mount'

import ReportsView from '../ReportsView.vue'
import { report } from './reports.fixtures'

describe('ReportsView', () => {
  it('explains reports when there are none', async () => {
    mockApi({ 'GET /api/v1/reports': () => json(200, { items: [] }) })
    const { wrapper } = await mountView(ReportsView, { path: '/reports' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.text()).toContain('No reports yet'))
    expect(wrapper.find('[data-testid="report-new"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('lists reports with their last run and pauses them', async () => {
    const calls = mockApi({
      'GET /api/v1/reports': () => json(200, { items: [report] }),
      [`POST /api/v1/reports/${report.id}/pause`]: () => json(200, { ...report, enabled: false, status: 'paused', next_run_at: null }),
    })
    const { wrapper } = await mountView(ReportsView, { path: '/reports', locale: 'pt-BR' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="report-row-daily-sales"]').exists()).toBe(true))
    const row = wrapper.find('[data-testid="report-row-daily-sales"]')
    expect(row.text()).toContain('Ativo')
    expect(row.text()).toContain('Concluída')
    await wrapper.find('[data-testid="report-toggle-daily-sales"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="report-status-paused"]').exists()).toBe(true))
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/pause'))).toBe(true)
    wrapper.unmount()
  })
})
