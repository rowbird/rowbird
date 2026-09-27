import { describe, expect, it, vi } from 'vitest'

import HealthBanner from '@/components/layout/HealthBanner.vue'
import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { mountView } from '@/test/mount'

import HomeView from '../HomeView.vue'

const dashboard = {
  next_runs: [{ id: 'r1', title: 'Daily sales', next_run_at: '2026-09-27T08:00:00Z', timezone: 'UTC', consecutive_failures: 0, paused_reason: null }],
  recent_failures: [{
    id: 'run-9', report_id: 'r2', report_title: 'Stock', trigger: 'schedule', triggered_by_name: null, deliver: true, status: 'failed', scheduled_for: null,
    started_at: null, finished_at: '2026-09-26T10:00:00Z', duration_ms: 10, row_count: null, truncated: false, attempt: 3, error_code: 'connection.auth_failed',
    created_at: '2026-09-26T10:00:00Z',
  }],
  success_rate_7d: { succeeded: 3, total: 4 },
  success_rate_30d: { succeeded: 0, total: 0 },
  failing_channels: [{ id: 'c1', name: 'team-mail', type: 'email', last_error: '535 authentication failed', last_failure_at: '2026-09-26T10:00:00Z' }],
  failing_reports: [{ id: 'r2', title: 'Stock', next_run_at: null, timezone: 'UTC', consecutive_failures: 5, paused_reason: 'auto_failures' }],
}

describe('HomeView', () => {
  it('summarizes next runs, failures, success rate and failing channels', async () => {
    mockApi({ 'GET /api/v1/dashboard': () => json(200, dashboard) })
    const { wrapper } = await mountView(HomeView, { path: '/' })
    useSessionStore().me = { ...sampleMe, role: 'viewer' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="home-next-runs"]').text()).toContain('Daily sales'))
    expect(wrapper.find('[data-testid="home-success-rate"]').text()).toContain('75%')
    expect(wrapper.find('[data-testid="home-success-rate"]').text()).toContain('3 of 4 runs')
    expect(wrapper.find('[data-testid="home-success-rate"]').text()).toContain('No runs yet')
    expect(wrapper.find('[data-testid="home-failures"]').text()).toContain('Stock')
    expect(wrapper.find('[data-testid="home-channels"]').text()).toContain('535 authentication failed')
    expect(wrapper.text()).not.toContain('New report')
    wrapper.unmount()
  })

  it('shows the health banner while something fails', async () => {
    mockApi({ 'GET /api/v1/dashboard': () => json(200, dashboard) })
    const { wrapper } = await mountView(HealthBanner, { path: '/', locale: 'pt-BR' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="health-banner"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="health-banner-channels"]').text()).toBe('O canal team-mail está falhando')
    expect(wrapper.find('[data-testid="health-banner-channels"]').attributes('href')).toBe('/channels/c1')
    expect(wrapper.find('[data-testid="health-banner-reports"]').text()).toBe('O relatório Stock está falhando')
    wrapper.unmount()

    mockApi({ 'GET /api/v1/dashboard': () => json(200, { ...dashboard, failing_channels: [], failing_reports: [] }) })
    const quiet = await mountView(HealthBanner, { path: '/' })
    await new Promise((r) => setTimeout(r, 20))
    expect(quiet.wrapper.find('[data-testid="health-banner"]').exists()).toBe(false)
    quiet.wrapper.unmount()
  })
})
