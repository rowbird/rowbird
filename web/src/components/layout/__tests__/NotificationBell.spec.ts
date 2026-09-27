import { describe, expect, it, vi } from 'vitest'

import { useEventsStore } from '@/stores/events'
import { json, mockApi } from '@/test/api'
import { mountView } from '@/test/mount'

import NotificationBell from '../NotificationBell.vue'

const failing = {
  id: 'n1', type: 'channel_failing', severity: 'error', title_key: 'notifications.channel_failing', params: { channel: 'team-mail', error: '535 authentication failed' },
  entity_type: 'channel', entity_id: '01900000-0000-7000-8000-0000000000c1', count: 3, first_at: '2026-09-26T12:00:00Z', last_at: '2026-09-26T12:10:00Z',
  read_at: null, resolved_at: null,
}
const recovered = { ...failing, id: 'n2', type: 'channel_recovered', severity: 'info', count: 1, read_at: '2026-09-26T12:20:00Z', resolved_at: '2026-09-26T12:20:00Z' }

describe('NotificationBell', () => {
  it('shows unread notifications with their count, reloads on events and marks them read', async () => {
    let unread = 1
    let reads = 0
    const calls = mockApi({
      'GET /api/v1/notifications': () => json(200, { items: [failing, recovered], unread_count: unread, next_cursor: null }),
      'POST /api/v1/notifications/read-all': () => {
        unread = 0
        return json(200, { marked: 1 })
      },
      'POST /api/v1/notifications/n1/read': () => {
        reads++
        return json(200, { ...failing, read_at: '2026-09-26T13:00:00Z' })
      },
    })
    const { wrapper, router } = await mountView(NotificationBell, { path: '/', locale: 'pt-BR' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="notification-count"]').text()).toBe('1'))
    expect(wrapper.find('[data-testid="notification-bell"]').attributes('aria-label')).toBe('Notificações, 1 não lidas')

    useEventsStore().emit({ type: 'notification.created', data: { id: 'n1' } })
    await vi.waitFor(() => expect(calls.filter((c) => c.method === 'GET').length).toBe(2))

    await wrapper.find('[data-testid="notification-bell"]').trigger('click')
    const items = await vi.waitFor(() => {
      const found = document.body.querySelectorAll('[data-testid="notification"]')
      expect(found.length).toBe(2)
      return found
    })
    expect(items[0]!.textContent).toContain('O canal team-mail está falhando')
    expect(items[0]!.textContent).toContain('3 vezes')
    expect(items[0]!.textContent).toContain('535 authentication failed')
    expect(items[1]!.textContent).toContain('O canal team-mail voltou a funcionar')

    ;(items[0] as HTMLElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe(`/channels/${failing.entity_id}`))
    expect(reads).toBe(1)
    await vi.waitFor(() => expect(wrapper.find('[data-testid="notification-count"]').exists()).toBe(false))
    wrapper.unmount()
  })
})
