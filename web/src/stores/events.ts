import { defineStore } from 'pinia'
import { ref } from 'vue'

/** Real-time event types (docs/spec/05-api.md, "SSE events"). */
export type EventType = 'run.updated' | 'report.updated' | 'channel.health' | 'notification.created'
export const EVENT_TYPES: readonly EventType[] = ['run.updated', 'report.updated', 'channel.health', 'notification.created']

export interface LiveEvent {
  type: EventType
  data: Record<string, unknown>
}

type Listener = (e: LiveEvent) => void

/**
 * One EventSource for the whole app, opened after sign-in. Components subscribe with on(); when
 * the stream is down (or the browser has no EventSource) `connected` is false and views fall back
 * to polling.
 */
export const useEventsStore = defineStore('events', () => {
  const connected = ref(false)
  const listeners = new Set<Listener>()
  let source: EventSource | null = null

  function emit(e: LiveEvent) {
    for (const l of listeners) l(e)
  }

  function connect() {
    if (source || typeof EventSource === 'undefined') return
    // EventSource reconnects by itself (the server sends retry: 5000).
    source = new EventSource('/api/v1/events')
    source.onopen = () => {
      connected.value = true
    }
    source.onerror = () => {
      connected.value = false
    }
    for (const type of EVENT_TYPES) {
      source.addEventListener(type, (m) => {
        let data: Record<string, unknown> = {}
        try {
          data = JSON.parse((m as MessageEvent<string>).data) as Record<string, unknown>
        } catch {
          // A malformed event carries no data; listeners still refresh.
        }
        emit({ type, data })
      })
    }
  }

  function disconnect() {
    source?.close()
    source = null
    connected.value = false
  }

  function on(listener: Listener) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  }

  return { connected, connect, disconnect, on, emit }
})
