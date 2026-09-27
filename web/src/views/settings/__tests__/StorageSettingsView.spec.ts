import { describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import StorageSettingsView from '../StorageSettingsView.vue'

const settings = { default_locale: 'en', default_timezone: 'UTC', require_2fa: false, links_enabled: true, retention_runs_days: 90, retention_artifacts_days: 30 }

describe('StorageSettingsView', () => {
  it('saves retention and shows storage and backups', async () => {
    const calls = mockApi({
      'GET /api/v1/settings': () => json(200, settings),
      'PATCH /api/v1/settings': async (req) => json(200, { ...settings, ...(await req.json()) }),
      'GET /api/v1/system/storage': () => json(200, {
        backend: 'local', artifact_bytes: 1536, retention_last_run_at: null,
        backup: { available: true, enabled: true, schedule: '0 3 * * *', destination: 'local', keep: 7, last_at: '2026-09-25T03:00:00Z', last_file: 'rowbird-backup-20260925T030000Z.tar.gz', last_size_bytes: 2048, last_error: 'disk full', next_at: '2026-09-26T03:00:00Z' },
      }),
    })
    const { wrapper } = await mountView(StorageSettingsView, { path: '/settings/storage' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="retention-runs"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="storage-used"]').text()).toBe('1.5 KB')
    expect(wrapper.find('[data-testid="backup-last"]').text()).toContain('2 KB')
    expect(wrapper.find('[data-testid="backup-error"]').text()).toContain('disk full')
    expect(wrapper.text()).toContain('do not include the master key')

    await wrapper.find('[data-testid="retention-runs"]').setValue('30')
    await wrapper.find('form').trigger('submit')
    await flush()
    const patch = await calls.find((c) => c.method === 'PATCH')!.json()
    expect(patch).toEqual({ retention_runs_days: 30, retention_artifacts_days: 30 })
    wrapper.unmount()
  })

  it('explains how to turn backups on, and Postgres', async () => {
    mockApi({
      'GET /api/v1/settings': () => json(200, settings),
      'GET /api/v1/system/storage': () => json(200, { backend: 's3', artifact_bytes: 0, retention_last_run_at: null, backup: { available: true, enabled: false } }),
    })
    const { wrapper } = await mountView(StorageSettingsView, { path: '/settings/storage' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="backup-off"]').exists()).toBe(true))
    expect(wrapper.text()).toContain('ROWBIRD_BACKUP_SCHEDULE')
    expect(wrapper.find('[data-testid="storage-backend"]').text()).toBe('S3 bucket')
    wrapper.unmount()
  })
})
