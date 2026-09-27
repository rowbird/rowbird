import { onBeforeUnmount, onMounted } from 'vue'

import { type EventType, type LiveEvent, useEventsStore } from '@/stores/events'

/** Polling while something is active and events are flowing: they do not cross instances. */
export const SLOW_POLL_MS = 15_000
/** Polling while something is active and the event stream is down. */
export const FAST_POLL_MS = 2_000

export interface LiveRefreshOptions {
  /** Event types that trigger a refresh. */
  events: EventType[]
  /** Narrows the events to the ones about what the view shows. */
  match?: (e: LiveEvent) => boolean
  /** Whether the view shows something unfinished; it is polled only then. */
  active?: () => boolean
}

/**
 * Calls refresh when a matching real-time event arrives, and polls while `active()` holds: slowly
 * with a live stream, quickly without one. Refreshes never overlap; an event that arrives during
 * one causes one more afterwards. Failures are ignored: the next event or tick tries again.
 */
export function useLiveRefresh(refresh: () => Promise<unknown> | unknown, opts: LiveRefreshOptions) {
  const events = useEventsStore()
  let running = false
  let again = false
  let lastPoll = 0
  let timer: ReturnType<typeof setInterval> | undefined
  let off: (() => void) | undefined

  async function run() {
    if (running) {
      again = true
      return
    }
    running = true
    try {
      do {
        again = false
        try {
          await refresh()
        } catch {
          // The next event or tick tries again.
        }
      } while (again)
    } finally {
      running = false
      lastPoll = Date.now()
    }
  }

  onMounted(() => {
    off = events.on((e) => {
      if (opts.events.includes(e.type) && (!opts.match || opts.match(e))) void run()
    })
    timer = setInterval(() => {
      if (!opts.active?.()) return
      const every = events.connected ? SLOW_POLL_MS : FAST_POLL_MS
      if (Date.now() - lastPoll >= every) void run()
    }, FAST_POLL_MS)
  })
  onBeforeUnmount(() => {
    off?.()
    clearInterval(timer)
  })

  return { refresh: run }
}
