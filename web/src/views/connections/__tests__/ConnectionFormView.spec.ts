import { describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'
import { flush, mountView } from '@/test/mount'
import { pgSchema } from '@/test/schema'

import ConnectionFormView from '../ConnectionFormView.vue'

const catalog = {
  plugins: [{ kind: 'connector', id: 'postgres', name: 'plugin.postgres.name', description: 'plugin.postgres.description', icon: 'postgres', version: '1', schema: pgSchema, capabilities: {} }],
  messages: { en: { 'plugin.postgres.name': 'PostgreSQL', 'plugin.postgres.description': 'Postgres', 'plugin.common.ssh_host_key.label': 'SSH host key' } },
}
const saved = { id: '01900000-0000-7000-8000-00000000000a', name: 'sales', driver: 'postgres', config: {}, query_timeout_seconds: 60, max_rows: 100000, allow_multi_statement: false, ai_excluded_tables: [], status: 'ok', server_version: 'PostgreSQL 17', last_error: '', created_at: '', updated_at: '', version: 1 }

describe('ConnectionFormView', () => {
  it('tests, confirms the SSH fingerprint, and saves', async () => {
    let tests = 0
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'POST /api/v1/connections/test': () => {
        tests++
        return tests === 1
          ? json(200, { ok: false, error_code: 'connection.ssh_host_key_unknown', error_detail: { fingerprint: 'SHA256:xyz' } })
          : json(200, { ok: true, latency_ms: 12, server_version: 'PostgreSQL 17', can_write: true, tables: 3 })
      },
      'POST /api/v1/connections': () => json(201, saved),
      'GET /api/v1/connections/01900000-0000-7000-8000-00000000000a': () => json(200, saved),
    })
    const { wrapper, router } = await mountView(ConnectionFormView, { path: '/connections/new' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="driver-postgres"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="driver-postgres"]').text()).toContain('PostgreSQL')
    await wrapper.find('[data-testid="driver-postgres"]').trigger('click')
    await wrapper.find('#connection-name').setValue('sales')
    await wrapper.find('#cfg-host').setValue('db.internal')
    await wrapper.find('#cfg-ssh_enabled').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('#cfg-ssh_host').exists()).toBe(true))
    await wrapper.find('#cfg-ssh_host').setValue('bastion.example.com')
    await wrapper.find('[data-testid="connection-test"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="fingerprint-confirm"]').exists()).toBe(true))
    expect(wrapper.text()).toContain('SHA256:xyz')

    await wrapper.find('[data-testid="fingerprint-trust"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="write-warning"]').exists()).toBe(true))
    const second = await calls[2]!.json()
    expect(second.config).toMatchObject({ host: 'db.internal', ssh_enabled: true, ssh_host: 'bastion.example.com', ssh_host_key: 'SHA256:xyz' })

    await wrapper.find('form').trigger('submit')
    await flush()
    const created = await calls.find((c) => c.method === 'POST' && new URL(c.url).pathname === '/api/v1/connections')!.json()
    expect(created).toMatchObject({ name: 'sales', driver: 'postgres', config: { host: 'db.internal', port: 5432, ssh_host_key: 'SHA256:xyz' } })
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe(`/connections/${saved.id}`))
    wrapper.unmount()
  })

  it('places server field errors on the config fields', async () => {
    mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'POST /api/v1/connections': () => json(400, { code: 'validation.failed', errors: [{ field: 'config.host', code: 'validation.required' }, { field: 'name', code: 'validation.slug' }] }),
    })
    const { wrapper } = await mountView(ConnectionFormView, { path: '/connections/new' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="driver-postgres"]').exists()).toBe(true))
    await wrapper.find('[data-testid="driver-postgres"]').trigger('click')
    await wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(wrapper.find('#cfg-host-error').exists()).toBe(true))
    expect(wrapper.find('#cfg-host-error').text()).toBe('Required.')
    expect(wrapper.find('#connection-name-error').text()).toContain('lowercase')
    wrapper.unmount()
  })
})
