import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { deliveryCatalog } from '@/test/catalog'
import { flush, mountView } from '@/test/mount'

import ChannelDetailView from '../ChannelDetailView.vue'
import ChannelFormView from '../ChannelFormView.vue'
import ChannelsView from '../ChannelsView.vue'

const channel = {
  id: '01900000-0000-7000-8000-0000000000c1', name: 'team-mail', type: 'email', config: { host: 'smtp.example.com', from: 'rb@example.com', password: { configured: true } },
  capabilities: deliveryCatalog.plugins[3]!.capabilities, status: 'failing', last_success_at: null, last_failure_at: '2026-09-25T12:00:00Z',
  last_error: '535 authentication failed', is_system_mailer: true, used_by: [{ type: 'report', id: 'r1', name: 'Daily sales' }],
  created_at: '2026-09-01T12:00:00Z', updated_at: '2026-09-01T12:00:00Z', version: 1,
}

describe('channels', () => {
  it('lists channels with their health, or explains them when there are none', async () => {
    mockApi({ 'GET /api/v1/plugins': () => json(200, deliveryCatalog), 'GET /api/v1/channels': () => json(200, { items: [channel] }) })
    const { wrapper } = await mountView(ChannelsView, { path: '/channels' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="channel-row-team-mail"]').exists()).toBe(true))
    const row = wrapper.find('[data-testid="channel-row-team-mail"]').text()
    expect(row).toContain('Email')
    expect(row).toContain('Failing')
    expect(row).toContain('System mail')
    expect(row).toContain('1 report')
    wrapper.unmount()

    mockApi({ 'GET /api/v1/plugins': () => json(200, deliveryCatalog), 'GET /api/v1/channels': () => json(200, { items: [] }) })
    const empty = await mountView(ChannelsView, { path: '/channels', locale: 'pt-BR' })
    await vi.waitFor(() => expect(empty.wrapper.text()).toContain('Nenhum canal ainda'))
    empty.wrapper.unmount()
  })

  it('tests an email channel at an address and saves it as the system mailer', async () => {
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, deliveryCatalog),
      'POST /api/v1/channels/test': () => json(200, { ok: false, error_code: 'delivery.auth_failed', error_message: '535 authentication failed' }),
      'POST /api/v1/channels': () => json(201, channel),
    })
    const { wrapper, router } = await mountView(ChannelFormView, { path: '/channels/new' })
    useSessionStore().me = { ...sampleMe }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="type-email"]').exists()).toBe(true))
    await wrapper.find('[data-testid="type-email"]').trigger('click')
    await wrapper.find('#channel-name').setValue('team-mail')
    await wrapper.find('#cfg-host').setValue('smtp.example.com')
    await wrapper.find('#cfg-from').setValue('rb@example.com')
    await wrapper.find('#channel-test-to').setValue('ops@example.com')
    await wrapper.find('[data-testid="channel-test"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="channel-test-result"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="channel-test-result"]').text()).toContain('refused the credentials')
    expect(await calls[1]!.json()).toMatchObject({ type: 'email', to: 'ops@example.com', config: { host: 'smtp.example.com' } })

    await wrapper.find('[data-testid="channel-system-mailer"]').trigger('click')
    await wrapper.find('form').trigger('submit')
    await flush()
    const created = await calls.find((c) => c.method === 'POST' && new URL(c.url).pathname === '/api/v1/channels')!.json()
    expect(created).toMatchObject({ name: 'team-mail', type: 'email', is_system_mailer: true })
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe(`/channels/${channel.id}`))
    wrapper.unmount()
  })

  it('shows a failing channel and resends its failed deliveries', async () => {
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, deliveryCatalog),
      [`GET /api/v1/channels/${channel.id}`]: () => json(200, channel),
      [`POST /api/v1/channels/${channel.id}/retry-failed`]: () => json(202, { queued: 2 }),
    })
    const { wrapper } = await mountView(ChannelDetailView, { path: `/channels/${channel.id}` })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="channel-failing"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="channel-used-by"]').text()).toContain('Daily sales')
    expect(wrapper.find('[data-testid="channel-edit"]').exists()).toBe(false)
    await wrapper.find('[data-testid="channel-retry-failed"]').trigger('click')
    await vi.waitFor(() => expect(calls.some((c) => c.url.endsWith('/retry-failed'))).toBe(true))
    wrapper.unmount()
  })
})
