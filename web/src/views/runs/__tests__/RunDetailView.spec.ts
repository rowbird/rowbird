import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { conditionCatalog } from '@/test/catalog'
import { mountView } from '@/test/mount'

import { runSummary } from '../../reports/__tests__/reports.fixtures'
import RunDetailView from '../RunDetailView.vue'

const catalog = {
  ...conditionCatalog,
  plugins: [
    ...conditionCatalog.plugins,
    { kind: 'formatter', id: 'csv', name: 'plugin.format.csv.name', description: '', icon: '', version: '1', schema: { type: 'object', properties: {}, required: [], 'x-order': [] }, capabilities: { kind: 'file', extension: 'csv' } },
    { kind: 'formatter', id: 'html_table', name: 'plugin.format.html_table.name', description: '', icon: '', version: '1', schema: { type: 'object', properties: {}, required: [], 'x-order': [] }, capabilities: { kind: 'inline' } },
  ],
}

const finished = {
  ...runSummary, error_message: null, cancel_requested_at: null, result_expires_at: '2999-01-01T00:00:00Z',
  query: { query_id: 'q', version: 2, sql: 'select total from orders' },
  params: { timezone: 'America/Sao_Paulo', values: [{ name: 'today', type: 'date', value: '2026-09-25', builtin: true }] },
  condition: { passed: true, match: 'all', rules: [{ type: 'row_count', passed: true, detail: { row_count: 150 } }] },
  sample: { columns: [{ name: 'total', type: 'decimal', db_type: 'DECIMAL' }], rows: [['1234.5'], ['10']] },
  files: [], deliveries: [],
}

const attempt = {
  id: 'a1', delivery_id: 'd1', channel_id: 'c1', channel_name: 'team-mail', channel_type: 'email', status: 'failed', attempts: 3,
  error_code: 'delivery.auth_failed', error_message: '535 authentication failed', sent_at: null, meta: { mode: 'attachment' }, resendable: true,
  created_at: '2026-09-25T12:00:00Z', updated_at: '2026-09-25T12:00:05Z',
}

describe('RunDetailView', () => {
  it('shows the steps, the condition and the result sample', async () => {
    mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      [`GET /api/v1/runs/${finished.id}`]: () => json(200, finished),
    })
    const { wrapper } = await mountView(RunDetailView, { path: `/runs/${finished.id}` })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="run-steps"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="run-rows"]').text()).toContain('150 rows')
    expect(wrapper.find('[data-testid="run-param"]').text()).toBe('today = 2026-09-25')
    expect(wrapper.find('[data-testid="step-condition"]').text()).toContain('Row count')
    expect(wrapper.find('[data-testid="step-deliveries"]').text()).toContain('only shows the result')
    expect(wrapper.find('[data-testid="run-sample-summary"]').text()).toBe('The first 2 of 150 rows.')
    expect(wrapper.find('[data-testid="results"]').text()).toContain('1,234.5')
    expect(wrapper.find('[data-testid="run-cancel"]').exists()).toBe(false)
    const links = wrapper.findAll('[data-testid="run-downloads"] a')
    expect(links.map((a) => a.attributes('href'))).toEqual([`/api/v1/runs/${finished.id}/result?format=csv`])
    wrapper.unmount()
  })

  it('cancels an active run and explains failures', async () => {
    const running = { ...finished, result_expires_at: null, status: 'running', row_count: null, condition: null, sample: null, params: null, finished_at: null, duration_ms: null }
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      [`GET /api/v1/runs/${finished.id}`]: () => json(200, running),
      [`POST /api/v1/runs/${finished.id}/cancel`]: () => json(200, { ...running, cancel_requested_at: '2026-09-25T12:00:02Z' }),
    })
    const { wrapper } = await mountView(RunDetailView, { path: `/runs/${finished.id}`, locale: 'pt-BR' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="run-cancel"]').exists()).toBe(true))
    await wrapper.find('[data-testid="run-cancel"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="run-cancel"]').text()).toBe('Cancelando...'))
    expect(calls.some((c) => c.method === 'POST')).toBe(true)
    wrapper.unmount()

    mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      [`GET /api/v1/runs/${finished.id}`]: () => json(200, { ...running, status: 'failed', error_code: 'connection.auth_failed', error_message: 'password authentication failed' }),
    })
    const failed = await mountView(RunDetailView, { path: `/runs/${finished.id}`, locale: 'pt-BR' })
    await vi.waitFor(() => expect(failed.wrapper.find('[data-testid="run-error"]').exists()).toBe(true))
    expect(failed.wrapper.find('[data-testid="run-error"]').text()).toContain('password authentication failed')
    failed.wrapper.unmount()
  })

  it('lists the delivered files and resends a failed delivery', async () => {
    const partial = {
      ...finished, deliver: true, status: 'partial', error_code: 'run.delivery_failed', result_expires_at: null,
      files: [{ format: 'csv', file_name: 'sales-2026-09-25-1200.csv', content_type: 'text/csv', size_bytes: 2048, expires_at: '2999-01-01T00:00:00Z' }],
      deliveries: [
        attempt,
        { ...attempt, id: 'a2', channel_name: 'ops-slack', channel_type: 'slack', status: 'sent', attempts: 1, error_code: null, error_message: null, resendable: false,
          sent_at: '2026-09-25T12:00:03Z', meta: { mode: 'link', links: ['sales-2026-09-25-1200.csv'], fallback_to_link: true } },
      ],
    }
    let current: typeof partial = partial
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      [`GET /api/v1/runs/${finished.id}`]: () => json(200, current),
      [`POST /api/v1/runs/${finished.id}/attempts/a1/retry`]: () => {
        current = { ...partial, status: 'success', error_code: null, deliveries: [{ ...attempt, status: 'sent', attempts: 4, resendable: false }, partial.deliveries[1]] } as never
        return json(200, { ...attempt, status: 'sent', attempts: 4, resendable: false })
      },
    })
    const { wrapper } = await mountView(RunDetailView, { path: `/runs/${finished.id}` })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="run-attempts"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="run-error"]').text()).toContain('some deliveries failed')
    expect(wrapper.find('[data-testid="run-files"]').text()).toContain('sales-2026-09-25-1200.csv')
    // The stored file stays downloadable after the spooled result expired.
    expect(wrapper.findAll('[data-testid="run-downloads"] a').map((a) => a.attributes('href'))).toEqual([`/api/v1/runs/${finished.id}/result?format=csv`])
    const failed = wrapper.find('[data-testid="attempt-team-mail"]')
    expect(failed.find('[data-testid="attempt-error"]').text()).toContain('refused the credentials')
    expect(failed.text()).toContain('3 tries')
    expect(wrapper.find('[data-testid="attempt-ops-slack"] [data-testid="attempt-fallback"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="attempt-ops-slack"] [data-testid="attempt-resend"]').exists()).toBe(false)

    await failed.find('[data-testid="attempt-resend"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="attempt-team-mail"] [data-testid="attempt-resend"]').exists()).toBe(false))
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/attempts/a1/retry'))).toBe(true)
    expect(wrapper.find('[data-testid="run-error"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
