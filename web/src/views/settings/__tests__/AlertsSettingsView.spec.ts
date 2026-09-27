import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { deliveryCatalog } from '@/test/catalog'
import { flush, mountView } from '@/test/mount'

import AlertsSettingsView from '../AlertsSettingsView.vue'

const mail = {
  id: 'c-mail', name: 'team-mail', type: 'email', config: {}, capabilities: { ...deliveryCatalog.plugins[3]!.capabilities, supports_alerts: true },
  status: 'ok', last_success_at: null, last_failure_at: null, last_error: null, is_system_mailer: false, used_by: [],
  created_at: '2026-09-01T12:00:00Z', updated_at: '2026-09-01T12:00:00Z', version: 1,
}
const hook = { ...mail, id: 'c-hook', name: 'ops-hook', type: 'webhook', capabilities: { ...deliveryCatalog.plugins[4]!.capabilities, supports_alerts: true } }
const bucket = { ...mail, id: 'c-s3', name: 'bucket', type: 'webhook', capabilities: { ...hook.capabilities, supports_alerts: false } }

describe('AlertsSettingsView', () => {
  it('saves the primary with its recipients and the heartbeat, keeping the stored URL secret', async () => {
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, deliveryCatalog),
      'GET /api/v1/channels': () => json(200, { items: [mail, hook, bucket] }),
      'GET /api/v1/settings/alerts': () =>
        json(200, { primary: null, fallback: { channel_id: 'c-hook', options: {} }, heartbeat_url_configured: true, heartbeat_interval_seconds: 60, same_destination: false }),
      'PUT /api/v1/settings/alerts': async (req) => {
        const b = (await req.json()) as Record<string, unknown>
        return json(200, { primary: b.primary, fallback: b.fallback, heartbeat_url_configured: true, heartbeat_interval_seconds: b.heartbeat_interval_seconds, same_destination: false })
      },
    })
    const { wrapper } = await mountView(AlertsSettingsView, { path: '/settings/alerts' })
    useSessionStore().me = { ...sampleMe }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="alert-settings"]').exists()).toBe(true))
    const select = wrapper.find('[data-testid="alert-primary-channel"]')
    expect(select.findAll('option').map((o) => o.text())).toEqual(['None', 'team-mail (Email)', 'ops-hook (Webhook)'])
    expect(wrapper.find('[data-testid="heartbeat-configured"]').exists()).toBe(true)

    await select.setValue('c-mail')
    await flush()
    // Message templates do not apply to alerts; recipients do.
    expect(wrapper.find('#alert-primary-to').exists()).toBe(true)
    expect(wrapper.find('#alert-primary-subject').exists()).toBe(false)
    await wrapper.find('#alert-primary-to').setValue('ops@example.com')
    await wrapper.find('#heartbeat-interval').setValue('120')
    await wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true))
    const body = (await calls.find((c) => c.method === 'PUT')!.json()) as Record<string, unknown>
    expect(body).toEqual({
      primary: { channel_id: 'c-mail', options: { to: 'ops@example.com' } },
      fallback: { channel_id: 'c-hook', options: {} },
      heartbeat_interval_seconds: 120,
    })
    wrapper.unmount()
  })

  it('shows field errors and warns when both channels are of one kind', async () => {
    mockApi({
      'GET /api/v1/plugins': () => json(200, deliveryCatalog),
      'GET /api/v1/channels': () => json(200, { items: [hook] }),
      'GET /api/v1/settings/alerts': () =>
        json(200, { primary: { channel_id: 'c-hook', options: {} }, fallback: null, heartbeat_url_configured: false, heartbeat_interval_seconds: 60, same_destination: true }),
      'PUT /api/v1/settings/alerts': () =>
        json(400, { type: 'x', title: 'Invalid', status: 400, code: 'validation.failed', errors: [{ field: 'heartbeat_url', code: 'validation.url' }] }),
    })
    const { wrapper } = await mountView(AlertsSettingsView, { path: '/settings/alerts', locale: 'pt-BR' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="alert-same-destination"]').exists()).toBe(true))
    await wrapper.find('[data-testid="heartbeat-url"]').setValue('ftp://x')
    await wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(wrapper.text()).toContain('Informe um endereço http ou https'))
    wrapper.unmount()
  })
})
