import { describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import ImportExportView from '../ImportExportView.vue'

const pos = { file: 'import.yaml', line: 1, column: 1 }
const item = (kind: string, name: string, action: string, extra = {}) => ({ kind, name, pos, action, target: name, changes: [], ...extra })

describe('ImportExportView', () => {
  it('maps a missing channel, takes a secret, decides a conflict and applies', async () => {
    const bodies: Record<string, unknown>[] = []
    const calls = mockApi({
      'GET /api/v1/connections': () => json(200, { items: [] }),
      'GET /api/v1/channels': () => json(200, { items: [{ id: 'c1', name: 'team-mail' }] }),
      'POST /api/v1/import': async (req) => {
        const body = await req.json()
        bodies.push(body)
        const mapped = body.map?.['old-mail'] === 'team-mail'
        const secret = body.secrets?.ROWBIRD_CHANNEL_HOOK_TOKEN === 's3cret'
        const decided = body.items?.['Report/daily'] === 'overwrite'
        return json(200, {
          applied: body.dry_run === false,
          items: [
            item('Channel', 'hook', 'create'),
            decided
              ? item('Report', 'daily', 'update', { policy: 'overwrite', changes: [{ field: 'spec.schedule.cron', from: '0 7 * * *', to: '0 8 * * *' }] })
              : item('Report', 'daily', 'conflict', { policy: 'fail', changes: [{ field: 'spec.schedule.cron', from: '0 7 * * *', to: '0 8 * * *' }] }),
          ],
          missing: mapped ? [] : [{ kind: 'Channel', name: 'old-mail', pos }],
          secrets: [{ env: 'ROWBIRD_CHANNEL_HOOK_TOKEN', kind: 'Channel', name: 'hook', field: 'token', required: true, provided: secret }],
          errors: [],
        })
      },
    })
    const { wrapper } = await mountView(ImportExportView, { path: '/settings/import-export', locale: 'pt-BR' })
    await wrapper.find('[data-testid="import-yaml"]').setValue('apiVersion: rowbird.dev/v1')
    await wrapper.find('[data-testid="import-analyze"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="import-missing"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="import-apply"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="import-item-Report-daily"]').text()).toContain('Existe e é diferente')
    expect(wrapper.find('[data-testid="import-item-Report-daily"]').text()).toContain('"0 7 * * *" → "0 8 * * *"')

    await wrapper.find('#map-old-mail').setValue('team-mail')
    await flush()
    await wrapper.find('#secret-ROWBIRD_CHANNEL_HOOK_TOKEN').setValue('s3cret')
    await wrapper.find('#secret-ROWBIRD_CHANNEL_HOOK_TOKEN').trigger('change')
    await flush()
    await wrapper.find('[data-testid="import-policy-Report-daily"]').setValue('overwrite')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="import-apply"]').attributes('disabled')).toBeUndefined())

    await wrapper.find('[data-testid="import-apply"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="import-done"]').exists()).toBe(true))
    const last = bodies[bodies.length - 1]!
    expect(last).toMatchObject({ dry_run: false, map: { 'old-mail': 'team-mail' }, secrets: { ROWBIRD_CHANNEL_HOOK_TOKEN: 's3cret' }, items: { 'Report/daily': 'overwrite' } })
    expect(bodies.slice(0, -1).every((b) => b.dry_run === true)).toBe(true)
    expect(calls.length).toBeGreaterThan(4)
    wrapper.unmount()
  })

  it('shows document errors with their lines', async () => {
    mockApi({
      'GET /api/v1/connections': () => json(200, { items: [] }),
      'GET /api/v1/channels': () => json(200, { items: [] }),
      'POST /api/v1/import': () => json(200, { applied: false, items: [], missing: [], secrets: [], errors: [{ pos: { file: 'import.yaml', line: 7, column: 3 }, message: 'unknown field "sqll"' }] }),
    })
    const { wrapper } = await mountView(ImportExportView, { path: '/settings/import-export' })
    await wrapper.find('[data-testid="import-yaml"]').setValue('x')
    await wrapper.find('[data-testid="import-analyze"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="import-errors"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="import-errors"]').text()).toContain('line 7: unknown field "sqll"')
    wrapper.unmount()
  })
})
