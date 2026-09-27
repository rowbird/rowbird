import { defineStore } from 'pinia'
import { ref } from 'vue'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'

import { useEventsStore } from './events'

export type AppNotification = Schemas['Notification']

/** Where a notification leads: the page of what it is about. */
export function notificationLink(n: AppNotification): string | null {
  if (!n.entity_id) return null
  if (n.entity_type === 'channel') return `/channels/${n.entity_id}`
  if (n.entity_type === 'report') return `/reports/${n.entity_id}`
  return null
}

/**
 * The signed-in user's notifications for the bell. The first page is reloaded when a
 * notification.created event arrives, so grouped occurrences update their count in place.
 */
export const useNotificationsStore = defineStore('notifications', () => {
  const items = ref<AppNotification[]>([])
  const unread = ref(0)
  const next = ref<string | null>(null)
  const loaded = ref(false)
  let listening = false

  async function load() {
    const page = unwrap(await api.GET('/api/v1/notifications', { params: { query: { limit: 20 } } }))
    items.value = page.items
    unread.value = page.unread_count
    next.value = page.next_cursor ?? null
    loaded.value = true
  }

  async function loadMore() {
    if (!next.value) return
    const page = unwrap(await api.GET('/api/v1/notifications', { params: { query: { limit: 20, cursor: next.value } } }))
    const known = new Set(items.value.map((n) => n.id))
    items.value = [...items.value, ...page.items.filter((n) => !known.has(n.id))]
    unread.value = page.unread_count
    next.value = page.next_cursor ?? null
  }

  async function markRead(id: string) {
    const n = unwrap(await api.POST('/api/v1/notifications/{notificationId}/read', { params: { path: { notificationId: id } } }))
    const i = items.value.findIndex((x) => x.id === id)
    if (i >= 0 && !items.value[i]!.read_at) unread.value = Math.max(0, unread.value - 1)
    if (i >= 0) items.value[i] = n
  }

  async function markAllRead() {
    unwrap(await api.POST('/api/v1/notifications/read-all'))
    const now = new Date().toISOString()
    items.value = items.value.map((n) => (n.read_at ? n : { ...n, read_at: now }))
    unread.value = 0
  }

  /** Loads the first page and keeps it current from then on. */
  function start() {
    if (listening) return
    listening = true
    useEventsStore().on((e) => {
      if (e.type === 'notification.created') void load().catch(() => undefined)
    })
    void load().catch(() => undefined)
  }

  return { items, unread, next, loaded, load, loadMore, markRead, markAllRead, start }
})
