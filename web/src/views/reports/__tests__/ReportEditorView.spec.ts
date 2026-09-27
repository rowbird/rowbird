import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { conditionCatalog } from '@/test/catalog'
import { flush, mountView } from '@/test/mount'

import ReportEditorView from '../ReportEditorView.vue'
import { query, querySummary, report } from './reports.fixtures'

const preview = () => json(200, { expression: '0 8 * * 1-5', timezone: 'America/Sao_Paulo', description: 'At 08:00, Monday through Friday', next: [] })

describe('ReportEditorView', () => {
  it('creates a report with overrides, a schedule and a condition', async () => {
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, conditionCatalog),
      'GET /api/v1/queries': () => json(200, { items: [querySummary] }),
      [`GET /api/v1/queries/${query.id}`]: () => json(200, query),
      'GET /api/v1/settings': () => json(200, { default_locale: 'en', default_timezone: 'America/Sao_Paulo', require_2fa: false }),
      'POST /api/v1/schedules/preview': preview,
      'POST /api/v1/reports': () => json(201, report),
      [`GET /api/v1/reports/${report.id}`]: () => json(200, report),
    })
    const { wrapper, router } = await mountView(ReportEditorView, { path: '/reports/new' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="report-overrides"]').exists()).toBe(true))

    await wrapper.find('[data-testid="report-title"]').setValue('Vendas diárias')
    expect((wrapper.find('#report-slug').element as HTMLInputElement).value).toBe('vendas-diarias')
    expect(wrapper.find('[data-testid="report-overrides"]').text()).toContain('Default: 50')
    await wrapper.find('#override-region').setValue('south')
    await wrapper.find('#schedule-kind').setValue('weekdays')
    await wrapper.find('#condition-add-type').setValue('row_count')
    await wrapper.find('[data-testid="condition-add"]').trigger('click')
    await flush()
    await wrapper.find('form').trigger('submit')

    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe(`/reports/${report.id}`))
    const body = await calls.find((c) => c.method === 'POST' && new URL(c.url).pathname === '/api/v1/reports')!.json()
    expect(body).toMatchObject({
      title: 'Vendas diárias', slug: 'vendas-diarias', query_id: query.id, cron: '0 8 * * 1-5', timezone: 'America/Sao_Paulo',
      param_overrides: { region: 'south' }, condition: { match: 'all', rules: [{ type: 'row_count', op: 'gt', value: 0 }] },
      enabled: true, max_rows: 0, retry_max: 2, overlap_policy: 'skip',
    })
    wrapper.unmount()
  })

  it('shows field errors from the server', async () => {
    mockApi({
      'GET /api/v1/plugins': () => json(200, conditionCatalog),
      'GET /api/v1/queries': () => json(200, { items: [querySummary] }),
      [`GET /api/v1/queries/${query.id}`]: () => json(200, query),
      [`GET /api/v1/reports/${report.id}`]: () => json(200, report),
      'POST /api/v1/schedules/preview': preview,
      [`PATCH /api/v1/reports/${report.id}`]: () => json(400, {
        code: 'validation.failed', status: 400,
        errors: [{ field: 'param_overrides.region', code: 'validation.required' }, { field: 'condition.rules[0].value', code: 'validation.range' }],
      }),
    })
    const { wrapper } = await mountView(ReportEditorView, { path: `/reports/${report.id}/edit` })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect((wrapper.find('#override-region').element as HTMLInputElement).value).toBe('south'))
    await wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="report-overrides"]').text()).toContain('Required.'))
    expect(wrapper.find('[data-testid="condition-rule-0"]').text()).toContain('Out of the allowed range.')
    wrapper.unmount()
  })
})
